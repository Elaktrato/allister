package library

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// skipFor returns the reason the item was skipped, or "" when it was not.
func skipFor(res BulkResult, id int64) string {
	for _, s := range res.Skipped {
		if s.ItemID == id {
			return s.Reason
		}
	}
	return ""
}

func wantSkipped(t *testing.T, res BulkResult, id int64, substr string) {
	t.Helper()
	r := skipFor(res, id)
	if r == "" || !strings.Contains(r, substr) {
		t.Fatalf("skip reason of %d = %q, want it to mention %q (result %+v)", id, r, substr, res)
	}
}

// MatchingIDs is exactly the union of all list pages, in list order.
func TestMatchingIDsIsTheUnionOfAllPages(t *testing.T) {
	e := newEnv(t)
	for i := range 7 {
		st := Active
		if i%3 == 0 {
			st = Planned
		}
		addBook(t, e.lib, "m"+string(rune('a'+i)), nil, Entry{Status: st, Rating: ip(i)})
	}
	addShow(t, e.lib, "other", nil, Entry{Status: Active})
	active := Active
	for _, q := range []Query{
		{PluginID: "book", Kind: "book", Status: &active, Sort: Sort{Kind: SortRating, Desc: true}},
		{PluginID: "book", Kind: "book", Sort: Sort{Kind: SortTitle}},
		{PluginID: "book", Kind: "book"},
	} {
		var want []int64
		q.PageSize = 2
		for p := 1; ; p++ {
			q.Page = p
			pg, err := e.lib.List(bg, q, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range pg.Rows {
				want = append(want, r.Item.ID)
			}
			if p >= pg.Pages {
				break
			}
		}
		q.Page = 3 // paging is ignored
		got, err := e.lib.MatchingIDs(bg, q, "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || len(got) == 0 {
			t.Fatalf("MatchingIDs(%+v) = %v, want %v", q, got, want)
		}
	}
	if _, err := e.lib.MatchingIDs(bg, Query{PluginID: "book", Kind: "book", Sort: Sort{Kind: "nope"}}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad sort err = %v", err)
	}
}

func TestBulkSetStatusCompleted(t *testing.T) {
	e := newEnv(t)
	today := time.Date(2026, 11, 16, 23, 30, 0, 0, time.FixedZone("x", 2*3600))
	// a known total: progress moves to it, the empty finish date becomes today
	known := addBook(t, e.lib, "k", fp(300), Entry{Status: Active, StartedOn: tp(day(2026, 1, 2)), Progress: map[string]float64{"pages": 40}})
	// a finish date already set stays
	dated := addBook(t, e.lib, "d", fp(100), Entry{Status: Paused, FinishedOn: tp(day(2026, 3, 4))})
	// season is unbounded and stays; episode has a known total
	show := addShow(t, e.lib, "s", fp(12), Entry{Status: Active, Progress: map[string]float64{"season": 2, "episode": 5}}, WithProgressKeys([]string{"season", "episode"}))
	// no known total: progress stays
	open := addBook(t, e.lib, "o", nil, Entry{Status: Active, Progress: map[string]float64{"pages": 7}})
	before, err := e.lib.ActivityDays(bg, day(2000, 1, 1), day(2100, 1, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.lib.BulkSetStatus(bg, []int64{known, dated, show, open, 9999}, Completed, today)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 4 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, 9999, "not in your library")
	want := day(2026, 11, 16)
	k := mustEntry(t, e.lib, known)
	if k.Status != Completed || k.FinishedOn == nil || !k.FinishedOn.Equal(want) || k.Progress["pages"] != 300 || !k.StartedOn.Equal(day(2026, 1, 2)) {
		t.Fatalf("known = %+v", k)
	}
	if d := mustEntry(t, e.lib, dated); d.Status != Completed || !d.FinishedOn.Equal(day(2026, 3, 4)) || d.Progress["pages"] != 100 {
		t.Fatalf("dated = %+v", d)
	}
	if s := mustEntry(t, e.lib, show); s.Progress["season"] != 2 || s.Progress["episode"] != 12 || s.Status != Completed {
		t.Fatalf("show = %+v", s)
	}
	if o := mustEntry(t, e.lib, open); o.Progress["pages"] != 7 || o.Status != Completed || !o.FinishedOn.Equal(want) {
		t.Fatalf("open = %+v", o)
	}
	// every change is logged as bulk, so the heatmap does not move
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE origin <> 'bulk'`); n != 0 {
		t.Fatalf("non-bulk events = %d", n)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE origin = 'bulk' AND event_type = 'status' AND new_text = 'completed'`); n != 4 {
		t.Fatalf("bulk status events = %d", n)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events WHERE origin = 'bulk' AND event_type = 'progress'`); n != 3 {
		t.Fatalf("bulk progress events = %d", n)
	}
	after, err := e.lib.ActivityDays(bg, day(2000, 1, 1), day(2100, 1, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("ActivityDays changed: %v -> %v", before, after)
	}
}

func TestBulkSetStatusActiveAndOthers(t *testing.T) {
	e := newEnv(t)
	today := day(2026, 11, 16)
	empty := addBook(t, e.lib, "e", fp(300), Entry{Status: Planned})
	started := addBook(t, e.lib, "s", fp(300), Entry{Status: Paused, StartedOn: tp(day(2026, 2, 3)), Progress: map[string]float64{"pages": 9}})
	res, err := e.lib.BulkSetStatus(bg, []int64{empty, started}, Active, today)
	if err != nil || res.Changed != 2 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if en := mustEntry(t, e.lib, empty); en.Status != Active || !en.StartedOn.Equal(today) || en.FinishedOn != nil {
		t.Fatalf("empty = %+v", en)
	}
	if en := mustEntry(t, e.lib, started); en.Status != Active || !en.StartedOn.Equal(day(2026, 2, 3)) || en.Progress["pages"] != 9 {
		t.Fatalf("started = %+v", en)
	}
	// other statuses change the status only
	res, err = e.lib.BulkSetStatus(bg, []int64{empty, started}, Dropped, today)
	if err != nil || res.Changed != 2 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if en := mustEntry(t, e.lib, started); en.Status != Dropped || en.FinishedOn != nil || en.Progress["pages"] != 9 {
		t.Fatalf("dropped = %+v", en)
	}
	if _, err := e.lib.BulkSetStatus(bg, []int64{empty}, Status("nope"), today); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status err = %v", err)
	}
}

// A past display run behind a dropped repeat cannot be reopened: it is
// skipped with a reason, the rest of the batch is applied.
func TestBulkSetStatusSkipsPastDisplayRun(t *testing.T) {
	e := newEnv(t)
	past := droppedRepeat(t, e, "p")
	other := addShow(t, e.lib, "o", fp(12), Entry{Status: Planned})
	res, err := e.lib.BulkSetStatus(bg, []int64{past, other}, Active, day(2026, 11, 16))
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, past, "can only be completed or dropped")
	if strings.HasPrefix(skipFor(res, past), "invalid") {
		t.Fatalf("reason not readable: %q", skipFor(res, past))
	}
	if en := mustEntry(t, e.lib, past); en.Status != Completed || en.RunNumber != 1 {
		t.Fatalf("past = %+v", en)
	}
	if en := mustEntry(t, e.lib, other); en.Status != Active {
		t.Fatalf("other = %+v", en)
	}
}

func TestBulkAddToList(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", nil, Entry{Status: Active})
	b := addBook(t, e.lib, "b", nil, Entry{Status: Active})
	s := addShow(t, e.lib, "s", nil, Entry{Status: Active})
	noEntry := mustSave(t, e.lib, "book", "book", bookData("x"))
	lst, err := e.lib.CreateList(bg, "Shelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetListOrder(bg, lst.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddToCustomList(bg, lst.ID, b); err != nil {
		t.Fatal(err)
	}
	res, err := e.lib.BulkAddToList(bg, []int64{s, a, b, noEntry, a}, lst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 2 || len(res.Skipped) != 2 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, noEntry, "not in your library")
	wantSkipped(t, res, b, "already")
	rows, err := e.lib.ListRows(bg, lst.ID, ListSortPosition, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, r := range rows {
		got = append(got, r.Item.ID)
	}
	if !reflect.DeepEqual(got, []int64{b, s, a}) {
		t.Fatalf("list = %v, want %v", got, []int64{b, s, a})
	}
	if _, err := e.lib.BulkAddToList(bg, []int64{a}, 9999); !errors.Is(err, ErrNoList) {
		t.Fatalf("unknown list err = %v", err)
	}
}

func TestBulkTags(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", nil, Entry{Status: Active})
	b := addBook(t, e.lib, "b", nil, Entry{Status: Active})
	setTags(t, e.lib, a, "Äpfel", "keep")
	setTags(t, e.lib, b, "Comfort")
	res, err := e.lib.BulkTags(bg, []int64{a, b, 9999}, []string{"COMFORT", " Neu "}, []string{"ÄPFEL"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 2 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, 9999, "not in your library")
	got := entryTagsOf(t, e.lib, a, b)
	want := map[int64][]string{a: {"Comfort", "keep", "Neu"}, b: {"Comfort", "Neu"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	if _, err := e.lib.BulkTags(bg, []int64{a}, []string{"bad\x01"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name err = %v", err)
	}
	if got := entryTagsOf(t, e.lib, a); !reflect.DeepEqual(got[a], want[a]) {
		t.Fatalf("refused batch changed tags: %v", got)
	}
}

// An entry that would carry more than the most tags is skipped.
func TestBulkTagsSkipsTooManyTags(t *testing.T) {
	e := newEnv(t)
	full := addBook(t, e.lib, "f", nil, Entry{Status: Active})
	var names []string
	for i := range maxEntryTags {
		names = append(names, "t"+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	setTags(t, e.lib, full, names...)
	ok := addBook(t, e.lib, "o", nil, Entry{Status: Active})
	res, err := e.lib.BulkTags(bg, []int64{full, ok}, []string{"one more"}, nil)
	if err != nil || res.Changed != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	wantSkipped(t, res, full, "more than")
	if got := entryTagsOf(t, e.lib, full); len(got[full]) != maxEntryTags {
		t.Fatalf("full entry tags = %d", len(got[full]))
	}
}

func TestBulkAddToSeries(t *testing.T) {
	e := newEnv(t)
	first := addBook(t, e.lib, "1", nil, Entry{Status: Completed})
	s := mustSeries(t, e, first, "Saga")
	b2 := addBook(t, e.lib, "2", nil, Entry{Status: Active})
	b3 := addBook(t, e.lib, "3", nil, Entry{Status: Planned})
	otherPlugin, err := e.lib.AddToList(bg, "book2", "book", bookWith("4", nil), Entry{Status: Active})
	if err != nil {
		t.Fatal(err)
	}
	show := addShow(t, e.lib, "s", nil, Entry{Status: Active})
	elsewhere := addBook(t, e.lib, "5", nil, Entry{Status: Active})
	mustSeries(t, e, elsewhere, "Other")
	noEntry := mustSave(t, e.lib, "book", "book", bookData("x"))
	res, err := e.lib.BulkAddToSeries(bg, []int64{b3, otherPlugin, show, elsewhere, first, noEntry, b2}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 2 || len(res.Skipped) != 5 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, otherPlugin, "kind")
	wantSkipped(t, res, show, "kind")
	wantSkipped(t, res, elsewhere, "another series")
	wantSkipped(t, res, first, "already")
	wantSkipped(t, res, noEntry, "not in your library")
	parts, err := e.lib.SeriesParts(bg, s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, p := range parts {
		got = append(got, p.Item.ID)
	}
	if !reflect.DeepEqual(got, []int64{first, b3, b2}) {
		t.Fatalf("parts = %v, want %v", got, []int64{first, b3, b2})
	}
	if _, err := e.lib.BulkAddToSeries(bg, []int64{b2}, 9999); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("unknown series err = %v", err)
	}
}

func TestBulkRemove(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", nil, Entry{Status: Active})
	b := addBook(t, e.lib, "b", nil, Entry{Status: Active})
	c := addBook(t, e.lib, "c", nil, Entry{Status: Active})
	s := mustSeries(t, e, a, "Saga")
	mustAddPart(t, e, s.ID, b)
	res, err := e.lib.BulkRemove(bg, []int64{a, b, 9999})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 2 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	wantSkipped(t, res, 9999, "not in your library")
	for _, id := range []int64{a, b} {
		if en, err := e.lib.Entry(bg, id); err != nil || en != nil {
			t.Fatalf("entry %d = %+v, %v", id, en, err)
		}
		mustItem(t, e.lib, id) // the item stays
	}
	if n := count(t, e.db, `SELECT count(*) FROM series`); n != 0 {
		t.Fatalf("series left = %d", n)
	}
	ids, err := e.lib.MatchingIDs(bg, Query{PluginID: "book", Kind: "book"}, "")
	if err != nil || !slices.Equal(ids, []int64{c}) {
		t.Fatalf("left = %v, %v", ids, err)
	}
}

// Under a title sort MatchingIDs follows the preferred title language like
// the list pages do.
func TestMatchingIDsFollowsTheTitleLanguage(t *testing.T) {
	e := newEnv(t)
	for _, b := range []struct{ id, en, de string }{{"a", "Alpha", "Zeta"}, {"b", "Beta", "Anfang"}, {"c", "Gamma", "Mitte"}} {
		d := bookWith(b.id, nil)
		d.Titles = []plugin.Title{{Lang: "en", Text: b.en}, {Lang: "de", Text: b.de}}
		if _, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Active}); err != nil {
			t.Fatal(err)
		}
	}
	q := Query{PluginID: "book", Kind: "book", Sort: Sort{Kind: SortTitle}}
	pageIDs := func(lang string) []int64 {
		rows, err := e.lib.Rows(bg, q, lang, -1, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, r := range rows {
			ids = append(ids, r.Item.ID)
		}
		return ids
	}
	if slices.Equal(pageIDs("de"), pageIDs("en")) {
		t.Fatal("fixture: the language does not reorder the title sort")
	}
	for _, lang := range []string{"de", "en", ""} {
		got, err := e.lib.MatchingIDs(bg, q, lang)
		if err != nil {
			t.Fatal(err)
		}
		if want := pageIDs(lang); !slices.Equal(got, want) {
			t.Fatalf("MatchingIDs(%q) = %v, want %v", lang, got, want)
		}
	}
}
