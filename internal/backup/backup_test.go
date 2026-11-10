package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

var bg = context.Background()

var clock = time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

type env struct {
	lib    *library.Library
	reg    *plugin.Registry
	covers string
	root   string // the data dir: covers live below it, nothing else may appear
	t      *testing.T
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	for _, p := range []plugin.Plugin{plugintest.BookPlugin(), plugintest.TVPlugin()} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return &env{lib: library.New(d, reg), reg: reg, covers: filepath.Join(root, "covers"), root: root, t: t}
}

func fp(f float64) *float64 { return &f }
func ip(i int) *int         { return &i }

// pngBytes is a real (tiny) PNG; seed makes distinct contents.
func pngBytes(seed uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{seed, 1, 2, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// saveCover stores an image in the env's covers dir like an upload does.
func (e *env) saveCover(seed uint8) string {
	e.t.Helper()
	name, err := covers.Save(e.covers, bytes.NewReader(pngBytes(seed)))
	if err != nil {
		e.t.Fatal(err)
	}
	return name
}

func (e *env) book(id string, cover string, en *library.Entry, opts ...library.SaveOption) int64 {
	e.t.Helper()
	return e.bookFrom("books", id, cover, en, opts...)
}

func (e *env) bookFrom(source, id string, cover string, en *library.Entry, opts ...library.SaveOption) int64 {
	e.t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: source, ID: id, Kind: "book",
		Titles: []plugin.Title{{Lang: "en", Text: "Book " + id}},
		Fields: map[string]plugin.FieldValue{"author": {Text: "Author " + id}, "pages": {Num: fp(300)}}, CoverURL: cover},
		Genres: []string{"Fantasy"}, ExternalIDs: map[string]string{"isbn": "isbn-" + id}}
	var itemID int64
	var err error
	if en != nil {
		itemID, err = e.lib.AddToList(bg, "book", "book", d, *en, opts...)
	} else {
		itemID, err = e.lib.SaveItem(bg, "book", "book", d, opts...)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return itemID
}

func (e *env) show(id string, en *library.Entry) int64 {
	e.t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: id, Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: "Show " + id}, {Lang: "original", Text: "原題"}},
		Fields: map[string]plugin.FieldValue{"episodes": {Num: fp(10)}, "type": {Text: "series"}}, CoverURL: "https://img.example/" + id + ".jpg"},
		Genres: []string{"Drama", "Mystery"}, Tags: []plugin.Tag{{Name: "Heist", Rank: ip(90)}, {Name: "Ending", Rank: ip(0), Spoiler: true}, {Name: "Plain"}},
		Relations: []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "tvdata", ID: id + "b", Kind: "show", URL: "https://tv.example/show/" + id + "b"}}}}
	var itemID int64
	var err error
	if en != nil {
		itemID, err = e.lib.AddToList(bg, "tv", "show", d, *en)
	} else {
		itemID, err = e.lib.SaveItem(bg, "tv", "show", d)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.lib.SaveCredits(bg, itemID, []plugin.Credit{{Role: "actor", Detail: "MAIN",
		Person: plugin.PersonRef{ID: "c" + id, Name: "Character " + id, ImageURL: "https://img.example/c.jpg"},
		With:   &plugin.PersonRef{ID: "a" + id, Name: "Actor " + id}, WithRole: "voice"}}); err != nil {
		e.t.Fatal(err)
	}
	return itemID
}

// archive writes the env's backup and returns the zip bytes.
func (e *env) archive() []byte {
	e.t.Helper()
	var b bytes.Buffer
	if err := Write(bg, &b, e.lib, []PluginVersion{{ID: "book", Version: "1.0.0"}}, e.covers, clock); err != nil {
		e.t.Fatal(err)
	}
	return b.Bytes()
}

// restore restores zip bytes into the env with optional limits.
func (e *env) restore(data []byte, lim Limits, enq func(int64, string)) (*Report, error) {
	opts := Options{Library: e.lib, CoversDir: e.covers, Limits: lim}
	if enq != nil {
		opts.Enqueue = func(_ context.Context, id int64, pluginID string) { enq(id, pluginID) }
	}
	return Restore(bg, bytes.NewReader(data), int64(len(data)), opts)
}

func mustRestore(t *testing.T, e *env, data []byte) *Report {
	t.Helper()
	rep, err := e.restore(data, Limits{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Aborted != "" {
		t.Fatalf("aborted: %s", rep.Aborted)
	}
	return rep
}

// doc is the decoded backup.json.
type doc struct {
	Format        string               `json:"format"`
	FormatVersion int                  `json:"format_version"`
	CreatedAt     string               `json:"created_at"`
	Plugins       []PluginVersion      `json:"plugins"`
	Items         []library.BackupItem `json:"items"`
}

// open reads the archive: its backup.json and the names of its files.
func open(t *testing.T, data []byte) (doc, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = b
	}
	var d doc
	if err := json.Unmarshal(files["backup.json"], &d); err != nil {
		t.Fatalf("backup.json: %v\n%s", err, files["backup.json"])
	}
	return d, files
}

// entry is one hand-made zip entry.
type entry struct {
	name string
	body []byte
	mode fs.FileMode // 0: regular file
	raw  *zip.FileHeader
}

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, en := range entries {
		if en.raw != nil {
			w, err := zw.CreateRaw(en.raw)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(en.body)
			continue
		}
		h := &zip.FileHeader{Name: en.name, Method: zip.Deflate}
		if en.mode != 0 {
			h.SetMode(en.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(en.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func header(version string) string {
	return `{"format":"allister.backup","format_version":` + version + `,"created_at":"2026-01-01T00:00:00Z","plugins":[],"items":[`
}

func jsonOf(t *testing.T, items ...any) []byte {
	t.Helper()
	var parts []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			parts = append(parts, s)
			continue
		}
		b, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, string(b))
	}
	return []byte(header("1") + strings.Join(parts, ",") + "]}")
}

func bookItem(id string) library.BackupItem {
	return library.BackupItem{PluginID: "book", Kind: "book", Source: "books", SourceID: id,
		Titles: []plugin.Title{{Lang: "en", Text: "Book " + id}}, ProgressKeys: []string{"pages"}}
}

// files lists every file below the env's data dir (relative paths).
func (e *env) files() []string {
	var out []string
	filepath.WalkDir(e.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(e.root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func itemCount(t *testing.T, e *env) int {
	t.Helper()
	n := 0
	if err := e.lib.BackupItems(bg, 200, func(library.BackupItem) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupRoundTripsAllPluginsIncludingDisabled(t *testing.T) {
	a := newEnv(t)
	cover := a.saveCover(1)
	a.book("1", cover, &library.Entry{Status: library.Completed, Rating: ip(9), Comment: "great\nbook",
		StartedOn: ptime(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)), FinishedOn: ptime(time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC)),
		Progress: map[string]float64{"pages": 300}})
	// unmatched manual item with an external id and an uploaded cover
	a.bookFrom("manual", "m", a.saveCover(2), &library.Entry{Status: library.Planned}, library.WithSyncState("done"))
	showID := a.show("1", &library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 4}})
	// an inactive progress value stays in the backup
	if err := a.lib.SetActiveProgress(bg, showID, []string{"season", "episode"}); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.UpsertEntry(bg, library.Entry{ItemID: showID, Status: library.Active, Progress: map[string]float64{"episode": 4, "season": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.SetActiveProgress(bg, showID, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	a.show("2", nil)
	// the tv plugin is disabled: its data is still in the backup
	if err := a.reg.SetEnabled(bg, "tv", false); err != nil {
		t.Fatal(err)
	}
	// more than one batch
	for i := 0; i < 450; i++ {
		a.book(fmt.Sprint("bulk", i), "", nil)
	}
	data := a.archive()
	d, files := open(t, data)
	if d.Format != "allister.backup" || d.FormatVersion != 1 || d.CreatedAt != "2026-11-10T09:30:00Z" || len(d.Plugins) != 1 {
		t.Fatalf("header = %+v", d)
	}
	if len(d.Items) != 454 {
		t.Fatalf("items = %d", len(d.Items))
	}
	var tvItems int
	for _, it := range d.Items {
		if it.PluginID == "tv" {
			tvItems++
		}
	}
	if tvItems != 2 {
		t.Fatalf("disabled plugin items = %d", tvItems)
	}
	if len(files) != 3 { // backup.json + 2 covers
		t.Fatalf("files = %v", keys(files))
	}

	b := newEnv(t)
	if err := b.reg.SetEnabled(bg, "tv", false); err != nil {
		t.Fatal(err)
	}
	var enq []string
	rep, err := b.restore(data, Limits{}, func(id int64, p string) { enq = append(enq, p) })
	if err != nil || rep.Aborted != "" {
		t.Fatalf("restore: %v %q", err, rep.Aborted)
	}
	if rep.Created != 454 || rep.Skipped+rep.Failed+rep.UnknownPlugin != 0 {
		t.Fatalf("report = %+v", rep)
	}
	// sync jobs only for pending (non-manual) items, also of the disabled plugin
	if len(enq) != 453 || !contains(enq, "tv") {
		t.Fatalf("enqueued %d (%v...)", len(enq), enq[:3])
	}
	d2, files2 := open(t, b.archive())
	if !reflect.DeepEqual(d.Items, d2.Items) {
		for i := range d.Items {
			if !reflect.DeepEqual(d.Items[i], d2.Items[i]) {
				x, _ := json.Marshal(d.Items[i])
				y, _ := json.Marshal(d2.Items[i])
				t.Fatalf("item %d differs:\nA %s\nB %s", i, x, y)
			}
		}
		t.Fatal("items differ")
	}
	if !reflect.DeepEqual(keys(files), keys(files2)) || !bytes.Equal(files["covers/"+cover], files2["covers/"+cover]) {
		t.Fatalf("covers differ: %v vs %v", keys(files), keys(files2))
	}
}

func ptime(t time.Time) *time.Time { return &t }

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestBackupRoundTripsGenresAndTags(t *testing.T) {
	a := newEnv(t)
	a.show("1", nil)
	data := a.archive()
	b := newEnv(t)
	mustRestore(t, b, data)
	d, _ := open(t, b.archive())
	it := d.Items[0]
	if !reflect.DeepEqual(it.Genres, []string{"Drama", "Mystery"}) {
		t.Fatalf("genres = %v", it.Genres)
	}
	if len(it.Tags) != 3 || it.Tags[0].Name != "Heist" || *it.Tags[0].Rank != 90 || *it.Tags[1].Rank != 0 || !it.Tags[1].Spoiler || it.Tags[2].Rank != nil {
		t.Fatalf("tags = %+v", it.Tags)
	}
}

func TestBackupRestoreMergesAndIsIdempotent(t *testing.T) {
	a := newEnv(t)
	a.book("1", a.saveCover(1), &library.Entry{Status: library.Active})
	a.book("2", "", nil)
	data := a.archive()

	b := newEnv(t)
	// B already holds book 1 with an edited title: it must stay untouched
	if _, err := b.lib.SaveItem(bg, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "books", ID: "1", Kind: "book",
		Titles: []plugin.Title{{Lang: "en", Text: "Edited in B"}}}}); err != nil {
		t.Fatal(err)
	}
	rep := mustRestore(t, b, data)
	if rep.Created != 1 || rep.Skipped != 1 || len(rep.SkippedRows) != 1 || rep.SkippedRows[0].SourceID != "1" {
		t.Fatalf("first = %+v", rep)
	}
	it, _ := b.lib.ItemBySource(bg, "book", "book", "books", "1")
	if it.Titles[0].Text != "Edited in B" || it.CoverPath != "" {
		t.Fatalf("existing item changed: %+v", it)
	}
	if en, _ := b.lib.Entry(bg, it.ID); en != nil {
		t.Fatal("entry added to an existing item")
	}
	rep = mustRestore(t, b, data)
	if rep.Created != 0 || rep.Skipped != 2 {
		t.Fatalf("second = %+v", rep)
	}
	if n := itemCount(t, b); n != 2 {
		t.Fatalf("items = %d", n)
	}
}

func TestBackupIncludesEveryItemCover(t *testing.T) {
	a := newEnv(t)
	sourced := a.saveCover(1)
	uploaded := a.saveCover(2)
	a.book("1", sourced, nil)
	a.book("2", sourced, nil) // shared content-hash name: one file
	a.bookFrom("manual", "m", uploaded, nil, library.WithSyncState("done"))
	a.book("r", "https://img.example/remote.jpg", nil)
	a.book("gone", "deadbeef.jpg", nil) // the file is not on disk
	// a person photo on disk is not part of the backup
	photo := a.saveCover(3)
	id := a.show("s1", nil)
	cs, _ := a.lib.Credits(bg, id)
	if err := a.lib.SetPersonImagePath(bg, cs[0].Person.ID, photo); err != nil {
		t.Fatal(err)
	}
	d, files := open(t, a.archive())
	want := []string{"backup.json", "covers/" + sourced, "covers/" + uploaded}
	sort.Strings(want)
	if got := keys(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	by := map[string]library.BackupItem{}
	for _, it := range d.Items {
		by[it.SourceID] = it
	}
	if by["1"].Cover != sourced || by["m"].Cover != uploaded || by["r"].CoverURL != "https://img.example/remote.jpg" || by["r"].Cover != "" {
		t.Fatalf("covers = %+v / %+v / %+v", by["1"], by["m"], by["r"])
	}
	if by["gone"].Cover != "" || !by["gone"].CoverMissing {
		t.Fatalf("missing cover = %+v", by["gone"])
	}
	if by["1"].CoverMissing {
		t.Fatal("present cover marked missing")
	}
	raw := string(files["backup.json"])
	if !strings.Contains(raw, `"cover_missing":true`) || strings.Count(raw, "cover_missing") != 1 {
		t.Fatalf("cover_missing: %s", raw)
	}
	if by["1"].Credits != nil {
		t.Fatal("unexpected credits on books")
	}
	for _, it := range d.Items {
		for _, c := range it.Credits {
			if c.Person.ImageURL != "" && !strings.HasPrefix(c.Person.ImageURL, "https://") {
				t.Fatalf("local person photo in backup: %+v", c.Person)
			}
		}
	}
	// the restored covers are stored under their content hash and referenced
	b := newEnv(t)
	mustRestore(t, b, func() []byte { return a.archive() }())
	it, _ := b.lib.ItemBySource(bg, "book", "book", "manual", "m")
	if it.CoverPath != uploaded {
		t.Fatalf("restored cover = %q", it.CoverPath)
	}
	if _, err := os.Stat(filepath.Join(b.covers, uploaded)); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRefusesNewerFormat(t *testing.T) {
	item, _ := json.Marshal(bookItem("1"))
	for _, tc := range []struct{ name, json, want string }{
		{"newer", header("2") + string(item) + "]}", "format version 2"},
		{"zero", header("0") + string(item) + "]}", "format version 0"},
		{"negative", header("-1") + string(item) + "]}", "format version -1"},
		{"fraction", header("1.5") + string(item) + "]}", "format version"},
		{"string", header(`"1"`) + string(item) + "]}", "format version"},
		{"missing version", `{"format":"allister.backup","items":[` + string(item) + "]}", "format version"},
		{"other format", `{"format":"something.else","format_version":1,"items":[` + string(item) + "]}", "not an Allister backup"},
		{"missing format", `{"format_version":1,"items":[` + string(item) + "]}", "not an Allister backup"},
		{"array", `[` + string(item) + `]`, "not an Allister backup"},
		{"not json", `hello`, "not an Allister backup"},
		{"empty", ``, "not an Allister backup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(tc.json)}), Limits{}, nil)
			var rej *RejectError
			if !errors.As(err, &rej) || !strings.Contains(rej.Error(), tc.want) {
				t.Fatalf("err = %v, want reject mentioning %q", err, tc.want)
			}
			if n := itemCount(t, e); n != 0 {
				t.Fatalf("restored %d items", n)
			}
		})
	}
	// the version message names the supported version too
	e := newEnv(t)
	_, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(header("7") + "]}")}), Limits{}, nil)
	if err == nil || !strings.Contains(err.Error(), "7") || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupRejectsNonZipAndMissingJSON(t *testing.T) {
	e := newEnv(t)
	var rej *RejectError
	if _, err := e.restore([]byte("this is not a zip file at all"), Limits{}, nil); !errors.As(err, &rej) {
		t.Fatalf("non-zip err = %v", err)
	}
	if _, err := e.restore(zipOf(t, entry{name: "covers/a.png", body: pngBytes(1)}), Limits{}, nil); !errors.As(err, &rej) ||
		!strings.Contains(err.Error(), "backup.json") {
		t.Fatalf("missing json err = %v", err)
	}
	// backup.json in a sub directory does not count
	if _, err := e.restore(zipOf(t, entry{name: "x/backup.json", body: jsonOf(t, bookItem("1"))}), Limits{}, nil); !errors.As(err, &rej) {
		t.Fatalf("nested json err = %v", err)
	}
	if e.files() != nil {
		t.Fatalf("files written: %v", e.files())
	}
}

func TestBackupMalformedJSONKeepsEarlierItems(t *testing.T) {
	good1, _ := json.Marshal(bookItem("1"))
	good2, _ := json.Marshal(bookItem("2"))
	t.Run("mid-stream syntax error", func(t *testing.T) {
		e := newEnv(t)
		body := header("1") + string(good1) + `,{"plugin_id": "book", "kind": ` + `,` + string(good2) + "]}"
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Created != 1 || rep.Aborted == "" || !strings.Contains(rep.Aborted, "1 item") {
			t.Fatalf("report = %+v", rep)
		}
		if n := itemCount(t, e); n != 1 {
			t.Fatalf("items = %d", n)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		e := newEnv(t)
		body := header("1") + string(good1) + "," + string(good2[:len(good2)/2])
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil || rep.Created != 1 || rep.Aborted == "" {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
	t.Run("trailing garbage", func(t *testing.T) {
		e := newEnv(t)
		body := header("1") + string(good1) + "]} trailing"
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil || rep.Aborted == "" || !strings.Contains(rep.Aborted, "after the end") {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		// each item is its own transaction: the one read before stays
		if rep.Created != 1 {
			t.Fatalf("created = %d", rep.Created)
		}
	})
	t.Run("item not an object and wrong types are failed rows", func(t *testing.T) {
		e := newEnv(t)
		body := header("1") + string(good1) + `,42,"text",[1],{"plugin_id":"book","kind":"book","source":"books","source_id":"x","titles":"nope"},` + string(good2) + "]}"
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil || rep.Aborted != "" {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		if rep.Created != 2 || rep.Failed != 4 || len(rep.FailedRows) != 4 || rep.FailedRows[0].Index != 2 ||
			!strings.Contains(rep.FailedRows[0].Reason, "not a JSON object") {
			t.Fatalf("report = %+v", rep)
		}
		if rep.FailedRows[3].SourceID != "" && rep.FailedRows[3].Reason == "" {
			t.Fatalf("row = %+v", rep.FailedRows[3])
		}
	})
	t.Run("items not an array", func(t *testing.T) {
		e := newEnv(t)
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(`{"format":"allister.backup","format_version":1,"items":{}}`)}), Limits{}, nil)
		if err != nil || rep.Aborted == "" {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
}

func TestBackupReportsUnknownPluginRowsWithoutFailing(t *testing.T) {
	e := newEnv(t)
	unknown := bookItem("u1")
	unknown.PluginID = "music"
	badKind := bookItem("k1")
	badKind.Kind = "album"
	badField := bookItem("f1")
	badField.Fields = map[string]plugin.FieldValue{"mood": {Text: "x"}}
	badProgress := bookItem("p1")
	badProgress.Entry = &library.BackupEntry{Status: library.Active, Progress: map[string]float64{"chapter": 3}}
	hostile := bookItem("h1")
	hostile.Titles[0].Text = "evil\x1b[31m"
	hostile2 := bookItem("h2")
	hostile2.ExternalURL = "javascript:alert(1)"
	data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"), unknown, badKind, badField, badProgress, hostile, hostile2, bookItem("2"))})
	rep, err := e.restore(data, Limits{}, nil)
	if err != nil || rep.Aborted != "" {
		t.Fatalf("restore: %v %+v", err, rep)
	}
	if rep.Created != 2 || rep.UnknownPlugin != 1 || rep.Failed != 5 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.UnknownRows) != 1 || rep.UnknownRows[0].PluginID != "music" || rep.UnknownRows[0].SourceID != "u1" {
		t.Fatalf("unknown rows = %+v", rep.UnknownRows)
	}
	reasons := ""
	for _, r := range rep.FailedRows {
		reasons += r.Reason + "|"
		if r.Reason == "" || r.SourceID == "" {
			t.Fatalf("row without identity or reason: %+v", r)
		}
		if strings.ContainsRune(r.Title, 0x1b) {
			t.Fatalf("control character echoed: %q", r.Title)
		}
	}
	for _, want := range []string{"no kind", "unknown field", "not a progress", "control character", "http(s) URL"} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("reasons %q lack %q", reasons, want)
		}
	}
}

func TestBackupRejectsZipSlipAndNonImages(t *testing.T) {
	long := strings.Repeat("a", 129) + ".png"
	html := []byte("<!DOCTYPE html><html><script>alert(1)</script></html>")
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	items := []any{}
	add := func(id, cover string) {
		it := bookItem(id)
		it.Cover = cover
		items = append(items, it)
	}
	add("ok", "good.png")
	add("dup", "dup.png")
	add("svg", "pic.svg")
	add("html", "page.png")
	add("missing", "absent.png")
	add("nested", "x.png") // only covers/sub/x.png exists
	add("link", "link.png")
	add("slip", "evil.png") // only ../evil.png and covers/../evil.png exist
	data := zipOf(t,
		entry{name: "backup.json", body: jsonOf(t, items...)},
		entry{name: "backup.json", body: []byte("garbage: first wins")},
		entry{name: "covers/", mode: fs.ModeDir | 0o755},
		entry{name: "covers/good.png", body: pngBytes(1)},
		entry{name: "covers/dup.png", body: pngBytes(2)},
		entry{name: "covers/dup.png", body: html},
		entry{name: "covers/pic.svg", body: svg},
		entry{name: "covers/page.png", body: html},
		entry{name: "covers/sub/x.png", body: pngBytes(3)},
		entry{name: "covers/link.png", body: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777},
		entry{name: "../evil.png", body: pngBytes(4)},
		entry{name: "covers/../evil.png", body: pngBytes(4)},
		entry{name: "/abs.png", body: pngBytes(4)},
		entry{name: `covers\back.png`, body: pngBytes(4)},
		entry{name: `covers/a\b.png`, body: pngBytes(4)},
		entry{name: "covers/.hidden.png", body: pngBytes(4)},
		entry{name: "covers/" + long, body: pngBytes(4)},
		entry{name: "other.txt", body: []byte("x")},
	)
	e := newEnv(t)
	rep, err := e.restore(data, Limits{}, nil)
	if err != nil || rep.Aborted != "" {
		t.Fatalf("restore: %v %+v", err, rep)
	}
	if rep.Created != 8 || rep.Failed != 0 {
		t.Fatalf("report = %+v", rep)
	}
	// ignored: duplicate backup.json, covers/ dir, duplicate dup.png, nested,
	// symlink, ../evil, covers/../evil, /abs, two backslash names, dot name,
	// long name, other.txt
	if rep.Ignored != 13 {
		t.Fatalf("ignored = %d", rep.Ignored)
	}
	cover := func(id string) string {
		it, _ := e.lib.ItemBySource(bg, "book", "book", "books", id)
		return it.CoverPath
	}
	if c := cover("ok"); c == "" || c == "good.png" || !strings.HasSuffix(c, ".png") {
		t.Fatalf("good cover = %q (want a content-hash name)", c)
	}
	if cover("dup") == "" {
		t.Fatal("first duplicate did not win")
	}
	for _, id := range []string{"svg", "html", "missing", "nested", "link", "slip"} {
		if c := cover(id); c != "" {
			t.Fatalf("%s cover = %q", id, c)
		}
	}
	if len(rep.Notes) != 6 {
		t.Fatalf("notes = %+v", rep.Notes)
	}
	// nothing but the two hashed images was written, all inside covers/
	files := e.files()
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	for _, f := range files {
		if !strings.HasPrefix(f, "covers/") || strings.Count(f, "/") != 1 {
			t.Fatalf("file outside covers: %s", f)
		}
	}
}

func TestBackupRejectsOversizedArchive(t *testing.T) {
	item, _ := json.Marshal(bookItem("1"))
	t.Run("backup.json over its cap", func(t *testing.T) {
		e := newEnv(t)
		body := header("1") + string(item) + "," + `{"plugin_id":"book","kind":"book","source":"books","source_id":"2","titles":[{"lang":"en","text":"` + strings.Repeat("x", 5000) + `"}]}]}`
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{MaxJSONBytes: 2000}, nil)
		if err != nil || !strings.Contains(rep.Aborted, "backup.json is larger than") {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		if rep.Created != 1 {
			t.Fatalf("created = %d", rep.Created)
		}
	})
	t.Run("total uncompressed bytes (zip bomb)", func(t *testing.T) {
		e := newEnv(t)
		big := bookItem("2")
		big.Cover = "big.png"
		bomb := append(pngBytes(1), make([]byte, 50000)...) // inflates far beyond its compressed size
		data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"), big)}, entry{name: "covers/big.png", body: bomb})
		if len(data) > 5000 {
			t.Fatalf("test zip is not small: %d", len(data))
		}
		rep, err := e.restore(data, Limits{MaxTotalBytes: 20000}, nil)
		if err != nil || !strings.Contains(rep.Aborted, "unpacks to more than") {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		if rep.Created != 1 {
			t.Fatalf("created = %d", rep.Created)
		}
		for _, f := range e.files() {
			t.Fatalf("partial cover left: %s", f)
		}
	})
	t.Run("header claims a huge size", func(t *testing.T) {
		e := newEnv(t)
		body := jsonOf(t, bookItem("1"))
		// a raw entry with the real bytes but a claimed size of 1 TiB
		raw := &zip.FileHeader{Name: "backup.json", Method: zip.Store, CRC32: crc32.ChecksumIEEE(body),
			CompressedSize64: uint64(len(body)), UncompressedSize64: 1 << 40}
		data := zipOf(t, entry{raw: raw, body: body})
		// the claim is never trusted: the real bytes are read through the
		// limits and the size mismatch shows up as a damaged file at its end
		rep, err := e.restore(data, Limits{}, nil)
		if err != nil || rep.Created != 1 || !strings.Contains(rep.Aborted, "damaged") {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
	t.Run("cover over the per-file cap", func(t *testing.T) {
		e := newEnv(t)
		it := bookItem("1")
		it.Cover = "huge.png"
		huge := append(pngBytes(1), make([]byte, covers.MaxUploadBytes)...)
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: jsonOf(t, it)}, entry{name: "covers/huge.png", body: huge}), Limits{}, nil)
		if err != nil || rep.Created != 1 || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0].Reason, "larger than") {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		if len(e.files()) != 0 {
			t.Fatalf("files = %v", e.files())
		}
	})
	t.Run("too many entries", func(t *testing.T) {
		e := newEnv(t)
		entries := []entry{{name: "backup.json", body: jsonOf(t, bookItem("1"))}}
		for i := 0; i < 10; i++ {
			entries = append(entries, entry{name: fmt.Sprintf("covers/f%d.png", i), body: pngBytes(1)})
		}
		_, err := e.restore(zipOf(t, entries...), Limits{MaxEntries: 10}, nil)
		var rej *RejectError
		if !errors.As(err, &rej) || !strings.Contains(err.Error(), "more than 10 files") {
			t.Fatalf("err = %v", err)
		}
		if n := itemCount(t, e); n != 0 {
			t.Fatalf("items = %d", n)
		}
	})
	t.Run("too many items", func(t *testing.T) {
		e := newEnv(t)
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"), bookItem("2"), bookItem("3"))}), Limits{MaxItems: 2}, nil)
		if err != nil || !strings.Contains(rep.Aborted, "more than 2 items") || rep.Created != 2 {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
}

func TestBackupRestoreStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(bg)
	data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"), bookItem("2"), bookItem("3"))})
	opts := Options{Library: e.lib, CoversDir: e.covers, Enqueue: func(context.Context, int64, string) { cancel() }}
	rep, err := Restore(ctx, bytes.NewReader(data), int64(len(data)), opts)
	if !errors.Is(err, context.Canceled) || rep == nil || rep.Created != 1 {
		t.Fatalf("rep = %+v, err = %v", rep, err)
	}
	if n := itemCount(t, e); n != 1 {
		t.Fatalf("items = %d", n)
	}
}

func TestBackupReportListsAreCapped(t *testing.T) {
	e := newEnv(t)
	var items []any
	for i := 0; i < 250; i++ {
		it := bookItem(fmt.Sprint(i))
		it.PluginID = "nope"
		items = append(items, it)
	}
	rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: jsonOf(t, items...)}), Limits{}, nil)
	if err != nil || rep.UnknownPlugin != 250 || len(rep.UnknownRows) != MaxListed || rep.MoreUnknown != 50 {
		t.Fatalf("report: %d rows, more %d, %v", len(rep.UnknownRows), rep.MoreUnknown, err)
	}
}

func TestBackupWriteStopsOnWriteError(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 300; i++ {
		e.book(fmt.Sprint(i), "", nil)
	}
	boom := errors.New("client gone")
	err := Write(bg, failAfter{n: 1000, err: boom}, e.lib, nil, e.covers, clock)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

type failAfter struct {
	n   int
	err error
}

func (f failAfter) Write(p []byte) (int, error) { return 0, f.err }
