package db

import "testing"

// TestProgressUpgradesMigration: 00022 adds the progress_upgrades record of
// applied plugin progress upgrades, one row per plugin, kind and upgrade id,
// and leaves existing items' progress keys alone (the library applies the
// upgrades at startup).
func TestProgressUpgradesMigration(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 21)
	mustExec(t, old, `INSERT INTO items(id, plugin_id, kind, source, source_id, progress_keys) VALUES(1, 'p', 'k', 's', '1', '["count"]')`)
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if tableCount(t, d, "progress_upgrades") != 1 {
		t.Fatal("progress_upgrades missing")
	}
	if n := rowCount(t, d, `SELECT count(*) FROM items WHERE progress_keys = '["count"]'`); n != 1 {
		t.Fatalf("items changed by the migration: %d", n)
	}
	mustExec(t, d, `INSERT INTO progress_upgrades(plugin_id, kind, upgrade_id, applied_at) VALUES('p', 'k', 'u', 't')`)
	if _, err := d.Exec(`INSERT INTO progress_upgrades(plugin_id, kind, upgrade_id, applied_at) VALUES('p', 'k', 'u', 't')`); err == nil {
		t.Fatal("a second record of the same upgrade was accepted")
	}
}
