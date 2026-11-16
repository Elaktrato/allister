package db

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// oldEntry is one entry as stored by the 00018 schema.
type oldEntry struct {
	itemID            int64
	status            string
	rating            any // nil (unrated) or an int
	comment           string
	started, finished any // nil or a calendar day
	updated           string
}

var runsMigrationEntries = []oldEntry{
	{1, "completed", 8, "c1", "2024-01-02", "2024-02-03", "2024-02-03T10:00:00Z"},
	{2, "active", nil, "", "2024-03-01", nil, "2024-03-05T11:00:00Z"},
	{3, "dropped", 0, "", nil, nil, "2024-04-01T12:00:00Z"},
	{4, "planned", nil, "", nil, nil, "2024-05-01T13:00:00Z"},
}

// seedRunsMigration fills a database at version 18 with real-shaped rows:
// several statuses, an unrated and a 0 rating, an active and an inactive
// progress key, and events.
func seedRunsMigration(t *testing.T, d *sql.DB) {
	t.Helper()
	for _, e := range runsMigrationEntries {
		mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id, progress_keys) VALUES(?, 'p', 'k', 's', ?, '["count"]')`, e.itemID, e.itemID)
		mustExec(t, d, `INSERT INTO entries(item_id, status, rating, comment, started_on, finished_on, updated_at) VALUES(?,?,?,?,?,?,?)`,
			e.itemID, e.status, e.rating, e.comment, e.started, e.finished, e.updated)
	}
	// item 1: the active key and a retained inactive key
	mustExec(t, d, `INSERT INTO entry_progress(item_id, key, value) VALUES(1, 'count', 12), (1, 'percent', 40.5)`)
	mustExec(t, d, `INSERT INTO entry_progress(item_id, key, value) VALUES(2, 'count', 3)`)
	mustExec(t, d, `INSERT INTO entry_events(item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin) VALUES
		(1, '2024-02-01T10:00:00Z', 'progress', 'count', 11, 12, '', '', 'manual'),
		(1, '2024-02-03T10:00:00Z', 'status', '', NULL, NULL, 'active', 'completed', 'manual'),
		(2, '2024-03-05T11:00:00Z', 'rating', '', NULL, 5, '', '', 'import')`)
}

func progressOf(t *testing.T, d *sql.DB, q string, arg int64) map[string]float64 {
	t.Helper()
	rows, err := d.Query(q, arg)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// sameNullable compares a scanned nullable column with the seeded value.
func sameNullable(got sql.NullString, want any) bool {
	if want == nil {
		return !got.Valid
	}
	return got.Valid && got.String == want
}

func sameRating(got sql.NullInt64, want any) bool {
	if want == nil {
		return !got.Valid
	}
	return got.Valid && got.Int64 == int64(want.(int))
}

var wantProgress = map[int64]map[string]float64{
	1: {"count": 12, "percent": 40.5},
	2: {"count": 3},
	3: {},
	4: {},
}

func TestRunsMigrationConvertsEntries(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 18)
	seedRunsMigration(t, old)
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion || latestVersion != 20 {
		t.Fatalf("version = %d, %v", v, err)
	}
	for _, tbl := range []string{"entry_runs", "run_progress", "unit_notes", "user_tags", "entry_user_tags", "lists", "list_items"} {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
	if tableCount(t, d, "entry_progress") != 0 {
		t.Error("entry_progress still exists")
	}
	for _, e := range runsMigrationEntries {
		if n := rowCount(t, d, `SELECT count(*) FROM entry_runs WHERE item_id = ?`, e.itemID); n != 1 {
			t.Fatalf("item %d: %d runs, want 1", e.itemID, n)
		}
		var runID int64
		var number int
		var status, review, created, updated string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := d.QueryRow(`SELECT id, number, status, rating, review, started_on, finished_on, created_at, updated_at FROM entry_runs WHERE item_id = ?`, e.itemID).
			Scan(&runID, &number, &status, &rating, &review, &started, &finished, &created, &updated); err != nil {
			t.Fatal(err)
		}
		if number != 1 || status != e.status || !sameRating(rating, e.rating) || review != "" ||
			!sameNullable(started, e.started) || !sameNullable(finished, e.finished) || updated != e.updated || created != e.updated {
			t.Errorf("item %d run: %d %s %v %q %v %v %s %s", e.itemID, number, status, rating, review, started, finished, created, updated)
		}
		var notes, added, entryUpdated string
		var display sql.NullInt64
		if err := d.QueryRow(`SELECT notes, added_at, updated_at, display_run_id FROM entries WHERE item_id = ?`, e.itemID).
			Scan(&notes, &added, &entryUpdated, &display); err != nil {
			t.Fatal(err)
		}
		if notes != e.comment || added != e.updated || entryUpdated != e.updated || !display.Valid || display.Int64 != runID {
			t.Errorf("item %d entry: %q %s %s %v (run %d)", e.itemID, notes, added, entryUpdated, display, runID)
		}
		if got := progressOf(t, d, `SELECT key, value FROM run_progress WHERE run_id = ?`, runID); !reflect.DeepEqual(got, wantProgress[e.itemID]) {
			t.Errorf("item %d run_progress = %v, want %v", e.itemID, got, wantProgress[e.itemID])
		}
		if got := progressOf(t, d, `SELECT key, value FROM display_progress WHERE item_id = ?`, e.itemID); !reflect.DeepEqual(got, wantProgress[e.itemID]) {
			t.Errorf("item %d display_progress = %v, want %v", e.itemID, got, wantProgress[e.itemID])
		}
		if n := rowCount(t, d, `SELECT count(*) FROM entry_events WHERE item_id = ? AND (run_id IS NULL OR run_id <> ?)`, e.itemID, runID); n != 0 {
			t.Errorf("item %d: %d events not on run 1", e.itemID, n)
		}
		var sStatus, sUpdated, sNotes, sReview string
		var sRating sql.NullInt64
		var sStarted, sFinished sql.NullString
		var sRun int64
		var sNumber, sRepeats int
		if err := d.QueryRow(`SELECT status, rating, started_on, finished_on, updated_at, notes, run_id, run_number, review, repeats FROM entry_state WHERE item_id = ?`, e.itemID).
			Scan(&sStatus, &sRating, &sStarted, &sFinished, &sUpdated, &sNotes, &sRun, &sNumber, &sReview, &sRepeats); err != nil {
			t.Fatal(err)
		}
		wantRepeats := 0
		if e.status == "completed" {
			wantRepeats = 1
		}
		if sStatus != e.status || !sameRating(sRating, e.rating) || !sameNullable(sStarted, e.started) || !sameNullable(sFinished, e.finished) ||
			sUpdated != e.updated || sNotes != e.comment || sRun != runID || sNumber != 1 || sReview != "" || sRepeats != wantRepeats {
			t.Errorf("item %d entry_state: %s %v %v %v %s %q %d %d %q %d", e.itemID, sStatus, sRating, sStarted, sFinished, sUpdated, sNotes, sRun, sNumber, sReview, sRepeats)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM entry_events`); n != 3 {
		t.Errorf("events = %d, want 3", n)
	}
	var origin string
	var newNum float64
	if err := d.QueryRow(`SELECT origin, new_num FROM entry_events WHERE event_type = 'rating'`).Scan(&origin, &newNum); err != nil || origin != "import" || newNum != 5 {
		t.Errorf("rating event: %s %v %v", origin, newNum, err)
	}
}

// TestRunsMigrationViews checks the view rules with more than one run: the
// newer of the two timestamps, the count of completed runs and the display
// run's progress.
func TestRunsMigrationViews(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// deleting a run sets display_run_id to NULL: the column is indexed
	if n := rowCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='entries_display_run' AND tbl_name='entries'`); n != 1 {
		t.Errorf("index entries_display_run missing")
	}
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1, 'p', 'k', 's', '1')`)
	mustExec(t, d, `INSERT INTO entries(item_id, notes, added_at, updated_at) VALUES(1, 'n', '2024-01-01T00:00:00Z', '2024-06-01T00:00:00Z')`)
	mustExec(t, d, `INSERT INTO entry_runs(id, item_id, number, status, created_at, updated_at) VALUES
		(10, 1, 1, 'completed', '2024-01-01T00:00:00Z', '2024-02-01T00:00:00Z'),
		(11, 1, 2, 'completed', '2024-03-01T00:00:00Z', '2024-04-01T00:00:00Z'),
		(12, 1, 3, 'active', '2024-05-01T00:00:00Z', '2024-07-01T00:00:00Z')`)
	mustExec(t, d, `INSERT INTO run_progress(run_id, key, value) VALUES(10, 'count', 12), (12, 'count', 2)`)
	mustExec(t, d, `UPDATE entries SET display_run_id = 12 WHERE item_id = 1`)
	var updated string
	var number, repeats int
	if err := d.QueryRow(`SELECT updated_at, run_number, repeats FROM entry_state WHERE item_id = 1`).Scan(&updated, &number, &repeats); err != nil {
		t.Fatal(err)
	}
	if updated != "2024-07-01T00:00:00Z" || number != 3 || repeats != 2 {
		t.Errorf("entry_state: %s %d %d", updated, number, repeats)
	}
	if got := progressOf(t, d, `SELECT key, value FROM display_progress WHERE item_id = ?`, 1); !reflect.DeepEqual(got, map[string]float64{"count": 2}) {
		t.Errorf("display_progress = %v", got)
	}
	mustExec(t, d, `UPDATE entries SET display_run_id = 10, updated_at = '2024-08-01T00:00:00Z' WHERE item_id = 1`)
	if err := d.QueryRow(`SELECT updated_at, run_number FROM entry_state WHERE item_id = 1`).Scan(&updated, &number); err != nil {
		t.Fatal(err)
	}
	if updated != "2024-08-01T00:00:00Z" || number != 1 {
		t.Errorf("entry_state after switch: %s %d", updated, number)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM entry_runs WHERE item_id = 1 AND number = 2`); n != 1 {
		t.Fatal("run 2 missing")
	}
	if _, err := d.Exec(`INSERT INTO entry_runs(item_id, number, status, created_at, updated_at) VALUES(1, 2, 'planned', 'x', 'x')`); err == nil {
		t.Error("a duplicate run number was accepted")
	}
	// removing the entry removes its runs and progress, the events stay with
	// their item and lose the run
	mustExec(t, d, `INSERT INTO entry_events(item_id, run_id, occurred_at, event_type) VALUES(1, 12, '2024-07-01T00:00:00Z', 'status')`)
	mustExec(t, d, `DELETE FROM entries WHERE item_id = 1`)
	if n := rowCount(t, d, `SELECT count(*) FROM entry_runs`) + rowCount(t, d, `SELECT count(*) FROM run_progress`); n != 0 {
		t.Errorf("%d run rows survived the entry", n)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM entry_events WHERE run_id IS NULL`); n != 1 {
		t.Errorf("events without run = %d, want 1", n)
	}
}

func TestRunsMigrationDown(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 18)
	seedRunsMigration(t, old)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := goose.DownTo(d, "migrations", 18); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"entry_runs", "run_progress", "unit_notes", "user_tags", "entry_user_tags", "lists", "list_items"} {
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists after down", tbl)
		}
	}
	for _, e := range runsMigrationEntries {
		var status, comment, updated string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := d.QueryRow(`SELECT status, rating, comment, started_on, finished_on, updated_at FROM entries WHERE item_id = ?`, e.itemID).
			Scan(&status, &rating, &comment, &started, &finished, &updated); err != nil {
			t.Fatal(err)
		}
		if status != e.status || !sameRating(rating, e.rating) || comment != e.comment ||
			!sameNullable(started, e.started) || !sameNullable(finished, e.finished) || updated != e.updated {
			t.Errorf("item %d after down: %s %v %q %v %v %s", e.itemID, status, rating, comment, started, finished, updated)
		}
		if got := progressOf(t, d, `SELECT key, value FROM entry_progress WHERE item_id = ?`, e.itemID); !reflect.DeepEqual(got, wantProgress[e.itemID]) {
			t.Errorf("item %d entry_progress = %v, want %v", e.itemID, got, wantProgress[e.itemID])
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM entry_events`); n != 3 {
		t.Errorf("events = %d, want 3", n)
	}
	// the 00018 schema works again: the old constraint and the cascade
	if _, err := d.Exec(`UPDATE entries SET status = 'bogus' WHERE item_id = 1`); err == nil {
		t.Error("the status check is gone")
	}
	mustExec(t, d, `DELETE FROM items WHERE id = 1`)
	if n := rowCount(t, d, `SELECT count(*) FROM entries WHERE item_id = 1`) + rowCount(t, d, `SELECT count(*) FROM entry_progress WHERE item_id = 1`); n != 0 {
		t.Errorf("rows of a deleted item survived: %d", n)
	}
	// and up again
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM entry_runs`); n != 3 {
		t.Errorf("runs after up again = %d, want 3", n)
	}
}

// Tag and list names are unique by name_key (the library's Unicode case
// folding, written by the library); name keeps the first-typed spelling and
// is not unique on its own (COLLATE NOCASE would fold ASCII only). The down
// migration removes both tables.
func TestRunsMigrationNameKeys(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tc := range []struct{ tbl, cols, vals, noKey string }{
		{"user_tags", "name, name_key, created_at", "?, ?, 'x'", `INSERT INTO user_tags(name, created_at) VALUES('n', 'x')`},
		{"lists", "name, name_key, created_at, updated_at", "?, ?, 'x', 'x'", `INSERT INTO lists(name, created_at, updated_at) VALUES('n', 'x', 'x')`},
	} {
		insert := `INSERT INTO ` + tc.tbl + `(` + tc.cols + `) VALUES(` + tc.vals + `)`
		mustExec(t, d, insert, "Äpfel", "äpfel")
		if _, err := d.Exec(insert, "äpfel", "äpfel"); err == nil {
			t.Errorf("%s: a second row with the same name_key was accepted", tc.tbl)
		}
		// the spelling alone is not unique: only the key decides
		mustExec(t, d, insert, "Äpfel", "other")
		if _, err := d.Exec(tc.noKey); err == nil || !strings.Contains(err.Error(), "name_key") {
			t.Errorf("%s: a row without name_key: %v", tc.tbl, err)
		}
	}
	if err := goose.DownTo(d, "migrations", 18); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"user_tags", "lists"} {
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists after down", tbl)
		}
	}
}
