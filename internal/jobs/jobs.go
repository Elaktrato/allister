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
	"strings"
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

// ErrPermanent, wrapped in a handler's error, means the job can never
// succeed (for example a payload that contradicts the stored data): it fails
// at once instead of retrying.
var ErrPermanent = errors.New("jobs: permanent failure")

// ErrPaused, wrapped in a handler's error, means the job is deliberately on
// hold: it goes back to pending without counting an attempt or a partial and
// without a backoff, because something else (the plugin gate) holds it until
// it may run again. Unlike ErrRetryLater it is not bounded; a handler must
// only pause when something will hold the job until then.
var ErrPaused = errors.New("jobs: paused")

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

// WithPluginGate makes the queue skip pending jobs whose JSON payload has a
// non-empty "plugin_id" for which allow returns false. Such a job stays
// pending (no attempt counted, run_after unchanged) and is claimed on a later
// poll once allow returns true; jobs without plugin_id are never blocked.
// allow is called once per distinct pending plugin_id at every claim.
func WithPluginGate(allow func(pluginID string) bool) Option {
	return func(q *Queue) { q.gate = allow }
}

// Queue is a persistent job queue.
type Queue struct {
	gate        func(string) bool
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

// EnqueueAt stores a job that may run no earlier than at (a self-rescheduling
// job's next round, for example).
func (q *Queue) EnqueueAt(ctx context.Context, kind string, payload any, at time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	_, err = q.db.ExecContext(ctx, `INSERT INTO jobs(kind, payload_json, run_after) VALUES (?,?,?)`,
		kind, string(b), q.stamp(at))
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
// donePruneDays is how long a done job row is kept before Recover prunes
// it (failed rows keep their last_error for debugging and stay).
const donePruneDays = 30

// Recover resets jobs left in state running by a crash to pending and
// prunes done jobs older than donePruneDays.
func (q *Queue) Recover(ctx context.Context) error {
	if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET state='pending' WHERE state='running'`); err != nil {
		return fmt.Errorf("reset running jobs: %w", err)
	}
	if _, err := q.db.ExecContext(ctx, `DELETE FROM jobs WHERE state='done' AND finished_at < ?`,
		q.stamp(q.now().Add(-donePruneDays*24*time.Hour))); err != nil {
		return fmt.Errorf("prune done jobs: %w", err)
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

	disabled, err := q.disabledPlugins(ctx)
	if err != nil {
		return false, err
	}
	var (
		id                 int64
		kind, payload      string
		attempts, partials int
	)
	// One atomic statement. The disabled plugin ids are bound parameters; a
	// job without plugin_id (NULL or empty) is never excluded.
	args := []any{q.stamp(now)}
	gateSQL := ""
	if len(disabled) > 0 {
		gateSQL = ` AND COALESCE(` + pluginIDSQL + `, '') NOT IN (?` +
			strings.Repeat(",?", len(disabled)-1) + `)`
		for _, id := range disabled {
			args = append(args, id)
		}
	}
	err = q.db.QueryRowContext(ctx, `UPDATE jobs SET state='running' WHERE id = (
		SELECT id FROM jobs WHERE state='pending' AND run_after <= ?`+gateSQL+` ORDER BY run_after, id LIMIT 1)
		RETURNING id, kind, payload_json, attempts, partials`, args...).
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

// pluginIDSQL is a job row's plugin_id, NULL when the payload is not valid
// JSON: json_extract raises an error on malformed JSON, and one such row
// must not stop the queue (it is claimed and fails on its own).
const pluginIDSQL = `CASE WHEN json_valid(payload_json) THEN json_extract(payload_json, '$.plugin_id') END`

// disabledPlugins returns the plugin ids of pending jobs that the gate refuses.
// A plugin whose first job is enqueued between this read and the claim can be
// claimed once; the gate is a pause for the common case, not a barrier.
func (q *Queue) disabledPlugins(ctx context.Context) ([]string, error) {
	if q.gate == nil {
		return nil, nil
	}
	rows, err := q.db.QueryContext(ctx, `SELECT DISTINCT `+pluginIDSQL+` FROM jobs
		WHERE state='pending' AND `+pluginIDSQL+` IS NOT NULL AND `+pluginIDSQL+` <> ''`)
	if err != nil {
		return nil, fmt.Errorf("read plugin ids: %w", err)
	}
	var ids []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read plugin ids: %w", err)
		}
		if s, ok := v.(string); ok {
			ids = append(ids, s)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin ids: %w", err)
	}
	var disabled []string
	for _, id := range ids {
		if !q.gate(id) {
			disabled = append(disabled, id)
		}
	}
	return disabled, nil
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
// context is already cancelled. Unknown kinds and ErrPermanent fail at
// once; when shutdown interrupted a handler the job goes back to pending
// without counting an attempt; ErrPaused also goes back to pending without
// counting anything or waiting (the plugin gate holds the job).
func (q *Queue) finish(id int64, kind string, attempts, partials int, herr error, interrupted bool) error {
	ctx := context.Background()
	now := q.now()
	var err error
	fin := q.stamp(now)
	switch {
	case herr == nil:
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='done', last_error='', finished_at=? WHERE id=?`, fin, id)
	case errors.Is(herr, errUnknownKind), errors.Is(herr, ErrPermanent):
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', last_error=?, finished_at=? WHERE id=?`, herr.Error(), fin, id)
	case interrupted:
		// interrupted by shutdown: run again after restart, no attempt used
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', last_error=? WHERE id=?`, herr.Error(), id)
	case errors.Is(herr, ErrPaused):
		// a deliberate pause: run again as soon as whatever paused it (the
		// plugin gate) lets the job go, with no attempt, partial or backoff
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', last_error=? WHERE id=?`, herr.Error(), id)
	case errors.Is(herr, ErrRetryLater):
		partials++
		if partials >= maxPartials {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', partials=?, last_error=?, finished_at=? WHERE id=?`,
				partials, fmt.Sprintf("gave up after %d partial runs: %v", partials, herr), fin, id)
		} else {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='pending', partials=?, run_after=?, last_error=? WHERE id=?`,
				partials, q.stamp(now.Add(q.backoff(partials))), herr.Error(), id)
		}
	default:
		attempts++
		if attempts >= maxAttempts {
			_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state='failed', attempts=?, last_error=?, finished_at=? WHERE id=?`,
				attempts, herr.Error(), fin, id)
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
