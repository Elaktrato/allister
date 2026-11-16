package library

import (
	"database/sql"
	"reflect"
	"testing"
)

// runRow is one stored run with its progress, as the tests compare it.
type runRow struct {
	ID                int64
	Number            int
	Status            string
	Rating            sql.NullInt64
	Review            string
	Started, Finished sql.NullString
	Progress          map[string]float64
}

func runsOf(t *testing.T, e *env, itemID int64) []runRow {
	t.Helper()
	rows, err := e.db.Query(`SELECT id, number, status, rating, review, started_on, finished_on FROM entry_runs WHERE item_id = ? ORDER BY number`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	var out []runRow
	for rows.Next() {
		var r runRow
		if err := rows.Scan(&r.ID, &r.Number, &r.Status, &r.Rating, &r.Review, &r.Started, &r.Finished); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := closeRows(rows); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		out[i].Progress = map[string]float64{}
		prows, err := e.db.Query(`SELECT key, value FROM run_progress WHERE run_id = ?`, out[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		for prows.Next() {
			var k string
			var v float64
			if err := prows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			out[i].Progress[k] = v
		}
		if err := closeRows(prows); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func displayRunOf(t *testing.T, e *env, itemID int64) int64 {
	t.Helper()
	var id sql.NullInt64
	if err := e.db.QueryRow(`SELECT display_run_id FROM entries WHERE item_id = ?`, itemID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if !id.Valid {
		t.Fatalf("item %d has no display run", itemID)
	}
	return id.Int64
}

// seedRun inserts a run straight into the database; the caller points the
// display run at it when the test needs that.
func seedRun(t *testing.T, e *env, itemID int64, number int, status Status, rating any, started, finished any, progress map[string]float64) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO entry_runs(item_id, number, status, rating, started_on, finished_on, created_at, updated_at)
		VALUES (?,?,?,?,?,?,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`, itemID, number, string(status), rating, started, finished)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range progress {
		if _, err := e.db.Exec(`INSERT INTO run_progress(run_id, key, value) VALUES (?,?,?)`, id, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// setRun rewrites run 1 of an item straight in the database.
func setRun(t *testing.T, e *env, runID int64, status Status, rating any, started, finished any, progress map[string]float64) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE entry_runs SET status = ?, rating = ?, started_on = ?, finished_on = ? WHERE id = ?`,
		string(status), rating, started, finished, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM run_progress WHERE run_id = ?`, runID); err != nil {
		t.Fatal(err)
	}
	for k, v := range progress {
		if _, err := e.db.Exec(`INSERT INTO run_progress(run_id, key, value) VALUES (?,?,?)`, runID, k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func setDisplayRun(t *testing.T, e *env, itemID, runID int64) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE entries SET display_run_id = ? WHERE item_id = ?`, runID, itemID); err != nil {
		t.Fatal(err)
	}
}

// wantDisplayRun is the spec's display run rule, recomputed in Go: the latest
// run, except that a dropped repeat (number > 1) gives way to the latest
// completed run when there is one.
func wantDisplayRun(runs []runRow) int64 {
	if len(runs) == 0 {
		return 0
	}
	latest := runs[len(runs)-1]
	if latest.Number > 1 && latest.Status == string(Dropped) {
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].Status == string(Completed) {
				return runs[i].ID
			}
		}
	}
	return latest.ID
}

func TestNewEntryCreatesRunOne(t *testing.T) {
	e := newEnv(t)
	check := func(id int64) {
		t.Helper()
		runs := runsOf(t, e, id)
		if len(runs) != 1 || runs[0].Number != 1 {
			t.Fatalf("item %d runs = %+v, want one run 1", id, runs)
		}
		if got := displayRunOf(t, e, id); got != runs[0].ID {
			t.Fatalf("item %d display run = %d, want %d", id, got, runs[0].ID)
		}
		en := mustEntry(t, e.lib, id)
		if en.RunID != runs[0].ID || en.RunNumber != 1 {
			t.Fatalf("item %d entry run fields: %+v", id, en)
		}
	}
	added := addBook(t, e.lib, "1", fp(300), Entry{Status: Active, Rating: ip(0), Notes: "n1", Progress: map[string]float64{"pages": 12}})
	check(added)
	if en := mustEntry(t, e.lib, added); en.Repeats != 0 {
		t.Fatalf("an active run 1 is no completion: %+v", en)
	}
	runs := runsOf(t, e, added)
	if runs[0].Status != string(Active) || !runs[0].Rating.Valid || runs[0].Rating.Int64 != 0 || runs[0].Progress["pages"] != 12 {
		t.Fatalf("run 1 = %+v", runs[0])
	}
	var notes, addedAt string
	if err := e.db.QueryRow(`SELECT notes, added_at FROM entries WHERE item_id = ?`, added).Scan(&notes, &addedAt); err != nil || notes != "n1" || addedAt == "" {
		t.Fatalf("entry row: %q %q %v", notes, addedAt, err)
	}
	// creating through UpsertEntry and CreateEntry makes run 1 as well
	upserted := mustSave(t, e.lib, "book", "book", bookWith("2", fp(300)))
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: upserted, Status: Planned}); err != nil {
		t.Fatal(err)
	}
	check(upserted)
	created := mustSave(t, e.lib, "book", "book", bookWith("3", fp(300)))
	if err := e.lib.CreateEntry(bg, Entry{ItemID: created, Status: Completed, Rating: ip(7)}); err != nil {
		t.Fatal(err)
	}
	check(created)
	if en := mustEntry(t, e.lib, created); en.Repeats != 1 {
		t.Fatalf("a completed run 1 counts as one completion: %+v", en)
	}
	// saving again never adds a run
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: added, Status: Completed}); err != nil {
		t.Fatal(err)
	}
	check(added)
}

func TestUpsertWritesDisplayRun(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "s1", fp(12), Entry{Status: Active, Progress: map[string]float64{"episode": 2}})
	run1 := displayRunOf(t, e, id)
	setRun(t, e, run1, Completed, 8, "2024-01-01", "2024-02-01", map[string]float64{"episode": 12})
	run2 := seedRun(t, e, id, 2, Active, 8, "2024-03-01", nil, map[string]float64{"episode": 1})
	setDisplayRun(t, e, id, run2)
	before := runsOf(t, e, id)

	if err := e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Paused, Rating: ip(6), Notes: "entry notes",
		StartedOn: new(day(2024, 3, 2)), Progress: map[string]float64{"episode": 4}}); err != nil {
		t.Fatal(err)
	}
	after := runsOf(t, e, id)
	if len(after) != 2 {
		t.Fatalf("runs = %+v", after)
	}
	if !reflect.DeepEqual(after[0], before[0]) {
		t.Fatalf("run 1 changed: %+v -> %+v", before[0], after[0])
	}
	r2 := after[1]
	if r2.ID != run2 || r2.Status != string(Paused) || r2.Rating.Int64 != 6 || r2.Started.String != "2024-03-02" || r2.Finished.Valid || r2.Progress["episode"] != 4 {
		t.Fatalf("run 2 = %+v", r2)
	}
	en := mustEntry(t, e.lib, id)
	if en.Notes != "entry notes" || en.Status != Paused || en.RunNumber != 2 || en.RunID != run2 || en.Repeats != 1 || en.Progress["episode"] != 4 {
		t.Fatalf("entry = %+v", en)
	}
	// the change is logged on run 2
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ? AND (run_id IS NULL OR run_id <> ?)`, id, run2); n != 0 {
		t.Fatalf("%d events not on run 2", n)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ?`, id); n != 3 {
		t.Fatalf("events = %d, want progress, status and rating", n)
	}
}

func TestDisplayRunSkipsDroppedRepeat(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "s1", fp(12), Entry{Status: Active})
	run1 := displayRunOf(t, e, id)
	setRun(t, e, run1, Completed, 8, "2024-01-01", "2024-02-01", map[string]float64{"episode": 10})
	run2 := seedRun(t, e, id, 2, Dropped, 5, "2024-03-01", nil, map[string]float64{"episode": 3})
	// a write recomputes the display run: the dropped repeat gives way to
	// the completed run 1
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Completed, Rating: ip(8),
		StartedOn: new(day(2024, 1, 1)), FinishedOn: new(day(2024, 2, 1)), Progress: map[string]float64{"episode": 10}}); err != nil {
		t.Fatal(err)
	}
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d, want run 1 (%d)", got, run1)
	}
	en := mustEntry(t, e.lib, id)
	if en.Status != Completed || en.Rating == nil || *en.Rating != 8 || en.RunNumber != 1 ||
		!en.StartedOn.Equal(day(2024, 1, 1)) || !en.FinishedOn.Equal(day(2024, 2, 1)) || en.Progress["episode"] != 10 {
		t.Fatalf("entry = %+v", en)
	}
	run2Before := runsOf(t, e, id)[1]
	if err := e.lib.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, e, id)
	if runs[0].Progress["episode"] != 11 || runs[0].Status != string(Completed) {
		t.Fatalf("run 1 after +1 = %+v", runs[0])
	}
	if !reflect.DeepEqual(runs[1], run2Before) || runs[1].ID != run2 {
		t.Fatalf("run 2 changed: %+v -> %+v", run2Before, runs[1])
	}
	var evRun sql.NullInt64
	if err := e.db.QueryRow(`SELECT run_id FROM entry_events WHERE item_id = ? AND event_type = 'progress' ORDER BY id DESC LIMIT 1`, id).Scan(&evRun); err != nil || evRun.Int64 != run1 {
		t.Fatalf("+1 event run = %v %v, want %d", evRun, err, run1)
	}
	// with no completed run before it, a dropped repeat is shown itself
	other := addShow(t, e.lib, "s2", fp(12), Entry{Status: Dropped})
	o2 := seedRun(t, e, other, 2, Dropped, nil, nil, nil, nil)
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: other, Status: Dropped}); err != nil {
		t.Fatal(err)
	}
	if got := displayRunOf(t, e, other); got != o2 {
		t.Fatalf("display run = %d, want the latest run %d", got, o2)
	}
}

func TestApplyImportNeverAddsRun(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "s1", fp(12), Entry{Status: Active, Notes: "mine"})
	run1 := displayRunOf(t, e, id)
	setRun(t, e, run1, Completed, 8, "2024-01-01", "2024-02-01", map[string]float64{"episode": 12})
	run2 := seedRun(t, e, id, 2, Active, nil, "2024-03-01", nil, map[string]float64{"episode": 2})
	setDisplayRun(t, e, id, run2)
	before := runsOf(t, e, id)
	ok, err := e.lib.ApplyImport(bg, id, Entry{Status: Completed, Rating: ip(9), Progress: map[string]float64{"episode": 12}}, nil)
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	after := runsOf(t, e, id)
	if len(after) != 2 {
		t.Fatalf("runs = %+v", after)
	}
	if !reflect.DeepEqual(after[0], before[0]) {
		t.Fatalf("run 1 changed: %+v -> %+v", before[0], after[0])
	}
	if r := after[1]; r.Status != string(Completed) || r.Rating.Int64 != 9 || r.Progress["episode"] != 12 || r.Started.String != "2024-03-01" {
		t.Fatalf("run 2 = %+v", r)
	}
	if en := mustEntry(t, e.lib, id); en.Notes != "mine" || en.Repeats != 2 || en.RunNumber != 2 {
		t.Fatalf("entry = %+v", en)
	}
}

func TestDisplayRunInvariant(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	a := addShow(t, l, "a", fp(12), Entry{Status: Completed, Rating: ip(7)})
	b := addShow(t, l, "b", fp(12), Entry{Status: Completed, Rating: ip(9)})
	c := addShow(t, l, "c", fp(12), Entry{Status: Dropped})
	d := addBook(t, l, "d", fp(300), Entry{Status: Planned})
	// extra runs straight in the database, then library writes on top
	seedRun(t, e, a, 2, Active, 7, "2024-03-01", nil, nil)
	seedRun(t, e, b, 2, Completed, 8, nil, nil, nil)
	seedRun(t, e, b, 3, Paused, nil, nil, nil, nil)
	seedRun(t, e, c, 2, Completed, 6, nil, nil, nil)
	seedRun(t, e, c, 3, Dropped, nil, nil, nil, nil)
	for _, id := range []int64{a, b, c} {
		tx, err := e.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := refreshDisplayRun(bg, tx, id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	steps := []func() error{
		func() error { return l.Increment(bg, a) },
		// dropping the repeat run hands the display back to run 1
		func() error { return l.UpsertEntry(bg, Entry{ItemID: a, Status: Dropped}) },
		func() error { return l.Increment(bg, a) },
		func() error { return l.Increment(bg, b) },
		func() error {
			return l.UpsertEntry(bg, Entry{ItemID: b, Status: Active, Progress: map[string]float64{"episode": 3}})
		},
		func() error {
			_, err := l.ApplyImport(bg, c, Entry{Status: Completed, Rating: ip(5)}, nil)
			return err
		},
		func() error { return l.Increment(bg, d) },
		func() error { return l.ClearProgress(bg, d, "pages") },
		func() error { return l.RemoveEntry(bg, d) },
		func() error { return l.UpsertEntry(bg, Entry{ItemID: d, Status: Active}) },
		func() error { return l.UpdateEntry(bg, Entry{ItemID: d, Status: Paused}, nil) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		for _, id := range []int64{a, b, c, d} {
			if count(t, e.db, `SELECT count(*) FROM entries WHERE item_id = ?`, id) == 0 {
				continue // removed
			}
			if got, want := displayRunOf(t, e, id), wantDisplayRun(runsOf(t, e, id)); got != want {
				t.Fatalf("step %d item %d: display run %d, want %d (%+v)", i, id, got, want, runsOf(t, e, id))
			}
		}
	}
	// the dropped repeat of a left run 1 (completed) on display
	if en := mustEntry(t, l, a); en.RunNumber != 1 || en.Status != Completed || en.Rating == nil || *en.Rating != 7 {
		t.Fatalf("a = %+v", en)
	}
	if runs := runsOf(t, e, a); runs[1].Status != string(Dropped) {
		t.Fatalf("a run 2 = %+v", runs[1])
	}
	// c: run 3 dropped, the import completed the display run 2 and left run 3
	if runs := runsOf(t, e, c); runs[1].Rating.Int64 != 5 || runs[2].Status != string(Dropped) {
		t.Fatalf("c runs = %+v", runs)
	}
}
