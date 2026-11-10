package library

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestExportEntriesHoldEveryStoredValue(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	// 1: a full entry whose pages value became inactive (percent is active now)
	d1 := bookWith("e1", fp(300))
	d1.ExternalIDs = map[string]string{"isbn": "978-1", "olid": "OL1"}
	id1, err := l.AddToList(bg, "book", "book", d1, Entry{Status: Completed, Rating: ip(8), Comment: "line one\nline two",
		StartedOn: tp(day(2024, 3, 1)), FinishedOn: tp(day(2024, 3, 9)), Progress: map[string]float64{"pages": 120}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetActiveProgress(bg, id1, []string{"percent"}); err != nil {
		t.Fatal(err)
	}
	if err := l.UpsertEntry(bg, Entry{ItemID: id1, Status: Completed, Rating: ip(8), Comment: "line one\nline two",
		StartedOn: tp(day(2024, 3, 1)), FinishedOn: tp(day(2024, 3, 9)), Progress: map[string]float64{"percent": 40}}); err != nil {
		t.Fatal(err)
	}
	// an item without an entry, an item of another plugin and of a twin plugin are left out
	mustSave(t, l, "book", "book", bookData("noentry"))
	addShow(t, l, "s1", nil, Entry{Status: Active})
	if _, err := l.AddToList(bg, "book2", "book", bookData("twin"), Entry{Status: Planned}); err != nil {
		t.Fatal(err)
	}
	// 2: a bare entry
	id2 := addBook(t, l, "e2", nil, Entry{Status: Planned})

	got, err := l.ExportEntries(bg, "book", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []plugin.ExportEntry{
		{Kind: "book", Titles: bookData("e1").Titles, ExternalIDs: map[string]string{"isbn": "978-1", "olid": "OL1"},
			Status: plugin.Completed, Rating: ip(8), Progress: map[string]float64{"pages": 120, "percent": 40},
			StartedOn: tp(day(2024, 3, 1)), FinishedOn: tp(day(2024, 3, 9)), Comment: "line one\nline two"},
		{Kind: "book", Titles: bookData("e2").Titles, Status: plugin.Planned},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export =\n%+v\nwant\n%+v", got, want)
	}
	if id1 >= id2 {
		t.Fatalf("ids %d %d", id1, id2)
	}
	// the kind filter: the plugin's kind gives the same, an unknown kind or plugin is invalid
	if again, err := l.ExportEntries(bg, "book", "book"); err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("kind filter: %+v %v", again, err)
	}
	if _, err := l.ExportEntries(bg, "book", "show"); err == nil {
		t.Fatal("foreign kind accepted")
	}
	if _, err := l.ExportEntries(bg, "nope", ""); err == nil {
		t.Fatal("unknown plugin accepted")
	}
	if none, err := l.ExportEntries(bg, "nop", ""); err != nil || len(none) != 0 {
		t.Fatalf("empty plugin: %+v %v", none, err)
	}
}

func TestExportEntriesFilterByKindAndOrderByItem(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	p := plugintest.BookPlugin()
	comic := p.M.Kinds[0]
	comic.Key, comic.Label, comic.LabelPlural = "comic", "Comic", "Comics"
	p.M.Kinds = append(p.M.Kinds, comic)
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	l := New(d, reg)
	add := func(kind, id string) {
		t.Helper()
		data := bookData(id)
		data.Kind = kind
		data.Titles = []plugin.Title{{Lang: "en", Text: id}}
		if _, err := l.AddToList(bg, "book", kind, data, Entry{Status: Active}); err != nil {
			t.Fatal(err)
		}
	}
	add("comic", "c1")
	add("book", "b1")
	add("comic", "c2")
	titles := func(es []plugin.ExportEntry) (out []string) {
		for _, e := range es {
			out = append(out, e.Kind+":"+e.Titles[0].Text)
		}
		return out
	}
	all, err := l.ExportEntries(bg, "book", "")
	if err != nil || !reflect.DeepEqual(titles(all), []string{"comic:c1", "book:b1", "comic:c2"}) {
		t.Fatalf("all: %v %v", titles(all), err)
	}
	comics, err := l.ExportEntries(bg, "book", "comic")
	if err != nil || !reflect.DeepEqual(titles(comics), []string{"comic:c1", "comic:c2"}) {
		t.Fatalf("comics: %v %v", titles(comics), err)
	}
}
