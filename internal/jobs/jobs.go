// Package jobs is a small persistent job queue backed by the jobs table, with
// a single worker that runs one job at a time.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	maxAttempts = 5
	maxPartials = 10
	timeLayout  = "2006-01-02T15:04:05Z"
)

// ErrRetryLater, wrapped in a handler's error, means the job is not finished
// and should run again after the backoff without counting as a failed attempt.
// Such reschedules are bounded by maxPartials.
var ErrRetryLater = errors.New("jobs: retry later")

var errUnknownKind = errors.New("unknown job kind")

// Handler processes one job payload.
type Handler func(ctx context.Context, payload json.RawMessage) error

// Option configures a Queue.
type Option func(*Queue)

// WithPollInterval sets how often an idle worker polls for due jobs.
func WithPollInterval(d time.Duration) Option { return func(q *Queue) { q.poll = d } }

// WithMinInterval sets the minimum gap between starting two jobs.
func WithMinInterval(d time.Duration) Option { return func(q *Queue) { q.minInterval = d } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(q *Queue) { q.now = now } }

// WithBackoff replaces the retry delay for the n-th attempt/partial (n >= 1).
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(q *Queue) { q.backoff = f }
}

// Queue is a persistent job queue.
type Queue struct {
	db          *sql.DB
	poll        time.Duration
	minInterval time.Duration
	now         func() time.Time
	backoff     func(int) time.Duration

	mu        sync.Mutex
	handlers  map[string]Handler
	lastStart time.Time
}

// NewQueue returns a queue over db (which must have the migrations applied).
func NewQueue(db *sql.DB, opts ...Option) *Queue {
	q := &Queue{
		db:       db,
		poll:     time.Second,
		now:      time.Now,
		backoff:  defaultBackoff,
		handlers: map[string]Handler{},
	}
	for _, o := range opts {
		o(q)
	}
	return q
}

// defaultBackoff is 5s, 10s, 20s, ... capped at 15 minutes.
func defaultBackoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt && d < 15*time.Minute; i++ {
		d *= 2
	}
	return min(d, 15*time.Minute)
}

// Register sets the handler for a job kind.
func (q *Queue) Register(kind string, h Handler) {
	q.mu.Lock()
	q.handlers[kind] = h
	q.mu.Unlock()
}

// Enqueue stores a pending job; payload is JSON-encoded.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	_, err = q.db.ExecContext(ctx, `INSERT INTO jobs(kind, payload_json, run_after) VALUES (?,?,?)`,
		kind, string(b), q.stamp(q.now()))
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return nil
}

func (q *Queue) stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

// Run processes jobs until ctx is cancelled. Jobs left in state running by a
// crash are reset to pending first.
func (q *Queue) Run(ctx context.Context) {
	if err := q.Recover(ctx); err != nil {
		log.Printf("jobs: %v", err)
	}
	for ctx.Err() == nil {
		ran, err := q.runOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("jobs: %v", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(q.poll):
		}
	}
}

// Recover resets jobs left in state running by a crash to pending. Run does
// this itself; call it earlier (before SweepSync) when startup work needs the
// reset to have happened. It is idempotent.
func (q *Queue) Recover(ctx context.Context) error {
	if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='pending' WHERE state='running'`); err != nil {
		return fmt.Errorf("reset running jobs: %w", err)
	}
	return nil
}

// runOnce claims and runs one due job. It reports false when nothing was run
// (no due job, or the minimum interval since the last start has not passed).
func (q *Queue) runOnce(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	q.mu.Lock()
	now := q.now()
	if q.minInterval > 0 && !q.lastStart.IsZero() && now.Sub(q.lastStart) < q.minInterval {
		q.mu.Unlock()
		return false, nil
	}
	q.mu.Unlock()

	var (
		id                 int64
		kind, payload      string
		attempts, partials int
	)
	err := q.db.QueryRowContext(ctx, `UPDATE jobs SET state='running' WHERE id = (
		SELECT id FROM jobs WHERE state='pending' AND run_after <= ? ORDER BY run_after, id LIMIT 1)
		RETURNING id, kind, payload_json, attempts, partials`, q.stamp(now)).
		Scan(&id, &kind, &payload, &attempts, &partials)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim job: %w", err)
	}
	q.mu.Lock()
	q.lastStart = now
	h := q.handlers[kind]
	q.mu.Unlock()

	if h == nil {
		herr := fmt.Errorf("%w %q", errUnknownKind, kind)
		return true, q.finish(id, kind, attempts, partials, herr, false)
	}
	herr := q.call(ctx, h, json.RawMessage(payload))
	return true, q.finish(id, kind, attempts, partials, herr, ctx.Err() != nil)
}

func (q *Queue) call(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h(ctx, payload)
}

// finish records the outcome. The job row is updated even when the worker's
// context is already cancelled. Unknown kinds fail at once; when shutdown
// interrupted a handler the job goes back to pending without counting an
// attempt.
func (q *Queue) finish(id int64, kind string, attempts, partials int, herr error, interrupted bool) error {
	ctx := context.Background()
	now := q.now()
	var err error
	switch {
	case herr == nil:
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='done', last_error='' WHERE id=?`, id)
	case errors.Is(herr, errUnknownKind):
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', last_error=? WHERE id=?`, herr.Error(), id)
	case interrupted:
		// interrupted by shutdown: run again after restart, no attempt used
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', last_error=? WHERE id=?`, herr.Error(), id)
	case errors.Is(herr, ErrRetryLater):
		partials++
		if partials >= maxPartials {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', partials=?, last_error=? WHERE id=?`,
				partials, fmt.Sprintf("gave up after %d partial runs: %v", partials, herr), id)
		} else {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', partials=?, run_after=?, last_error=? WHERE id=?`,
				partials, q.stamp(now.Add(q.backoff(partials))), herr.Error(), id)
		}
	default:
		attempts++
		if attempts >= maxAttempts {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', attempts=?, last_error=? WHERE id=?`,
				attempts, herr.Error(), id)
		} else {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', attempts=?, run_after=?, last_error=? WHERE id=?`,
				attempts, q.stamp(now.Add(q.backoff(attempts))), herr.Error(), id)
		}
	}
	if err != nil {
		return fmt.Errorf("finish job %d (%s): %w", id, kind, err)
	}
	return nil
}
