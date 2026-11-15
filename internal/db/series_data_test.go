package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// 00019 keeps existing series and parts (main parts, no data) and Down keeps
// them too.
func TestSeriesDataMigrationKeepsSeries(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 18)
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
	if err := goose.DownTo(d, "migrations", 18); err != nil {
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
	if err := goose.UpTo(d, "migrations", 19); err != nil {
		t.Fatal(err)
	}
}
