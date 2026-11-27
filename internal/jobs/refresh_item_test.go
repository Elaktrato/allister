package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// refreshEnv is a syncEnv with a queue running refresh_item jobs and a
// record of the image syncs the handler asked for.
type refreshEnv struct {
	*syncEnv
	clk    *fakeClock
	q      *Queue
	g      *gate
	mu     sync.Mutex
	synced []int64
}

func newRefreshEnv(t *testing.T) *refreshEnv {
	t.Helper()
	e := &refreshEnv{syncEnv: newSyncEnv(t), clk: newClock(), g: &gate{}}
	e.q = testQueue(t, e.d, e.clk, WithPluginGate(e.g.allow))
	e.q.Register(KindRefreshItem, RefreshItem(e.deps, func(_ context.Context, pluginID string, itemID int64) error {
		if pluginID != "tv" {
			t.Errorf("image sync for plugin %q", pluginID)
		}
		e.mu.Lock()
		e.synced = append(e.synced, itemID)
		e.mu.Unlock()
		return nil
	}))
	return e
}

// listed stores a "tv" show with a list entry.
func (e *refreshEnv) listed(t *testing.T, sourceID, title string, mod func(*plugin.ItemData)) int64 {
	t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: sourceID, Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: title}}}}
	if mod != nil {
		mod(&d)
	}
	id, err := e.lib.AddToList(context.Background(), "tv", "show", d, library.Entry{Status: library.Planned})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// start starts a run and enqueues its jobs.
func (e *refreshEnv) start(t *testing.T) library.RefreshRun {
	t.Helper()
	run, ids, err := e.lib.StartRefreshRun(context.Background(), "tv")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		kind, p := RefreshJobFor("tv", run.ID)(id)
		if err := e.q.Enqueue(context.Background(), kind, p); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func (e *refreshEnv) drain(t *testing.T) {
	t.Helper()
	for step(t, e.q) {
	}
}

func (e *refreshEnv) latest(t *testing.T) library.RefreshRun {
	t.Helper()
	r, err := e.lib.LatestRefreshRun(context.Background(), "tv")
	if err != nil || r == nil {
		t.Fatalf("latest: %v %v", r, err)
	}
	return *r
}

func refreshCounts(r library.RefreshRun) [4]int {
	return [4]int{r.Total, r.Updated, r.Unchanged, r.Failed}
}

func TestRefreshJobFor(t *testing.T) {
	kind, p := RefreshJobFor("tv", 7)(9)
	if kind != "refresh_item" || KindRefreshItem != library.RefreshJobKind || p != (RefreshPayload{RunID: 7, ItemID: 9, PluginID: "tv"}) {
		t.Fatalf("%s %+v", kind, p)
	}
	b, _ := json.Marshal(p)
	if string(b) != `{"run_id":7,"item_id":9,"plugin_id":"tv"}` {
		t.Fatalf("payload %s", b)
	}
}

func TestRefreshItemOutcomes(t *testing.T) {
	e := newRefreshEnv(t)
	ctx := context.Background()
	credits := []plugin.Credit{{Role: "actor", Person: person("c1", "")}}
	// fresh: no genres, no cover, no credits
	fresh := e.listed(t, "1", "Fresh", nil)
	// same: already holds everything the source answers
	same := e.listed(t, "2", "Same", func(d *plugin.ItemData) {
		d.Genres = []string{"Drama"}
		d.CoverURL = "local.jpg"
	})
	if err := e.lib.SaveCredits(ctx, same, credits); err != nil {
		t.Fatal(err)
	}
	e.listed(t, "3", "Gone", nil)
	limited := e.listed(t, "4", "Limited", func(d *plugin.ItemData) {
		d.Genres = []string{"Drama"}
		d.CoverURL = "local.jpg"
	})
	if err := e.lib.SaveCredits(ctx, limited, credits); err != nil {
		t.Fatal(err)
	}
	run := e.start(t)
	if run.Total != 4 {
		t.Fatalf("total %d", run.Total)
	}

	e.src.Data = plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: "Other title"}}, CoverURL: e.srv.URL + "/ok/cover"}, Genres: []string{"Drama"}}
	e.src.Credits = credits
	// one job at a time, so each item sees the source it is meant to
	for range 2 {
		if !step(t, e.q) {
			t.Fatal("job did not run")
		}
	}
	e.src.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotFound}
	step(t, e.q)
	e.src.Errs = map[string]error{plugintest.OpDetails: plugin.ErrRateLimited}
	step(t, e.q)
	jobID := lastJob(t, e)
	if r := row(t, e.d, jobID); r.State != "pending" || r.Partials != 1 || r.Attempts != 0 {
		t.Fatalf("rate limit not retried later: %+v", r)
	}
	if got := e.latest(t); got.FinishedAt != nil || refreshCounts(got) != [4]int{4, 1, 1, 1} {
		t.Fatalf("mid run %+v", got)
	}
	e.src.Errs = nil
	e.clk.Advance(time.Hour)
	e.drain(t)

	got := e.latest(t)
	if got.FinishedAt == nil || refreshCounts(got) != [4]int{4, 1, 2, 1} || !reflect.DeepEqual(got.FailedTitles, []string{"Gone"}) {
		t.Fatalf("final %+v", got)
	}
	// fill only: the fresh item got the genre, the cover URL and the credits
	// but keeps its title; its image sync was asked for, the others' not
	it := e.saved(t, fresh)
	if !reflect.DeepEqual(it.Genres, []string{"Drama"}) || it.CoverPath != e.srv.URL+"/ok/cover" || it.Titles[0].Text != "Fresh" {
		t.Fatalf("fresh %+v", it)
	}
	if cs, err := e.lib.Credits(ctx, fresh); err != nil || len(cs) != 1 {
		t.Fatalf("credits %+v %v", cs, err)
	}
	if !reflect.DeepEqual(e.synced, []int64{fresh}) {
		t.Fatalf("synced %v", e.synced)
	}
	if it := e.saved(t, limited); it.CoverPath != "local.jpg" {
		t.Fatalf("limited %+v", it)
	}
}

func lastJob(t *testing.T, e *refreshEnv) int64 {
	t.Helper()
	var id int64
	if err := e.d.QueryRow(`SELECT MAX(id) FROM jobs WHERE kind = ?`, KindRefreshItem).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRefreshRunStopsOnNotConfigured(t *testing.T) {
	e := newRefreshEnv(t)
	e.listed(t, "1", "One", nil)
	e.listed(t, "2", "Two", nil)
	e.listed(t, "3", "Three", nil)
	e.start(t)
	e.src.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	e.drain(t)
	got := e.latest(t)
	if got.FinishedAt == nil || got.Stopped != library.RefreshStoppedCredential || refreshCounts(got) != [4]int{3, 0, 0, 3} {
		t.Fatalf("%+v", got)
	}
	if n := e.src.Calls(plugintest.OpDetails); n != 1 {
		t.Fatalf("source asked %d times after the credential stop", n)
	}
	var failed int
	if err := e.d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ? AND state <> 'done'`, KindRefreshItem).Scan(&failed); err != nil || failed != 0 {
		t.Fatalf("jobs not done: %d %v", failed, err)
	}
}

func TestRefreshRunSurvivesRecover(t *testing.T) {
	e := newRefreshEnv(t)
	for _, id := range []string{"1", "2", "3"} {
		e.listed(t, id, "T"+id, nil)
	}
	e.start(t)
	e.src.Data = plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", Kind: "show"}, Genres: []string{"Drama"}}
	step(t, e.q)
	// a crash while the second job runs: it stays running
	if _, err := e.d.Exec(`UPDATE jobs SET state = 'running' WHERE id = (SELECT MIN(id) FROM jobs WHERE state = 'pending')`); err != nil {
		t.Fatal(err)
	}
	// a restart: a new queue over the same database
	e.q = testQueue(t, e.d, e.clk)
	e.q.Register(KindRefreshItem, RefreshItem(e.deps, func(context.Context, string, int64) error { return nil }))
	if err := e.q.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got := e.latest(t)
	if got.FinishedAt == nil || refreshCounts(got) != [4]int{3, 3, 0, 0} {
		t.Fatalf("%+v", got)
	}
}

func TestRefreshRunPausedWhilePluginDisabled(t *testing.T) {
	e := newRefreshEnv(t)
	e.listed(t, "1", "One", nil)
	e.listed(t, "2", "Two", nil)
	e.start(t)
	e.g.set("tv", true)
	e.drain(t)
	if got := e.latest(t); got.FinishedAt != nil || got.Done() != 0 || e.src.Calls(plugintest.OpDetails) != 0 {
		t.Fatalf("ran while disabled: %+v", got)
	}
	e.g.set("tv", false)
	e.drain(t)
	if got := e.latest(t); got.FinishedAt == nil || refreshCounts(got) != [4]int{2, 0, 2, 0} {
		t.Fatalf("%+v", got)
	}
}

func TestRefreshItemEdgeCases(t *testing.T) {
	e := newRefreshEnv(t)
	ctx := context.Background()
	a := e.listed(t, "1", "One", nil)
	b := e.listed(t, "2", "Two", nil)
	e.listed(t, "3", "Three", nil)
	run := e.start(t)
	// a deleted item has nothing new; a bad answer fails the item
	if err := e.lib.DeleteItem(ctx, a); err != nil {
		t.Fatal(err)
	}
	step(t, e.q)
	e.src.Errs = map[string]error{plugintest.OpDetails: plugin.ErrBadResponse}
	step(t, e.q)
	// a people error other than a rate limit keeps the details
	e.src.Errs = map[string]error{plugintest.OpPeople: plugin.ErrBadResponse}
	e.src.Data = plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", Kind: "show"}, Genres: []string{"Drama"}}
	e.drain(t)
	got := e.latest(t)
	if got.FinishedAt == nil || refreshCounts(got) != [4]int{3, 1, 1, 1} || !reflect.DeepEqual(got.FailedTitles, []string{"Two"}) {
		t.Fatalf("%+v", got)
	}
	// a bad payload fails at once
	err := RefreshItem(e.deps, nil)(ctx, json.RawMessage(`{`))
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("bad payload: %v", err)
	}
	// a job of an unknown run does nothing
	p, _ := json.Marshal(RefreshPayload{RunID: run.ID + 100, ItemID: b, PluginID: "tv"})
	if err := RefreshItem(e.deps, nil)(ctx, p); err != nil {
		t.Fatalf("unknown run: %v", err)
	}
}
