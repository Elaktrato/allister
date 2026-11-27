package library

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

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
	if !reflect.DeepEqual(ids, []RefreshTarget{{a, "show", "tvdata"}, {b, "show", "tvdata"}}) || run.Total != 2 || run.PluginID != "tv" || run.FinishedAt != nil || run.ID == 0 {
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
	for _, it := range ids {
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES (?, ?, '2000-01-01T00:00:00Z')`,
			RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, it.ID)); err != nil {
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
	for _, it := range ids {
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after, state) VALUES (?, ?, '2000-01-01T00:00:00Z', 'failed')`,
			RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, it.ID)); err != nil {
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

// A start interrupted while enqueuing (a crash) leaves fewer jobs than
// items and no stop reason: once its jobs are done and the grace period
// has passed, the run finishes with the items never asked counted failed.
func TestRefreshRunStrandedByInterruptedStartFinishes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"1", "2", "3"} {
		addRefreshShow(t, e, "tvdata", id)
	}
	run, ids, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	// only the first job made it into the queue, and it ran
	if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after, state) VALUES (?, ?, '2000-01-01T00:00:00Z', 'done')`,
		RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, ids[0].ID)); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, RefreshUpdated, ""); err != nil {
		t.Fatal(err)
	}
	// within the grace period it may still be enqueuing: it stays open
	if got, err := e.lib.LatestRefreshRun(ctx, "tv"); err != nil || got.FinishedAt != nil {
		t.Fatalf("finished within the grace period: %+v %v", got, err)
	}
	if _, _, err := e.lib.StartRefreshRun(ctx, "tv"); !errors.Is(err, ErrRefreshRunning) {
		t.Fatalf("start within the grace period: %v", err)
	}
	// after it the run finishes and a new one may start
	old := time.Now().UTC().Add(-refreshGrace - time.Minute).Format(time.RFC3339)
	if _, err := e.db.Exec(`UPDATE source_refresh_runs SET started_at = ? WHERE id = ?`, old, run.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.LatestRefreshRun(ctx, "tv")
	if err != nil || got.FinishedAt == nil || got.Updated != 1 || got.Failed != 2 {
		t.Fatalf("stranded run %+v %v", got, err)
	}
	if _, _, err := e.lib.StartRefreshRun(ctx, "tv"); err != nil {
		t.Fatalf("new start: %v", err)
	}

	// a live job keeps an old run open however long it waits
	e2 := newEnv(t)
	addRefreshShow(t, e2, "tvdata", "1")
	addRefreshShow(t, e2, "tvdata", "2")
	run2, ids2, err := e2.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e2.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES (?, ?, '2000-01-01T00:00:00Z')`,
		RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run2.ID, ids2[0].ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.db.Exec(`UPDATE source_refresh_runs SET started_at = ? WHERE id = ?`, old, run2.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e2.lib.LatestRefreshRun(ctx, "tv"); err != nil || got.FinishedAt != nil {
		t.Fatalf("finished with a live job: %+v %v", got, err)
	}
}

// A stop finishes the run at once, even while its jobs are still queued:
// the items not counted yet count as failed, and later records of the
// finished run change nothing.
func TestStopRefreshRunFinishesAtOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"1", "2", "3"} {
		addRefreshShow(t, e, "tvdata", id)
	}
	run, ids, err := e.lib.StartRefreshRun(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range ids {
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES (?, ?, '2000-01-01T00:00:00Z')`,
			RefreshJobKind, fmt.Sprintf(`{"run_id":%d,"item_id":%d,"plugin_id":"tv"}`, run.ID, it.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.lib.RecordRefresh(ctx, run.ID, RefreshFailed, "Show 1"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.StopRefreshRun(ctx, run.ID, RefreshStoppedCredential); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.RefreshRunByID(ctx, run.ID)
	if err != nil || got.FinishedAt == nil || got.Failed != 3 || got.Stopped != RefreshStoppedCredential ||
		!reflect.DeepEqual(got.FailedTitles, []string{"Show 1"}) {
		t.Fatalf("stopped run %+v %v", got, err)
	}
	// the queued jobs' records do not count again
	for _, o := range []string{RefreshUpdated, RefreshUnchanged, RefreshFailed} {
		if err := e.lib.RecordRefresh(ctx, run.ID, o, "late"); err != nil {
			t.Fatalf("record %s after the stop: %v", o, err)
		}
	}
	after, err := e.lib.RefreshRunByID(ctx, run.ID)
	if err != nil || refreshCountsOf(*after) != [4]int{3, 0, 0, 3} || !after.FinishedAt.Equal(*got.FinishedAt) ||
		!reflect.DeepEqual(after.FailedTitles, []string{"Show 1"}) {
		t.Fatalf("after the stop %+v %v", after, err)
	}
	// a second stop keeps the first reason; a new run may start at once
	if err := e.lib.StopRefreshRun(ctx, run.ID, "enqueue"); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.lib.RefreshRunByID(ctx, run.ID); r.Stopped != RefreshStoppedCredential {
		t.Fatalf("reason %q", r.Stopped)
	}
	if _, _, err := e.lib.StartRefreshRun(ctx, "tv"); err != nil {
		t.Fatalf("new start: %v", err)
	}
	if err := e.lib.RecordRefresh(ctx, run.ID+100, RefreshUpdated, ""); !errors.Is(err, ErrNoRefreshRun) {
		t.Fatalf("unknown run: %v", err)
	}
}

func refreshCountsOf(r RefreshRun) [4]int { return [4]int{r.Total, r.Updated, r.Unchanged, r.Failed} }
