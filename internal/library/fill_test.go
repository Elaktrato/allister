package library

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// fillEnv registers "fillp", a richBook with tags enabled, beside the usual
// fixtures.
func fillEnv(t *testing.T) *env {
	t.Helper()
	p := richBook()
	p.M.ID = "fillp"
	p.M.Kinds[0].Tags = plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true}
	return newEnvWith(t, p)
}

func fillItem(t *testing.T, e *env, d plugin.ItemData) int64 {
	t.Helper()
	return mustSave(t, e.lib, "fillp", "book", d)
}

func fillData(id string) plugin.ItemData {
	return plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "books", ID: id, Kind: "book",
		Titles: []plugin.Title{{Lang: "en", Text: "Mine"}},
		Fields: map[string]plugin.FieldValue{"author": {Text: "Mine"}}}}
}

func itemStamp(t *testing.T, e *env, id int64) string {
	t.Helper()
	var s string
	if err := e.db.QueryRow(`SELECT updated_at FROM items WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func setItemStamp(t *testing.T, e *env, id int64, stamp string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE items SET updated_at = ? WHERE id = ?`, stamp, id); err != nil {
		t.Fatal(err)
	}
}

func TestFillKeepsStoredValues(t *testing.T) {
	e := fillEnv(t)
	id := fillItem(t, e, fillData("k1"))
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'k1.jpg', banner_path = 'b1.jpg',
		trailer_url = 'https://video.example/mine', trailer_thumb = '' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Titles:   []plugin.Title{{Lang: "en", Text: "Mine"}},
			Fields:   map[string]plugin.FieldValue{"author": {Text: "Theirs"}},
			CoverURL: "https://img.example/theirs.jpg",
		},
		BannerURL: "https://img.example/banner.jpg",
		Trailer:   &plugin.Trailer{URL: "https://video.example/theirs", ThumbnailURL: "https://img.example/t.jpg"},
	}
	applied, err := e.lib.FillFromSource(ctx, id, in, nil, false)
	if err != nil || len(applied) != 0 {
		t.Fatalf("FillFromSource = %v, %v; want none, nil", applied, err)
	}
	it := mustItem(t, e.lib, id)
	if it.Fields["author"].Text != "Mine" || it.CoverPath != "k1.jpg" || it.BannerPath != "b1.jpg" ||
		it.Trailer == nil || it.Trailer.URL != "https://video.example/mine" || it.Trailer.ThumbnailURL != "" {
		t.Fatalf("item = %+v", it)
	}
	if got := itemStamp(t, e, id); got != "2000-01-01T00:00:00Z" {
		t.Fatalf("updated_at = %q, want unchanged", got)
	}

	// a stored remote cover URL counts as set; a renamed title in a language
	// the item has is not appended
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'https://img.example/mine.jpg' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	in.Titles = []plugin.Title{{Lang: "en", Text: "Theirs"}}
	applied, err = e.lib.FillFromSource(ctx, id, in, nil, false)
	if err != nil || len(applied) != 0 {
		t.Fatalf("FillFromSource = %v, %v; want none, nil", applied, err)
	}
	// a title in a language the item lacks is appended after the stored one
	in.Titles = []plugin.Title{{Lang: "en", Text: "Theirs"}, {Lang: "de", Text: "Ihrs"}}
	applied, err = e.lib.FillFromSource(ctx, id, in, nil, false)
	if err != nil || !slices.Equal(applied, []string{"titles"}) {
		t.Fatalf("FillFromSource = %v, %v; want [titles], nil", applied, err)
	}
	it = mustItem(t, e.lib, id)
	if want := []plugin.Title{{Lang: "en", Text: "Mine"}, {Lang: "de", Text: "Ihrs"}}; !reflect.DeepEqual(it.Titles, want) {
		t.Fatalf("titles = %+v, want %+v", it.Titles, want)
	}
	if it.CoverPath != "https://img.example/mine.jpg" || it.Fields["author"].Text != "Mine" {
		t.Fatalf("item = %+v", it)
	}
}

func TestFillFillsEmpty(t *testing.T) {
	e := fillEnv(t)
	id := fillItem(t, e, fillData("f1"))
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Fields:   map[string]plugin.FieldValue{"format": {Text: "paperback"}, "pages": {Num: fp(412)}, "author": {Text: "Theirs"}},
			CoverURL: "https://img.example/f1.jpg",
		},
		BannerURL: "https://img.example/banner.jpg",
		Trailer:   &plugin.Trailer{URL: "https://video.example/f1", ThumbnailURL: "https://img.example/t.jpg"},
	}
	applied, err := e.lib.FillFromSource(ctx, id, in, nil, false)
	want := []string{"field.pages", "field.format", "cover", "banner", "trailer"}
	if err != nil || !slices.Equal(applied, want) {
		t.Fatalf("FillFromSource = %v, %v; want %v, nil", applied, err, want)
	}
	it := mustItem(t, e.lib, id)
	if it.Fields["format"].Text != "paperback" || it.Fields["pages"].Num == nil || *it.Fields["pages"].Num != 412 ||
		it.Fields["author"].Text != "Mine" {
		t.Fatalf("fields = %+v", it.Fields)
	}
	if it.CoverPath != "https://img.example/f1.jpg" || it.BannerPath != "https://img.example/banner.jpg" ||
		it.Trailer == nil || *it.Trailer != *in.Trailer {
		t.Fatalf("item = %+v", it)
	}
	if got := itemStamp(t, e, id); got == "2000-01-01T00:00:00Z" {
		t.Fatal("updated_at not bumped")
	}
}

func TestFillListsAppendOnly(t *testing.T) {
	e := fillEnv(t)
	d := fillData("l1")
	d.Titles = append(d.Titles, plugin.Title{Lang: plugin.AltTitleLang, Text: "Syn A"})
	d.Genres = []string{"B"}
	d.Tags = []plugin.Tag{{Name: "T", Rank: ip(50)}}
	d.Links = []plugin.Link{{Label: "Mine", URL: "https://watch.example/1"}}
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "r1", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "R1"}}}}}
	id := fillItem(t, e, d)
	// a value stored as text before the field became a list reads as [X]
	if _, err := e.db.Exec(`INSERT INTO item_fields(item_id, key, vtype, text, seq) VALUES (?, 'translators', 'text', 'X', 0)`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Stored Name"}}}); err != nil {
		t.Fatal(err)
	}
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Titles: []plugin.Title{{Lang: "en", Text: "Mine"}, {Lang: plugin.AltTitleLang, Text: "Syn B"}, {Lang: plugin.AltTitleLang, Text: "Syn A"}},
			Fields: map[string]plugin.FieldValue{"translators": {List: []string{"Y", "X"}}},
		},
		Genres: []string{"A", "b", "C"},
		Tags:   []plugin.Tag{{Name: "T", Rank: ip(90)}, {Name: "U", Rank: ip(10), Spoiler: true}},
		Links:  []plugin.Link{{Label: "Theirs", URL: "https://watch.example/1"}, {Label: "Other", URL: "https://watch.example/2"}},
		Relations: []plugin.Relation{
			{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "r1", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "R1 renamed"}}}},
			{Kind: "prequel", Item: plugin.ItemSummary{Source: "books", ID: "r0", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "R0"}}}},
		},
	}
	credits := []plugin.Credit{
		{Role: "author", Person: plugin.PersonRef{ID: "p2", Name: "Two"}},
		{Role: "author", Person: plugin.PersonRef{ID: "p1"}}, // same as stored: no name keeps the stored one
	}
	// without withCredits the credits are not touched
	applied, err := e.lib.FillFromSource(ctx, id, in, credits, false)
	want := []string{"titles", "field.translators", "genres", "tags", "links", "relations"}
	if err != nil || !slices.Equal(applied, want) {
		t.Fatalf("FillFromSource = %v, %v; want %v, nil", applied, err, want)
	}
	if n := count(t, e.db, `SELECT COUNT(*) FROM credits WHERE item_id = ?`, id); n != 1 {
		t.Fatalf("credits = %d, want 1", n)
	}
	it := mustItem(t, e.lib, id)
	wantTitles := []plugin.Title{{Lang: "en", Text: "Mine"}, {Lang: plugin.AltTitleLang, Text: "Syn A"}, {Lang: plugin.AltTitleLang, Text: "Syn B"}}
	if !reflect.DeepEqual(it.Titles, wantTitles) {
		t.Fatalf("titles = %+v, want %+v", it.Titles, wantTitles)
	}
	if want := []string{"B", "A", "C"}; !slices.Equal(it.Genres, want) {
		t.Fatalf("genres = %v, want %v", it.Genres, want)
	}
	if want := []string{"X", "Y"}; !slices.Equal(it.Fields["translators"].List, want) {
		t.Fatalf("translators = %+v, want %v", it.Fields["translators"], want)
	}
	wantTags := []plugin.Tag{{Name: "T", Rank: ip(50)}, {Name: "U", Rank: ip(10), Spoiler: true}}
	if !reflect.DeepEqual(it.Tags, wantTags) {
		t.Fatalf("tags = %+v, want %+v", it.Tags, wantTags)
	}
	wantLinks := []plugin.Link{{Label: "Mine", URL: "https://watch.example/1"}, {Label: "Other", URL: "https://watch.example/2"}}
	if !reflect.DeepEqual(it.Links, wantLinks) {
		t.Fatalf("links = %+v, want %+v", it.Links, wantLinks)
	}
	if len(it.Relations) != 2 || it.Relations[0].Item.Titles[0].Text != "R1" || it.Relations[1].Item.ID != "r0" {
		t.Fatalf("relations = %+v", it.Relations)
	}

	// with withCredits the missing credit is appended after the stored one
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	applied, err = e.lib.FillFromSource(ctx, id, in, credits, true)
	want = []string{"credits"}
	if err != nil || !slices.Equal(applied, want) {
		t.Fatalf("FillFromSource(credits) = %v, %v; want %v, nil", applied, err, want)
	}
	if itemStamp(t, e, id) == "2000-01-01T00:00:00Z" {
		t.Fatal("updated_at not bumped by appended credits")
	}
	cs, err := e.lib.Credits(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Person.SourceID != "p1" || cs[0].Person.Name != "Stored Name" || cs[1].Person.SourceID != "p2" {
		t.Fatalf("credits = %+v", cs)
	}
	// an empty credit list never removes any
	if applied, err := e.lib.FillFromSource(ctx, id, plugin.ItemData{}, nil, true); err != nil || len(applied) != 0 {
		t.Fatalf("FillFromSource(no credits) = %v, %v", applied, err)
	}
	if n := count(t, e.db, `SELECT COUNT(*) FROM credits WHERE item_id = ?`, id); n != 2 {
		t.Fatalf("credits = %d, want 2", n)
	}
}

func TestFillNothingNew(t *testing.T) {
	e := fillEnv(t)
	d := fillData("n1")
	id, err := e.lib.AddToList(ctx, "fillp", "book", d, Entry{Status: Planned})
	if err != nil {
		t.Fatal(err)
	}
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Titles:   []plugin.Title{{Lang: "en", Text: "Theirs"}},
			Fields:   map[string]plugin.FieldValue{"format": {Text: "hardcover"}, "translators": {List: []string{"Y"}}},
			CoverURL: "https://img.example/n1.jpg",
		},
		Genres: []string{"A"},
		Tags:   []plugin.Tag{{Name: "T"}},
		Links:  []plugin.Link{{Label: "L", URL: "https://watch.example/n1"}},
	}
	credits := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "One"}}}
	if applied, err := e.lib.FillFromSource(ctx, id, in, credits, true); err != nil || len(applied) == 0 {
		t.Fatalf("first FillFromSource = %v, %v", applied, err)
	}
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	setEntryUpdated(t, e.db, "2000-01-02T00:00:00Z", id)
	events := count(t, e.db, `SELECT COUNT(*) FROM entry_events WHERE item_id = ?`, id)
	applied, err := e.lib.FillFromSource(ctx, id, in, credits, true)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second FillFromSource = %v, %v; want none, nil", applied, err)
	}
	if got := itemStamp(t, e, id); got != "2000-01-01T00:00:00Z" {
		t.Fatalf("updated_at = %q, want unchanged", got)
	}
	// a fill that writes is no entry change either
	in.Genres = []string{"New"}
	if applied, err := e.lib.FillFromSource(ctx, id, in, credits, true); err != nil || len(applied) == 0 {
		t.Fatalf("third FillFromSource = %v, %v", applied, err)
	}
	if n := count(t, e.db, `SELECT COUNT(*) FROM entry_events WHERE item_id = ?`, id); n != events {
		t.Fatalf("entry events = %d, want %d", n, events)
	}
	if n := count(t, e.db, `SELECT COUNT(*) FROM entries WHERE item_id = ? AND updated_at = '2000-01-02T00:00:00Z'`, id); n != 1 {
		t.Fatal("entry updated_at changed")
	}
}

func TestFillRespectsBounds(t *testing.T) {
	e := fillEnv(t)
	d := fillData("b1")
	stored := make([]string, maxListValues-1)
	for i := range stored {
		stored[i] = fmt.Sprintf("v%d", i)
	}
	d.Fields["translators"] = plugin.FieldValue{List: stored}
	for i := range maxGenres - 1 {
		d.Genres = append(d.Genres, fmt.Sprintf("g%d", i))
	}
	for i := range maxLinks - 1 {
		d.Links = append(d.Links, plugin.Link{Label: "L", URL: fmt.Sprintf("https://watch.example/%d", i)})
	}
	id := fillItem(t, e, d)
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"translators": {List: []string{"new1", "new2", "new3"}}}},
		Genres:      []string{"n1", "n2"},
		Links:       []plugin.Link{{Label: "N", URL: "https://watch.example/n1"}, {Label: "N", URL: "https://watch.example/n2"}},
	}
	applied, err := e.lib.FillFromSource(ctx, id, in, nil, false)
	want := []string{"field.translators", "genres", "links"}
	if err != nil || !slices.Equal(applied, want) {
		t.Fatalf("FillFromSource = %v, %v; want %v, nil", applied, err, want)
	}
	it := mustItem(t, e.lib, id)
	if l := it.Fields["translators"].List; len(l) != maxListValues || l[len(l)-1] != "new1" {
		t.Fatalf("translators: %d values, last %q", len(l), l[len(l)-1])
	}
	if len(it.Genres) != maxGenres || it.Genres[maxGenres-1] != "n1" {
		t.Fatalf("genres = %v", it.Genres)
	}
	if len(it.Links) != maxLinks || it.Links[maxLinks-1].URL != "https://watch.example/n1" {
		t.Fatalf("links = %+v", it.Links)
	}
	// full lists: nothing more to append
	if applied, err := e.lib.FillFromSource(ctx, id, in, nil, false); err != nil || len(applied) != 0 {
		t.Fatalf("FillFromSource(full) = %v, %v; want none, nil", applied, err)
	}
}

func TestFillRejectsInvalidSourceValues(t *testing.T) {
	e := fillEnv(t)
	id := fillItem(t, e, fillData("i1"))
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	for name, in := range map[string]plugin.ItemData{
		"enum":   {ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"format": {Text: "scroll"}}}, Genres: []string{"ok"}},
		"cover":  {ItemSummary: plugin.ItemSummary{CoverURL: "../x.jpg"}, Genres: []string{"ok"}},
		"banner": {BannerURL: "http://img.example/b.jpg", Genres: []string{"ok"}},
		"link":   {Links: []plugin.Link{{Label: "L", URL: "javascript:alert(1)"}}, Genres: []string{"ok"}},
		// a stored single value does not excuse an unstorable incoming one
		"stored": {ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"author": {Text: "bad\x00"}}}, Genres: []string{"ok"}},
	} {
		applied, err := e.lib.FillFromSource(ctx, id, in, nil, false)
		if len(applied) != 0 || !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: FillFromSource = %v, %v; want ErrInvalid", name, applied, err)
		}
	}
	if it := mustItem(t, e.lib, id); len(it.Genres) != 0 || itemStamp(t, e, id) != "2000-01-01T00:00:00Z" {
		t.Fatalf("written despite an invalid value: %+v", it)
	}
	if _, err := e.lib.FillFromSource(ctx, 9999, plugin.ItemData{}, nil, false); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item: err = %v, want ErrNoEntry", err)
	}
}

func TestFillKeepsStoredCreditsAndPeople(t *testing.T) {
	e := fillEnv(t)
	id := fillItem(t, e, fillData("c1"))
	if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "One", URL: "https://people.example/one"}}}); err != nil {
		t.Fatal(err)
	}
	setItemStamp(t, e, id, "2000-01-01T00:00:00Z")
	renamed := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Renamed", URL: "https://people.example/renamed"}}}
	applied, err := e.lib.FillFromSource(ctx, id, plugin.ItemData{}, renamed, true)
	if err != nil || len(applied) != 0 {
		t.Fatalf("FillFromSource = %v, %v; want none, nil", applied, err)
	}
	cs, err := e.lib.Credits(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Person.Name != "One" || cs[0].Person.ExternalURL != "https://people.example/one" {
		t.Fatalf("credits = %+v", cs)
	}
	if itemStamp(t, e, id) != "2000-01-01T00:00:00Z" {
		t.Fatal("updated_at changed")
	}
	// a new credit naming a stored person (here as counterpart) leaves that
	// person as stored too
	withP1 := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p2", Name: "Two"}, With: &renamed[0].Person, WithRole: "editor"}}
	applied, err = e.lib.FillFromSource(ctx, id, plugin.ItemData{}, withP1, true)
	if err != nil || !slices.Equal(applied, []string{"credits"}) {
		t.Fatalf("FillFromSource = %v, %v; want [credits], nil", applied, err)
	}
	if cs, err = e.lib.Credits(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[1].Person.Name != "Two" || cs[1].With == nil || cs[1].With.Name != "One" ||
		cs[1].With.ExternalURL != "https://people.example/one" {
		t.Fatalf("credits = %+v", cs)
	}
}

// TestFillRefreshesSourceStatistics: a field marked SourceRefresh (a value the
// source keeps current and the owner cannot edit) is replaced by a differing
// incoming value even when one is stored; other set fields stay as stored.
func TestFillRefreshesSourceStatistics(t *testing.T) {
	p := richBook()
	p.M.ID = "fillp"
	p.M.Kinds[0].Fields = append(p.M.Kinds[0].Fields,
		plugin.FieldDef{Key: "stat", Label: "Stat", Type: plugin.FieldNumber, Min: fp(0), Max: fp(100), Show: true, SourceRefresh: true})
	e := newEnvWith(t, p)
	d := fillData("s1")
	d.Fields["stat"] = plugin.FieldValue{Num: fp(70)}
	id := fillItem(t, e, d)

	in := fillData("s1")
	in.Fields = map[string]plugin.FieldValue{"author": {Text: "Theirs"}, "stat": {Num: fp(82)}}
	applied, err := e.lib.FillFromSource(ctx, id, in, nil, false)
	if err != nil || len(applied) != 1 || applied[0] != "field.stat" {
		t.Fatalf("FillFromSource = %v, %v; want [field.stat], nil", applied, err)
	}
	it := mustItem(t, e.lib, id)
	if it.Fields["stat"].Num == nil || *it.Fields["stat"].Num != 82 || it.Fields["author"].Text != "Mine" {
		t.Fatalf("fields = %+v", it.Fields)
	}

	// the same value again, or no incoming value, changes nothing
	if applied, err := e.lib.FillFromSource(ctx, id, in, nil, false); err != nil || len(applied) != 0 {
		t.Fatalf("same value: FillFromSource = %v, %v", applied, err)
	}
	if applied, err := e.lib.FillFromSource(ctx, id, fillData("s1"), nil, false); err != nil || len(applied) != 0 {
		t.Fatalf("no value: FillFromSource = %v, %v", applied, err)
	}
	if it := mustItem(t, e.lib, id); *it.Fields["stat"].Num != 82 {
		t.Fatalf("stat = %v, want 82 kept", *it.Fields["stat"].Num)
	}
}
