package db

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func seedManagingEvents(t *testing.T, path string) {
	t.Helper()
	old := openAt(t, path, 19)
	defer old.Close()
	for _, id := range []int64{1, 2} {
		mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id, progress_keys) VALUES(?, 'p', 'k', 's', ?, '["count"]')`, id, id)
		mustExec(t, old, `INSERT INTO entries(item_id, notes, added_at, updated_at) VALUES(?, '', 't', 't')`, id)
	}
	mustExec(t, old, `INSERT INTO entry_runs(id, item_id, number, status, created_at, updated_at) VALUES(10, 1, 1, 'active', 't', 't')`)
	mustExec(t, old, `INSERT INTO entry_events(id, item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin, run_id) VALUES
		(1, 1, '2024-02-01T10:00:00Z', 'progress', 'count', 1, 2, '', '', 'manual', 10),
		(2, 1, '2024-02-02T10:00:00Z', 'status', '', NULL, NULL, 'planned', 'active', 'import', NULL),
		(3, 2, '2024-02-03T10:00:00Z', 'rating', '', NULL, 7, '', '', 'restore', NULL)`)
}

func TestManagingMigration(t *testing.T) {
	path := tempPath(t)
	seedManagingEvents(t, path)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion || latestVersion != 23 {
		t.Fatalf("version = %d, %v", v, err)
	}
	if n := rowCount(t, d, `SELECT COUNT(*) FROM entry_events`); n != 3 {
		t.Fatalf("events = %d, want 3", n)
	}
	for id, want := range map[int]string{1: "manual", 2: "import", 3: "restore"} {
		var origin string
		var runID *int64
		if err := d.QueryRow(`SELECT origin, run_id FROM entry_events WHERE id = ?`, id).Scan(&origin, &runID); err != nil {
			t.Fatal(err)
		}
		if origin != want || (id == 1) != (runID != nil) {
			t.Fatalf("event %d = %s run %v", id, origin, runID)
		}
	}
	for _, ix := range []string{"entry_events_item", "entry_events_time", "entry_events_run"} {
		if n := rowCount(t, d, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, ix); n != 1 {
			t.Fatalf("index %s missing", ix)
		}
	}
	ins := `INSERT INTO entry_events(item_id, occurred_at, event_type, origin) VALUES(1, 't', 'status', ?)`
	mustExec(t, d, ins, "bulk")
	if _, err := d.Exec(ins, "other"); err == nil {
		t.Fatal("unknown origin accepted")
	}
	// run_id keeps ON DELETE SET NULL
	mustExec(t, d, `DELETE FROM entry_runs WHERE id = 10`)
	if n := rowCount(t, d, `SELECT COUNT(*) FROM entry_events WHERE id = 1 AND run_id IS NULL`); n != 1 {
		t.Fatal("run_id not set to NULL on run delete")
	}

	mustExec(t, d, `INSERT INTO dismissed_duplicates(item_a, item_b, dismissed_at) VALUES(1, 2, 't')`)
	if _, err := d.Exec(`INSERT INTO dismissed_duplicates(item_a, item_b, dismissed_at) VALUES(1, 2, 't')`); err == nil {
		t.Fatal("duplicate pair accepted")
	}
	if _, err := d.Exec(`INSERT INTO dismissed_duplicates(item_a, item_b, dismissed_at) VALUES(2, 1, 't')`); err == nil {
		t.Fatal("a > b accepted")
	}
	if _, err := d.Exec(`INSERT INTO dismissed_duplicates(item_a, item_b, dismissed_at) VALUES(1, 1, 't')`); err == nil {
		t.Fatal("a == b accepted")
	}
	if _, err := d.Exec(`INSERT INTO dismissed_duplicates(item_a, item_b, dismissed_at) VALUES(1, 99, 't')`); err == nil {
		t.Fatal("unknown item accepted")
	}

	alias := `INSERT INTO item_aliases(item_id, plugin_id, kind, source, source_id) VALUES(?, 'p', 'k', 's', 'x')`
	mustExec(t, d, alias, 1)
	if _, err := d.Exec(alias, 2); err == nil {
		t.Fatal("alias identity not unique")
	}
	if _, err := d.Exec(`INSERT INTO item_aliases(item_id, plugin_id, kind, source, source_id) VALUES(99, 'p', 'k', 's', 'y')`); err == nil {
		t.Fatal("alias of unknown item accepted")
	}
	// items.cover_source: empty for existing items, holds a cover's origin
	if n := rowCount(t, d, `SELECT COUNT(*) FROM items WHERE cover_source = ''`); n != 2 {
		t.Fatalf("cover_source default: %d rows empty, want 2", n)
	}
	mustExec(t, d, `UPDATE items SET cover_source = 'https://img.example/a.jpg' WHERE id = 2`)

	mustExec(t, d, `DELETE FROM items WHERE id = 1`)
	if n := rowCount(t, d, `SELECT COUNT(*) FROM item_aliases`) + rowCount(t, d, `SELECT COUNT(*) FROM dismissed_duplicates`); n != 0 {
		t.Fatalf("rows did not cascade: %d", n)
	}
}

func TestManagingMigrationDown(t *testing.T) {
	path := tempPath(t)
	seedManagingEvents(t, path)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO entry_events(item_id, occurred_at, event_type, origin) VALUES(1, 't', 'status', 'bulk')`)
	if err := goose.DownTo(d, "migrations", 19); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"dismissed_duplicates", "item_aliases"} {
		if n := rowCount(t, d, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, tbl); n != 0 {
			t.Fatalf("%s survived down", tbl)
		}
	}
	if n := rowCount(t, d, `SELECT COUNT(*) FROM pragma_table_info('items') WHERE name = 'cover_source'`); n != 0 {
		t.Fatal("items.cover_source survived down")
	}
	if n := rowCount(t, d, `SELECT COUNT(*) FROM items`); n != 2 {
		t.Fatalf("items after down = %d, want 2", n)
	}
	if n := rowCount(t, d, `SELECT COUNT(*) FROM entry_events`); n != 3 {
		t.Fatalf("events after down = %d, want 3 (bulk deleted)", n)
	}
	_, err = d.Exec(`INSERT INTO entry_events(item_id, occurred_at, event_type, origin) VALUES(1, 't', 'status', 'bulk')`)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("bulk accepted after down: %v", err)
	}
	for _, ix := range []string{"entry_events_item", "entry_events_time", "entry_events_run"} {
		if n := rowCount(t, d, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, ix); n != 1 {
			t.Fatalf("index %s missing after down", ix)
		}
	}
}
