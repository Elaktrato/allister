package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "a.db")
}

// openAt opens a database migrated up to (and including) version.
func openAt(t *testing.T, path string, version int64) *sql.DB {
	t.Helper()
	d, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := setupGoose(); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(d, "migrations", version); err != nil {
		t.Fatal(err)
	}
	return d
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func rowCount(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

var legacyTables = []string{"anime", "anime_titles", "anime_relations", "people", "anime_cast", "list_entries"}

func TestOpenCreatesSchema(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tbl := range append([]string{"settings", "jobs", "imports", "import_rows", "entries", "entry_progress"}, genericTables...) {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
}

func TestUniqueSourceID(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	q := `INSERT INTO items(plugin_id, kind, source, source_id) VALUES('anime','anime','anilist','1')`
	mustExec(t, d, q)
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
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','anilist','1')`)
	mustExec(t, d, `INSERT INTO item_titles(item_id, lang, text) VALUES(1,'en','X')`)
	mustExec(t, d, `INSERT INTO entries(item_id, status, updated_at) VALUES(1,'active','2024-01-01T00:00:00Z')`)
	if _, err := d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(99,'en','orphan')`); err == nil {
		t.Fatal("expected FK violation")
	}
	mustExec(t, d, `DELETE FROM items WHERE id=1`)
	for _, tbl := range []string{"item_titles", "entries"} {
		if n := rowCount(t, d, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("%s rows=%d", tbl, n)
		}
	}
}

var genericTables = []string{"items", "item_titles", "item_fields", "external_ids", "people", "credits", "relations", "item_terms"}

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
	if _, err := d.Exec(`INSERT INTO items(plugin_id, kind, source, source_id) VALUES('p','k','s','1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO item_terms(item_id, kind, term, term_key) VALUES(1,'bogus','a','a')`); err == nil {
		t.Fatal("kind CHECK not enforced")
	}
}

func TestLegacyTablesDropped(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tbl := range append([]string{"people_g", "relations_g"}, legacyTables...) {
		if tbl == "people" {
			continue // the generic people table took the name
		}
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists", tbl)
		}
	}
	// "people" is the generic table now (it has plugin_id)
	if n := rowCount(t, d, `SELECT count(*) FROM pragma_table_info('people') WHERE name = 'plugin_id'`); n != 1 {
		t.Fatal("people is not the generic people table")
	}
	if v, err := goose.GetDBVersion(d); err != nil || v != 7 {
		t.Fatalf("version = %d, %v, want 7", v, err)
	}
}

// seedLegacy fills every legacy table (and import_rows pointing at a legacy
// anime) of a database at version 6.
func seedLegacy(t *testing.T, d *sql.DB) {
	t.Helper()
	mustExec(t, d, `INSERT INTO anime(id, source, source_id, type) VALUES(1,'anilist','1','TV')`)
	mustExec(t, d, `INSERT INTO anime_titles(anime_id, lang, text) VALUES(1,'en','X')`)
	mustExec(t, d, `INSERT INTO anime_relations(anime_id, kind, rel_source, rel_source_id) VALUES(1,'sequel','anilist','2')`)
	mustExec(t, d, `INSERT INTO people(id, source, source_id, name) VALUES(1,'anilist','c1','C'),(2,'anilist','v1','V')`)
	mustExec(t, d, `INSERT INTO anime_cast(anime_id, character_person_id, actor_person_id, role) VALUES(1,1,2,'MAIN')`)
	mustExec(t, d, `INSERT INTO list_entries(anime_id, status) VALUES(1,'watching')`)
	mustExec(t, d, `INSERT INTO imports(id, filename) VALUES(1,'y.csv')`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, mal_id, title, outcome, anime_id, message, data) VALUES(1,1,'5','T','imported',1,'m','{}')`)
	mustExec(t, d, `INSERT INTO settings(key, value) VALUES('k','v')`)
	mustExec(t, d, `INSERT INTO jobs(kind, payload_json) VALUES('sync_anime','{"anime_id":1}')`)
	// generic rows written at 00006 must survive the renames
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(10,'anime','anime','anilist','10')`)
	mustExec(t, d, `INSERT INTO people_g(id, plugin_id, source, source_id, name) VALUES(20,'anime','anilist','c','C'),(21,'anime','anilist','v','V')`)
	mustExec(t, d, `INSERT INTO credits(item_id, seq, role, person_id, counterpart_person_id, counterpart_role) VALUES(10,0,'character',20,21,'Voice actor')`)
	mustExec(t, d, `INSERT INTO relations_g(item_id, seq, kind, source, source_id) VALUES(10,0,'sequel','anilist','11')`)
}

func TestMigrationsApplyOnEmptyAndExistingVolume(t *testing.T) {
	// a fresh file goes straight to the latest version
	fresh, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := goose.GetDBVersion(fresh); err != nil || v != 7 {
		t.Fatalf("fresh version = %d, %v", v, err)
	}
	fresh.Close()

	// a volume at 00006 holding legacy rows upgrades cleanly
	path := tempPath(t)
	old := openAt(t, path, 6)
	seedLegacy(t, old)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tbl := range legacyTables {
		if tbl != "people" && tableCount(t, d, tbl) != 0 {
			t.Errorf("legacy table %s survived", tbl)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM people WHERE id IN (1, 2)`); n != 0 {
		t.Errorf("legacy people copied into the generic table: %d", n)
	}
	// the tables that stay keep their rows; import_rows loses its legacy link
	if n := rowCount(t, d, `SELECT count(*) FROM settings`); n != 1 {
		t.Errorf("settings rows %d", n)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM jobs`); n != 1 {
		t.Errorf("jobs rows %d", n)
	}
	var msg string
	var animeID sql.NullInt64
	if err := d.QueryRow(`SELECT message, anime_id FROM import_rows WHERE import_id=1 AND seq=1`).Scan(&msg, &animeID); err != nil || msg != "m" || animeID.Valid {
		t.Fatalf("import row: %q %v %v", msg, animeID, err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_import_rows_outcome'`); n != 1 {
		t.Error("idx_import_rows_outcome missing")
	}
	// the generic rows survived with working foreign keys
	if n := rowCount(t, d, `SELECT count(*) FROM credits c JOIN people p ON p.id = c.person_id JOIN people w ON w.id = c.counterpart_person_id WHERE c.item_id = 10`); n != 1 {
		t.Fatalf("credit with counterpart lost: %d", n)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM relations WHERE item_id = 10`); n != 1 {
		t.Fatalf("relation lost: %d", n)
	}
	if rows, err := d.Query(`PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		bad := rows.Next()
		rows.Close()
		if bad {
			t.Fatal("foreign_key_check reports violations after the upgrade")
		}
	}
	if _, err := d.Exec(`INSERT INTO credits(item_id, seq, person_id) VALUES(10,1,999)`); err == nil {
		t.Fatal("credits accepted a missing person after the upgrade")
	}
	mustExec(t, d, `DELETE FROM items WHERE id = 10`)
	for _, tbl := range []string{"credits", "relations"} {
		if n := rowCount(t, d, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("%s rows %d after deleting the item", tbl, n)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM people`); n != 2 {
		t.Errorf("people rows %d, want the 2 generic people", n)
	}
	// reopening is a no-op
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestCreditsForeignKeysFollowRename(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var ddl string
	if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='credits'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ddl, "people_g") || !strings.Contains(ddl, `"people"`) && !strings.Contains(ddl, "people(") {
		t.Fatalf("credits DDL not rewritten: %s", ddl)
	}
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','anilist','1')`)
	mustExec(t, d, `INSERT INTO people(id, plugin_id, source, source_id, name) VALUES(1,'anime','anilist','c1','C'),(2,'anime','anilist','v1','V')`)
	mustExec(t, d, `INSERT INTO credits(item_id, seq, role, person_id, counterpart_person_id, counterpart_role, detail) VALUES(1,0,'character',1,2,'Voice actor','MAIN')`)
	mustExec(t, d, `INSERT INTO relations(item_id, seq, kind) VALUES(1,0,'sequel')`)
	if _, err := d.Exec(`INSERT INTO credits(item_id, seq, person_id) VALUES(1,1,99)`); err == nil {
		t.Fatal("credits accepted a missing person")
	}
	mustExec(t, d, `DELETE FROM items WHERE id=1`)
	for _, tbl := range []string{"credits", "relations"} {
		if n := rowCount(t, d, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("%s rows=%d after deleting the item", tbl, n)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM people`); n != 2 {
		t.Errorf("people rows %d", n)
	}
	if rows, err := d.Query(`PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		if rows.Next() {
			t.Error("foreign_key_check reports violations")
		}
		rows.Close()
	}
}

func TestImportRowsReferenceItems(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO imports(id, filename) VALUES(1,'y.csv')`)
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(5,'anime','anime','anilist','1')`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, anime_id) VALUES(1,1,5)`)
	if _, err := d.Exec(`INSERT INTO import_rows(import_id, seq, anime_id) VALUES(1,2,77)`); err == nil {
		t.Fatal("import_rows accepted a missing item")
	}
	mustExec(t, d, `DELETE FROM items WHERE id=5`)
	var id sql.NullInt64
	if err := d.QueryRow(`SELECT anime_id FROM import_rows WHERE seq=1`).Scan(&id); err != nil || id.Valid {
		t.Fatalf("anime_id after item delete: %v %v", id, err)
	}
}

func TestDropLegacyDownRestoresEmptyLegacyTables(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','anilist','1')`)
	mustExec(t, d, `INSERT INTO people(id, plugin_id, source, source_id, name) VALUES(1,'anime','anilist','c1','C')`)
	mustExec(t, d, `INSERT INTO credits(item_id, seq, person_id) VALUES(1,0,1)`)
	mustExec(t, d, `INSERT INTO imports(id) VALUES(1)`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, anime_id) VALUES(1,1,1)`)
	if err := goose.Down(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	if v, err := goose.GetDBVersion(d); err != nil || v != 6 {
		t.Fatalf("version = %d, %v, want 6", v, err)
	}
	for _, tbl := range append([]string{"people_g", "relations_g"}, legacyTables...) {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing after down", tbl)
		}
	}
	for _, tbl := range legacyTables {
		if n := rowCount(t, d, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("legacy %s has %d rows after down", tbl, n)
		}
	}
	if n := rowCount(t, d, `SELECT count(*) FROM people_g`); n != 1 {
		t.Errorf("people_g rows %d", n)
	}
	// credits follow the rename back; import_rows references anime again
	mustExec(t, d, `DELETE FROM items WHERE id=1`)
	if n := rowCount(t, d, `SELECT count(*) FROM credits`); n != 0 {
		t.Errorf("credits rows %d", n)
	}
	mustExec(t, d, `INSERT INTO anime(id, source, source_id) VALUES(3,'anilist','3')`)
	mustExec(t, d, `INSERT INTO import_rows(import_id, seq, anime_id) VALUES(1,2,3)`)
	var id sql.NullInt64
	if err := d.QueryRow(`SELECT anime_id FROM import_rows WHERE seq=1`).Scan(&id); err != nil || id.Valid {
		t.Fatalf("anime_id kept across down: %v %v", id, err)
	}
	// and up again
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	if tableCount(t, d, "anime") != 0 || tableCount(t, d, "people_g") != 0 {
		t.Fatal("up after down did not drop the legacy tables")
	}
}

func TestGenericCoreMigratesDBAtVersion4AndDownWorks(t *testing.T) {
	path := tempPath(t)
	d := openAt(t, path, 6)
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
	for _, tbl := range []string{"items", "item_titles", "item_fields", "external_ids", "people_g", "credits", "relations_g", "item_terms"} {
		if tableCount(t, d, tbl) != 0 {
			t.Errorf("table %s still exists after down", tbl)
		}
	}
	if v, err := goose.GetDBVersion(d); err != nil || v != 4 {
		t.Fatalf("version = %d, %v, want 4", v, err)
	}
	if err := goose.UpTo(d, "migrations", 6); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"entries", "entry_progress", "items", "people_g", "relations_g"} {
		if tableCount(t, d, tbl) != 1 {
			t.Errorf("table %s missing after up", tbl)
		}
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM anime`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("legacy anime rows = %d, %v", n, err)
	}
}
