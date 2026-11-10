package library

import (
	"fmt"
	"github.com/allister/allister/internal/plugin/plugintest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

var badChars = map[string]string{
	"NUL":          "a\x00b",
	"invalid utf8": "a\xffb",
	"bell":         "a\x07b",
	"C1 control":   "a\u0085b",
	"DEL":          "a\x7fb",
}

var seqID atomic.Int64

// saveMod saves a fresh book item after applying mod and returns the error.
func saveMod(l *Library, mod func(*plugin.ItemData)) error {
	d := bookData(fmt.Sprint("h", seqID.Add(1)))
	mod(&d)
	_, err := l.SaveItem(ctx, "book", "book", d)
	return err
}

func TestTextHygieneRejectedEverywhere(t *testing.T) {
	l := newLib(t)
	places := map[string]struct {
		mod  func(*plugin.ItemData, string)
		want string
	}{
		"title":      {func(d *plugin.ItemData, s string) { d.Titles = []plugin.Title{{Lang: "en", Text: s}} }, "title"},
		"field text": {func(d *plugin.ItemData, s string) { d.Fields = map[string]plugin.FieldValue{"author": {Text: s}} }, "author"},
		"field list": {func(d *plugin.ItemData, s string) {
			d.Fields = map[string]plugin.FieldValue{"translators": {List: []string{s}}}
		}, "translators"},
		"genre":         {func(d *plugin.ItemData, s string) { d.Genres = []string{s} }, "genre"},
		"ext id type":   {func(d *plugin.ItemData, s string) { d.ExternalIDs = map[string]string{s: "1"} }, "external id"},
		"ext id value":  {func(d *plugin.ItemData, s string) { d.ExternalIDs = map[string]string{"isbn": s} }, "external id"},
		"source":        {func(d *plugin.ItemData, s string) { d.Source = s }, "source"},
		"source id":     {func(d *plugin.ItemData, s string) { d.ID = s }, "source id"},
		"relation kind": {func(d *plugin.ItemData, s string) { d.Relations = []plugin.Relation{{Kind: s}} }, "relation"},
		"relation id": {func(d *plugin.ItemData, s string) {
			d.Relations = []plugin.Relation{{Kind: "k", Item: plugin.ItemSummary{ID: s}}}
		}, "relation"},
		"relation title": {func(d *plugin.ItemData, s string) {
			d.Relations = []plugin.Relation{{Kind: "k", Item: plugin.ItemSummary{Titles: []plugin.Title{{Lang: "en", Text: s}}}}}
		}, "relation"},
		"relation field": {func(d *plugin.ItemData, s string) {
			d.Relations = []plugin.Relation{{Kind: "k", Item: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"x": {Text: s}}}}}
		}, "relation"},
	}
	for name, p := range places {
		for cname, bad := range badChars {
			t.Run(name+"/"+cname, func(t *testing.T) {
				err := saveMod(l, func(d *plugin.ItemData) { p.mod(d, bad) })
				wantInvalid(t, err, p.want)
			})
		}
	}
	for cname, bad := range badChars {
		t.Run("tag/"+cname, func(t *testing.T) {
			d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "x" + cname, Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S"}}}}
			d.Tags = []plugin.Tag{{Name: bad}}
			_, err := l.SaveItem(ctx, "tv", "show", d)
			wantInvalid(t, err, "tag")
		})
	}
}

func TestTabAndNewlineRules(t *testing.T) {
	l := newLib(t)
	// text fields are long text: tab, newline and CRLF are fine
	if err := saveMod(l, func(d *plugin.ItemData) {
		d.Fields = map[string]plugin.FieldValue{"author": {Text: "a\tb\nc\r\nd"}}
	}); err != nil {
		t.Fatal(err)
	}
	// single-line places reject both
	for _, s := range []string{"a\tb", "a\nb"} {
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.Titles = []plugin.Title{{Lang: "en", Text: s}} }), "title")
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.Genres = []string{s} }), "genre")
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.Fields = map[string]plugin.FieldValue{"translators": {List: []string{s}}} }), "translators")
	}
}

func TestCreditTextHygiene(t *testing.T) {
	l := newLib(t)
	id := tvItem(t, l, "1")
	for cname, bad := range badChars {
		t.Run(cname, func(t *testing.T) {
			for _, c := range []plugin.Credit{
				{Role: bad, Person: plugin.PersonRef{ID: "p", Name: "N"}},
				{Role: "r", Person: plugin.PersonRef{ID: "p", Name: bad}},
				{Role: "r", Person: plugin.PersonRef{ID: bad, Name: "N"}},
				{Role: "r", Person: plugin.PersonRef{ID: "p", Name: "N"}, With: &plugin.PersonRef{ID: "w", Name: bad}},
				{Role: "r", Person: plugin.PersonRef{ID: "p", Name: "N"}, With: &plugin.PersonRef{ID: "w", Name: "N"}, WithRole: bad},
			} {
				wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{c}), "credit")
			}
		})
	}
}

func TestStoredDateCorruptionIsAnError(t *testing.T) {
	e := newEnv(t)
	d := bookData("1")
	d.Fields["released"] = plugin.FieldValue{Date: ptrTime(day(2000, 1, 2))}
	id := mustSave(t, e.lib, "book", "book", d)
	if _, err := e.db.Exec(`UPDATE item_fields SET date = 'garbage' WHERE item_id = ? AND key = 'released'`, id); err != nil {
		t.Fatal(err)
	}
	if it, err := e.lib.Item(ctx, id); err == nil || it != nil {
		t.Fatalf("Item = %v, %v, want error", it, err)
	}
	if _, err := e.db.Exec(`UPDATE item_fields SET date = '2000-01-02', num = NULL, vtype = 'number' WHERE item_id = ? AND key = 'released'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.Item(ctx, id); err == nil {
		t.Fatal("number row without num must error")
	}
}

func TestItemOfRemovedPluginStillLoadsWithRightShapes(t *testing.T) {
	e := newEnv(t)
	d := bookData("1")
	d.Fields = map[string]plugin.FieldValue{
		"author":      {Text: "A"},
		"pages":       {Num: fp(5)},
		"released":    {Date: ptrTime(day(2000, 1, 2))},
		"translators": {List: []string{"only"}},
		"format":      {Text: "paperback"},
		"homepage":    {Text: "https://x.example/"},
	}
	id := mustSave(t, e.lib, "book", "book", d)
	gone := New(e.db, plugin.NewRegistry(plugintest.NewMemEnabled()))
	it, err := gone.Item(ctx, id)
	if err != nil || it == nil {
		t.Fatalf("Item = %v, %v", it, err)
	}
	if !reflect.DeepEqual(it.Fields, d.Fields) {
		t.Fatalf("fields = %+v\nwant %+v", it.Fields, d.Fields)
	}
}

func TestListFieldValuesDedupedCaseSensitiveKeepFirst(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	d.Fields = map[string]plugin.FieldValue{"translators": {List: []string{"A", " A ", "a", "B", "A"}}}
	it := mustItem(t, l, mustSave(t, l, "book", "book", d))
	if want := []string{"A", "a", "B"}; !reflect.DeepEqual(it.Fields["translators"].List, want) {
		t.Fatalf("list = %q, want %q", it.Fields["translators"].List, want)
	}
}

func TestSaveCreditsAtomicOnFailure(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"credits insert", `CREATE TRIGGER boom BEFORE INSERT ON credits WHEN NEW.role = 'boom' BEGIN SELECT RAISE(ABORT,'boom'); END`},
		{"people insert", `CREATE TRIGGER boom BEFORE INSERT ON people WHEN NEW.source_id = 'bad' BEGIN SELECT RAISE(ABORT,'boom'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := tvItem(t, e.lib, "1")
			old := []plugin.Credit{
				{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Old"}},
				{Role: "actor", Person: plugin.PersonRef{ID: "p2", Name: "Two"}},
			}
			if err := e.lib.SaveCredits(ctx, id, old); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			next := []plugin.Credit{
				{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Renamed"}},
				{Role: "boom", Person: plugin.PersonRef{ID: "bad", Name: "New"}},
				{Role: "actor", Person: plugin.PersonRef{ID: "p3", Name: "Three"}},
			}
			if err := e.lib.SaveCredits(ctx, id, next); err == nil {
				t.Fatal("expected failure")
			}
			got, err := e.lib.Credits(ctx, id)
			if err != nil || len(got) != 2 || got[0].Person.Name != "Old" || got[1].Person.SourceID != "p2" {
				t.Fatalf("old credits not intact: %+v %v", got, err)
			}
			if n := count(t, e.db, `SELECT count(*) FROM people`); n != 2 {
				t.Fatalf("people = %d, want 2 (changes rolled back)", n)
			}
		})
	}
}

func TestURLHostMustExist(t *testing.T) {
	l := newLib(t)
	for _, u := range []string{"http://:80", "https://:443/x"} {
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.Fields = map[string]plugin.FieldValue{"homepage": {Text: u}} }), "homepage")
	}
}

func TestExternalIDKeyCollisionAfterTrim(t *testing.T) {
	l := newLib(t)
	wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.ExternalIDs = map[string]string{"isbn": "1", " isbn": "2"} }), "external id")
}

func TestPersonNameRules(t *testing.T) {
	l := newLib(t)
	id := tvItem(t, l, "1")
	wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p1", Name: "  "}}}), "name")
	if got, _ := l.Credits(ctx, id); len(got) != 0 {
		t.Fatal("rejected save stored credits")
	}
	if err := l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p1", Name: " Real "}}}); err != nil {
		t.Fatal(err)
	}
	// an empty incoming name keeps the stored one
	if err := l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p1", Name: ""}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Credits(ctx, id)
	if len(got) != 1 || got[0].Person.Name != "Real" {
		t.Fatalf("credits = %+v", got)
	}
}

func TestURLAndFileReferencesAreValidated(t *testing.T) {
	l := newLib(t)
	badURLs := []string{"javascript:alert(1)", "data:text/html,x", "//host/x", "../x", "ftp://h/x", "http://", "/abs", "a b\x00"}
	for _, u := range badURLs {
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.ExternalURL = u }), "external")
	}
	badRefs := append([]string{"a/b.jpg", `a\b.jpg`, "..", "a..b", "x:y"}, badURLs...)
	for _, u := range badRefs {
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) { d.CoverURL = u }), "cover")
	}
	for _, ok := range []string{"", "https://img.example/a.jpg", "http://img.example/a.jpg", "12.jpg"} {
		if err := saveMod(l, func(d *plugin.ItemData) { d.CoverURL = ok; d.ExternalURL = "https://e.example/x" }); err != nil {
			t.Fatalf("cover %q: %v", ok, err)
		}
	}
	for _, u := range badURLs {
		wantInvalid(t, saveMod(l, func(d *plugin.ItemData) {
			d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "r", URL: u}}}
		}), "relation item url")
	}
	id := tvItem(t, l, "9")
	for _, u := range badURLs {
		wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p", Name: "N", URL: u}}}), "url")
	}
	for _, u := range badRefs {
		wantInvalid(t, l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p", Name: "N", ImageURL: u}}}), "image")
	}
	// setters
	it := mustSave(t, l, "book", "book", bookData("s"))
	for _, u := range badRefs {
		wantInvalid(t, l.SetCoverPath(ctx, it, u), "cover")
	}
	if err := l.SetCoverPath(ctx, it, "5.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := l.SaveCredits(ctx, id, []plugin.Credit{{Role: "a", Person: plugin.PersonRef{ID: "p", Name: "N"}}}); err != nil {
		t.Fatal(err)
	}
	pp, _ := l.ItemPeople(ctx, id)
	for _, u := range badRefs {
		wantInvalid(t, l.SetPersonImagePath(ctx, pp[0].ID, u), "image")
	}
	if err := l.SetPersonImagePath(ctx, pp[0].ID, "7.png"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncStateValues(t *testing.T) {
	l := newLib(t)
	for _, s := range []string{"pending", "partial", "done"} {
		d := bookData("sync" + s)
		id := mustSave(t, l, "book", "book", d, WithSyncState(s))
		if err := l.SetSyncState(ctx, id, s); err != nil {
			t.Fatal(err)
		}
	}
	d := bookData("bad")
	_, err := l.SaveItem(ctx, "book", "book", d, WithSyncState("whatever"))
	wantInvalid(t, err, "sync")
	id := mustSave(t, l, "book", "book", d)
	wantInvalid(t, l.SetSyncState(ctx, id, ""), "sync")
}
