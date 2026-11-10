package db

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

const olderVersionNote = "created by an older version; upload the file again"

// columns returns the column names of a table.
func columns(t *testing.T, d *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := d.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

// TestImportPluginMigrationUpgradesDBAtVersion9 simulates an existing
// database at 00009 holding legacy (Yamtrack, anime-shaped) imports and jobs
// and runs goose Up on it.
func TestImportPluginMigrationUpgradesDBAtVersion9(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 9)
	mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(5,'anime','anime','anilist','1')`)
	mustExec(t, old, `INSERT INTO imports(id, filename, state, total, note) VALUES
		(1,'a.csv','done',2,NULL), (2,'b.csv','running',2,'Waiting for AniList (retry 1/5)'),
		(3,'c.csv','cancelled',2,NULL), (4,'d.csv','pending',1,NULL)`)
	mustExec(t, old, `INSERT INTO import_rows(import_id, seq, mal_id, title, outcome, anime_id, message, data) VALUES
		(1,1,'11','A','imported',5,'','{}'), (1,2,'','S','skipped',NULL,'unknown status','{}'),
		(2,1,'21','B','imported',NULL,'','{}'), (2,2,'22','C','pending',NULL,'','{"MALID":"22"}'),
		(3,1,'31','D','pending',NULL,'','{}'), (3,2,'32','E','imported',NULL,'','{}'),
		(4,1,'41','F','pending',NULL,'','{}')`)
	mustExec(t, old, `INSERT INTO jobs(id, kind, payload_json, state) VALUES
		(1,'import_yamtrack','{"import_id":2,"plugin_id":"anime"}','running'),
		(2,'import_yamtrack','{"import_id":4}','pending'),
		(3,'import_yamtrack','{"import_id":1}','done'),
		(4,'import_yamtrack','not json','pending'),
		(5,'sync_item','{"item_id":5,"plugin_id":"anime"}','pending')`)
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion || latestVersion != 10 {
		t.Fatalf("version = %d, %v", v, err)
	}
	ic, rc := columns(t, d, "imports"), columns(t, d, "import_rows")
	for _, c := range []string{"plugin_id", "importer_id"} {
		if !ic[c] {
			t.Errorf("imports.%s missing", c)
		}
	}
	for _, c := range []string{"external_id", "external_id_type", "item_id"} {
		if !rc[c] {
			t.Errorf("import_rows.%s missing", c)
		}
	}
	if rc["mal_id"] || rc["anime_id"] {
		t.Errorf("legacy columns kept: %v", rc)
	}
	// imports: every legacy import belongs to the anime plugin and keeps the
	// legacy marker importer_id ''; unfinished ones are failed
	wantImports := map[int64][3]string{
		1: {"done", "", "anime"},
		2: {"failed", olderVersionNote, "anime"},
		3: {"cancelled", "", "anime"},
		4: {"failed", olderVersionNote, "anime"},
	}
	for id, w := range wantImports {
		var state, note, pluginID, importerID string
		var finished sql.NullString
		if err := d.QueryRow(`SELECT state, COALESCE(note,''), plugin_id, importer_id, finished_at FROM imports WHERE id=?`, id).
			Scan(&state, &note, &pluginID, &importerID, &finished); err != nil {
			t.Fatal(err)
		}
		if state != w[0] || note != w[1] || pluginID != w[2] || importerID != "" {
			t.Errorf("import %d: %s %q %s %q, want %v", id, state, note, pluginID, importerID, w)
		}
		if state == "failed" && !finished.Valid {
			t.Errorf("import %d: failed without finished_at", id)
		}
	}
	// rows: the values move to the renamed columns; legacy pending rows
	// cannot be resumed and fail with the reason
	type row struct{ ext, typ, outcome, msg string }
	wantRows := map[[2]int]row{
		{1, 1}: {"11", "mal", "imported", ""},
		{1, 2}: {"", "", "skipped", "unknown status"},
		{2, 2}: {"22", "mal", "failed", olderVersionNote},
		{3, 1}: {"31", "mal", "failed", olderVersionNote},
		{3, 2}: {"32", "mal", "imported", ""},
		{4, 1}: {"41", "mal", "failed", olderVersionNote},
	}
	for k, w := range wantRows {
		var got row
		if err := d.QueryRow(`SELECT external_id, external_id_type, outcome, message FROM import_rows WHERE import_id=? AND seq=?`, k[0], k[1]).
			Scan(&got.ext, &got.typ, &got.outcome, &got.msg); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("row %v: %+v, want %+v", k, got, w)
		}
	}
	var item sql.NullInt64
	if err := d.QueryRow(`SELECT item_id FROM import_rows WHERE import_id=1 AND seq=1`).Scan(&item); err != nil || item.Int64 != 5 {
		t.Fatalf("item_id = %v, %v", item, err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM import_rows WHERE outcome='pending'`); n != 0 {
		t.Errorf("legacy pending rows left: %d", n)
	}
	// jobs: queued legacy import jobs become import_run with a plugin_id;
	// history and unreadable payloads stay as they were
	wantJobs := map[int64][2]string{
		1: {"import_run", `{"import_id":2,"plugin_id":"anime"}`},
		2: {"import_run", `{"import_id":4,"plugin_id":"anime"}`},
		3: {"import_yamtrack", `{"import_id":1}`},
		4: {"import_yamtrack", `not json`},
		5: {"sync_item", `{"item_id":5,"plugin_id":"anime"}`},
	}
	for id, w := range wantJobs {
		var kind, payload string
		if err := d.QueryRow(`SELECT kind, payload_json FROM jobs WHERE id=?`, id).Scan(&kind, &payload); err != nil {
			t.Fatal(err)
		}
		if kind != w[0] || payload != w[1] {
			t.Errorf("job %d: %s %s, want %v", id, kind, payload, w)
		}
	}
	// the foreign key follows the rename
	if _, err := d.Exec(`INSERT INTO import_rows(import_id, seq, item_id) VALUES(1,9,777)`); err == nil {
		t.Fatal("import_rows.item_id accepted a missing item")
	}
	mustExec(t, d, `DELETE FROM items WHERE id=5`)
	if err := d.QueryRow(`SELECT item_id FROM import_rows WHERE import_id=1 AND seq=1`).Scan(&item); err != nil || item.Valid {
		t.Fatalf("item_id after delete = %v, %v", item, err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_import_rows_outcome'`); n != 1 {
		t.Error("idx_import_rows_outcome missing")
	}
	// reopening is a no-op
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestImportPluginMigrationOnFreshDB(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO imports(id, filename, plugin_id, importer_id) VALUES(1,'x.csv','book','books-csv')`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, external_id_type, external_id, title) VALUES(1,1,'isbn','978','T')`)
	var p, im, typ, ext string
	if err := d.QueryRow(`SELECT i.plugin_id, i.importer_id, r.external_id_type, r.external_id FROM imports i JOIN import_rows r ON r.import_id = i.id`).
		Scan(&p, &im, &typ, &ext); err != nil {
		t.Fatal(err)
	}
	if p != "book" || im != "books-csv" || typ != "isbn" || ext != "978" {
		t.Fatalf("%s %s %s %s", p, im, typ, ext)
	}
}

// Down restores the legacy column names. It is meant for empty import
// tables; imports made by the newer version that are still unfinished are
// failed (and their pending rows too) so the older version can never resume
// rows it cannot read, and queued import_run jobs get the legacy kind back.
func TestImportPluginMigrationDown(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO imports(id, filename, state, plugin_id, importer_id) VALUES
		(1,'x.csv','running','book','books-csv'), (2,'y.csv','done','book','books-csv')`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, external_id_type, external_id, outcome) VALUES
		(1,1,'isbn','1','imported'), (1,2,'isbn','2','pending'), (2,1,'isbn','3','imported')`)
	mustExec(t, d, `INSERT INTO jobs(id, kind, payload_json, state) VALUES
		(1,'import_run','{"import_id":1,"plugin_id":"book"}','pending'),
		(2,'import_run','{"import_id":2,"plugin_id":"book"}','done')`)
	if err := goose.DownTo(d, "migrations", 9); err != nil {
		t.Fatal(err)
	}
	ic, rc := columns(t, d, "imports"), columns(t, d, "import_rows")
	if ic["plugin_id"] || ic["importer_id"] || !rc["mal_id"] || !rc["anime_id"] || rc["external_id"] || rc["external_id_type"] || rc["item_id"] {
		t.Fatalf("columns after down: %v %v", ic, rc)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM imports WHERE id=1`).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("unfinished newer import: %q %v", state, err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM import_rows WHERE outcome='pending'`); n != 0 {
		t.Errorf("pending rows after down: %d", n)
	}
	var mal string
	if err := d.QueryRow(`SELECT mal_id FROM import_rows WHERE import_id=2 AND seq=1`).Scan(&mal); err != nil || mal != "3" {
		t.Fatalf("mal_id = %q, %v", mal, err)
	}
	var kind string
	if err := d.QueryRow(`SELECT kind FROM jobs WHERE id=1`).Scan(&kind); err != nil || kind != "import_yamtrack" {
		t.Fatalf("job kind = %q, %v", kind, err)
	}
	if err := d.QueryRow(`SELECT kind FROM jobs WHERE id=2`).Scan(&kind); err != nil || kind != "import_run" {
		t.Fatalf("done job kind = %q, %v", kind, err)
	}
	// and up again
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion {
		t.Fatalf("version = %d, %v", v, err)
	}
}
