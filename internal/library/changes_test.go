package library

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// changesBook stores a richBook item with every part set.
func changesBook(t *testing.T, e *env) int64 {
	t.Helper()
	d := bookData("c1")
	d.Fields = map[string]plugin.FieldValue{
		"author": {Text: "Frank Herbert"},
		"pages":  {Num: fp(412)},
		"isbn":   {Text: "978-0"},
	}
	d.Genres = []string{"Sci-Fi", "Classic"}
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "c2", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "Dune Messiah"}}}}}
	return mustSave(t, e.lib, "book", "book", d)
}

func changeKeys(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Key)
	}
	return out
}

func changeByKey(cs []Change, key string) *Change {
	for i := range cs {
		if cs[i].Key == key {
			return &cs[i]
		}
	}
	return nil
}

func TestItemChangesFields(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{
		"author":   {Text: "Frank Herbert"}, // unchanged
		"pages":    {Num: fp(500)},          // changed
		"isbn":     {},                      // no value: never a difference
		"released": {Date: ptrTime(time.Date(1965, 8, 1, 0, 0, 0, 0, time.UTC))},
		"bogus":    {Text: "dropped silently"},
	}}}
	cs, err := e.lib.ItemChanges(ctx, id, in, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := changeKeys(cs), []string{"field.pages", "field.released"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	p := changeByKey(cs, "field.pages")
	if p.Label != "Pages" || p.Stored != "412" || p.Incoming != "500" || p.StoredEmpty {
		t.Fatalf("pages = %+v", *p)
	}
	r := changeByKey(cs, "field.released")
	if r.Label != "Released" || r.Stored != "" || r.Incoming != "1965-08-01" || !r.StoredEmpty {
		t.Fatalf("released = %+v", *r)
	}
}

func TestItemChangesSameAfterNormalisation(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			// same titles after trimming, lower-casing the language and dropping a duplicate
			Titles:   []plugin.Title{{Lang: " EN ", Text: " Dune "}, {Lang: "ja", Text: "デューン"}, {Lang: "de", Text: "Der Wüstenplanet"}, {Lang: "en", Text: "Dune"}},
			Fields:   map[string]plugin.FieldValue{"author": {Text: "Frank Herbert"}, "pages": {Num: fp(412)}},
			CoverURL: "https://img.example/c1.jpg",
		},
		Genres:    []string{" Sci-Fi ", "Classic", "classic"},
		Relations: []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "c2", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "Dune Messiah"}}}}},
	}
	cs, err := e.lib.ItemChanges(ctx, id, in, nil, false)
	if err != nil || len(cs) != 0 {
		t.Fatalf("changes = %+v, %v", cs, err)
	}
}

func TestItemChangesEmptyIncomingIsNoDifference(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{Genres: []string{}, Relations: []plugin.Relation{}}
	cs, err := e.lib.ItemChanges(ctx, id, in, nil, false)
	if err != nil || len(cs) != 0 {
		t.Fatalf("changes = %+v, %v", cs, err)
	}
	// credits are only compared with withCredits and a non-empty list
	sid := tvItem(t, e.lib, "1")
	if err := e.lib.SaveCredits(ctx, sid, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "A"}}}); err != nil {
		t.Fatal(err)
	}
	other := []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p2", Name: "B"}}}
	for _, tc := range []struct {
		credits []plugin.Credit
		with    bool
	}{{nil, true}, {[]plugin.Credit{}, true}, {other, false}} {
		cs, err := e.lib.ItemChanges(ctx, sid, plugin.ItemData{}, tc.credits, tc.with)
		if err != nil || len(cs) != 0 {
			t.Fatalf("%+v: changes = %+v, %v", tc, cs, err)
		}
	}
}

func TestItemChangesListsAndOrder(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Titles:   []plugin.Title{{Lang: "en", Text: "Dune"}, {Lang: "fr", Text: "Dune (fr)"}},
			Fields:   map[string]plugin.FieldValue{"pages": {Num: fp(1)}, "author": {Text: "F. Herbert"}},
			CoverURL: "https://img.example/new.jpg",
		},
		Genres:    []string{"Sci-Fi", "Adventure"},
		Relations: []plugin.Relation{{Kind: "prequel", Item: plugin.ItemSummary{Source: "books", ID: "c0", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "Dune Zero"}}}}},
	}
	cs, err := e.lib.ItemChanges(ctx, id, in, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := changeKeys(cs), []string{"titles", "field.author", "field.pages", "genres", "cover", "relations"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	ti := changeByKey(cs, "titles")
	if !slices.Equal(ti.Added, []string{"fr: Dune (fr)"}) || !slices.Equal(ti.Removed, []string{"ja: デューン", "de: Der Wüstenplanet"}) || ti.StoredEmpty {
		t.Fatalf("titles = %+v", *ti)
	}
	g := changeByKey(cs, "genres")
	if !slices.Equal(g.Added, []string{"Adventure"}) || !slices.Equal(g.Removed, []string{"Classic"}) {
		t.Fatalf("genres = %+v", *g)
	}
	r := changeByKey(cs, "relations")
	if !slices.Equal(r.Added, []string{"prequel: Dune Zero"}) || !slices.Equal(r.Removed, []string{"sequel: Dune Messiah"}) {
		t.Fatalf("relations = %+v", *r)
	}
	c := changeByKey(cs, "cover")
	if c.Stored != "https://img.example/c1.jpg" || c.Incoming != "https://img.example/new.jpg" || c.StoredEmpty {
		t.Fatalf("cover = %+v", *c)
	}
	// a local cover file shows as such
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'abc.jpg' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	cs, _ = e.lib.ItemChanges(ctx, id, in, nil, false)
	if c := changeByKey(cs, "cover"); c == nil || c.Stored != "local cover" {
		t.Fatalf("cover = %+v", c)
	}
}

func TestItemChangesTagsAndCredits(t *testing.T) {
	e := newEnv(t)
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S1"}}}}
	id := mustSave(t, e.lib, "tv", "show", d)
	if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Anna"}}}); err != nil {
		t.Fatal(err)
	}
	in := plugin.ItemData{Tags: []plugin.Tag{{Name: "Time Travel", Rank: ip(80)}}}
	credits := []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Anna"}}, {Role: "director", Person: plugin.PersonRef{ID: "p2", Name: "Ben"}}}
	cs, err := e.lib.ItemChanges(ctx, id, in, credits, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := changeKeys(cs), []string{"tags", "credits"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	tg := changeByKey(cs, "tags")
	if !tg.StoredEmpty || !slices.Equal(tg.Added, []string{"Time Travel"}) || len(tg.Removed) != 0 {
		t.Fatalf("tags = %+v", *tg)
	}
	cr := changeByKey(cs, "credits")
	if cr.StoredEmpty || !slices.Equal(cr.Added, []string{"director: Ben"}) || len(cr.Removed) != 0 {
		t.Fatalf("credits = %+v", *cr)
	}
	n, err := e.lib.ApplyChanges(ctx, id, in, credits, true, []string{"tags", "credits"})
	if err != nil || n != 2 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	it, _ := e.lib.Item(ctx, id)
	if len(it.Tags) != 1 || it.Tags[0].Name != "Time Travel" || it.Tags[0].Rank == nil || *it.Tags[0].Rank != 80 {
		t.Fatalf("tags = %+v", it.Tags)
	}
	got, _ := e.lib.Credits(ctx, id)
	if len(got) != 2 || got[1].Role != "director" || got[1].Person.Name != "Ben" {
		t.Fatalf("credits = %+v", got)
	}
	if cs, _ := e.lib.ItemChanges(ctx, id, in, credits, true); len(cs) != 0 {
		t.Fatalf("after apply = %+v", cs)
	}
}

func TestItemChangesIncludesNonEditableFields(t *testing.T) {
	e := newEnv(t)
	p := richBook(plugintest.WithID("locked"))
	p.M.Kinds[0].Fields[2].Editable = false // isbn
	if err := e.reg.Register(p); err != nil {
		t.Fatal(err)
	}
	id := mustSave(t, e.lib, "locked", "book", bookData("l1"))
	in := plugin.ItemData{ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"isbn": {Text: "123"}}}}
	cs, err := e.lib.ItemChanges(ctx, id, in, nil, false)
	if err != nil || !slices.Equal(changeKeys(cs), []string{"field.isbn"}) {
		t.Fatalf("changes = %+v, %v", cs, err)
	}
}

func TestApplyChangesWritesOnlySelected(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Titles:   []plugin.Title{{Lang: "en", Text: "Dune!"}},
			Fields:   map[string]plugin.FieldValue{"pages": {Num: fp(500)}, "author": {Text: "F. Herbert"}},
			CoverURL: "https://img.example/new.jpg",
		},
		Genres: []string{"Adventure"},
	}
	before, err := e.lib.Item(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n, err := e.lib.ApplyChanges(ctx, id, in, nil, false, []string{"field.pages", "genres"})
	if err != nil || n != 2 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	after, _ := e.lib.Item(ctx, id)
	want := *before
	want.Fields = map[string]plugin.FieldValue{}
	for k, v := range before.Fields {
		want.Fields[k] = v
	}
	want.Fields["pages"] = plugin.FieldValue{Num: fp(500)}
	want.Genres = []string{"Adventure"}
	if !reflect.DeepEqual(*after, want) {
		t.Fatalf("after = %+v\nwant  %+v", *after, want)
	}
	cs, _ := e.lib.ItemChanges(ctx, id, in, nil, false)
	if got, want := changeKeys(cs), []string{"titles", "field.author", "cover"}; !slices.Equal(got, want) {
		t.Fatalf("left = %v, want %v", got, want)
	}
}

func TestApplyChangesSkipsKeysNoLongerDiffering(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	in := plugin.ItemData{ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{"pages": {Num: fp(500)}, "isbn": {Text: "999"}}}}
	// meanwhile the stored pages became equal to the incoming value
	if err := e.lib.UpdateDetails(ctx, id, DetailsUpdate{
		Titles: bookData("c1").Titles,
		Fields: map[string]plugin.FieldValue{"author": {Text: "Frank Herbert"}, "pages": {Num: fp(500)}, "isbn": {Text: "978-0"}},
		Genres: []string{"Sci-Fi", "Classic"},
	}); err != nil {
		t.Fatal(err)
	}
	n, err := e.lib.ApplyChanges(ctx, id, in, nil, false, []string{"field.pages", "field.isbn"})
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	it, _ := e.lib.Item(ctx, id)
	if it.Fields["isbn"].Text != "999" || *it.Fields["pages"].Num != 500 {
		t.Fatalf("fields = %+v", it.Fields)
	}
}

func TestApplyChangesInvalidWritesNothing(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	before, _ := e.lib.Item(ctx, id)
	bad := plugin.ItemData{ItemSummary: plugin.ItemSummary{Fields: map[string]plugin.FieldValue{
		"isbn":  {Text: "999"},
		"score": {Num: fp(11)}, // above the field's max of 10
	}}}
	if _, err := e.lib.ApplyChanges(ctx, id, bad, nil, false, []string{"field.isbn", "field.score"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if _, err := e.lib.ItemChanges(ctx, id, bad, nil, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changes err = %v, want ErrInvalid", err)
	}
	for _, keys := range [][]string{{"nope"}, {"field.bogus"}, {"field.isbn", "credits2"}} {
		if _, err := e.lib.ApplyChanges(ctx, id, bad, nil, false, keys); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%v: err = %v, want ErrInvalid", keys, err)
		}
	}
	after, _ := e.lib.Item(ctx, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("written: %+v", after)
	}
	if _, err := e.lib.ApplyChanges(ctx, 9999, bad, nil, false, []string{"field.isbn"}); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item err = %v", err)
	}
	if _, err := e.lib.ItemChanges(ctx, 9999, bad, nil, false); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item err = %v", err)
	}
}

func TestApplyChangesCoverSetsSyncPending(t *testing.T) {
	e := newEnv(t)
	id := changesBook(t, e)
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'abc.jpg', sync_state = 'done' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	in := plugin.ItemData{ItemSummary: plugin.ItemSummary{CoverURL: "https://img.example/new.jpg"}}
	n, err := e.lib.ApplyChanges(ctx, id, in, nil, false, []string{"cover"})
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	it, _ := e.lib.Item(ctx, id)
	if it.CoverPath != "https://img.example/new.jpg" || it.SyncState != "pending" {
		t.Fatalf("cover = %q, sync = %q", it.CoverPath, it.SyncState)
	}
}
