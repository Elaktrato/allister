package library

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

var ctx = context.Background()

func TestSaveItemRoundTripsAllParts(t *testing.T) {
	l := newLib(t)
	rel := plugin.Relation{Kind: "sequel", Item: plugin.ItemSummary{
		Source: "books", ID: "99", Kind: "book", CoverURL: "https://img.example/99.jpg",
		Titles: []plugin.Title{{Lang: "en", Text: "Dune Messiah"}},
		Fields: map[string]plugin.FieldValue{"pages": {Num: fp(256)}},
	}}
	rel2 := plugin.Relation{Kind: "prequel", Item: plugin.ItemSummary{Source: "books", ID: "98", Kind: "book", Titles: []plugin.Title{{Lang: "en", Text: "Before"}}}}
	d := bookData("1")
	rel2.Item.Fields = nil
	d.Fields = map[string]plugin.FieldValue{
		"author":      {Text: "Frank Herbert"},
		"pages":       {Num: fp(412)},
		"released":    {Date: ptrTime(day(1965, 8, 1))},
		"translators": {List: []string{"A", "B", "C"}},
		"homepage":    {Text: "https://dune.example/"},
		"format":      {Text: "paperback"},
	}
	d.ExternalIDs = map[string]string{"isbn": "9780441013593"}
	d.Relations = []plugin.Relation{rel, rel2}
	d.Genres = []string{"Sci-Fi", "Classic"}
	d.Tags = []plugin.Tag{} // book has no tags
	id := mustSave(t, l, "book", "book", d, WithSyncState("done"))

	it := mustItem(t, l, id)
	if it.PluginID != "book" || it.Kind != "book" || it.Source != "books" || it.SourceID != "1" ||
		it.SyncState != "done" || it.CoverPath != d.CoverURL || it.ExternalURL != d.ExternalURL {
		t.Fatalf("scalars wrong: %+v", it)
	}
	if !reflect.DeepEqual(it.Titles, d.Titles) {
		t.Errorf("titles = %v", it.Titles)
	}
	if !reflect.DeepEqual(it.Fields, d.Fields) {
		t.Errorf("fields = %+v\nwant %+v", it.Fields, d.Fields)
	}
	if !reflect.DeepEqual(it.ExternalIDs, d.ExternalIDs) {
		t.Errorf("external ids = %v", it.ExternalIDs)
	}
	if !reflect.DeepEqual(it.Relations, d.Relations) {
		t.Errorf("relations = %+v\nwant %+v", it.Relations, d.Relations)
	}
	if !reflect.DeepEqual(it.Genres, d.Genres) {
		t.Errorf("genres = %v", it.Genres)
	}
	if !reflect.DeepEqual(it.ProgressKeys, []string{"pages"}) {
		t.Errorf("progress keys = %v, want kind defaults", it.ProgressKeys)
	}
	got, err := l.ItemBySource(ctx, "book", "book", "books", "1")
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("ItemBySource = %v, %v", got, err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestSaveItemDefaultsSyncStatePending(t *testing.T) {
	l := newLib(t)
	it := mustItem(t, l, mustSave(t, l, "book", "book", bookData("1")))
	if it.SyncState != "pending" {
		t.Fatalf("sync state = %q", it.SyncState)
	}
}

func TestSaveItemRejectsUnknownFieldAndBadType(t *testing.T) {
	l := newLib(t)
	cases := []struct {
		name string
		key  string
		v    plugin.FieldValue
		want string
	}{
		{"unknown key", "nope", plugin.FieldValue{Text: "x"}, "nope"},
		{"number below min", "pages", plugin.FieldValue{Num: fp(0)}, "pages"},
		{"number above max", "score", plugin.FieldValue{Num: fp(10.5)}, "score"},
		{"number NaN", "score", plugin.FieldValue{Num: fp(nan())}, "score"},
		{"enum outside values", "format", plugin.FieldValue{Text: "scroll"}, "format"},
		{"text for number field", "pages", plugin.FieldValue{Text: "412"}, "pages"},
		{"num for text field", "author", plugin.FieldValue{Num: fp(1)}, "author"},
		{"date for text field", "author", plugin.FieldValue{Date: ptrTime(day(2000, 1, 1))}, "author"},
		{"list for text field", "author", plugin.FieldValue{List: []string{"a"}}, "author"},
		{"text for date field", "released", plugin.FieldValue{Text: "2000-01-01"}, "released"},
		{"text for list field", "translators", plugin.FieldValue{Text: "a"}, "translators"},
		{"blank list value", "translators", plugin.FieldValue{List: []string{"a", "  "}}, "translators"},
		{"url without scheme", "homepage", plugin.FieldValue{Text: "dune.example"}, "homepage"},
		{"url ftp", "homepage", plugin.FieldValue{Text: "ftp://dune.example/"}, "homepage"},
		{"url without host", "homepage", plugin.FieldValue{Text: "https://"}, "homepage"},
		{"zero date", "released", plugin.FieldValue{Date: ptrTime(time.Time{})}, "released"},
		{"text too long", "author", plugin.FieldValue{Text: strings.Repeat("あ", 20001)}, "author"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := bookData("1")
			d.Fields = map[string]plugin.FieldValue{c.key: c.v}
			_, err := l.SaveItem(ctx, "book", "book", d)
			wantInvalid(t, err, c.want)
			if it, _ := l.ItemBySource(ctx, "book", "book", "books", "1"); it != nil {
				t.Fatal("rejected item was stored")
			}
		})
	}
}

func TestSaveItemAcceptsBoundariesAndPartialData(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	d.Fields = map[string]plugin.FieldValue{
		"score": {Num: fp(10)}, "pages": {Num: fp(1)}, // bounds are inclusive; required author missing is fine
		"translators": {List: []string{" x "}},
		"isbn":        {}, // empty value is skipped
	}
	it := mustItem(t, l, mustSave(t, l, "book", "book", d))
	if _, ok := it.Fields["isbn"]; ok {
		t.Error("empty field stored")
	}
	if got := it.Fields["translators"].List; !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("list = %q, want trimmed", got)
	}
	if it.Fields["score"].Num == nil || *it.Fields["score"].Num != 10 {
		t.Errorf("score = %+v", it.Fields["score"])
	}
}

func nan() float64 { var z float64; return z / z }

func TestDateFieldStoresWrittenCalendarDay(t *testing.T) {
	e := newEnv(t)
	jst := time.FixedZone("JST", 9*3600)
	d := bookData("1")
	d.Fields = map[string]plugin.FieldValue{"released": {Date: ptrTime(time.Date(2024, 3, 5, 0, 0, 0, 0, jst))}}
	id := mustSave(t, e.lib, "book", "book", d)
	var s string
	if err := e.db.QueryRow(`SELECT date FROM item_fields WHERE item_id=? AND key='released'`, id).Scan(&s); err != nil || s != "2024-03-05" {
		t.Fatalf("stored %q err=%v, want 2024-03-05", s, err)
	}
	got := mustItem(t, e.lib, id).Fields["released"].Date
	if got == nil || !got.Equal(day(2024, 3, 5)) {
		t.Fatalf("date = %v", got)
	}
}

func TestSaveItemRejectsUnknownPluginAndKind(t *testing.T) {
	l := newLib(t)
	_, err := l.SaveItem(ctx, "ghost", "book", bookData("1"))
	wantInvalid(t, err, "ghost")
	_, err = l.SaveItem(ctx, "book", "show", bookData("1"))
	wantInvalid(t, err, "show")
	d := bookData("1")
	d.Kind = "show"
	_, err = l.SaveItem(ctx, "book", "book", d)
	wantInvalid(t, err, "")
}

func TestSaveItemRequiresIdentityAndTitle(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	d.Titles = []plugin.Title{{Lang: "en", Text: "  "}}
	_, err := l.SaveItem(ctx, "book", "book", d)
	wantInvalid(t, err, "title")
	d = bookData("")
	_, err = l.SaveItem(ctx, "book", "book", d)
	wantInvalid(t, err, "")
	d = bookData("1")
	d.Source = ""
	_, err = l.SaveItem(ctx, "book", "book", d)
	wantInvalid(t, err, "")
}

func TestTitlesAreDedupedAndValidated(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	d.Titles = []plugin.Title{{Lang: "EN", Text: " Dune "}, {Lang: "en", Text: "Dune"}, {Lang: "en", Text: ""}, {Lang: "ja-latn", Text: "Dyun"}}
	it := mustItem(t, l, mustSave(t, l, "book", "book", d))
	want := []plugin.Title{{Lang: "en", Text: "Dune"}, {Lang: "ja-latn", Text: "Dyun"}}
	if !reflect.DeepEqual(it.Titles, want) {
		t.Fatalf("titles = %v, want %v", it.Titles, want)
	}
	for _, bad := range []plugin.Title{{Lang: "e n", Text: "x"}, {Lang: "en_US", Text: "x"}, {Lang: "", Text: "x"}, {Lang: "en", Text: strings.Repeat("x", 301)}} {
		d.Titles = []plugin.Title{bad}
		d.ID = "2"
		_, err := l.SaveItem(ctx, "book", "book", d)
		wantInvalid(t, err, "title")
	}
	d.Titles = []plugin.Title{{Lang: "en", Text: strings.Repeat("あ", 300)}}
	mustSave(t, l, "book", "book", d)
}

func TestSaveItemBlocksDuplicate(t *testing.T) {
	l := newLib(t)
	first := mustSave(t, l, "book", "book", bookData("1"))
	_, err := l.SaveItem(ctx, "book", "book", bookData("1"))
	if !errors.Is(err, ErrAlreadySaved) {
		t.Fatalf("err = %v", err)
	}
	var as *AlreadySavedError
	if !errors.As(err, &as) || as.ExistingID != first {
		t.Fatalf("err = %#v, want AlreadySavedError{%d}", err, first)
	}
}

func TestSaveItemConcurrentDuplicates(t *testing.T) {
	l := newLib(t)
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = l.SaveItem(ctx, "book", "book", bookData("1"))
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrAlreadySaved):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d saves won, want 1", won)
	}
}

func TestSameSourceIDDifferentPluginAllowed(t *testing.T) {
	l := newLib(t)
	a := mustSave(t, l, "book", "book", bookData("1"))
	b := mustSave(t, l, "book2", "book", bookData("1"))
	if a == b {
		t.Fatal("same id")
	}
	if it := mustItem(t, l, b); it.PluginID != "book2" {
		t.Fatalf("plugin = %q", it.PluginID)
	}
}

func TestItemByExternalID(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	d.ExternalIDs = map[string]string{"isbn": "123"}
	id := mustSave(t, l, "book", "book", d)
	mustSave(t, l, "book2", "book", func() plugin.ItemData { d := bookData("1"); d.ExternalIDs = map[string]string{"isbn": "123"}; return d }())
	it, err := l.ItemByExternalID(ctx, "book", "book", "isbn", "123")
	if err != nil || it == nil || it.ID != id {
		t.Fatalf("got %v, %v", it, err)
	}
	for _, c := range [][4]string{{"book", "book", "isbn", "999"}, {"book", "book", "other", "123"}, {"book", "show", "isbn", "123"}, {"ghost", "book", "isbn", "123"}} {
		it, err := l.ItemByExternalID(ctx, c[0], c[1], c[2], c[3])
		if err != nil || it != nil {
			t.Fatalf("%v: got %v, %v, want nil, nil", c, it, err)
		}
	}
}

func TestItemUnknownIDIsNil(t *testing.T) {
	l := newLib(t)
	it, err := l.Item(ctx, 4242)
	if err != nil || it != nil {
		t.Fatalf("got %v, %v", it, err)
	}
	it, err = l.ItemBySource(ctx, "book", "book", "books", "nope")
	if err != nil || it != nil {
		t.Fatalf("got %v, %v", it, err)
	}
}

func TestProgressKeys(t *testing.T) {
	l := newLib(t)
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S"}}}}
	it := mustItem(t, l, mustSave(t, l, "tv", "show", d))
	if !reflect.DeepEqual(it.ProgressKeys, []string{"episode"}) {
		t.Fatalf("default keys = %v", it.ProgressKeys)
	}
	d.ID = "2"
	it = mustItem(t, l, mustSave(t, l, "tv", "show", d, WithProgressKeys([]string{"episode", "season"}))) // order-insensitive
	if len(it.ProgressKeys) != 2 {
		t.Fatalf("keys = %v", it.ProgressKeys)
	}
	d.ID = "3"
	for _, bad := range [][]string{{"season"}, {"nope"}, {"season", "position"}, {"episode", "episode"}, {}} {
		_, err := l.SaveItem(ctx, "tv", "show", d, WithProgressKeys(bad))
		wantInvalid(t, err, "progress")
	}
}

func TestValidateItemRequiredFields(t *testing.T) {
	l := newLib(t)
	d := bookData("m1")
	d.Fields = nil
	err := l.ValidateItem("book", "book", d)
	wantInvalid(t, err, "author")
	d.Fields = map[string]plugin.FieldValue{"author": {Text: "  "}}
	wantInvalid(t, l.ValidateItem("book", "book", d), "author")
	d.Fields = map[string]plugin.FieldValue{"author": {Text: "X"}}
	if err := l.ValidateItem("book", "book", d); err != nil {
		t.Fatal(err)
	}
	// SaveItem itself accepts partial data
	d.Fields = nil
	mustSave(t, l, "book", "book", d)
	// ValidateItem also runs the other checks
	d.Fields = map[string]plugin.FieldValue{"author": {Text: "X"}, "nope": {Text: "y"}}
	wantInvalid(t, l.ValidateItem("book", "book", d), "nope")
}

func TestRelationsRoundTripInOrderAndCap(t *testing.T) {
	l := newLib(t)
	d := bookData("1")
	for i := 0; i < 200; i++ {
		d.Relations = append(d.Relations, plugin.Relation{Kind: "other", Item: plugin.ItemSummary{Source: "books", ID: string(rune('a'+i%26)) + string(rune('A'+i/26)), Kind: "book"}})
	}
	it := mustItem(t, l, mustSave(t, l, "book", "book", d))
	if len(it.Relations) != 200 {
		t.Fatalf("len = %d", len(it.Relations))
	}
	for i := range it.Relations {
		if it.Relations[i].Item.ID != d.Relations[i].Item.ID {
			t.Fatalf("order differs at %d", i)
		}
	}
	d.ID = "2"
	d.Relations = append(d.Relations, d.Relations[0])
	_, err := l.SaveItem(ctx, "book", "book", d)
	wantInvalid(t, err, "relations")
}

func TestSaveItemStoresGenresAndTags(t *testing.T) {
	l := newLib(t)
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S"}}}}
	d.Genres = []string{" Drama ", "drama", "DRAMA", "Comedy", "  "}
	d.Tags = []plugin.Tag{{Name: "Time Travel", Rank: ip(80), Spoiler: true}, {Name: "time travel", Rank: ip(10)}, {Name: "Robots"}, {Name: "Zero", Rank: ip(0)}, {Name: "Hundred", Rank: ip(100)}}
	it := mustItem(t, l, mustSave(t, l, "tv", "show", d))
	if want := []string{"Drama", "Comedy"}; !reflect.DeepEqual(it.Genres, want) {
		t.Errorf("genres = %v", it.Genres)
	}
	want := []plugin.Tag{{Name: "Time Travel", Rank: ip(80), Spoiler: true}, {Name: "Robots"}, {Name: "Zero", Rank: ip(0)}, {Name: "Hundred", Rank: ip(100)}}
	if !reflect.DeepEqual(it.Tags, want) {
		t.Errorf("tags = %+v", it.Tags)
	}

	bad := func(name string, mod func(*plugin.ItemData)) {
		t.Run(name, func(t *testing.T) {
			x := d
			x.ID = name
			x.Genres, x.Tags = nil, nil
			mod(&x)
			_, err := l.SaveItem(ctx, "tv", "show", x)
			wantInvalid(t, err, "")
		})
	}
	bad("genre 61 runes", func(x *plugin.ItemData) { x.Genres = []string{strings.Repeat("あ", 61)} })
	bad("tag 61 runes", func(x *plugin.ItemData) { x.Tags = []plugin.Tag{{Name: strings.Repeat("a", 61)}} })
	bad("rank 101", func(x *plugin.ItemData) { x.Tags = []plugin.Tag{{Name: "a", Rank: ip(101)}} })
	bad("rank -1", func(x *plugin.ItemData) { x.Tags = []plugin.Tag{{Name: "a", Rank: ip(-1)}} })
	bad("51 genres", func(x *plugin.ItemData) { x.Genres = nGenres(51) })
	bad("101 tags", func(x *plugin.ItemData) {
		for _, g := range nGenres(101) {
			x.Tags = append(x.Tags, plugin.Tag{Name: g})
		}
	})
	// the limits themselves are fine, and 60 runes is fine
	ok := d
	ok.ID = "limits"
	ok.Genres = append(nGenres(49), strings.Repeat("あ", 60))
	ok.Tags = nil
	for _, g := range nGenres(100) {
		ok.Tags = append(ok.Tags, plugin.Tag{Name: g})
	}
	mustSave(t, l, "tv", "show", ok)

	// a kind without tags rejects them (book enables genres only)
	b := bookData("1")
	b.Tags = []plugin.Tag{{Name: "x"}}
	_, err := l.SaveItem(ctx, "book", "book", b)
	wantInvalid(t, err, "tags")
}

func nGenres(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "g" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return out
}

func TestSetTermsReplacesAtomically(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "S"}}}}
	d.Genres = []string{"Drama"}
	d.Tags = []plugin.Tag{{Name: "Old"}}
	id := mustSave(t, l, "tv", "show", d)

	if err := l.SetTerms(ctx, id, []string{"Comedy", "comedy", "Action"}, []plugin.Tag{{Name: "New", Rank: ip(5)}}); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, l, id)
	if !reflect.DeepEqual(it.Genres, []string{"Comedy", "Action"}) || !reflect.DeepEqual(it.Tags, []plugin.Tag{{Name: "New", Rank: ip(5)}}) {
		t.Fatalf("terms = %v %+v", it.Genres, it.Tags)
	}
	// invalid input changes nothing
	wantInvalid(t, l.SetTerms(ctx, id, []string{"Ok"}, []plugin.Tag{{Name: "x", Rank: ip(101)}}), "rank")
	// a failing write rolls back the delete
	if _, err := e.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON item_terms WHEN NEW.term = 'Boom' BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := l.SetTerms(ctx, id, []string{"Fine", "Boom"}, nil); err == nil {
		t.Fatal("expected failure")
	}
	it = mustItem(t, l, id)
	if !reflect.DeepEqual(it.Genres, []string{"Comedy", "Action"}) || len(it.Tags) != 1 {
		t.Fatalf("terms changed after failure: %v %+v", it.Genres, it.Tags)
	}
	// clearing works; unknown item; disabled kind
	if err := l.SetTerms(ctx, id, nil, nil); err != nil {
		t.Fatal(err)
	}
	if it = mustItem(t, l, id); len(it.Genres)+len(it.Tags) != 0 {
		t.Fatal("not cleared")
	}
	if err := l.SetTerms(ctx, 999, nil, nil); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	bid := mustSave(t, l, "book", "book", bookData("1"))
	wantInvalid(t, l.SetTerms(ctx, bid, nil, []plugin.Tag{{Name: "x"}}), "tags")
	if err := l.SetTerms(ctx, bid, []string{"Epic"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSaveItemIsAtomicOnFailure(t *testing.T) {
	for _, tbl := range []string{"item_titles", "item_fields", "external_ids", "relations_g", "item_terms"} {
		t.Run(tbl, func(t *testing.T) {
			e := newEnv(t)
			if _, err := e.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON ` + tbl + ` BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
				t.Fatal(err)
			}
			d := bookData("1")
			d.Fields["pages"] = plugin.FieldValue{Num: fp(3)}
			d.ExternalIDs = map[string]string{"isbn": "1"}
			d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "2", Kind: "book"}}}
			d.Genres = []string{"g"}
			if _, err := e.lib.SaveItem(ctx, "book", "book", d); err == nil || errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want a database error", err)
			}
			for _, t2 := range []string{"items", "item_titles", "item_fields", "external_ids", "relations_g", "item_terms"} {
				if n := count(t, e.db, `SELECT count(*) FROM `+t2); n != 0 {
					t.Errorf("%s has %d rows after failed save", t2, n)
				}
			}
		})
	}
}

func TestSettersAndDeleteItem(t *testing.T) {
	l := newLib(t)
	id := mustSave(t, l, "book", "book", bookData("1"))
	if err := l.SetCoverPath(ctx, id, "1.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetSyncState(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, l, id)
	if it.CoverPath != "1.jpg" || it.SyncState != "done" {
		t.Fatalf("%+v", it)
	}
	if err := l.SetCoverPath(ctx, 999, "x"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	if err := l.SetSyncState(ctx, 999, "done"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	if err := l.DeleteItem(ctx, 999); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	if err := l.DeleteItem(ctx, id); err != nil {
		t.Fatal(err)
	}
	if it, err := l.Item(ctx, id); err != nil || it != nil {
		t.Fatalf("after delete: %v %v", it, err)
	}
}

func TestFieldIndexesExist(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{"key,num": false, "key,text": false, "key,date": false}
	rows, err := e.db.Query(`SELECT name FROM pragma_index_list('item_fields')`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, n := range names {
		irows, err := e.db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, n)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for irows.Next() {
			var c string
			if err := irows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		irows.Close()
		if _, ok := want[strings.Join(cols, ",")]; ok {
			want[strings.Join(cols, ",")] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("missing index on item_fields(%s)", k)
		}
	}
	for _, q := range []string{
		`SELECT count(*) FROM pragma_index_list('external_ids') l JOIN pragma_index_info(l.name) i WHERE i.seqno=0 AND i.name='type'`,
		`SELECT count(*) FROM pragma_index_list('item_terms') l JOIN pragma_index_info(l.name) i WHERE i.seqno=0 AND i.name='kind'`,
	} {
		if count(t, e.db, q) == 0 {
			t.Errorf("missing index: %s", q)
		}
	}
}
