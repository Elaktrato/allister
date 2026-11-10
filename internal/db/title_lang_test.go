package db

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

// setting returns the value of a settings key and whether it is stored.
func setting(t *testing.T, d *sql.DB, key string) (string, bool) {
	t.Helper()
	var v string
	err := d.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

// TestTitleLangMigrationMovesTheGlobalValue: 00011 copies the old global
// title language (the only plugin that existed was anime) into the anime
// plugin's own setting and removes the global key; other settings stay.
func TestTitleLangMigrationMovesTheGlobalValue(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 10)
	mustExec(t, old, `INSERT INTO settings(key, value) VALUES('title_lang','en'), ('plugin.anime.enabled','1')`)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion {
		t.Fatalf("version = %d, %v", v, err)
	}
	if v, ok := setting(t, d, "plugin.anime.title_lang"); !ok || v != "en" {
		t.Fatalf("plugin.anime.title_lang = %q %v", v, ok)
	}
	if _, ok := setting(t, d, "title_lang"); ok {
		t.Fatal("global title_lang still stored")
	}
	if v, ok := setting(t, d, "plugin.anime.enabled"); !ok || v != "1" {
		t.Fatalf("other settings changed: %q %v", v, ok)
	}

	// Down puts the global key back (for the older version)
	if err := goose.DownTo(d, "migrations", 10); err != nil {
		t.Fatal(err)
	}
	if v, ok := setting(t, d, "title_lang"); !ok || v != "en" {
		t.Fatalf("down: title_lang = %q %v", v, ok)
	}
	if _, ok := setting(t, d, "plugin.anime.title_lang"); ok {
		t.Fatal("down: per-plugin key still stored")
	}
}

// TestTitleLangMigrationWithoutTheOldKey: nothing stored, nothing created
// (the plugin's default applies); an empty old value is dropped as well.
func TestTitleLangMigrationWithoutTheOldKey(t *testing.T) {
	for _, seed := range []string{``, `INSERT INTO settings(key, value) VALUES('title_lang','')`} {
		path := tempPath(t)
		old := openAt(t, path, 10)
		if seed != "" {
			mustExec(t, old, seed)
		}
		old.Close()
		d, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"title_lang", "plugin.anime.title_lang"} {
			if v, ok := setting(t, d, k); ok {
				t.Errorf("seed %q: %s = %q stored", seed, k, v)
			}
		}
		d.Close()
	}
	// a fresh database too
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n := rowCount(t, d, `SELECT COUNT(*) FROM settings`); n != 0 {
		t.Fatalf("fresh db has %d settings", n)
	}
}
