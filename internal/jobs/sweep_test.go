package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestSyncPeopleTroubleTakesPartialPath(t *testing.T) {
	for _, perr := range []error{plugin.ErrRateLimited, plugin.ErrUnavailable} {
		e := newSyncEnv(t)
		clk := newClock()
		q := testQueue(t, e.d, clk)
		q.Register(KindSyncItem, SyncItem(e.deps))
		id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
		e.src.Errs = map[string]error{plugintest.OpPeople: perr}
		jid := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: id, PluginID: "tv"})
		clk.Advance(time.Hour)
		step(t, q)
		r := row(t, e.d, jid)
		if r.State != "pending" || r.Attempts != 0 || r.Partials != 1 {
			t.Fatalf("%v: %+v", perr, r)
		}
		if s := e.saved(t, id); s.SyncState != "partial" {
			t.Fatalf("state %q", s.SyncState)
		}
		// the cause stays in the chain and the log column
		err := e.run(id)
		if !errors.Is(err, perr) || !errors.Is(err, ErrRetryLater) {
			t.Fatalf("err = %v", err)
		}
		if r.LastError == "" {
			t.Fatal("cause not recorded")
		}
	}
}

func TestSyncOtherPeopleErrorStillConsumesAttempts(t *testing.T) {
	e := newSyncEnv(t)
	clk := newClock()
	q := testQueue(t, e.d, clk)
	q.Register(KindSyncItem, SyncItem(e.deps))
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	e.src.Errs = map[string]error{plugintest.OpPeople: errors.New("boom")}
	jid := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: id, PluginID: "tv"})
	clk.Advance(time.Hour)
	step(t, q)
	if r := row(t, e.d, jid); r.State != "pending" || r.Attempts != 1 || r.Partials != 0 {
		t.Fatalf("%+v", r)
	}
}

func liveSyncJobs(t *testing.T, e *syncEnv, itemID int64) int {
	t.Helper()
	var n int
	err := e.d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind=? AND json_extract(payload_json,'$.item_id')=?`,
		KindSyncItem, itemID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSweepEnqueuesSyncItemForPendingItems is the legacy "stuck anime only"
// sweep test over generic items.
func TestSweepEnqueuesSyncItemForPendingItems(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	q := testQueue(t, e.d, newClock())
	mk := func(extID, state string) int64 {
		id := e.saveID(t, "tvdata", extID, "")
		if err := e.lib.SetSyncState(ctx, id, state); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pendingNoJob := mk("1", "pending")
	partialNoJob := mk("2", "partial")
	done := mk("3", "done")
	pendingLive := mk("4", "pending")
	partialFailed := mk("5", "partial")
	partialRunning := mk("6", "partial")
	enqueue(t, q, KindSyncItem, SyncPayload{ItemID: pendingLive, PluginID: "tv"})
	fj := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: partialFailed, PluginID: "tv"})
	e.d.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, fj)
	rj := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: partialRunning, PluginID: "tv"})
	e.d.Exec(`UPDATE jobs SET state='running' WHERE id=?`, rj)
	// a live job of another kind does not count
	enqueue(t, q, "other", SyncPayload{ItemID: pendingNoJob, PluginID: "tv"})

	n, err := q.SweepSync(ctx, e.lib)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for id, want := range map[int64]int{pendingNoJob: 1, partialNoJob: 1, done: 0, pendingLive: 1, partialFailed: 2, partialRunning: 1} {
		if got := liveSyncJobs(t, e, id); got != want {
			t.Errorf("item %d: %d jobs, want %d", id, got, want)
		}
	}
	// idempotent
	if n, err := q.SweepSync(ctx, e.lib); err != nil || n != 0 {
		t.Fatalf("second sweep n=%d err=%v", n, err)
	}
}

func TestRecoverResetsRunningBeforeSweep(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	q := testQueue(t, e.d, newClock())
	id := e.save(t, "tvdata", "")
	jid := enqueue(t, q, KindSyncItem, SyncPayload{ItemID: id, PluginID: "tv"})
	e.d.Exec(`UPDATE jobs SET state='running' WHERE id=?`, jid)
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if r := row(t, e.d, jid); r.State != "pending" {
		t.Fatalf("%+v", r)
	}
	if n, err := q.SweepSync(ctx, e.lib); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if liveSyncJobs(t, e, id) != 1 {
		t.Fatal("duplicate job")
	}
}

func TestSweepCarriesPluginIDAndSkipsGatedLiveJobs(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	g := &gate{}
	g.set("tv", true) // disabled: its pending jobs stay live, so the sweep must not duplicate them
	q := testQueue(t, e.d, newClock(), WithPluginGate(g.allow))
	a, b := e.saveID(t, "tvdata", "1", ""), e.saveID(t, "tvdata", "2", "")
	book, err := e.lib.SaveItem(ctx, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: "books", ID: "9", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "B"}}}}, library.WithSyncState("partial"))
	if err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, KindSyncItem, SyncPayload{ItemID: a, PluginID: "tv"})
	n, err := q.SweepSync(ctx, e.lib)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if liveSyncJobs(t, e, a) != 1 || liveSyncJobs(t, e, b) != 1 || liveSyncJobs(t, e, book) != 1 {
		t.Fatal("sweep duplicated or missed jobs")
	}
	for id, want := range map[int64]string{b: "tv", book: "book"} {
		var pid string
		if err := e.d.QueryRow(`SELECT json_extract(payload_json,'$.plugin_id') FROM jobs
			WHERE json_extract(payload_json,'$.item_id') = ?`, id).Scan(&pid); err != nil || pid != want {
			t.Fatalf("item %d: plugin_id %q err=%v", id, pid, err)
		}
	}
}
