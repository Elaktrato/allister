package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// TestFontsTable: 00013 adds the uploaded fonts table on a fresh database
// and on one at version 12 (keeping its settings), with a unique file
// name and a known format; down drops it again.
func TestFontsTable(t *testing.T) {
	fresh, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	mustExec(t, fresh, `INSERT INTO fonts(name, file, format, size) VALUES('A', 'a.woff2', 'woff2', 10)`)
	if _, err := fresh.Exec(`INSERT INTO fonts(name, file, format, size) VALUES('B', 'a.woff2', 'woff2', 10)`); err == nil {
		t.Error("duplicate file accepted")
	}
	if _, err := fresh.Exec(`INSERT INTO fonts(name, file, format, size) VALUES('C', 'c.svg', 'svg', 10)`); err == nil {
		t.Error("unknown format accepted")
	}
	var created string
	if err := fresh.QueryRow(`SELECT created_at FROM fonts WHERE name = 'A'`).Scan(&created); err != nil || len(created) != 20 {
		t.Errorf("created_at %q %v", created, err)
	}

	path := tempPath(t)
	old := openAt(t, path, 12)
	mustExec(t, old, `INSERT INTO settings(key, value) VALUES('appearance.preset', 'paper')`)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion || latestVersion != 15 {
		t.Fatalf("version = %d, %v", v, err)
	}
	if v, ok := setting(t, d, "appearance.preset"); !ok || v != "paper" {
		t.Errorf("settings lost: %q", v)
	}
	if tableCount(t, d, "fonts") != 1 || rowCount(t, d, `SELECT count(*) FROM fonts`) != 0 {
		t.Error("no empty fonts table after the upgrade")
	}
	if err := goose.DownTo(d, "migrations", 12); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='fonts'`); n != 0 {
		t.Error("down kept the fonts table")
	}
}
