package db

import (
	"path/filepath"
	"testing"
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
