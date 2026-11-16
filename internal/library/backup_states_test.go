package library

import (
	"encoding/json"
	"reflect"
	"testing"
)

// droppedRepeat builds, through public calls only, a show watched once
// (completed), started again and dropped: run 1 is the display run while
// run 2 is the latest. Both runs carry a unit note; the entry has a tag and
// sits in a list. The displayed run 1 is then edited (allowed: it stays
// completed).
func droppedRepeat(t *testing.T, e *env, id string) int64 {
	t.Helper()
	itemID := addShow(t, e.lib, id, fp(12), Entry{Status: Completed, Rating: ip(8), Notes: "first", Progress: map[string]float64{"episode": 12}})
	if err := e.lib.SetUnitNote(bg, mustEntry(t, e.lib, itemID).RunID, "episode", 4, "the twist"); err != nil {
		t.Fatal(err)
	}
	run2, err := e.lib.StartRun(bg, itemID, day(2026, 2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpdateRun(bg, run2.ID, RunUpdate{Status: Dropped, StartedOn: tp(day(2026, 2, 1))}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(bg, run2.ID, "episode", 1, "not again"); err != nil {
		t.Fatal(err)
	}
	if en := mustEntry(t, e.lib, itemID); en.RunNumber != 1 || en.Status != Completed {
		t.Fatalf("display run = %d %s, want the completed run 1", en.RunNumber, en.Status)
	}
	if err := e.lib.UpdateEntry(bg, Entry{ItemID: itemID, Status: Completed, Rating: ip(10), Notes: "first, edited",
		Progress: map[string]float64{"episode": 12}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetEntryTags(bg, itemID, []string{"Tried twice"}); err != nil {
		t.Fatal(err)
	}
	l, err := e.lib.CreateList(bg, "Repeats "+id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddToCustomList(bg, l.ID, itemID); err != nil {
		t.Fatal(err)
	}
	return itemID
}

// roundTrip backs up a's library, restores it (items, then lists) into an
// empty library and checks that everything is created and backs up equal.
func roundTrip(t *testing.T, a *env) *env {
	t.Helper()
	before, lists := collect(t, a.lib, 200), collectLists(t, a.lib)
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var items []BackupItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	b := newEnv(t)
	for i, r := range restoreAll(t, b.lib, items, keepCovers) {
		if r.Outcome != RestoreCreated {
			t.Fatalf("item %d: %+v", i, r)
		}
	}
	for _, bl := range lists {
		if _, left, err := b.lib.RestoreList(bg, bl); err != nil || left != 0 {
			t.Fatalf("list %q: left out %d, %v", bl.Name, left, err)
		}
	}
	if after := collect(t, b.lib, 200); !reflect.DeepEqual(before, after) {
		ja, _ := json.MarshalIndent(before, "", " ")
		jb, _ := json.MarshalIndent(after, "", " ")
		t.Fatalf("round trip differs:\nA: %s\nB: %s", ja, jb)
	}
	if after := collectLists(t, b.lib); !reflect.DeepEqual(lists, after) {
		t.Fatalf("lists differ:\nA: %+v\nB: %+v", lists, after)
	}
	return b
}

func TestBackupRoundTripsADroppedRepeat(t *testing.T) {
	a := newEnv(t)
	droppedRepeat(t, a, "1")
	b := roundTrip(t, a)
	var number int
	if err := b.db.QueryRow(`SELECT r.number FROM entries e JOIN entry_runs r ON r.id = e.display_run_id`).Scan(&number); err != nil || number != 1 {
		t.Fatalf("restored display run = %d, %v", number, err)
	}
}

// A past display run (the fallback behind a dropped repeat) can only be
// completed or dropped by a manual edit; an import keeps its status.
func TestPastDisplayRunKeepsItsFinishedStatus(t *testing.T) {
	e := newEnv(t)
	id := droppedRepeat(t, e, "1")
	for _, st := range []Status{Active, Paused, Planned} {
		wantInvalid(t, e.lib.UpdateEntry(bg, Entry{ItemID: id, Status: st, Progress: map[string]float64{"episode": 12}}, nil), "can only be completed or dropped")
		wantInvalid(t, e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: st, Progress: map[string]float64{"episode": 12}}), "can only be completed or dropped")
	}
	written, err := e.lib.ApplyImport(bg, id, Entry{Status: Active, Rating: ip(5)}, nil)
	if err != nil || !written {
		t.Fatalf("import = %v, %v", written, err)
	}
	runs, err := e.lib.Runs(bg, id)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if runs[1].Status != Completed || runs[1].Rating == nil || *runs[1].Rating != 5 || runs[0].Status != Dropped {
		t.Fatalf("after the import: run 1 %s rated %v, run 2 %s", runs[1].Status, runs[1].Rating, runs[0].Status)
	}
	// the latest run still takes any status
	single := addShow(t, e.lib, "2", fp(12), Entry{Status: Completed})
	if err := e.lib.UpdateEntry(bg, Entry{ItemID: single, Status: Active}, nil); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, e)
}

// A restore stores every past run as written: its status and progress
// values above a total that was lowered later are not refused.
func TestRestoreKeepsPastRunsAsStored(t *testing.T) {
	a := newEnv(t)
	droppedRepeat(t, a, "1")
	bi := collect(t, a.lib, 200)[0]
	bi.Entry.Runs[0].Status = Active
	bi.Entry.Runs[0].Progress["episode"] = 99
	b := newEnv(t)
	res := restoreAll(t, b.lib, []BackupItem{bi}, keepCovers)
	if res[0].Outcome != RestoreCreated {
		t.Fatalf("result = %+v", res[0])
	}
	runs, err := b.lib.Runs(bg, res[0].ItemID)
	if err != nil || len(runs) != 2 || runs[1].Status != Active || runs[0].Status != Dropped {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if got := collect(t, b.lib, 200)[0]; got.Entry.Runs[0].Progress["episode"] != 99 {
		t.Fatalf("past run progress = %v", got.Entry.Runs[0].Progress)
	}
}

// A unit note above a total lowered by a conversion still round-trips.
func TestConvertedItemWithUnitNoteAboveTheNewTotalRoundTrips(t *testing.T) {
	a := newEnv(t)
	d := bookWith("cv", fp(30))
	d.Source = "manual"
	d.CoverURL = ""
	id, err := a.lib.AddToList(bg, "book", "book", d, Entry{Status: Completed, Progress: map[string]float64{"pages": 30}}, WithSyncState("done"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lib.SetUnitNote(bg, mustEntry(t, a.lib, id).RunID, "pages", 25, "the good part"); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.ConvertToSource(bg, id, sourced("b9", fp(12))); err != nil {
		t.Fatal(err)
	}
	if en := mustEntry(t, a.lib, id); en.Progress["pages"] != 12 {
		t.Fatalf("progress not clamped: %v", en.Progress)
	}
	roundTrip(t, a)
}
