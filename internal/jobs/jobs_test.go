package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testQueue(t *testing.T, d *sql.DB, clk *fakeClock, opts ...Option) *Queue {
	t.Helper()
	opts = append([]Option{WithClock(clk.Now), WithBackoff(func(n int) time.Duration { return time.Duration(n) * 10 * time.Second })}, opts...)
	return NewQueue(d, opts...)
}

type jobRow struct {
	State, LastError, RunAfter string
	Attempts, Partials         int
	FinishedAt                 sql.NullString
}

func row(t *testing.T, d *sql.DB, id int64) jobRow {
	t.Helper()
	var r jobRow
	err := d.QueryRow(`SELECT state, last_error, run_after, attempts, partials, finished_at FROM jobs WHERE id = ?`, id).
		Scan(&r.State, &r.LastError, &r.RunAfter, &r.Attempts, &r.Partials, &r.FinishedAt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func enqueue(t *testing.T, q *Queue, kind string, payload any) int64 {
	t.Helper()
	if err := q.Enqueue(context.Background(), kind, payload); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := q.db.QueryRow(`SELECT MAX(id) FROM jobs`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func step(t *testing.T, q *Queue) bool {
	t.Helper()
	ran, err := q.runOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ran
}

func TestJobSucceedsAndStoresPayload(t *testing.T) {
	d := openDB(t)
	q := testQueue(t, d, newClock())
	var got struct{ A int }
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { return json.Unmarshal(p, &got) })
	id := enqueue(t, q, "k", map[string]int{"A": 7})
	if !step(t, q) {
		t.Fatal("job not run")
	}
	if r := row(t, d, id); r.State != "done" || got.A != 7 {
		t.Fatalf("%+v %+v", r, got)
	}
	if step(t, q) {
		t.Fatal("done job ran again")
	}
}

func TestJobRetriesWithBackoff(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk)
	var calls int
	q.Register("k", func(ctx context.Context, p json.RawMessage) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("boom %d", calls)
		}
		return nil
	})
	id := enqueue(t, q, "k", nil)
	step(t, q)
	r := row(t, d, id)
	want := clk.Now().Add(10 * time.Second).UTC().Format(timeLayout)
	if r.State != "pending" || r.Attempts != 1 || r.LastError != "boom 1" || r.RunAfter != want {
		t.Fatalf("after 1st: %+v want run_after %s", r, want)
	}
	if step(t, q) {
		t.Fatal("ran before backoff elapsed")
	}
	clk.Advance(10 * time.Second)
	step(t, q)
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 2 || r.RunAfter != clk.Now().Add(20*time.Second).UTC().Format(timeLayout) {
		t.Fatalf("after 2nd: %+v", r)
	}
	clk.Advance(20 * time.Second)
	step(t, q)
	if r := row(t, d, id); r.State != "done" || r.Attempts != 2 || calls != 3 {
		t.Fatalf("final: %+v calls=%d", r, calls)
	}
}

func TestJobGivesUpAfterFiveAttempts(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk)
	var calls int
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { calls++; return errors.New("always") })
	id := enqueue(t, q, "k", nil)
	for i := 0; i < 5; i++ {
		clk.Advance(time.Hour)
		if !step(t, q) {
			t.Fatalf("attempt %d not run", i+1)
		}
	}
	r := row(t, d, id)
	if r.State != "failed" || r.Attempts != 5 || r.LastError != "always" || calls != 5 {
		t.Fatalf("%+v calls=%d", r, calls)
	}
	clk.Advance(time.Hour)
	if step(t, q) {
		t.Fatal("failed job ran again")
	}
}

func TestUnknownKindFails(t *testing.T) {
	d := openDB(t)
	q := testQueue(t, d, newClock())
	id := enqueue(t, q, "nope", nil)
	step(t, q)
	if r := row(t, d, id); r.State != "failed" || !strings.Contains(r.LastError, `unknown job kind "nope"`) {
		t.Fatalf("%+v", r)
	}
}

func TestHandlerPanicIsRecovered(t *testing.T) {
	d := openDB(t)
	q := testQueue(t, d, newClock())
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { panic("kaboom") })
	id := enqueue(t, q, "k", nil)
	step(t, q)
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 1 || !strings.Contains(r.LastError, "kaboom") {
		t.Fatalf("%+v", r)
	}
}

func TestRetryLaterDoesNotConsumeAttemptsButIsBounded(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk)
	var calls int
	q.Register("k", func(ctx context.Context, p json.RawMessage) error {
		calls++
		return fmt.Errorf("partial: %w", ErrRetryLater)
	})
	id := enqueue(t, q, "k", nil)
	clk.Advance(time.Hour)
	step(t, q)
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 0 || r.Partials != 1 {
		t.Fatalf("%+v", r)
	}
	for i := 1; i < maxPartials; i++ {
		clk.Advance(time.Hour)
		step(t, q)
	}
	r := row(t, d, id)
	if r.State != "failed" || r.Attempts != 0 || calls != maxPartials || !strings.Contains(r.LastError, "partial") {
		t.Fatalf("%+v calls=%d", r, calls)
	}
}

// TestPausedJobRunsAgainWithoutCounting: an ErrPaused run is a deliberate
// pause (the plugin gate holds the job while the plugin is off): the job
// goes back to pending without an attempt, a partial or a backoff, and
// pausing far beyond both caps never fails it.
func TestPausedJobRunsAgainWithoutCounting(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk)
	var calls int
	q.Register("k", func(ctx context.Context, p json.RawMessage) error {
		calls++
		if calls <= maxPartials+2 {
			return fmt.Errorf("plugin off: %w", ErrPaused)
		}
		return nil
	})
	id := enqueue(t, q, "k", nil)
	step(t, q)
	want := clk.Now().UTC().Format(timeLayout)
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 0 || r.Partials != 0 ||
		r.RunAfter != want || !strings.Contains(r.LastError, "plugin off") {
		t.Fatalf("after pause: %+v want run_after %s", r, want)
	}
	for i := 0; i < maxPartials+1; i++ { // beyond maxPartials and maxAttempts
		if !step(t, q) {
			t.Fatalf("pause %d: the job must run again without waiting", i+2)
		}
	}
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 0 || r.Partials != 0 {
		t.Fatalf("after %d pauses: %+v", maxPartials+2, r)
	}
	step(t, q)
	if r := row(t, d, id); r.State != "done" || r.Attempts != 0 || r.Partials != 0 || calls != maxPartials+3 {
		t.Fatalf("final: %+v calls=%d", r, calls)
	}
}

// TestDoneJobsArePruned: a finished job records when it finished, and
// Recover deletes done jobs older than 30 days — only those: failed,
// pending and running rows stay (running is reset to pending first).
func TestDoneJobsArePruned(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk)
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { return nil })
	q.Register("f", func(ctx context.Context, p json.RawMessage) error {
		return fmt.Errorf("never: %w", ErrPermanent)
	})
	stamp := func(dt time.Duration) string {
		return clk.Now().Add(dt).UTC().Format(timeLayout)
	}
	done := enqueue(t, q, "k", nil)
	step(t, q)
	if r := row(t, d, done); r.State != "done" || !r.FinishedAt.Valid || r.FinishedAt.String != stamp(0) {
		t.Fatalf("done row: %+v", r)
	}
	failed := enqueue(t, q, "f", nil)
	clk.Advance(time.Hour)
	step(t, q)
	if r := row(t, d, failed); r.State != "failed" || !r.FinishedAt.Valid {
		t.Fatalf("failed row: %+v", r)
	}
	pending := enqueue(t, q, "k", nil)
	if _, err := d.Exec(`INSERT INTO jobs(kind, payload_json, state) VALUES ('k', '{}', 'running')`); err != nil {
		t.Fatal(err)
	}
	// the done job aged past the cutoff
	if _, err := d.Exec(`UPDATE jobs SET finished_at = ? WHERE id = ?`, stamp(-31*24*time.Hour), done); err != nil {
		t.Fatal(err)
	}
	clk.Advance(24 * time.Hour)
	if err := q.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id = ?`, done).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old done job still there: %d %v", n, err)
	}
	if r := row(t, d, failed); r.State != "failed" {
		t.Fatalf("failed row pruned or changed: %+v", r)
	}
	if r := row(t, d, pending); r.State != "pending" || r.FinishedAt.Valid {
		t.Fatalf("pending row: %+v", r)
	}
	var running int
	if err := d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state = 'running'`).Scan(&running); err != nil || running != 0 {
		t.Fatalf("running rows after Recover: %d %v", running, err)
	}
}

func TestMinIntervalSpacesJobs(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk, WithMinInterval(2*time.Second))
	var n int
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { n++; return nil })
	enqueue(t, q, "k", nil)
	enqueue(t, q, "k", nil)
	if !step(t, q) {
		t.Fatal("first not run")
	}
	clk.Advance(time.Second)
	if step(t, q) || n != 1 {
		t.Fatalf("second ran too early (n=%d)", n)
	}
	clk.Advance(time.Second)
	if !step(t, q) || n != 2 {
		t.Fatalf("second not run (n=%d)", n)
	}
}

func TestClaimIsAtomicAcrossWorkers(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	var count atomic.Int64
	h := func(ctx context.Context, p json.RawMessage) error {
		count.Add(1)
		time.Sleep(time.Millisecond)
		return nil
	}
	q1, q2 := testQueue(t, d, clk), testQueue(t, d, clk)
	q1.Register("k", h)
	q2.Register("k", h)
	const total = 40
	for i := 0; i < total; i++ {
		enqueue(t, q1, "k", nil)
	}
	var wg sync.WaitGroup
	for _, q := range []*Queue{q1, q2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ran, err := q.runOnce(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !ran {
					return
				}
			}
		}()
	}
	wg.Wait()
	var done int
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state='done'`).Scan(&done)
	if count.Load() != total || done != total {
		t.Fatalf("handler ran %d times, %d done, want %d", count.Load(), done, total)
	}
}

func TestPendingJobsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(d)
	pendingID := enqueue(t, q, "k", map[string]int{"x": 1})
	// simulate a crash mid-job
	runningID := enqueue(t, q, "k", nil)
	if _, err := d.Exec(`UPDATE jobs SET state='running' WHERE id=?`, runningID); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if r := row(t, d, pendingID); r.State != "pending" {
		t.Fatalf("pending job lost: %+v", r)
	}
	ran := make(chan struct{}, 4)
	q = NewQueue(d, WithPollInterval(5*time.Millisecond))
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { ran <- struct{}{}; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { q.Run(ctx); close(finished) }()
	for i := 0; i < 2; i++ {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("jobs not run after restart")
		}
	}
	cancel()
	<-finished
	if r := row(t, d, runningID); r.State != "done" {
		t.Fatalf("crashed job: %+v", r)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	d := openDB(t)
	q := NewQueue(d, WithPollInterval(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { q.Run(ctx); close(finished) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestCancelledJobIsNotCountedAsAttempt(t *testing.T) {
	d := openDB(t)
	q := testQueue(t, d, newClock())
	ctx, cancel := context.WithCancel(context.Background())
	q.Register("k", func(ctx context.Context, p json.RawMessage) error { cancel(); return ctx.Err() })
	id := enqueue(t, q, "k", nil)
	if _, err := q.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if r := row(t, d, id); r.State != "pending" || r.Attempts != 0 {
		t.Fatalf("%+v", r)
	}
}
