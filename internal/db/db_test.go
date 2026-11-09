package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "a.db")
}

func TestOpenCreatesSchema(t *testing.T) {
	path := tempPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tbl := range []string{"settings", "anime", "anime_titles", "anime_relations", "people", "anime_cast", "list_entries", "jobs"} {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", tbl, n, err)
		}
	}
}

func TestUniqueSourceID(t *testing.T) {
	path := tempPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	q := `INSERT INTO anime(source, source_id, type) VALUES('anilist','1','tv')`
	if _, err := d.Exec(q); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(q); err == nil {
		t.Fatal("expected unique violation")
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	path := tempPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO settings(key, value) VALUES('k','v')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var v string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key='k'`).Scan(&v); err != nil || v != "v" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}

func TestForeignKeyCascade(t *testing.T) {
	path := tempPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO anime(id, source, source_id, type) VALUES(1,'anilist','1','tv')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO anime_titles(anime_id, lang, text) VALUES(1,'en','X')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO list_entries(anime_id, status) VALUES(1,'watching')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO anime_titles(anime_id, lang, text) VALUES(99,'en','orphan')`); err == nil {
		t.Fatal("expected FK violation")
	}
	if _, err := d.Exec(`DELETE FROM anime WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"anime_titles", "list_entries"} {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows=%d err=%v", tbl, n, err)
		}
	}
}

var genericTables = []string{"items", "item_titles", "item_fields", "external_ids", "people_g", "credits", "relations_g", "item_terms"}

func tableCount(t *testing.T, d *sql.DB, tbl string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGenericCoreOnFreshDB(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tbl := range append(genericTables, "anime", "people") {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
	if _, err := d.Exec(`INSERT INTO items(plugin_id, kind, source, source_id) VALUES('p','k','s','1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO item_terms(item_id, kind, term, term_key) VALUES(1,'bogus','a','a')`); err == nil {
		t.Fatal("kind CHECK not enforced")
	}
}

func TestGenericCoreMigratesDBAtVersion4AndDownWorks(t *testing.T) {
	path := tempPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// legacy data survives a down/up cycle of 00005 (it never touches legacy tables)
	if _, err := d.Exec(`INSERT INTO anime(id, source, source_id, type) VALUES(1,'anilist','1','tv')`); err != nil {
		t.Fatal(err)
	}
	// 00006 (entries) sits on top of 00005: down it first
	if err := goose.Down(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"entries", "entry_progress"} {
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists after down of 00006", tbl)
		}
	}
	if err := goose.Down(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range genericTables {
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists after down", tbl)
		}
	}
	if v, err := goose.GetDBVersion(d); err != nil || v != 4 {
		t.Fatalf("version = %d, %v, want 4", v, err)
	}
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range append([]string{"entries", "entry_progress"}, genericTables...) {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing after up", tbl)
		}
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM anime`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("legacy anime rows = %d, %v", n, err)
	}
}
