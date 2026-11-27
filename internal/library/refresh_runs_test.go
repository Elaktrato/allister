package library

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func refreshShow(src, id string) plugin.ItemData {
	return plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: src, ID: id, Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: "Show " + id}}}}
}

func addRefreshShow(t *testing.T, e *env, src, id string) int64 {
	t.Helper()
	got, err := e.lib.AddToList(context.Background(), "tv", "show", refreshShow(src, id), Entry{Status: Planned})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStartRefreshRunCountsSourcedEntries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := addRefreshShow(t, e, "tvdata", "1")
	b := addRefreshShow(t, e, "tvdata", "2")
	addRefreshShow(t, e, "manual", "m1")                         // manual: nothing to ask
	mustSave(t, e.lib, "tv", "show", refreshShow("tvdata", "3")) // no entry
	mustSave(t, e.lib, "book2", "book", bookData("x"))           // another plugin

	if n, err := e.lib.RefreshCandidates(ctx, "tv"); err != nil || n != 2 {
		t.Fatalf("candidates = %d, %v", n, err)
	}
	run, ids, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int64{a, b}) || run.Total != 2 || run.PluginID != "tv" || run.FinishedAt != nil || run.ID == 0 {
		t.Fatalf("run %+v ids %v", run, ids)
	}
	if _, _, err := e.lib.StartRefreshRun(ctx, "tv"); !errors.Is(err, ErrRefreshRunning) {
		t.Fatalf("second start: %v", err)
	}
	// another plugin is not blocked
	if r2, ids2, err := e.lib.StartRefreshRun(ctx, "book2"); err != nil || len(ids2) != 0 || r2.FinishedAt == nil {
		t.Fatalf("other plugin: %+v %v %v", r2, ids2, err)
	}
	// enqueued jobs keep the run open (the reconcile sees them)
	for _, id := range ids {
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES (?, ?, '2000-01-01T00:00:00Z')`,
			RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, "updated", "Show 1"); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.LatestRefreshRun(ctx, "tv")
	if err != nil || got == nil || got.Updated != 1 || got.FinishedAt != nil {
		t.Fatalf("latest %+v %v", got, err)
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, "failed", "Show 2"); err != nil {
		t.Fatal(err)
	}
	got, err = e.lib.LatestRefreshRun(ctx, "tv")
	if err != nil || got.FinishedAt == nil || got.Failed != 1 || !reflect.DeepEqual(got.FailedTitles, []string{"Show 2"}) {
		t.Fatalf("finished %+v %v", got, err)
	}
	// finished: a new run may start
	if _, _, err := e.lib.StartRefreshRun(ctx, "tv"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, "bogus", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad outcome: %v", err)
	}
}

func TestRecordRefreshKeepsFiftyFailedTitles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range 60 {
		addRefreshShow(t, e, "tvdata", fmt.Sprint(i))
	}
	run, _, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		if err := e.lib.RecordRefresh(ctx, run.ID, "failed", fmt.Sprint("T", i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.lib.RefreshRunByID(ctx, run.ID)
	if err != nil || got.Failed != 60 || len(got.FailedTitles) != 50 || got.FailedTitles[49] != "T49" || got.FinishedAt == nil {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestRefreshRunReconcilesWhenJobsAreGone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	addRefreshShow(t, e, "tvdata", "1")
	addRefreshShow(t, e, "tvdata", "2")
	run, ids, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	// before the jobs are enqueued the run stays open
	if got, _ := e.lib.LatestRefreshRun(ctx, "tv"); got.FinishedAt != nil {
		t.Fatalf("finished early: %+v", got)
	}
	// both jobs exist but the queue gave up on them: nothing is live
	for _, id := range ids {
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after, state) VALUES (?, ?, '2000-01-01T00:00:00Z', 'failed')`,
			RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, "unchanged", ""); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.LatestRefreshRun(ctx, "tv")
	if err != nil || got.FinishedAt == nil || got.Unchanged != 1 || got.Failed != 1 {
		t.Fatalf("%+v %v", got, err)
	}

	// a stopped run with no live job finishes even before every job exists
	addRefreshShow(t, e, "tvdata", "3")
	run2, _, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.StopRefreshRun(ctx, run2.ID, "credential"); err != nil {
		t.Fatal(err)
	}
	got, err = e.lib.LatestRefreshRun(ctx, "tv")
	if err != nil || got.FinishedAt == nil || got.Failed != 3 || got.Stopped != "credential" {
		t.Fatalf("%+v %v", got, err)
	}
}
