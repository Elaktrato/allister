package library

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
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

// refresh recomputes the item's display run, for runs seeded straight into
// the database.
func refresh(t *testing.T, e *env, itemID int64) {
	t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshDisplayRun(bg, tx, itemID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustRuns(t *testing.T, l *Library, itemID int64) []Run {
	t.Helper()
	runs, err := l.Runs(bg, itemID)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func mustStartRun(t *testing.T, l *Library, itemID int64) Run {
	t.Helper()
	r, err := l.StartRun(bg, itemID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// activityAround counts the manual activity of the days around now.
func activityAround(t *testing.T, l *Library) map[string]int {
	t.Helper()
	now := time.Now().UTC()
	got, err := l.ActivityDays(bg, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStartRun(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Completed, Rating: ip(8), Review: "first time",
		StartedOn: new(day(2024, 1, 1)), FinishedOn: new(day(2024, 2, 1)), Progress: map[string]float64{"episode": 12}})
	run1 := displayRunOf(t, e, id)
	// "today" is the calendar day of the given time in its own zone (the
	// configured time zone), not the UTC day
	today := time.Date(2026, 11, 16, 0, 30, 0, 0, time.FixedZone("east", 2*3600))
	r, err := l.StartRun(bg, id, today)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 || r.ID == run1 || r.Number != 2 || r.Status != Active || r.Rating == nil || *r.Rating != 8 || r.Review != "" ||
		r.StartedOn == nil || !r.StartedOn.Equal(day(2026, 11, 16)) || r.FinishedOn != nil || len(r.Progress) != 0 ||
		r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		t.Fatalf("new run = %+v", r)
	}
	if got := displayRunOf(t, e, id); got != r.ID {
		t.Fatalf("display run = %d, want the new run %d", got, r.ID)
	}
	en := mustEntry(t, l, id)
	if en.Status != Active || en.RunNumber != 2 || en.Repeats != 1 || en.Review != "" || len(en.Progress) != 0 {
		t.Fatalf("entry = %+v", en)
	}
	runs := mustRuns(t, l, id)
	if len(runs) != 2 || !reflect.DeepEqual(runs[0], r) {
		t.Fatalf("runs = %+v, want the new run first", runs)
	}
	if p := runs[1]; p.ID != run1 || p.Number != 1 || p.Status != Completed || p.Review != "first time" ||
		!p.StartedOn.Equal(day(2024, 1, 1)) || !p.FinishedOn.Equal(day(2024, 2, 1)) || !reflect.DeepEqual(p.Progress, map[string]float64{"episode": 12}) {
		t.Fatalf("run 1 = %+v", p)
	}
	// one status event completed -> active, manual, on the new run
	var runID int64
	var oldText, newText, origin string
	if err := e.db.QueryRow(`SELECT run_id, old_text, new_text, origin FROM entry_events WHERE item_id = ? AND event_type = 'status'`, id).
		Scan(&runID, &oldText, &newText, &origin); err != nil {
		t.Fatal(err)
	}
	if runID != r.ID || oldText != "completed" || newText != "active" || origin != "manual" {
		t.Fatalf("event = %d %s %s %s", runID, oldText, newText, origin)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ?`, id); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	// the display run is active now: no second start
	_, err = l.StartRun(bg, id, today)
	wantInvalid(t, err, "completed")
	if n := len(runsOf(t, e, id)); n != 2 {
		t.Fatalf("runs after refused start = %d", n)
	}
	// an explicit 0 is copied as 0, unrated stays unrated
	zero := addBook(t, l, "b0", fp(300), Entry{Status: Completed, Rating: ip(0)})
	if r := mustStartRun(t, l, zero); r.Rating == nil || *r.Rating != 0 {
		t.Fatalf("rating 0 not copied: %+v", r)
	}
	unrated := addBook(t, l, "bn", fp(300), Entry{Status: Completed})
	if r := mustStartRun(t, l, unrated); r.Rating != nil {
		t.Fatalf("unrated became %d", *r.Rating)
	}
	if _, err := l.StartRun(bg, 9999, today); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item: %v, want ErrNoEntry", err)
	}
	if runs, err := l.Runs(bg, 9999); err != nil || len(runs) != 0 {
		t.Fatalf("runs of an unknown item = %v %v", runs, err)
	}
}

func TestStartRunAfterDroppedRepeat(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Active})
	run1 := displayRunOf(t, e, id)
	setRun(t, e, run1, Completed, 7, "2024-01-01", "2024-02-01", map[string]float64{"episode": 12})
	seedRun(t, e, id, 2, Dropped, 3, "2024-03-01", nil, map[string]float64{"episode": 4})
	refresh(t, e, id)
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d, want run 1", got)
	}
	r := mustStartRun(t, l, id)
	if r.Number != 3 || r.Status != Active || r.Rating == nil || *r.Rating != 7 {
		t.Fatalf("new run = %+v, want run 3 with run 1's rating", r)
	}
	if got := displayRunOf(t, e, id); got != r.ID {
		t.Fatalf("display run = %d, want %d", got, r.ID)
	}
	if runs := runsOf(t, e, id); runs[1].Status != string(Dropped) {
		t.Fatalf("the dropped repeat changed: %+v", runs[1])
	}
}

func TestDeleteLatestRunUndoesStart(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Completed, Rating: ip(8), Progress: map[string]float64{"episode": 12}})
	run1 := displayRunOf(t, e, id)
	r := mustStartRun(t, l, id)
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	before := activityAround(t, l)
	if len(before) == 0 {
		t.Fatal("the +1 on the new run counts as activity")
	}
	if err := l.DeleteRun(bg, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d, want run 1 (%d)", got, run1)
	}
	en := mustEntry(t, l, id)
	if en.Status != Completed || en.RunNumber != 1 || en.Rating == nil || *en.Rating != 8 || en.Progress["episode"] != 12 {
		t.Fatalf("entry = %+v", en)
	}
	if runs := mustRuns(t, l, id); len(runs) != 1 || runs[0].ID != run1 {
		t.Fatalf("runs = %+v", runs)
	}
	// past activity never changes: the events stay, without a run
	if after := activityAround(t, l); !reflect.DeepEqual(after, before) {
		t.Fatalf("activity %v -> %v", before, after)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ? AND run_id IS NULL`, id); n != 2 {
		t.Fatalf("events without run = %d, want the start and the +1", n)
	}
	if n := count(t, e.db, `SELECT count(*) FROM run_progress WHERE run_id = ?`, r.ID); n != 0 {
		t.Fatalf("%d progress rows of the deleted run survived", n)
	}
}

func TestDeleteOnlyRunRefused(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "s1", fp(12), Entry{Status: Completed})
	run1 := displayRunOf(t, e, id)
	wantInvalid(t, e.lib.DeleteRun(bg, run1), "only run")
	if runs := runsOf(t, e, id); len(runs) != 1 || runs[0].ID != run1 {
		t.Fatalf("runs = %+v", runs)
	}
	if err := e.lib.DeleteRun(bg, 9999); !errors.Is(err, ErrNoRun) {
		t.Fatalf("stale run: %v, want ErrNoRun", err)
	}
}

func TestDeleteRunRenumbers(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Completed, Rating: ip(6)})
	run1 := displayRunOf(t, e, id)
	run2 := seedRun(t, e, id, 2, Completed, 8, nil, nil, map[string]float64{"episode": 12})
	run3 := seedRun(t, e, id, 3, Active, nil, nil, nil, map[string]float64{"episode": 2})
	run4 := seedRun(t, e, id, 4, Dropped, nil, nil, nil, nil)
	refresh(t, e, id)
	if err := l.DeleteRun(bg, run2); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, e, id)
	if len(runs) != 3 || runs[0].ID != run1 || runs[0].Number != 1 || runs[1].ID != run3 || runs[1].Number != 2 ||
		runs[2].ID != run4 || runs[2].Number != 3 {
		t.Fatalf("runs = %+v, want 1, 3, 4 numbered 1, 2, 3", runs)
	}
	// run 4 (now 3) is a dropped repeat: the display falls back to run 1,
	// the only completed run left
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d, want run 1 (%d)", got, run1)
	}
	if en := mustEntry(t, l, id); en.Repeats != 1 || en.RunNumber != 1 {
		t.Fatalf("entry = %+v", en)
	}
	if err := l.DeleteRun(bg, run2); !errors.Is(err, ErrNoRun) {
		t.Fatalf("deleting again: %v, want ErrNoRun", err)
	}
}

func TestUpdatePastRunStatusLimited(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "s1", fp(12), Entry{Status: Completed, Rating: ip(8)})
	run1 := displayRunOf(t, e, id)
	run2 := mustStartRun(t, l, id).ID
	for _, st := range []Status{Planned, Active, Paused} {
		wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: st, Rating: ip(8)}), "completed or dropped")
	}
	// the display run takes any status: dropping the repeat hands the
	// display back to run 1
	if err := l.UpdateRun(bg, run2, RunUpdate{Status: Dropped, Rating: ip(8)}); err != nil {
		t.Fatal(err)
	}
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d, want run 1", got)
	}
	// run 1 is the display run now but still a past run (not the latest):
	// making it active would leave an active run that nothing shows
	for _, st := range []Status{Planned, Active, Paused} {
		wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: st, Rating: ip(8)}), "completed or dropped")
	}
	if got := displayRunOf(t, e, id); got != run1 {
		t.Fatalf("display run = %d after refused edits, want run 1", got)
	}
	// a past run's dates, score, review and status
	if err := l.UpdateRun(bg, run2, RunUpdate{Status: Completed, Rating: ip(5), Review: "second look",
		StartedOn: new(day(2024, 3, 1)), FinishedOn: new(day(2024, 3, 9))}); err != nil {
		t.Fatal(err)
	}
	runs := mustRuns(t, l, id)
	if r := runs[0]; r.ID != run2 || r.Status != Completed || r.Rating == nil || *r.Rating != 5 || r.Review != "second look" ||
		!r.StartedOn.Equal(day(2024, 3, 1)) || !r.FinishedOn.Equal(day(2024, 3, 9)) {
		t.Fatalf("run 2 = %+v", r)
	}
	if got := displayRunOf(t, e, id); got != run2 {
		t.Fatalf("display run = %d, want the completed run 2", got)
	}
	// the changes are logged on run 2: start, drop, complete, rating
	type ev struct {
		Type, Old, New string
		OldNum, NewNum sql.NullFloat64
	}
	rows, err := e.db.Query(`SELECT event_type, old_text, new_text, old_num, new_num FROM entry_events WHERE run_id = ? AND origin = 'manual' ORDER BY id`, run2)
	if err != nil {
		t.Fatal(err)
	}
	var got []ev
	for rows.Next() {
		var x ev
		if err := rows.Scan(&x.Type, &x.Old, &x.New, &x.OldNum, &x.NewNum); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	if err := closeRows(rows); err != nil {
		t.Fatal(err)
	}
	f := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	want := []ev{
		{Type: "status", Old: "completed", New: "active"},
		{Type: "status", Old: "active", New: "dropped"},
		{Type: "status", Old: "dropped", New: "completed"},
		{Type: "rating", OldNum: f(8), NewNum: f(5)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ? AND run_id <> ?`, id, run2); n != 0 {
		t.Fatalf("%d events not on run 2", n)
	}
	// invalid input and stale ids
	wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: Completed, Rating: ip(11)}), "rating")
	wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: Completed, StartedOn: new(day(2024, 2, 1)), FinishedOn: new(day(2024, 1, 1))}), "before")
	wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: "bogus"}), "status")
	wantInvalid(t, l.UpdateRun(bg, run1, RunUpdate{Status: Completed, Review: "a\x00b"}), "review")
	if err := l.UpdateRun(bg, 9999, RunUpdate{Status: Completed}); !errors.Is(err, ErrNoRun) {
		t.Fatalf("stale run: %v, want ErrNoRun", err)
	}
}

func TestStatsRowsLoadPastRuns(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	rewatched := addShow(t, l, "s1", fp(12), Entry{Status: Completed, Rating: ip(8),
		FinishedOn: new(day(2024, 2, 1)), Progress: map[string]float64{"episode": 12}})
	first := displayRunOf(t, e, rewatched)
	run2 := mustStartRun(t, l, rewatched)
	plain := addShow(t, l, "s2", fp(6), Entry{Status: Completed, Progress: map[string]float64{"episode": 6}})
	q := Query{PluginID: "tv", Kind: "show"}

	rows, err := l.StatsRows(bg, q, "en")
	if err != nil || len(rows) != 2 {
		t.Fatalf("StatsRows = %d rows, %v", len(rows), err)
	}
	byID := map[int64]Row{}
	for _, r := range rows {
		byID[r.Item.ID] = r
	}
	r := byID[rewatched]
	if r.Entry.RunID != run2.ID || len(r.PastRuns) != 1 {
		t.Fatalf("rewatched row = display run %d, past %+v", r.Entry.RunID, r.PastRuns)
	}
	if p := r.PastRuns[0]; p.ID != first || p.Number != 1 || p.Status != Completed || p.Rating == nil || *p.Rating != 8 ||
		!p.FinishedOn.Equal(day(2024, 2, 1)) || !reflect.DeepEqual(p.Progress, map[string]float64{"episode": 12}) {
		t.Fatalf("past run = %+v", p)
	}
	if got := byID[plain].PastRuns; got != nil {
		t.Fatalf("an entry without repeats has no past runs, got %+v", got)
	}

	// the other loaders leave PastRuns nil
	pg, err := l.List(bg, q, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range pg.Rows {
		if r.PastRuns != nil {
			t.Fatalf("List filled PastRuns: %+v", r.PastRuns)
		}
	}

	// a status filter matches an entry through any of its runs
	completed := Completed
	q.Status = &completed
	rows, err = l.StatsRows(bg, q, "en")
	if err != nil || len(rows) != 2 {
		t.Fatalf("completed filter = %d rows, %v (the active repeat has a completed past run)", len(rows), err)
	}
	dropped := Dropped
	q.Status = &dropped
	if rows, err = l.StatsRows(bg, q, "en"); err != nil || len(rows) != 0 {
		t.Fatalf("dropped filter = %d rows, %v", len(rows), err)
	}
	bad := Status("bogus")
	q.Status = &bad
	if _, err = l.StatsRows(bg, q, "en"); err == nil {
		t.Fatal("an unknown status must be rejected")
	}
}

// completedWithAirings adds a completed show whose stored schedule is the
// given air times.
func completedWithAirings(t *testing.T, e *env, sourceID string, at ...time.Time) int64 {
	t.Helper()
	id := addShow(t, e.lib, sourceID, fp(12), Entry{Status: Completed, Progress: map[string]float64{"episode": 12}})
	var airings []Airing
	for i, a := range at {
		airings = append(airings, Airing{Number: i + 1, AirsAt: a})
	}
	if err := e.lib.SaveSchedule(bg, id, airings, time.Now()); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStartRunRefusedWhileAiring(t *testing.T) {
	e := newEnv(t)
	id := completedWithAirings(t, e, "air1", day(2000, 1, 1), day(2999, 1, 1))
	_, err := e.lib.StartRun(bg, id, time.Now())
	if !errors.Is(err, ErrStillAiring) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrStillAiring wrapping ErrInvalid", err)
	}
	if got := err.Error(); got != "invalid: still airing: it has upcoming releases" {
		t.Fatalf("message %q", got)
	}
	if n := len(runsOf(t, e, id)); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
}

func TestStartRunAllowedAfterAirings(t *testing.T) {
	e := newEnv(t)
	id := completedWithAirings(t, e, "air2", day(2000, 1, 1), day(2000, 2, 1))
	if _, err := e.lib.StartRun(bg, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	none := completedWithAirings(t, e, "air3")
	if _, err := e.lib.StartRun(bg, none, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestStillAiring(t *testing.T) {
	e := newEnv(t)
	future := completedWithAirings(t, e, "f", day(2999, 1, 1))
	past := completedWithAirings(t, e, "p", day(2000, 1, 1))
	none := completedWithAirings(t, e, "n")
	for id, want := range map[int64]bool{future: true, past: false, none: false} {
		got, err := e.lib.StillAiring(bg, id)
		if err != nil || got != want {
			t.Fatalf("item %d: %v %v, want %v", id, got, err, want)
		}
	}
}
