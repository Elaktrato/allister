package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// 00021 keeps existing series and parts (main parts, no data) and Down keeps
// them too.
func TestSeriesDataMigrationKeepsSeries(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 20)
	mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'book','book','books','1'), (2,'book','book','books','2')`)
	mustExec(t, old, `INSERT INTO series(id, uid, plugin_id, kind, title, created_at, updated_at) VALUES(7,'u','book','book','S','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	mustExec(t, old, `INSERT INTO series_parts(series_id, item_id, position) VALUES(7,1,1), (7,2,2)`)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n := rowCount(t, d, `SELECT count(*) FROM series WHERE id = 7 AND finished = 0 AND parts_total IS NULL AND linked_item_id IS NULL AND parent_id IS NULL AND cover_path IS NULL AND description IS NULL`); n != 1 {
		t.Fatalf("series row = %d", n)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM series_parts WHERE series_id = 7 AND spin_off = 0`); n != 2 {
		t.Fatalf("parts = %d", n)
	}
	if tableCount(t, d, "series_fields") != 1 {
		t.Fatal("series_fields missing")
	}
	// the new references: deleting the linked item or the parent clears them
	mustExec(t, d, `INSERT INTO series(id, uid, plugin_id, kind, title, created_at, updated_at) VALUES(8,'v','book','book','P','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(3,'book','book','books','3')`)
	mustExec(t, d, `UPDATE series SET linked_item_id = 3, parent_id = 8 WHERE id = 7`)
	mustExec(t, d, `INSERT INTO series_fields(series_id, key, vtype, text, seq) VALUES(7, 'publisher', 'text', 'Acme', 0)`)
	mustExec(t, d, `DELETE FROM items WHERE id = 3`)
	mustExec(t, d, `DELETE FROM series WHERE id = 8`)
	if n := rowCount(t, d, `SELECT count(*) FROM series WHERE id = 7 AND linked_item_id IS NULL AND parent_id IS NULL`); n != 1 {
		t.Fatalf("references not cleared: %d", n)
	}
	// a spin-off part: both blocks start at 1
	mustExec(t, d, `UPDATE series_parts SET spin_off = 1, position = 1 WHERE item_id = 2`)
	if err := goose.DownTo(d, "migrations", 20); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM series_parts WHERE series_id = 7 AND ((item_id = 1 AND position = 1) OR (item_id = 2 AND position = 2))`); n != 2 {
		t.Fatalf("parts after down = %d", n)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM series WHERE id = 7 AND title = 'S'`); n != 1 {
		t.Fatalf("series after down = %d", n)
	}
	if tableCount(t, d, "series_fields") != 0 {
		t.Fatal("series_fields kept")
	}
	if err := goose.UpTo(d, "migrations", 21); err != nil {
		t.Fatal(err)
	}
}

// A database at main's version 20 (runs, tags, lists, aliases, a series with
// parts) upgrades to 21 with every value intact: entries, runs, progress,
// events, list places and the series order.
func TestSeriesDataMigrationUpgradesFrom20(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 20)
	const at = "2026-01-01T00:00:00Z"
	mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id, cover_source) VALUES(1,'book','book','books','1','https://x.example/1.jpg'), (2,'book','book','books','2',''), (3,'book','book','books','3','')`)
	for _, id := range []int{1, 2, 3} {
		mustExec(t, old, `INSERT INTO entries(item_id, notes, added_at, updated_at) VALUES(?, ?, ?, ?)`, id, "notes", at, at)
	}
	mustExec(t, old, `INSERT INTO entry_runs(id, item_id, number, status, rating, review, started_on, finished_on, created_at, updated_at) VALUES
		(10, 1, 1, 'completed', 8, 'r1', '2026-01-02', '2026-01-03', ?1, ?1),
		(11, 1, 2, 'active', NULL, '', '2026-02-01', NULL, ?1, ?1),
		(12, 2, 1, 'planned', 0, '', NULL, NULL, ?1, ?1),
		(13, 3, 1, 'paused', 5, '', NULL, NULL, ?1, ?1)`, at)
	mustExec(t, old, `UPDATE entries SET display_run_id = CASE item_id WHEN 1 THEN 11 WHEN 2 THEN 12 ELSE 13 END`)
	mustExec(t, old, `INSERT INTO run_progress(run_id, key, value) VALUES(10, 'pages', 300), (11, 'pages', 42)`)
	mustExec(t, old, `INSERT INTO entry_events(item_id, occurred_at, event_type, progress_key, old_num, new_num, origin, run_id) VALUES(1, ?, 'progress', 'pages', 41, 42, 'bulk', 11)`, at)
	mustExec(t, old, `INSERT INTO lists(id, name, name_key, manual_order, created_at, updated_at) VALUES(5, 'L', 'l', 1, ?1, ?1)`, at)
	mustExec(t, old, `INSERT INTO list_items(list_id, item_id, position, added_at) VALUES(5, 3, 1, ?1), (5, 1, 2, ?1)`, at)
	mustExec(t, old, `INSERT INTO item_aliases(item_id, plugin_id, kind, source, source_id) VALUES(1, 'book', 'book', 'books', '99')`)
	mustExec(t, old, `INSERT INTO series(id, uid, plugin_id, kind, title, created_at, updated_at) VALUES(7, 'u', 'book', 'book', 'S', ?1, ?1)`, at)
	mustExec(t, old, `INSERT INTO series_parts(series_id, item_id, position) VALUES(7, 2, 1), (7, 1, 2), (7, 3, 3)`)
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != 21 {
		t.Fatalf("version = %d, %v", v, err)
	}
	checks := []struct {
		q    string
		want int
	}{
		{`SELECT count(*) FROM entry_state WHERE (item_id = 1 AND status = 'active' AND run_number = 2 AND repeats = 1 AND notes = 'notes')
			OR (item_id = 2 AND status = 'planned' AND rating = 0) OR (item_id = 3 AND status = 'paused' AND rating = 5)`, 3},
		{`SELECT count(*) FROM entry_runs WHERE id = 10 AND status = 'completed' AND rating = 8 AND review = 'r1' AND started_on = '2026-01-02' AND finished_on = '2026-01-03'`, 1},
		{`SELECT count(*) FROM run_progress WHERE (run_id = 10 AND value = 300) OR (run_id = 11 AND value = 42)`, 2},
		{`SELECT count(*) FROM entry_events WHERE origin = 'bulk' AND run_id = 11 AND new_num = 42`, 1},
		{`SELECT count(*) FROM list_items WHERE list_id = 5 AND ((item_id = 3 AND position = 1) OR (item_id = 1 AND position = 2))`, 2},
		{`SELECT count(*) FROM item_aliases WHERE item_id = 1 AND source_id = '99'`, 1},
		{`SELECT count(*) FROM items WHERE id = 1 AND cover_source = 'https://x.example/1.jpg'`, 1},
		{`SELECT count(*) FROM series WHERE id = 7 AND uid = 'u' AND title = 'S' AND created_at = '` + at + `' AND finished = 0 AND parts_total IS NULL AND linked_item_id IS NULL AND parent_id IS NULL`, 1},
		{`SELECT count(*) FROM series_parts WHERE series_id = 7 AND spin_off = 0 AND ((item_id = 2 AND position = 1) OR (item_id = 1 AND position = 2) OR (item_id = 3 AND position = 3))`, 3},
	}
	for _, c := range checks {
		if n := rowCount(t, d, c.q); n != c.want {
			t.Errorf("%s = %d, want %d", c.q, n, c.want)
		}
	}
}
