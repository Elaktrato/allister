package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// TestSourceExtrasMigration: 00023 gives items an empty banner and trailer,
// adds the item links (deleted with their item) and the refresh run record,
// and its down removes them again.
func TestSourceExtrasMigration(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 22)
	mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1, 'p', 'k', 's', '1')`)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n := rowCount(t, d, `SELECT count(*) FROM items WHERE banner_path = '' AND trailer_url = '' AND trailer_thumb = ''`); n != 1 {
		t.Fatalf("existing item not defaulted: %d", n)
	}
	mustExec(t, d, `INSERT INTO item_links(item_id, seq, label, url) VALUES(1, 0, 'A', 'https://a.example/')`)
	if _, err := d.Exec(`INSERT INTO item_links(item_id, seq, label, url) VALUES(1, 0, 'B', 'https://b.example/')`); err == nil {
		t.Fatal("a second link at the same position was accepted")
	}
	mustExec(t, d, `INSERT INTO source_refresh_runs(plugin_id, started_at, total) VALUES('p', 't', 3)`)
	if n := rowCount(t, d, `SELECT count(*) FROM source_refresh_runs WHERE updated = 0 AND unchanged = 0 AND failed = 0
		AND failed_titles = '[]' AND stopped = '' AND finished_at IS NULL`); n != 1 {
		t.Fatalf("refresh run defaults: %d", n)
	}
	mustExec(t, d, `DELETE FROM items WHERE id = 1`)
	if n := rowCount(t, d, `SELECT count(*) FROM item_links`); n != 0 {
		t.Fatalf("links outlived their item: %d", n)
	}
	if err := goose.DownTo(d, "migrations", 22); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"item_links", "source_refresh_runs"} {
		if tableCount(t, d, tbl) != 0 {
			t.Fatalf("%s left after down", tbl)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM pragma_table_info('items') WHERE name IN ('banner_path', 'trailer_url', 'trailer_thumb')`); n != 0 {
		t.Fatalf("%d columns left after down", n)
	}
}
