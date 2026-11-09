package library

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// lockedEnv is newEnv plus plugin "locked": the rich book whose pages field
// and genres are NOT editable.
func lockedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	p := richBook(plugintest.WithID("locked"))
	k := &p.M.Kinds[0]
	k.Fields[1].Editable = false // pages
	k.Genres.Editable = false
	if err := e.reg.Register(p); err != nil {
		t.Fatal(err)
	}
	return e
}

func lockedBook(t *testing.T, e *env) int64 {
	t.Helper()
	d := bookWith("l1", fp(300))
	d.Fields["isbn"] = plugin.FieldValue{Text: "978"}
	d.Genres = []string{"Sci-Fi"}
	d.ExternalIDs = map[string]string{"isbn": "978"}
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "r1"}}}
	id, err := e.lib.AddToList(bg, "locked", "book", d, Entry{Status: Active, Rating: ip(7), Comment: "c", Progress: map[string]float64{"pages": 12}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func baseUpdate() DetailsUpdate {
	return DetailsUpdate{
		Titles: []plugin.Title{{Lang: "en", Text: "Dune Messiah"}},
		Fields: map[string]plugin.FieldValue{"author": {Text: "F. Herbert"}},
	}
}

func TestUpdateDetailsReplacesEditableFieldsOnly(t *testing.T) {
	e := lockedEnv(t)
	id := lockedBook(t, e)
	before := mustItem(t, e.lib, id)
	entryBefore := mustEntry(t, e.lib, id)
	u := baseUpdate()
	u.Fields["translators"] = plugin.FieldValue{List: []string{"A", "B"}}
	if err := e.lib.UpdateDetails(bg, id, u); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, e.lib, id)
	if !reflect.DeepEqual(it.Titles, u.Titles) {
		t.Fatalf("titles %+v", it.Titles)
	}
	if it.Fields["author"].Text != "F. Herbert" || !reflect.DeepEqual(it.Fields["translators"].List, []string{"A", "B"}) {
		t.Fatalf("editable fields %+v", it.Fields)
	}
	if _, ok := it.Fields["isbn"]; ok {
		t.Fatalf("editable isbn not cleared: %+v", it.Fields)
	}
	if it.Fields["pages"].Num == nil || *it.Fields["pages"].Num != 300 {
		t.Fatalf("locked pages touched: %+v", it.Fields)
	}
	if !reflect.DeepEqual(it.Genres, before.Genres) || !reflect.DeepEqual(it.ExternalIDs, before.ExternalIDs) ||
		!reflect.DeepEqual(it.Relations, before.Relations) || it.Source != before.Source || it.SourceID != before.SourceID ||
		it.CoverPath != "" || it.SyncState != before.SyncState || !reflect.DeepEqual(it.ProgressKeys, before.ProgressKeys) {
		t.Fatalf("other parts touched:\n%+v\n%+v", before, it)
	}
	if after := mustEntry(t, e.lib, id); !reflect.DeepEqual(after, entryBefore) {
		t.Fatalf("entry touched: %+v -> %+v", entryBefore, after)
	}
}

func TestUpdateDetailsRejectsUnknownAndLockedParts(t *testing.T) {
	e := lockedEnv(t)
	id := lockedBook(t, e)
	before := mustItem(t, e.lib, id)
	for name, mod := range map[string]func(*DetailsUpdate){
		"unknown key":      func(u *DetailsUpdate) { u.Fields["nope"] = plugin.FieldValue{Text: "x"} },
		"locked key":       func(u *DetailsUpdate) { u.Fields["pages"] = plugin.FieldValue{Num: fp(1)} },
		"locked genres":    func(u *DetailsUpdate) { u.Genres = []string{"Drama"} },
		"tags not enabled": func(u *DetailsUpdate) { u.Tags = []plugin.Tag{{Name: "x"}} },
	} {
		u := baseUpdate()
		mod(&u)
		if err := e.lib.UpdateDetails(bg, id, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err := e.lib.ValidateDetails(bg, id, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: validate err = %v", name, err)
		}
	}
	if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("changed:\n%+v\n%+v", before, after)
	}
}

func TestUpdateDetailsValidatesLikeSaveItem(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "b1", fp(300), Entry{Status: Planned})
	before := mustItem(t, e.lib, id)
	for name, c := range map[string]struct {
		mod  func(*DetailsUpdate)
		want string
	}{
		"no title":        {func(u *DetailsUpdate) { u.Titles = nil }, "at least one title"},
		"blank title":     {func(u *DetailsUpdate) { u.Titles = []plugin.Title{{Lang: "en", Text: "  "}} }, "at least one title"},
		"control title":   {func(u *DetailsUpdate) { u.Titles[0].Text = "a\x07b" }, "control character"},
		"newline title":   {func(u *DetailsUpdate) { u.Titles[0].Text = "a\nb" }, "control character"},
		"bad lang":        {func(u *DetailsUpdate) { u.Titles[0].Lang = "EN US" }, "language"},
		"long title":      {func(u *DetailsUpdate) { u.Titles[0].Text = strings.Repeat("a", 301) }, ""},
		"enum":            {func(u *DetailsUpdate) { u.Fields["format"] = plugin.FieldValue{Text: "scroll"} }, "format"},
		"above max":       {func(u *DetailsUpdate) { u.Fields["score"] = plugin.FieldValue{Num: fp(11)} }, "at most"},
		"below min":       {func(u *DetailsUpdate) { u.Fields["pages"] = plugin.FieldValue{Num: fp(0)} }, "at least"},
		"wrong shape":     {func(u *DetailsUpdate) { u.Fields["pages"] = plugin.FieldValue{Text: "300"} }, "number"},
		"bad url":         {func(u *DetailsUpdate) { u.Fields["homepage"] = plugin.FieldValue{Text: "javascript:alert(1)"} }, "URL"},
		"required gone":   {func(u *DetailsUpdate) { delete(u.Fields, "author") }, "required"},
		"control in list": {func(u *DetailsUpdate) { u.Fields["translators"] = plugin.FieldValue{List: []string{"a\tb"}} }, "control"},
		"long genre":      {func(u *DetailsUpdate) { u.Genres = []string{strings.Repeat("g", 61)} }, ""},
		"bad cover":       {func(u *DetailsUpdate) { u.CoverPath = "../etc/passwd" }, "cover"},
		"js cover":        {func(u *DetailsUpdate) { u.CoverPath = "javascript:alert(1)" }, "cover"},
	} {
		u := baseUpdate()
		c.mod(&u)
		err := e.lib.UpdateDetails(bg, id, u)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if c.want != "" {
			wantInvalid(t, err, c.want)
		}
	}
	if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("changed:\n%+v\n%+v", before, after)
	}
	if err := e.lib.UpdateDetails(bg, 9999, baseUpdate()); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item: %v", err)
	}
	if err := e.lib.ValidateDetails(bg, 9999, baseUpdate()); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("validate unknown item: %v", err)
	}
	if err := e.lib.ValidateDetails(bg, id, baseUpdate()); err != nil {
		t.Fatalf("validate valid: %v", err)
	}
}

func TestUpdateDetailsTermsAndCover(t *testing.T) {
	e := newEnv(t)
	d := tvData("s1", fp(10))
	d.Genres = []string{"Old"}
	d.Tags = []plugin.Tag{{Name: "Gone", Rank: ip(10)}}
	d.CoverURL = "https://img.example/s1.jpg"
	id, err := e.lib.SaveItem(bg, "tv", "show", d)
	if err != nil {
		t.Fatal(err)
	}
	u := DetailsUpdate{
		Titles:    []plugin.Title{{Lang: "en", Text: "Show"}, {Lang: "original", Text: "Orig"}},
		Fields:    map[string]plugin.FieldValue{"type": {Text: "miniseries"}, "episodes": {Num: fp(6)}},
		Genres:    []string{"Drama", "drama", " Mystery "},
		Tags:      []plugin.Tag{{Name: "Time Travel", Rank: ip(80), Spoiler: true}, {Name: "Twist"}},
		CoverPath: "abc.png",
	}
	if err := e.lib.UpdateDetails(bg, id, u); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, e.lib, id)
	if !reflect.DeepEqual(it.Genres, []string{"Drama", "Mystery"}) {
		t.Fatalf("genres %v", it.Genres)
	}
	if len(it.Tags) != 2 || it.Tags[0].Name != "Time Travel" || *it.Tags[0].Rank != 80 || !it.Tags[0].Spoiler ||
		it.Tags[1].Name != "Twist" || it.Tags[1].Rank != nil || it.Tags[1].Spoiler {
		t.Fatalf("tags %+v", it.Tags)
	}
	if it.CoverPath != "abc.png" || *it.Fields["episodes"].Num != 6 || it.Fields["type"].Text != "miniseries" {
		t.Fatalf("item %+v", it)
	}
	// clearing: no terms, no cover
	u.Genres, u.Tags, u.CoverPath = nil, nil, ""
	if err := e.lib.UpdateDetails(bg, id, u); err != nil {
		t.Fatal(err)
	}
	it = mustItem(t, e.lib, id)
	if len(it.Genres) != 0 || len(it.Tags) != 0 || it.CoverPath != "" {
		t.Fatalf("not cleared: %+v", it)
	}
	// term limits as in SetTerms
	u.Tags = []plugin.Tag{{Name: "x", Rank: ip(101)}}
	wantInvalid(t, e.lib.UpdateDetails(bg, id, u), "rank")
	many := make([]string, 51)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	u.Tags, u.Genres = nil, many
	wantInvalid(t, e.lib.UpdateDetails(bg, id, u), "more than 50")
}

func TestUpdateDetailsIsAtomic(t *testing.T) {
	for name, trigger := range map[string]string{
		// the first write (titles) succeeds, the fields insert fails
		"after first write": `CREATE TRIGGER boom BEFORE INSERT ON item_fields BEGIN SELECT RAISE(ABORT, 'boom'); END`,
		// the last write (items row: cover and updated_at) fails
		"last write": `CREATE TRIGGER boom BEFORE UPDATE ON items BEGIN SELECT RAISE(ABORT, 'boom'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			d := tvData("s1", fp(10))
			d.Genres = []string{"Old"}
			id, err := e.lib.SaveItem(bg, "tv", "show", d)
			if err != nil {
				t.Fatal(err)
			}
			before := mustItem(t, e.lib, id)
			if _, err := e.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			u := DetailsUpdate{Titles: []plugin.Title{{Lang: "en", Text: "New"}},
				Fields: map[string]plugin.FieldValue{"episodes": {Num: fp(3)}}, Genres: []string{"New"}, CoverPath: "n.png"}
			if err := e.lib.UpdateDetails(bg, id, u); err == nil || errors.Is(err, ErrInvalid) {
				t.Fatalf("want a database error, got %v", err)
			}
			if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
				t.Fatalf("not rolled back:\n%+v\n%+v", before, after)
			}
		})
	}
}

func TestUpdateDetailsBumpsUpdatedAtAndSearchSeesNewTitle(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "b1", fp(300), Entry{Status: Planned})
	if _, err := e.db.Exec(`UPDATE items SET updated_at = ? WHERE id = ?`, oldStamp, id); err != nil {
		t.Fatal(err)
	}
	u := baseUpdate()
	u.Titles = []plugin.Title{{Lang: "en", Text: "Children of Dune"}}
	if err := e.lib.UpdateDetails(bg, id, u); err != nil {
		t.Fatal(err)
	}
	var stamp string
	if err := e.db.QueryRow(`SELECT updated_at FROM items WHERE id = ?`, id).Scan(&stamp); err != nil || stamp == oldStamp {
		t.Fatalf("updated_at %q %v", stamp, err)
	}
	search := func(text string) int {
		q := bookQ0
		q.Text = text
		p, err := e.lib.List(bg, q, "en")
		if err != nil {
			t.Fatal(err)
		}
		return p.Total
	}
	if search("children") != 1 {
		t.Fatal("new title not found")
	}
	for _, old := range []string{"Dune Messiah", "デューン", "Wüstenplanet"} {
		if search(old) != 0 {
			t.Fatalf("removed title %q still found", old)
		}
	}
}
