package library

import (
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// upgradingTV is the tv fixture under the id "tv2" with a season track
// upgrade: items tracking only episodes start tracking seasons too.
func upgradingTV() *plugintest.Plugin {
	p := plugintest.TVPlugin(plugintest.WithID("tv2"))
	p.M.Kinds[0].Progress.Upgrades = []plugin.ProgressUpgrade{{ID: "add_season", From: []string{"episode"}, To: []string{"season", "episode"}}}
	return p
}

func countRows(t *testing.T, e *env, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestApplyProgressUpgrades: an upgrade switches exactly the items whose
// active keys equal From (in its plugin and kind) to To, keeps every stored
// value (a value held while inactive shows again), writes no events, and
// runs once: an item switched back afterwards keeps its choice.
func TestApplyProgressUpgrades(t *testing.T) {
	e := newEnvWith(t, upgradingTV())
	l := e.lib
	ctx := bg
	// held: tracked seasons before, then only episodes (season 2 retained)
	held, err := l.AddToList(ctx, "tv2", "show", tvData("1", nil), Entry{Status: Active, Progress: map[string]float64{"season": 2, "episode": 5}}, WithProgressKeys([]string{"season", "episode"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetActiveProgress(ctx, held, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	plain, err := l.AddToList(ctx, "tv2", "show", tvData("2", nil), Entry{Status: Active, Progress: map[string]float64{"episode": 7}}, WithProgressKeys([]string{"episode"}))
	if err != nil {
		t.Fatal(err)
	}
	saved := mustSave(t, l, "tv2", "show", tvData("3", nil), WithProgressKeys([]string{"episode"})) // not in the list
	other, err := l.AddToList(ctx, "tv2", "show", tvData("4", nil), Entry{Status: Active, Progress: map[string]float64{"episode": 1, "position": 60}}, WithProgressKeys([]string{"episode", "position"}))
	if err != nil {
		t.Fatal(err)
	}
	untouched, err := l.AddToList(ctx, "tv", "show", tvData("5", nil), Entry{Status: Active, Progress: map[string]float64{"episode": 3}}, WithProgressKeys([]string{"episode"}))
	if err != nil {
		t.Fatal(err)
	}
	events := countRows(t, e, `SELECT count(*) FROM entry_events`)

	n, err := l.ApplyProgressUpgrades(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("upgraded %d items, want 3", n)
	}
	want := map[int64][]string{held: {"season", "episode"}, plain: {"season", "episode"}, saved: {"season", "episode"},
		other: {"episode", "position"}, untouched: {"episode"}}
	for id, keys := range want {
		if got := mustItem(t, l, id).ProgressKeys; !slices.Equal(got, keys) {
			t.Errorf("item %d keys = %q, want %q", id, got, keys)
		}
	}
	en, err := l.Entry(ctx, held)
	if err != nil {
		t.Fatal(err)
	}
	if en.Progress["season"] != 2 || en.Progress["episode"] != 5 {
		t.Errorf("held progress = %v, want season 2 and episode 5", en.Progress)
	}
	if en, err := l.Entry(ctx, plain); err != nil || en.Progress["episode"] != 7 {
		t.Errorf("plain progress = %v, %v", en, err)
	} else if _, ok := en.Progress["season"]; ok {
		t.Errorf("plain gained a season value: %v", en.Progress)
	}
	if got := countRows(t, e, `SELECT count(*) FROM entry_events`); got != events {
		t.Errorf("events %d -> %d, want none written", events, got)
	}

	// idempotent, and the owner's later choice of From sticks
	if err := l.SetActiveProgress(ctx, plain, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	if n, err := l.ApplyProgressUpgrades(ctx); err != nil || n != 0 {
		t.Fatalf("second run: %d, %v, want 0", n, err)
	}
	if got := mustItem(t, l, plain).ProgressKeys; !slices.Equal(got, []string{"episode"}) {
		t.Errorf("switched-back item = %q, want the owner's choice kept", got)
	}
	if got := countRows(t, e, `SELECT count(*) FROM progress_upgrades WHERE plugin_id = 'tv2' AND kind = 'show' AND upgrade_id = 'add_season'`); got != 1 {
		t.Errorf("upgrade recorded %d times, want once", got)
	}
}

// TestApplyProgressUpgradesContinuesAfterAnError: an upgrade that fails (a
// damaged item) is reported, and the next upgrade still runs.
func TestApplyProgressUpgradesContinuesAfterAnError(t *testing.T) {
	other := upgradingTV()
	other.M.ID = "tv3"
	e := newEnvWith(t, upgradingTV(), other)
	bad := mustSave(t, e.lib, "tv2", "show", tvData("1", nil), WithProgressKeys([]string{"episode"}))
	if _, err := e.db.Exec(`UPDATE items SET progress_keys = 'not json' WHERE id = ?`, bad); err != nil {
		t.Fatal(err)
	}
	good := mustSave(t, e.lib, "tv3", "show", tvData("2", nil), WithProgressKeys([]string{"episode"}))
	n, err := e.lib.ApplyProgressUpgrades(bg)
	if err == nil || !strings.Contains(err.Error(), "tv2/show/add_season") {
		t.Fatalf("error %v, want the failed upgrade named", err)
	}
	if n != 1 {
		t.Fatalf("upgraded %d, want 1", n)
	}
	if got := mustItem(t, e.lib, good).ProgressKeys; !slices.Equal(got, []string{"season", "episode"}) {
		t.Fatalf("next upgrade did not run: %q", got)
	}
}

// TestRestoreAppliesUpgradesTheBackupDidNotRun: RestoreItem applies every
// upgrade of the item's kind that AppliedUpgrades does not list to the
// restored keys; a listed one leaves them.
func TestRestoreAppliesUpgradesTheBackupDidNotRun(t *testing.T) {
	e := newEnvWith(t, upgradingTV())
	bi := BackupItem{PluginID: "tv2", Kind: "show", Source: "tvdata", SourceID: "1", Titles: []plugin.Title{{Lang: "en", Text: "One"}}, ProgressKeys: []string{"episode"}}
	if res, err := e.lib.RestoreItem(bg, bi, RestoreOptions{}); err != nil || res.Outcome != RestoreCreated {
		t.Fatalf("%+v %v", res, err)
	} else if got := mustItem(t, e.lib, res.ItemID).ProgressKeys; !slices.Equal(got, []string{"season", "episode"}) {
		t.Fatalf("keys %q, want the upgrade applied", got)
	}
	bi.SourceID = "2"
	res, err := e.lib.RestoreItem(bg, bi, RestoreOptions{AppliedUpgrades: []BackupProgressUpgrade{{PluginID: "tv2", Kind: "show", ID: "add_season"}}})
	if err != nil || res.Outcome != RestoreCreated {
		t.Fatalf("%+v %v", res, err)
	}
	if got := mustItem(t, e.lib, res.ItemID).ProgressKeys; !slices.Equal(got, []string{"episode"}) {
		t.Fatalf("keys %q, want the backup's keys kept", got)
	}
}
