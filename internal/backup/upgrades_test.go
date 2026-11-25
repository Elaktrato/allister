package backup

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/manga"
)

// newMangaInstall is a fresh install with the real manga plugin: its
// startup has applied (and recorded) the progress upgrades, on no items.
func newMangaInstall(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(manga.New(manga.Options{})); err != nil {
		t.Fatal(err)
	}
	e := &env{db: d, lib: library.New(d, reg), reg: reg, covers: filepath.Join(root, "covers"), root: root, t: t}
	if n, err := e.lib.ApplyProgressUpgrades(bg); err != nil || n != 0 {
		t.Fatalf("startup upgrade: %d, %v", n, err)
	}
	return e
}

func mangaBackupItem(id string, keys ...string) library.BackupItem {
	return library.BackupItem{PluginID: manga.PluginID, Kind: manga.KindManga, Source: manga.SourceID, SourceID: id,
		Titles: []plugin.Title{{Lang: "en", Text: "Manga " + id}}, ProgressKeys: keys,
		Fields: map[string]plugin.FieldValue{"type": {Text: "MANGA"}},
		Entry: &library.BackupEntry{Runs: []library.BackupRun{{Number: 1, Status: library.Active,
			Progress: map[string]float64{"chapter": 10, "volume": 2}}}}}
}

func (e *env) keysOf(sourceID string) []string {
	e.t.Helper()
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM items WHERE plugin_id = ? AND source_id = ?`, manga.PluginID, sourceID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	it, err := e.lib.Item(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it.ProgressKeys
}

func (e *env) volumeOf(sourceID string) float64 {
	e.t.Helper()
	var v float64
	if err := e.db.QueryRow(`SELECT p.value FROM run_progress p JOIN entry_runs r ON r.id = p.run_id JOIN items i ON i.id = r.item_id
		WHERE i.source_id = ? AND p.key = 'volume'`, sourceID).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

// TestRestoreOfAnOldBackupAppliesProgressUpgrades: a backup without the
// progress_upgrades member was written before any upgrade ran, so its
// chapter-only manga track Chapter + Volume once restored (the stored
// volume shows), also into a fresh install that recorded the upgrade on no
// items. Restoring it again changes nothing.
func TestRestoreOfAnOldBackupAppliesProgressUpgrades(t *testing.T) {
	e := newMangaInstall(t)
	b, err := json.Marshal(mangaBackupItem("1", "chapter"))
	if err != nil {
		t.Fatal(err)
	}
	data := zipOf(t, entry{name: "backup.json", body: []byte(header("4") + string(b) + "]}")})
	if rep := mustRestore(t, e, data); rep.Created != 1 {
		t.Fatalf("report %+v", rep)
	}
	if got := e.keysOf("1"); !reflect.DeepEqual(got, []string{"chapter", "volume"}) {
		t.Fatalf("keys %q, want chapter and volume", got)
	}
	if v := e.volumeOf("1"); v != 2 {
		t.Fatalf("volume %v, want the stored 2", v)
	}
	if rep := mustRestore(t, e, data); rep.Skipped != 1 || rep.Created != 0 {
		t.Fatalf("second restore %+v", rep)
	}
	if got := e.keysOf("1"); !reflect.DeepEqual(got, []string{"chapter", "volume"}) {
		t.Fatalf("keys after a second restore %q", got)
	}
}

// TestBackupCarriesAppliedProgressUpgrades: a backup lists the upgrades its
// database applied, before the items; restoring it keeps a manga the owner
// set back to chapters only, while a chapter-only manga of a backup that
// does not list the upgrade is upgraded.
func TestBackupCarriesAppliedProgressUpgrades(t *testing.T) {
	src := newMangaInstall(t)
	id, err := src.lib.AddToList(bg, manga.PluginID, manga.KindManga, plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: manga.SourceID, ID: "7", Kind: manga.KindManga, Titles: []plugin.Title{{Lang: "en", Text: "Chosen"}},
		Fields: map[string]plugin.FieldValue{"type": {Text: "MANGA"}}}},
		library.Entry{Status: library.Active, Progress: map[string]float64{"chapter": 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.lib.SetActiveProgress(bg, id, []string{"chapter"}); err != nil { // the owner's choice
		t.Fatal(err)
	}
	data := src.archive()
	_, files := open(t, data)
	js := string(files["backup.json"])
	member := `"progress_upgrades":[{"plugin_id":"manga","kind":"manga","id":"chapter_volume"}]`
	if i, j := strings.Index(js, member), strings.Index(js, `"items":`); i < 0 || j < 0 || i > j {
		t.Fatalf("progress_upgrades member missing or after the items: %.300s", js)
	}
	dst := newMangaInstall(t)
	mustRestore(t, dst, data)
	if got := dst.keysOf("7"); !reflect.DeepEqual(got, []string{"chapter"}) {
		t.Fatalf("keys %q, want the owner's chapter-only choice", got)
	}
	mustRestore(t, dst, data)
	if got := dst.keysOf("7"); !reflect.DeepEqual(got, []string{"chapter"}) {
		t.Fatalf("keys after a second restore %q", got)
	}
	// the member names only what ran: an empty list upgrades
	b, _ := json.Marshal(mangaBackupItem("8", "chapter"))
	old := zipOf(t, entry{name: "backup.json", body: []byte(strings.Replace(header("4"), `"items":[`, `"progress_upgrades":[],"items":[`, 1) + string(b) + "]}")})
	mustRestore(t, dst, old)
	if got := dst.keysOf("8"); !reflect.DeepEqual(got, []string{"chapter", "volume"}) {
		t.Fatalf("keys %q, want chapter and volume", got)
	}
}
