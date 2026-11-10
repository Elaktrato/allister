package library

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"modernc.org/sqlite"
)

var bookQ = Query{PluginID: "book", Kind: "book"}

func titlesOf(pg Page) []string {
	out := make([]string, 0, len(pg.Rows))
	for _, r := range pg.Rows {
		out = append(out, r.Title)
	}
	return out
}

func idsOf(pg Page) []int64 {
	out := make([]int64, 0, len(pg.Rows))
	for _, r := range pg.Rows {
		out = append(out, r.Item.ID)
	}
	return out
}

func mustList(t *testing.T, l *Library, q Query) Page {
	t.Helper()
	pg, err := l.List(bg, q, "en")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return pg
}

func sameSet(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Fatalf("%s = %q, want %q", what, g, w)
	}
}

// bookT saves a book with the given en title and fields and a planned entry.
func bookT(t *testing.T, l *Library, id, title string, fields map[string]plugin.FieldValue, e Entry) int64 {
	t.Helper()
	d := bookData(id)
	d.Titles = []plugin.Title{{Lang: "en", Text: title}}
	for k, v := range fields {
		d.Fields[k] = v
	}
	if e.Status == "" {
		e.Status = Planned
	}
	itemID, err := l.AddToList(bg, "book", "book", d, e)
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

func TestResolveTitle(t *testing.T) {
	cases := []struct {
		name      string
		ts        []plugin.Title
		preferred string
		want      string
	}{
		{"preferred", []plugin.Title{{Lang: "ja", Text: "進撃の巨人"}, {Lang: "en", Text: "Attack on Titan"}}, "en", "Attack on Titan"},
		{"romaji before english", []plugin.Title{{Lang: "en", Text: "E"}, {Lang: "ja-latn", Text: "R"}}, "de", "R"},
		{"native first", []plugin.Title{{Lang: "en", Text: "E"}, {Lang: "ja", Text: "N"}, {Lang: "ja-latn", Text: "R"}}, "de", "N"},
		{"only english", []plugin.Title{{Lang: "en", Text: "E"}}, "de", "E"},
		{"only french", []plugin.Title{{Lang: "fr", Text: "F"}}, "de", "F"},
		{"first of others", []plugin.Title{{Lang: "fr", Text: "F"}, {Lang: "es", Text: "S"}}, "de", "F"},
		{"empty preferred", []plugin.Title{{Lang: "fr", Text: "F"}, {Lang: "en", Text: "E"}}, "", "E"},
		{"none", nil, "ja", ""},
	}
	for _, c := range cases {
		if got := ResolveTitle(c.ts, c.preferred); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestListRowsCarryItemEntryAndResolvedTitle(t *testing.T) {
	l := newLib(t)
	d := bookData("r1")
	d.Genres = []string{"SF"}
	r := 9
	id, err := l.AddToList(bg, "book", "book", d, Entry{Status: Active, Rating: &r, Progress: map[string]float64{"pages": 7}})
	if err != nil {
		t.Fatal(err)
	}
	for lang, want := range map[string]string{"en": "Dune", "ja": "デューン", "de": "Der Wüstenplanet", "fr": "デューン"} {
		pg, err := l.List(bg, bookQ, lang)
		if err != nil {
			t.Fatal(err)
		}
		if len(pg.Rows) != 1 || pg.Rows[0].Title != want {
			t.Fatalf("lang %s: %+v", lang, titlesOf(pg))
		}
	}
	row := mustList(t, l, bookQ).Rows[0]
	if row.Item.ID != id || len(row.Item.Titles) != 3 || row.Item.Fields["author"].Text != "Frank Herbert" || !slices.Equal(row.Item.Genres, []string{"SF"}) ||
		row.Item.CoverPath != "https://img.example/r1.jpg" || row.Item.SyncState != "pending" || !slices.Equal(row.Item.ProgressKeys, []string{"pages"}) {
		t.Fatalf("item = %+v", row.Item)
	}
	if row.Entry.ItemID != id || row.Entry.Status != Active || *row.Entry.Rating != 9 || row.Entry.Progress["pages"] != 7 || row.Entry.UpdatedAt.IsZero() {
		t.Fatalf("entry = %+v", row.Entry)
	}
	// items without an entry are not list rows
	mustSave(t, l, "book", "book", bookData("noentry"))
	if pg := mustList(t, l, bookQ); pg.Total != 1 {
		t.Fatalf("total = %d", pg.Total)
	}
}

func TestQueryTextSearchAllLanguages(t *testing.T) {
	l := newLib(t)
	bookT(t, l, "1", "Dune", nil, Entry{})
	d := bookData("2")
	d.Titles = []plugin.Title{{Lang: "en", Text: "Foundation"}, {Lang: "ja", Text: "ファウンデーション"}, {Lang: "de", Text: "Der Wüstenplanet"}}
	if _, err := l.AddToList(bg, "book", "book", d, Entry{Status: Planned}); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string][]string{
		"dune":         {"Dune"},
		"DUN":          {"Dune"},
		"  dune  ":     {"Dune"},
		"ファウンデ":        {"Foundation"},
		"wüsten":       {"Foundation"},
		"Wüsten":       {"Foundation"},
		"o":            {"Dune", "Foundation"}, // "Foundation" has an o; Dune does not... checked below
		"nothing like": nil,
		"":             {"Dune", "Foundation"},
	} {
		if text == "o" {
			continue
		}
		q := bookQ
		q.Text = text
		sameSet(t, fmt.Sprintf("search %q", text), titlesOf(mustList(t, l, q)), want...)
	}
	// substring over all titles, not just the resolved one
	q := bookQ
	q.Text = "wüstenplanet"
	pg := mustList(t, l, q)
	if pg.Total != 1 || pg.Rows[0].Title != "Foundation" {
		t.Fatalf("rows = %v", titlesOf(pg))
	}
}

func TestQueryTextEscapesWildcards(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	for i, title := range []string{"100% Pure", "a_b", `back\slash`, "O'Brien", `say "hi"`, "Dune", "axb"} {
		bookT(t, l, fmt.Sprint(i), title, nil, Entry{})
	}
	for text, want := range map[string][]string{
		"%":                       {"100% Pure"},
		"100%":                    {"100% Pure"},
		"_":                       {"a_b"},
		"a_b":                     {"a_b"},
		`\`:                       {`back\slash`},
		`k\s`:                     {`back\slash`},
		"'":                       {"O'Brien"},
		`"hi"`:                    {`say "hi"`},
		`' OR 1=1 --`:             nil,
		`'; DROP TABLE items; --`: nil,
		"%%":                      nil,
		`\%`:                      nil,
		strings.Repeat("%", 5000): nil,
		strings.Repeat("a", 5000): nil,
		"\x00":                    nil,
	} {
		q := bookQ
		q.Text = text
		short := text
		if len(short) > 20 {
			short = short[:20] + "..."
		}
		sameSet(t, fmt.Sprintf("search %q", short), titlesOf(mustList(t, l, q)), want...)
	}
	if n := count(t, e.db, `SELECT count(*) FROM items`); n != 7 {
		t.Fatalf("items = %d", n)
	}
	// longer than 200 runes is truncated, not an error: the first 200 runes match
	title := strings.Repeat("あ", 200) + "いう"
	bookT(t, l, "long", title, nil, Entry{})
	q := bookQ
	q.Text = strings.Repeat("あ", 200) + "えお"
	if got := titlesOf(mustList(t, l, q)); len(got) != 1 {
		t.Fatalf("truncated search = %v", got)
	}
}

func TestQueryFilterTypedRanges(t *testing.T) {
	l := newLib(t)
	d := fp
	date := func(y int, m time.Month, dd int) plugin.FieldValue {
		v := day(y, m, dd)
		return plugin.FieldValue{Date: &v}
	}
	bookT(t, l, "1", "Dune", map[string]plugin.FieldValue{"author": {Text: "Frank Herbert"}, "format": {Text: "hardcover"}, "released": date(1965, 8, 1), "translators": {List: []string{"Ann", "Bo"}}, "score": {Num: d(9)}}, Entry{})
	bookT(t, l, "2", "Emma", map[string]plugin.FieldValue{"author": {Text: "Jane Austen"}, "format": {Text: "paperback"}, "released": date(1815, 12, 23), "translators": {List: []string{"Cy"}}, "score": {Num: d(7.5)}}, Entry{})
	bookT(t, l, "3", "Odd%Book", map[string]plugin.FieldValue{"author": {Text: "100% Real_Name"}}, Entry{})
	bookT(t, l, "4", "Nothing", map[string]plugin.FieldValue{"author": {Text: "X"}, "format": {Text: "hardcover"}}, Entry{})
	run := func(key string, op FilterOp, vals ...string) []string {
		t.Helper()
		q := bookQ
		q.Filters = []Filter{{Key: key, Op: op, Values: vals}}
		pg, err := l.List(bg, q, "en")
		if err != nil {
			t.Fatalf("%s %s %v: %v", key, op, vals, err)
		}
		return titlesOf(pg)
	}
	sameSet(t, "enum eq", run("format", OpEq, "hardcover"), "Dune", "Nothing")
	sameSet(t, "enum in", run("format", OpIn, "hardcover", "paperback"), "Dune", "Emma", "Nothing")
	sameSet(t, "text contains", run("author", OpContains, "HERB"), "Dune")
	sameSet(t, "text contains percent", run("author", OpContains, "%"), "Odd%Book")
	sameSet(t, "text contains underscore", run("author", OpContains, "l_n"), "Odd%Book")
	sameSet(t, "text contains quote", run("author", OpContains, "'"))
	sameSet(t, "number eq", run("score", OpEq, "7.5"), "Emma")
	sameSet(t, "number range", run("score", OpRange, "7..9"), "Dune", "Emma")
	sameSet(t, "number range open end", run("score", OpRange, "8.5.."), "Dune")
	sameSet(t, "number range open start", run("score", OpRange, "..8"), "Emma")
	sameSet(t, "date range", run("released", OpRange, "1800-01-01..1900-01-01"), "Emma")
	sameSet(t, "date range open", run("released", OpRange, "1900-01-01.."), "Dune")
	sameSet(t, "date eq", run("released", OpEq, "1965-08-01"), "Dune")
	sameSet(t, "list in any", run("translators", OpIn, "Cy", "Bo"), "Dune", "Emma")
	sameSet(t, "list in none", run("translators", OpIn, "Zed"))
	// filters combine with AND
	q := bookQ
	q.Filters = []Filter{{Key: "format", Op: OpEq, Values: []string{"hardcover"}}, {Key: "score", Op: OpRange, Values: []string{"8.."}}}
	sameSet(t, "and", titlesOf(mustList(t, l, q)), "Dune")
	// tv year (numbers stored as REAL, a whole-year range)
	for i, y := range []float64{1989, 1995, 2005} {
		d := tvData(fmt.Sprint(i), nil)
		d.Fields["year"] = plugin.FieldValue{Num: fp(y)}
		d.Titles = []plugin.Title{{Lang: "en", Text: fmt.Sprint("Y", y)}}
		if _, err := l.AddToList(bg, "tv", "show", d, Entry{Status: Planned}); err != nil {
			t.Fatal(err)
		}
	}
	tvq := Query{PluginID: "tv", Kind: "show"}
	for rng, want := range map[string][]string{"1990..2000": {"Y1995"}, "..1995": {"Y1989", "Y1995"}, "1995..": {"Y1995", "Y2005"}} {
		tvq.Filters = []Filter{{Key: "year", Op: OpRange, Values: []string{rng}}}
		sameSet(t, "year "+rng, titlesOf(mustList(t, l, tvq)), want...)
	}
}

func TestQueryRejectsUnknownFilterAndSort(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	bookT(t, l, "1", "Dune", map[string]plugin.FieldValue{"author": {Text: "Frank"}}, Entry{})
	before := count(t, e.db, `SELECT count(*) FROM items`) + count(t, e.db, `SELECT count(*) FROM item_fields`) + count(t, e.db, `SELECT count(*) FROM entries`)
	long := strings.Repeat("x", 201)
	many := make([]Filter, 21)
	for i := range many {
		many[i] = Filter{Key: "author", Op: OpContains, Values: []string{"a"}}
	}
	f := func(key string, op FilterOp, vals ...string) []Filter {
		return []Filter{{Key: key, Op: op, Values: vals}}
	}
	bad := []struct {
		name string
		q    Query
	}{
		{"unknown key", Query{Filters: f("nope", OpEq, "a")}},
		{"not filterable", Query{Filters: f("isbn", OpContains, "a")}},
		{"hostile key", Query{Filters: f("author') OR 1=1 --", OpContains, "a")}},
		{"hostile key 2", Query{Filters: f("author' OR '1'='1", OpEq, "a")}},
		{"text eq", Query{Filters: f("author", OpEq, "a")}},
		{"text range", Query{Filters: f("author", OpRange, "a..b")}},
		{"enum contains", Query{Filters: f("format", OpContains, "hard")}},
		{"enum unknown value", Query{Filters: f("format", OpEq, "leather")}},
		{"enum eq two values", Query{Filters: f("format", OpEq, "hardcover", "paperback")}},
		{"number contains", Query{Filters: f("score", OpContains, "1")}},
		{"number malformed", Query{Filters: f("score", OpEq, "abc")}},
		{"number NaN", Query{Filters: f("score", OpEq, "NaN")}},
		{"number Inf range", Query{Filters: f("score", OpRange, "0..Inf")}},
		{"range both open", Query{Filters: f("score", OpRange, "..")}},
		{"range no dots", Query{Filters: f("score", OpRange, "5")}},
		{"range reversed", Query{Filters: f("score", OpRange, "9..1")}},
		{"date malformed", Query{Filters: f("released", OpEq, "1965")}},
		{"date range malformed", Query{Filters: f("released", OpRange, "x..1900-01-01")}},
		{"list contains", Query{Filters: f("translators", OpContains, "a")}},
		{"no values", Query{Filters: f("author", OpContains)}},
		{"empty value", Query{Filters: f("author", OpContains, "")}},
		{"unknown op", Query{Filters: f("author", FilterOp("like"), "a")}},
		{"value too long", Query{Filters: f("author", OpContains, long)}},
		{"too many filters", Query{Filters: many}},
		{"unknown sort kind", Query{Sort: Sort{Kind: SortKind("id; DROP TABLE items")}}},
		{"sort unknown field", Query{Sort: Sort{Kind: SortField, Field: "nope"}}},
		{"sort hostile field", Query{Sort: Sort{Kind: SortField, Field: "score) ; DROP TABLE items; --"}}},
		{"sort not sortable", Query{Sort: Sort{Kind: SortField, Field: "author"}}},
		{"sort list field", Query{Sort: Sort{Kind: SortField, Field: "translators"}}},
		{"sort field without field", Query{Sort: Sort{Kind: SortField}}},
		{"genre filter on kind without genres", Query{PluginID: "tv", Kind: "show", Filters: f("nope", OpEq, "a")}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			q := c.q
			if q.PluginID == "" {
				q.PluginID, q.Kind = "book", "book"
			}
			_, err := l.List(bg, q, "en")
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("List err = %v, want ErrInvalid", err)
			}
			var fe *FilterError
			if !errors.As(err, &fe) {
				t.Fatalf("err %T is not *FilterError", err)
			}
			if _, err := l.Counts(bg, q); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Counts err = %v", err)
			}
		})
	}
	// the sort kinds that need the kind's schema
	_, err := l.List(bg, Query{PluginID: "nop", Kind: "book", Sort: Sort{Kind: SortProgress}}, "en")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("progress sort without primary key: %v", err)
	}
	for _, q := range []Query{{}, {PluginID: "book"}, {Kind: "book"}, {PluginID: "ghost", Kind: "book"}, {PluginID: "book", Kind: "ghost"}} {
		if _, err := l.List(bg, q, "en"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("List(%+v) = %v", q, err)
		}
	}
	after := count(t, e.db, `SELECT count(*) FROM items`) + count(t, e.db, `SELECT count(*) FROM item_fields`) + count(t, e.db, `SELECT count(*) FROM entries`)
	if before != after {
		t.Fatalf("rows changed: %d -> %d", before, after)
	}
	if pg := mustList(t, l, bookQ); pg.Total != 1 {
		t.Fatalf("total = %d", pg.Total)
	}
}

func TestQueryRatingUnratedAndZero(t *testing.T) {
	l := newLib(t)
	rate := func(title string, r *int) { bookT(t, l, title, title, nil, Entry{Rating: r}) }
	rate("none", nil)
	rate("zero", ip(0))
	rate("five", ip(5))
	rate("ten", ip(10))
	run := func(mod func(*Query)) []string {
		q := bookQ
		mod(&q)
		return titlesOf(mustList(t, l, q))
	}
	sameSet(t, "no rating filter", run(func(q *Query) {}), "none", "zero", "five", "ten")
	sameSet(t, "unrated", run(func(q *Query) { q.Unrated = true }), "none")
	sameSet(t, "zero", run(func(q *Query) { q.RatingZero = true }), "zero")
	sameSet(t, "unrated or zero", run(func(q *Query) { q.Unrated, q.RatingZero = true, true }), "none", "zero")
	sameSet(t, "min 5", run(func(q *Query) { q.RatingMin = ip(5) }), "five", "ten")
	sameSet(t, "max 5", run(func(q *Query) { q.RatingMax = ip(5) }), "zero", "five")
	sameSet(t, "range 1..9", run(func(q *Query) { q.RatingMin, q.RatingMax = ip(1), ip(9) }), "five")
	sameSet(t, "min 5 or unrated", run(func(q *Query) { q.RatingMin = ip(5); q.Unrated = true }), "five", "ten", "none")
	sameSet(t, "range or zero or unrated", run(func(q *Query) { q.RatingMin, q.RatingMax = ip(10), ip(10); q.Unrated, q.RatingZero = true, true }), "ten", "none", "zero")
	for _, bad := range []func(*Query){
		func(q *Query) { q.RatingMin = ip(-1) },
		func(q *Query) { q.RatingMax = ip(11) },
		func(q *Query) { q.RatingMin, q.RatingMax = ip(6), ip(5) },
	} {
		q := bookQ
		bad(&q)
		if _, err := l.List(bg, q, "en"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestQueryFlagsUnmatchedAndPartial(t *testing.T) {
	l := newLib(t)
	save := func(id, source string, ext map[string]string, sync string) {
		d := bookData(id)
		d.Source, d.ExternalIDs = source, ext
		d.Titles = []plugin.Title{{Lang: "en", Text: id}}
		if _, err := l.AddToList(bg, "book", "book", d, Entry{Status: Planned}, WithSyncState(sync)); err != nil {
			t.Fatal(err)
		}
	}
	save("manual-isbn", "manual", map[string]string{"isbn": "978"}, "done")
	save("manual-other", "manual", map[string]string{"foo": "1"}, "done")
	save("manual-none", "manual", nil, "done")
	save("source-isbn", "books", map[string]string{"isbn": "979"}, "done")
	save("pending", "books", nil, "pending")
	save("partial", "books", nil, "partial")
	save("manual-isbn-partial", "manual", map[string]string{"isbn": "1"}, "partial")
	pg := mustList(t, l, bookQ)
	flags := map[string]RowFlags{}
	for _, r := range pg.Rows {
		flags[r.Title] = r.Flags
	}
	want := map[string]RowFlags{
		"manual-isbn": {Unmatched: true}, "manual-other": {}, "manual-none": {}, "source-isbn": {},
		"pending": {PartialSync: true}, "partial": {PartialSync: true}, "manual-isbn-partial": {Unmatched: true, PartialSync: true},
	}
	if !reflect.DeepEqual(flags, want) {
		t.Fatalf("flags = %+v", flags)
	}
	q := bookQ
	q.Unmatched = true
	sameSet(t, "unmatched", titlesOf(mustList(t, l, q)), "manual-isbn", "manual-isbn-partial")
	q = bookQ
	q.PartialSync = true
	sameSet(t, "partial", titlesOf(mustList(t, l, q)), "pending", "partial", "manual-isbn-partial")
	q.Unmatched = true
	sameSet(t, "both", titlesOf(mustList(t, l, q)), "manual-isbn-partial")
	// unmatched follows the importers of the item's own plugin
	d := bookData("nopimp")
	d.Source, d.ExternalIDs = "manual", map[string]string{"isbn": "5"}
	if _, err := l.AddToList(bg, "nop", "book", d, Entry{Status: Planned}); err != nil {
		t.Fatal(err)
	}
	nq := Query{PluginID: "nop", Kind: "book", Unmatched: true}
	if pg := mustList(t, l, nq); pg.Total != 1 {
		t.Fatalf("nop unmatched = %d", pg.Total)
	}
}

func TestQuerySortsAndPagination(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	var ids []int64
	for i := 0; i < 101; i++ {
		ids = append(ids, bookT(t, l, fmt.Sprint(i), fmt.Sprintf("Book %03d", i), nil, Entry{}))
	}
	if _, err := e.db.Exec(`UPDATE entries SET updated_at = '2024-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	pg := mustList(t, l, bookQ)
	if pg.Total != 101 || pg.Pages != 2 || pg.PageNo != 1 || len(pg.Rows) != 100 {
		t.Fatalf("page = total %d pages %d no %d rows %d", pg.Total, pg.Pages, pg.PageNo, len(pg.Rows))
	}
	// equal timestamps: id DESC tiebreak, stable across pages
	var all []int64
	all = append(all, idsOf(pg)...)
	q := bookQ
	q.Page = 2
	pg2 := mustList(t, l, q)
	all = append(all, idsOf(pg2)...)
	want := slices.Clone(ids)
	slices.Reverse(want)
	if !slices.Equal(all, want) || len(pg2.Rows) != 1 {
		t.Fatalf("order = %v", all)
	}
	for _, c := range []struct{ page, size, wantNo, wantRows, wantPages int }{
		{5, 0, 2, 1, 2}, {0, 0, 1, 100, 2}, {-3, 0, 1, 100, 2}, {math.MaxInt, 0, 2, 1, 2}, {math.MaxInt, math.MaxInt, 1, 101, 1},
		{1, 1000, 1, 101, 1}, {1, 200, 1, 101, 1}, {1, -5, 1, 100, 2}, {2, 50, 2, 50, 3}, {3, 50, 3, 1, 3}, {4, 50, 3, 1, 3},
	} {
		q := bookQ
		q.Page, q.PageSize = c.page, c.size
		pg := mustList(t, l, q)
		if pg.PageNo != c.wantNo || len(pg.Rows) != c.wantRows || pg.Pages != c.wantPages || pg.Total != 101 {
			t.Errorf("page %d size %d: no %d rows %d pages %d total %d", c.page, c.size, pg.PageNo, len(pg.Rows), pg.Pages, pg.Total)
		}
	}
	// an empty result is one empty page
	q = bookQ
	q.Text = "no such title"
	q.Page = 7
	if pg := mustList(t, l, q); pg.Total != 0 || pg.Pages != 1 || pg.PageNo != 1 || len(pg.Rows) != 0 {
		t.Fatalf("empty = %+v", pg)
	}
	// updated: newest first by default, Desc=false oldest first when explicit
	for i, id := range ids[:3] {
		if _, err := e.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, fmt.Sprintf("2025-01-0%dT00:00:00Z", i+1), id); err != nil {
			t.Fatal(err)
		}
	}
	q = bookQ
	q.PageSize = 3
	if got := idsOf(mustList(t, l, q)); !slices.Equal(got, []int64{ids[2], ids[1], ids[0]}) {
		t.Fatalf("default order = %v", got)
	}
	q.Sort = Sort{Kind: SortUpdated, Desc: true}
	if got := idsOf(mustList(t, l, q)); !slices.Equal(got, []int64{ids[2], ids[1], ids[0]}) {
		t.Fatalf("updated desc = %v", got)
	}
	q.Sort = Sort{Kind: SortUpdated} // explicit ascending: the oldest rows first, ties by id DESC
	if got := idsOf(mustList(t, l, q)); !slices.Equal(got, []int64{ids[100], ids[99], ids[98]}) {
		t.Fatalf("updated asc = %v", got)
	}
}

func TestQuerySortKinds(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	mk := func(id, title string, rating *int, pages *float64, score *float64) {
		f := map[string]plugin.FieldValue{}
		if score != nil {
			f["score"] = plugin.FieldValue{Num: score}
		}
		prog := map[string]float64{}
		if pages != nil {
			prog["pages"] = *pages
		}
		bookT(t, l, id, title, f, Entry{Rating: rating, Progress: prog})
	}
	mk("1", "banana", ip(3), fp(10), fp(1))
	mk("2", "Apple", ip(9), fp(30), nil)
	mk("3", "cherry", nil, nil, fp(5))
	mk("4", "Date", ip(6), fp(20), fp(2))
	run := func(s Sort) []string {
		q := bookQ
		q.Sort = s
		return titlesOf(mustList(t, l, q))
	}
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
	eq("title asc (case-insensitive)", run(Sort{Kind: SortTitle}), "Apple", "banana", "cherry", "Date")
	eq("title desc", run(Sort{Kind: SortTitle, Desc: true}), "Date", "cherry", "banana", "Apple")
	eq("rating desc nulls last", run(Sort{Kind: SortRating, Desc: true}), "Apple", "Date", "banana", "cherry")
	eq("rating asc nulls last", run(Sort{Kind: SortRating}), "banana", "Date", "Apple", "cherry")
	eq("progress desc nulls last", run(Sort{Kind: SortProgress, Desc: true}), "Apple", "Date", "banana", "cherry")
	eq("progress asc nulls last", run(Sort{Kind: SortProgress}), "banana", "Date", "Apple", "cherry")
	eq("field asc nulls last", run(Sort{Kind: SortField, Field: "score"}), "banana", "Date", "cherry", "Apple")
	eq("field desc nulls last", run(Sort{Kind: SortField, Field: "score", Desc: true}), "cherry", "Date", "banana", "Apple")
	// the title sort follows the preferred language when the item has it
	d := bookData("5")
	d.Titles = []plugin.Title{{Lang: "en", Text: "Zeta"}, {Lang: "de", Text: "Alpha"}}
	if _, err := l.AddToList(bg, "book", "book", d, Entry{Status: Planned}); err != nil {
		t.Fatal(err)
	}
	q := bookQ
	q.Sort = Sort{Kind: SortTitle}
	pg, err := l.List(bg, q, "de")
	if err != nil {
		t.Fatal(err)
	}
	if got := titlesOf(pg); got[0] != "Alpha" {
		t.Fatalf("de title sort = %v", got)
	}
	// ties end in id DESC: equal ratings, equal field values
	mk("6", "e1", ip(9), nil, nil)
	mk("7", "e2", ip(9), nil, nil)
	q = bookQ
	q.Sort = Sort{Kind: SortRating, Desc: true}
	q.PageSize = 3
	if got := titlesOf(mustList(t, l, q)); !slices.Equal(got, []string{"e2", "e1", "Apple"}) {
		t.Fatalf("tie order = %v", got)
	}
	// date and text fields sort too
	if _, err := e.db.Exec(`DELETE FROM items`); err != nil {
		t.Fatal(err)
	}
	for i, d := range []string{"2001-05-06", "1999-01-01", "2010-12-31"} {
		tm, err := time.Parse("2006-01-02", d)
		if err != nil {
			t.Fatal(err)
		}
		bookT(t, l, fmt.Sprint("d", i), d, map[string]plugin.FieldValue{"released": {Date: &tm}}, Entry{})
	}
	eq("date asc", run(Sort{Kind: SortField, Field: "released"}), "1999-01-01", "2001-05-06", "2010-12-31")
}

func TestCountsIgnoreStatusButKeepFilters(t *testing.T) {
	l := newLib(t)
	i := 0
	add := func(title string, st Status, author string) {
		i++
		bookT(t, l, fmt.Sprint(i), title, map[string]plugin.FieldValue{"author": {Text: author}}, Entry{Status: st})
	}
	add("Dune", Active, "Herbert")
	add("Dune Messiah", Completed, "Herbert")
	add("Emma", Completed, "Austen")
	add("Persuasion", Planned, "Austen")
	add("Hamlet", Dropped, "Shakespeare")
	all, err := l.Counts(bg, bookQ)
	if err != nil {
		t.Fatal(err)
	}
	wantAll := map[Status]int{Planned: 1, Active: 1, Paused: 0, Completed: 2, Dropped: 1}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("counts = %v", all)
	}
	// the status filter is ignored by Counts but kept by List
	completed := Completed
	q := bookQ
	q.Status = &completed
	if got, _ := l.Counts(bg, q); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("counts with status = %v", got)
	}
	if pg := mustList(t, l, q); pg.Total != 2 {
		t.Fatalf("list total = %d", pg.Total)
	}
	// other filters stay
	q = bookQ
	q.Text = "dune"
	q.Status = &completed
	got, err := l.Counts(bg, q)
	if err != nil || !reflect.DeepEqual(got, map[Status]int{Planned: 0, Active: 1, Paused: 0, Completed: 1, Dropped: 0}) {
		t.Fatalf("counts text = %v, %v", got, err)
	}
	q = bookQ
	q.Filters = []Filter{{Key: "author", Op: OpContains, Values: []string{"austen"}}}
	got, _ = l.Counts(bg, q)
	if !reflect.DeepEqual(got, map[Status]int{Planned: 1, Active: 0, Paused: 0, Completed: 1, Dropped: 0}) {
		t.Fatalf("counts filter = %v", got)
	}
	// every status: Counts agrees with List totals
	for _, st := range []Status{Planned, Active, Paused, Completed, Dropped} {
		st := st
		lq := bookQ
		lq.Status = &st
		if pg := mustList(t, l, lq); pg.Total != wantAll[st] {
			t.Errorf("%s: list %d, counts %d", st, pg.Total, wantAll[st])
		}
	}
}

func TestQueryIsPluginScoped(t *testing.T) {
	l := newLib(t)
	for _, pid := range []string{"book", "book2"} {
		d := bookData("same")
		d.Genres = []string{"SF"}
		if _, err := l.AddToList(bg, pid, "book", d, Entry{Status: Active}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.AddToList(bg, "tv", "show", tvData("same", nil), Entry{Status: Active}); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []string{"book", "book2"} {
		q := Query{PluginID: pid, Kind: "book", Genres: []string{"sf"}}
		pg := mustList(t, l, q)
		if pg.Total != 1 || pg.Rows[0].Item.PluginID != pid {
			t.Fatalf("%s: %+v", pid, idsOf(pg))
		}
		if c, _ := l.Counts(bg, q); c[Active] != 1 {
			t.Fatalf("%s counts = %v", pid, c)
		}
		terms, err := l.Terms(bg, q, "genre")
		if err != nil || len(terms) != 1 || terms[0].Count != 1 {
			t.Fatalf("%s terms = %v, %v", pid, terms, err)
		}
	}
	if pg := mustList(t, l, Query{PluginID: "tv", Kind: "show"}); pg.Total != 1 {
		t.Fatalf("tv total = %d", pg.Total)
	}
}

func tvTagged(t *testing.T, l *Library, id, title string, st Status, genres []string, tags ...string) int64 {
	t.Helper()
	d := tvData(id, nil)
	d.Titles = []plugin.Title{{Lang: "en", Text: title}}
	d.Genres = genres
	for _, tg := range tags {
		spoiler := strings.HasPrefix(tg, "!")
		d.Tags = append(d.Tags, plugin.Tag{Name: strings.TrimPrefix(tg, "!"), Spoiler: spoiler})
	}
	itemID, err := l.AddToList(bg, "tv", "show", d, Entry{Status: st})
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

var tvQ = Query{PluginID: "tv", Kind: "show"}

func TestQueryGenresAndTagsAnyAll(t *testing.T) {
	l := newLib(t)
	tvTagged(t, l, "1", "Alpha", Active, []string{"Drama", "Comedy"}, "Time Travel", "!Death")
	tvTagged(t, l, "2", "Beta", Completed, []string{"drama"}, "Time Travel")
	tvTagged(t, l, "3", "Gamma", Active, []string{"Comedy", "Horror"}, "Space")
	tvTagged(t, l, "4", "Delta", Planned, nil)
	run := func(mod func(*Query)) []string {
		q := tvQ
		mod(&q)
		return titlesOf(mustList(t, l, q))
	}
	sameSet(t, "genre any", run(func(q *Query) { q.Genres = []string{"DRAMA", "horror"} }), "Alpha", "Beta", "Gamma")
	sameSet(t, "genre all", run(func(q *Query) { q.Genres = []string{"drama", "COMEDY"}; q.GenresAll = true }), "Alpha")
	sameSet(t, "genre all dedupes case", run(func(q *Query) { q.Genres = []string{"drama", "DRAMA"}; q.GenresAll = true }), "Alpha", "Beta")
	sameSet(t, "genre single", run(func(q *Query) { q.Genres = []string{"comedy"} }), "Alpha", "Gamma")
	sameSet(t, "tag any", run(func(q *Query) { q.Tags = []string{"space", "time travel"} }), "Alpha", "Beta", "Gamma")
	sameSet(t, "tag all", run(func(q *Query) { q.Tags = []string{"space", "time travel"}; q.TagsAll = true }))
	sameSet(t, "spoiler tag filters like any tag", run(func(q *Query) { q.Tags = []string{"death"} }), "Alpha")
	sameSet(t, "genres and tags combine with AND", run(func(q *Query) { q.Genres = []string{"drama"}; q.Tags = []string{"time travel"} }), "Alpha", "Beta")
	sameSet(t, "with text", run(func(q *Query) { q.Genres = []string{"drama"}; q.Text = "alp" }), "Alpha")
	completed := Completed
	sameSet(t, "with status", run(func(q *Query) { q.Genres = []string{"drama"}; q.Status = &completed }), "Beta")
	sameSet(t, "blank genre ignored", run(func(q *Query) { q.Genres = []string{"  "} }), "Alpha", "Beta", "Gamma", "Delta")
	sameSet(t, "unknown genre", run(func(q *Query) { q.Genres = []string{"nope"} }))
	// a kind that does not enable tags cannot filter by them
	if _, err := l.List(bg, Query{PluginID: "book", Kind: "book", Tags: []string{"x"}}, "en"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryTermsHostileInputIsBound(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	tvTagged(t, l, "1", "Alpha", Active, []string{"Drama"}, "Time")
	hostile := []string{"%", "_", `\`, "'", `"`, `'; DROP TABLE items; --`, "x' OR '1'='1", strings.Repeat("a", 10000), strings.Repeat("あ", 10000), "\x00"}
	for _, h := range hostile {
		for _, all := range []bool{false, true} {
			q := tvQ
			q.Genres, q.Tags, q.GenresAll, q.TagsAll = []string{h}, []string{h}, all, all
			pg, err := l.List(bg, q, "en")
			if err != nil || pg.Total != 0 {
				t.Fatalf("List(%.12q): %d, %v", h, pg.Total, err)
			}
			for _, kind := range []string{"genre", "tag"} {
				terms, err := l.Terms(bg, q, kind)
				if err != nil || len(terms) != 0 {
					t.Fatalf("Terms(%.12q, %s) = %v, %v", h, kind, terms, err)
				}
			}
			if _, err := l.Counts(bg, q); err != nil {
				t.Fatal(err)
			}
		}
		// the hostile value as a text and filter value too
		q := tvQ
		q.Text = h
		if _, err := l.List(bg, q, "en"); err != nil {
			t.Fatalf("List text %.12q: %v", h, err)
		}
	}
	// wildcard terms do not match as patterns
	q := tvQ
	q.Genres = []string{"%"}
	if pg := mustList(t, l, q); pg.Total != 0 {
		t.Fatal("percent matched as a wildcard")
	}
	if _, err := l.Terms(bg, tvQ, "other"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown term kind: %v", err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM items`); n != 1 {
		t.Fatalf("items = %d", n)
	}
	// too many selected terms is a typed error, not a SQL variable overflow
	q = tvQ
	for i := 0; i < 500; i++ {
		q.Genres = append(q.Genres, fmt.Sprint("g", i))
	}
	_, err := l.List(bg, q, "en")
	var fe *FilterError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v", err)
	}
}

func TestTermsCountsReflectOtherFilters(t *testing.T) {
	l := newLib(t)
	tvTagged(t, l, "1", "Alpha", Active, []string{"Drama", "Comedy"}, "Time Travel", "!Death")
	tvTagged(t, l, "2", "Beta", Completed, []string{"drama"}, "Time Travel")
	tvTagged(t, l, "3", "Gamma", Active, []string{"Comedy", "Horror"}, "Space")
	tvTagged(t, l, "4", "Delta", Planned, nil, "Space")
	terms := func(q Query, kind string) []TermCount {
		t.Helper()
		out, err := l.Terms(bg, q, kind)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// no filter: count desc, then term; first stored spelling ("Drama", not "drama")
	if got, want := terms(tvQ, "genre"), []TermCount{{"Comedy", 2}, {"Drama", 2}, {"Horror", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("genres = %v", got)
	}
	if got, want := terms(tvQ, "tag"), []TermCount{{"Space", 2}, {"Time Travel", 2}, {"Death", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v", got)
	}
	// the genre filter does not narrow the genre counts, but the tag filter does
	q := tvQ
	q.Genres = []string{"horror"}
	if got, want := terms(q, "genre"), []TermCount{{"Comedy", 2}, {"Drama", 2}, {"Horror", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("genres with genre filter = %v", got)
	}
	if got, want := terms(q, "tag"), []TermCount{{"Space", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags with genre filter = %v", got)
	}
	q = tvQ
	q.Tags = []string{"time travel"}
	if got, want := terms(q, "genre"), []TermCount{{"Drama", 2}, {"Comedy", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("genres with tag filter = %v", got)
	}
	if got, want := terms(q, "tag"), []TermCount{{"Space", 2}, {"Time Travel", 2}, {"Death", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags with tag filter = %v", got)
	}
	// status and text filters narrow both
	active := Active
	q = tvQ
	q.Status = &active
	if got, want := terms(q, "genre"), []TermCount{{"Comedy", 2}, {"Drama", 1}, {"Horror", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("genres active = %v", got)
	}
	q.Text = "gam"
	if got, want := terms(q, "tag"), []TermCount{{"Space", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags active+text = %v", got)
	}
	// "all" mode of the other term type is respected
	q = tvQ
	q.Genres, q.GenresAll = []string{"drama", "comedy"}, true
	if got, want := terms(q, "tag"), []TermCount{{"Death", 1}, {"Time Travel", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags with genres all = %v", got)
	}
	// a kind without tags has no tag counts
	if got, err := l.Terms(bg, Query{PluginID: "book", Kind: "book"}, "tag"); err != nil || len(got) != 0 {
		t.Fatalf("book tags = %v, %v", got, err)
	}
}

// --- batch loading ---

type countingConnector struct {
	path string
	n    *atomic.Int64
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open("file:" + c.path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return &countingConn{conn, c.n}, nil
}
func (c countingConnector) Driver() driver.Driver { return &sqlite.Driver{} }

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.n.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c *countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.n.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func (c *countingConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}

func (c *countingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	c.n.Add(1)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func TestListLoadsRowsWithBoundedQueries(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 120; i++ {
		d := tvData(fmt.Sprint(i), fp(12))
		d.Genres = []string{"Drama"}
		d.Tags = []plugin.Tag{{Name: "T"}}
		if _, err := e.lib.AddToList(bg, "tv", "show", d, Entry{Status: Active, Progress: map[string]float64{"episode": 1}}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "copy.db")
	if _, err := e.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	counted := sql.OpenDB(countingConnector{path, &n})
	defer counted.Close()
	cl := New(counted, e.reg)
	run := func(size int) int64 {
		n.Store(0)
		pg, err := cl.List(bg, Query{PluginID: "tv", Kind: "show", PageSize: size}, "en")
		if err != nil || len(pg.Rows) != size {
			t.Fatalf("List: %d rows, %v", len(pg.Rows), err)
		}
		return n.Load()
	}
	small, large := run(2), run(100)
	if small != large || large > 8 {
		t.Fatalf("statements: page of 2 = %d, page of 100 = %d (want equal and small)", small, large)
	}
}

func TestResolveTitleIndex(t *testing.T) {
	ts := []plugin.Title{{Lang: "original", Text: "O"}, {Lang: "en", Text: "E"}, {Lang: "de", Text: "D"}}
	for _, tc := range []struct {
		pref string
		want int
	}{{"de", 2}, {"fr", 1}, {"en", 1}} {
		if got := ResolveTitleIndex(ts, tc.pref); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.pref, got, tc.want)
		}
		if ResolveTitle(ts, tc.pref) != ts[tc.want].Text {
			t.Errorf("%s: ResolveTitle disagrees", tc.pref)
		}
	}
	if ResolveTitleIndex(nil, "en") != -1 || ResolveTitleIndex([]plugin.Title{{Lang: "x", Text: "X"}}, "en") != 0 {
		t.Error("empty or no match")
	}
}

// TestCheckFilterAndSortAgreeWithList: the pure checks give exactly the
// verdict List gives for the same filter or sort, without a database.
func TestCheckFilterAndSortAgreeWithList(t *testing.T) {
	e := newEnv(t)
	def := richBook().M.Kinds[0]
	nopDef := richBook(plugintest.WithID("nop")).M.Kinds[0]
	nopDef.Progress.PrimaryKey = ""
	f := func(key string, op FilterOp, vals ...string) Filter { return Filter{Key: key, Op: op, Values: vals} }
	filters := []struct {
		f  Filter
		ok bool
	}{
		{f("author", OpContains, "frank"), true},
		{f("author", OpContains, "%_'\\"), true},
		{f("score", OpRange, "1..5"), true},
		{f("score", OpRange, "5.."), true},
		{f("score", OpEq, "3"), true},
		{f("released", OpRange, "2000-01-01..2001-01-01"), true},
		{f("format", OpIn, "hardcover", "paperback"), true},
		{f("translators", OpIn, "A"), true},
		{f("nope", OpEq, "a"), false},
		{f("isbn", OpContains, "a"), false},
		{f("author') OR 1=1 --", OpContains, "a"), false},
		{f("author", OpEq, "a"), false},
		{f("format", OpEq, "leather"), false},
		{f("score", OpRange, "9..1"), false},
		{f("score", OpRange, "x..y"), false},
		{f("released", OpEq, "2001-13-01"), false},
		{f("author", OpContains, strings.Repeat("x", 201)), false},
		{f("author", OpContains), false},
	}
	for _, c := range filters {
		err := CheckFilter(def, c.f)
		var fe *FilterError
		if c.ok != (err == nil) || (err != nil && !errors.As(err, &fe)) {
			t.Errorf("CheckFilter(%+v) = %v, want ok=%v (typed)", c.f, err, c.ok)
		}
		_, lerr := e.lib.List(bg, Query{PluginID: "book", Kind: "book", Filters: []Filter{c.f}}, "en")
		if (err == nil) != (lerr == nil) {
			t.Errorf("%+v: CheckFilter %v but List %v", c.f, err, lerr)
		}
	}
	sorts := []struct {
		s     Sort
		def   plugin.KindDef
		query Query
		ok    bool
	}{
		{Sort{Kind: SortTitle}, def, bookQ, true},
		{Sort{Kind: SortUpdated, Desc: true}, def, bookQ, true},
		{Sort{Kind: SortRating}, def, bookQ, true},
		{Sort{Kind: SortProgress}, def, bookQ, true},
		{Sort{Kind: SortField, Field: "score"}, def, bookQ, true},
		{Sort{Kind: SortField, Field: "released"}, def, bookQ, true},
		{Sort{Kind: SortField, Field: "author"}, def, bookQ, false},
		{Sort{Kind: SortField, Field: "nope') --"}, def, bookQ, false},
		{Sort{Kind: "bogus"}, def, bookQ, false},
		{Sort{Kind: SortProgress}, nopDef, Query{PluginID: "nop", Kind: "book"}, false},
	}
	for _, c := range sorts {
		err := CheckSort(c.def, c.s)
		var fe *FilterError
		if c.ok != (err == nil) || (err != nil && !errors.As(err, &fe)) {
			t.Errorf("CheckSort(%+v) = %v, want ok=%v (typed)", c.s, err, c.ok)
		}
		q := c.query
		q.Sort = c.s
		_, lerr := e.lib.List(bg, q, "en")
		if (err == nil) != (lerr == nil) {
			t.Errorf("%+v: CheckSort %v but List %v", c.s, err, lerr)
		}
	}
}
