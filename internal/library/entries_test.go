package library

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

var bg = context.Background()

func kindOf(t *testing.T, l *Library, pluginID, kind string) plugin.KindDef {
	t.Helper()
	k, err := l.kindDef(pluginID, kind)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// bookWith returns book data with the given page total (nil: no total).
func bookWith(id string, pages *float64) plugin.ItemData {
	d := bookData(id)
	if pages != nil {
		d.Fields["pages"] = plugin.FieldValue{Num: pages}
	}
	return d
}

func tvData(id string, episodes *float64) plugin.ItemData {
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: "tvdata", ID: id, Kind: "show",
		Titles: []plugin.Title{{Lang: "en", Text: "Show " + id}},
		Fields: map[string]plugin.FieldValue{},
	}}
	if episodes != nil {
		d.Fields["episodes"] = plugin.FieldValue{Num: episodes}
	}
	return d
}

func addBook(t *testing.T, l *Library, id string, pages *float64, e Entry, opts ...SaveOption) int64 {
	t.Helper()
	itemID, err := l.AddToList(bg, "book", "book", bookWith(id, pages), e, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

func addShow(t *testing.T, l *Library, id string, episodes *float64, e Entry, opts ...SaveOption) int64 {
	t.Helper()
	itemID, err := l.AddToList(bg, "tv", "show", tvData(id, episodes), e, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

func mustEntry(t *testing.T, l *Library, id int64) *Entry {
	t.Helper()
	e, err := l.Entry(bg, id)
	if err != nil || e == nil {
		t.Fatalf("Entry(%d) = %v, %v", id, e, err)
	}
	return e
}

func TestValidateEntryRules(t *testing.T) {
	l := newLib(t)
	book := kindOf(t, l, "book", "book")
	tv := kindOf(t, l, "tv", "show")
	pagesItem := &Item{ProgressKeys: []string{"pages"}, Fields: map[string]plugin.FieldValue{"pages": {Num: fp(300)}}}
	noTotal := &Item{ProgressKeys: []string{"pages"}}
	zeroTotal := &Item{ProgressKeys: []string{"pages"}, Fields: map[string]plugin.FieldValue{"pages": {Num: fp(0)}}}
	percentItem := &Item{ProgressKeys: []string{"percent"}}
	tvItem := &Item{ProgressKeys: []string{"season", "episode", "position"}, Fields: map[string]plugin.FieldValue{"episodes": {Num: fp(12)}}}
	jst := time.FixedZone("JST", 9*3600)
	pst := time.FixedZone("PST", -8*3600)
	at := func(y int, m time.Month, d, h, mi int, z *time.Location) *time.Time {
		v := time.Date(y, m, d, h, mi, 0, 0, z)
		return &v
	}
	long := strings.Repeat("あ", 2000)
	cases := []struct {
		name string
		kind plugin.KindDef
		item *Item
		e    Entry
		want string // "" = valid
	}{
		{"ok minimal", book, pagesItem, Entry{Status: Active}, ""},
		{"rating -1", book, pagesItem, Entry{Status: Active, Rating: ip(-1)}, "rating"},
		{"rating 0", book, pagesItem, Entry{Status: Active, Rating: ip(0)}, ""},
		{"rating 10", book, pagesItem, Entry{Status: Active, Rating: ip(10)}, ""},
		{"rating 11", book, pagesItem, Entry{Status: Active, Rating: ip(11)}, "rating"},
		{"unknown status", book, pagesItem, Entry{Status: "bogus"}, "status"},
		{"empty status", book, pagesItem, Entry{}, "status"},
		{"percent 100", book, percentItem, Entry{Status: Active, Progress: map[string]float64{"percent": 100}}, ""},
		{"percent 0", book, percentItem, Entry{Status: Active, Progress: map[string]float64{"percent": 0}}, ""},
		{"percent 101", book, percentItem, Entry{Status: Active, Progress: map[string]float64{"percent": 101}}, "percent"},
		{"percent -1", book, percentItem, Entry{Status: Active, Progress: map[string]float64{"percent": -1}}, "percent"},
		{"percent 2.5", book, percentItem, Entry{Status: Active, Progress: map[string]float64{"percent": 2.5}}, "percent"},
		{"duration ok", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"position": 3600}}, ""},
		{"duration -1", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"position": -1}}, "position"},
		{"duration 1.5", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"position": 1.5}}, "position"},
		{"count 2.5", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"episode": 2.5}}, "episode"},
		{"count -1", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"season": -1}}, "season"},
		{"NaN", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"season": math.NaN()}}, "season"},
		{"Inf", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"season": math.Inf(1)}}, "season"},
		{"key not active", book, pagesItem, Entry{Status: Active, Progress: map[string]float64{"percent": 5}}, "not active"},
		{"unknown key", book, pagesItem, Entry{Status: Active, Progress: map[string]float64{"zzz": 5}}, "zzz"},
		{"at total", book, pagesItem, Entry{Status: Active, Progress: map[string]float64{"pages": 300}}, ""},
		{"above total", book, pagesItem, Entry{Status: Active, Progress: map[string]float64{"pages": 301}}, "total"},
		{"above total episodes", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"episode": 13}}, "total"},
		{"zero total is unbounded", book, zeroTotal, Entry{Status: Active, Progress: map[string]float64{"pages": 500}}, ""},
		{"no total is unbounded", book, noTotal, Entry{Status: Active, Progress: map[string]float64{"pages": 1e6}}, ""},
		{"unbounded duration", tv, tvItem, Entry{Status: Active, Progress: map[string]float64{"position": 1e6}}, ""},
		{"finish before start", book, pagesItem, Entry{Status: Active, StartedOn: at(2024, 3, 5, 0, 0, time.UTC), FinishedOn: at(2024, 3, 4, 0, 0, time.UTC)}, "finish"},
		{"same day", book, pagesItem, Entry{Status: Active, StartedOn: at(2024, 3, 5, 0, 0, time.UTC), FinishedOn: at(2024, 3, 5, 0, 0, time.UTC)}, ""},
		// calendar days as written: JST 00:30 on the 5th is still the 5th (UTC says the 4th)
		{"jst start after utc finish day", book, pagesItem, Entry{Status: Active, StartedOn: at(2024, 3, 5, 0, 30, jst), FinishedOn: at(2024, 3, 4, 20, 0, time.UTC)}, "finish"},
		{"zones same written day", book, pagesItem, Entry{Status: Active, StartedOn: at(2024, 3, 5, 23, 0, pst), FinishedOn: at(2024, 3, 5, 1, 0, time.UTC)}, ""},
		{"started only", book, pagesItem, Entry{Status: Active, StartedOn: at(2024, 3, 5, 0, 0, time.UTC)}, ""},
		{"comment NUL", book, pagesItem, Entry{Status: Active, Comment: "a\x00b"}, "NUL"},
		{"comment control", book, pagesItem, Entry{Status: Active, Comment: "a\x07b"}, "control"},
		{"comment invalid utf8", book, pagesItem, Entry{Status: Active, Comment: "a\xffb"}, "UTF-8"},
		{"comment multi-line", book, pagesItem, Entry{Status: Active, Comment: "a\n\tb\r\nc"}, ""},
		{"comment 2000 runes", book, pagesItem, Entry{Status: Active, Comment: long}, ""},
		{"comment 2001 runes", book, pagesItem, Entry{Status: Active, Comment: long + "あ"}, "2000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := l.ValidateEntry(c.kind, c.item, c.e)
			if c.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			wantInvalid(t, err, c.want)
		})
	}
}

func TestEntryDatesAreCalendarDays(t *testing.T) {
	e := newEnv(t)
	jst := time.FixedZone("JST", 9*3600)
	start := time.Date(2024, 3, 5, 0, 0, 0, 0, jst) // 2024-03-04T15:00Z
	fin := time.Date(2024, 3, 6, 23, 59, 0, 0, jst)
	id := addBook(t, e.lib, "d1", nil, Entry{Status: Completed, StartedOn: &start, FinishedOn: &fin})
	var s, f string
	if err := e.db.QueryRow(`SELECT started_on, finished_on FROM entries WHERE item_id = ?`, id).Scan(&s, &f); err != nil {
		t.Fatal(err)
	}
	if s != "2024-03-05" || f != "2024-03-06" {
		t.Fatalf("stored %q..%q", s, f)
	}
	got := mustEntry(t, e.lib, id)
	if !got.StartedOn.Equal(day(2024, 3, 5)) || !got.FinishedOn.Equal(day(2024, 3, 6)) || got.StartedOn.Location() != time.UTC {
		t.Fatalf("read %v .. %v", got.StartedOn, got.FinishedOn)
	}
}

func TestAddToListSavesItemEntryAndProgress(t *testing.T) {
	e := newEnv(t)
	r := 8
	id := addBook(t, e.lib, "a1", fp(300), Entry{Status: Active, Rating: &r, Comment: "line1\nline2", Progress: map[string]float64{"pages": 42}})
	got := mustEntry(t, e.lib, id)
	if got.ItemID != id || got.Status != Active || got.Rating == nil || *got.Rating != 8 || got.Comment != "line1\nline2" ||
		!reflect.DeepEqual(got.Progress, map[string]float64{"pages": 42}) || got.UpdatedAt.IsZero() {
		t.Fatalf("entry = %+v", got)
	}
	if it := mustItem(t, e.lib, id); it.Source != "books" || it.Fields["author"].Text != "Frank Herbert" {
		t.Fatalf("item = %+v", it)
	}
	// active keys come from the options
	id2 := addBook(t, e.lib, "a2", nil, Entry{Status: Active, Progress: map[string]float64{"percent": 50}}, WithProgressKeys([]string{"percent"}))
	if got := mustEntry(t, e.lib, id2); got.Progress["percent"] != 50 {
		t.Fatalf("entry = %+v", got)
	}
	// the totals come from the item data (validated before any write)
	_, err := e.lib.AddToList(bg, "book", "book", bookWith("a3", fp(10)), Entry{Status: Active, Progress: map[string]float64{"pages": 11}})
	wantInvalid(t, err, "total")
	if n := count(t, e.db, `SELECT count(*) FROM items WHERE source_id = 'a3'`); n != 0 {
		t.Fatalf("invalid entry left an item behind")
	}
	// without entry data the entry still has an empty, non-nil progress map
	if got := mustEntry(t, e.lib, addBook(t, e.lib, "a4", nil, Entry{Status: Planned})); got.Progress == nil || got.Rating != nil {
		t.Fatalf("entry = %+v", got)
	}
}

func TestAddToListAlreadySavedWritesNothing(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "dup", nil, Entry{Status: Planned, Comment: "first"})
	_, err := e.lib.AddToList(bg, "book", "book", bookWith("dup", nil), Entry{Status: Completed, Comment: "second", Progress: map[string]float64{"pages": 1}})
	var as *AlreadySavedError
	if !errors.Is(err, ErrAlreadySaved) || !errors.As(err, &as) || as.ExistingID != id {
		t.Fatalf("err = %v", err)
	}
	if got := mustEntry(t, e.lib, id); got.Comment != "first" || got.Status != Planned || len(got.Progress) != 0 {
		t.Fatalf("entry was changed: %+v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress`); n != 0 {
		t.Fatalf("progress rows = %d", n)
	}
}

func TestAddToListRollsBackWhenEntryInsertFails(t *testing.T) {
	for _, table := range []string{"entries", "entry_progress"} {
		t.Run(table, func(t *testing.T) {
			e := newEnv(t)
			if _, err := e.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
				t.Fatal(err)
			}
			d := bookWith("rb", nil)
			d.Genres = []string{"SF"}
			_, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Active, Progress: map[string]float64{"pages": 3}})
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("err = %v", err)
			}
			for _, tb := range []string{"items", "item_titles", "item_fields", "item_terms", "entries", "entry_progress"} {
				if n := count(t, e.db, `SELECT count(*) FROM `+tb); n != 0 {
					t.Errorf("%s has %d rows after rollback", tb, n)
				}
			}
			if _, err := e.db.Exec(`DROP TRIGGER boom`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Active}); err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
		})
	}
}

func TestUpsertEntryReplacesOnlyKeysPresent(t *testing.T) {
	l := newLib(t)
	id := addShow(t, l, "u2", fp(12), Entry{Status: Active, Progress: map[string]float64{"season": 1, "episode": 3}}, WithProgressKeys([]string{"season", "episode"}))
	r := 7
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Paused, Rating: &r, Comment: "later", Progress: map[string]float64{"episode": 5}}); err != nil {
		t.Fatal(err)
	}
	got := mustEntry(t, l, id)
	if got.Status != Paused || *got.Rating != 7 || got.Comment != "later" || !reflect.DeepEqual(got.Progress, map[string]float64{"season": 1, "episode": 5}) {
		t.Fatalf("entry = %+v", got)
	}
	// replacing the scalar parts clears optional ones that are absent
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Completed}); err != nil {
		t.Fatal(err)
	}
	got = mustEntry(t, l, id)
	if got.Rating != nil || got.Comment != "" || got.Progress["episode"] != 5 || got.Status != Completed {
		t.Fatalf("entry = %+v", got)
	}
}

func TestUpsertEntryCreatesAndValidatesAgainstStoredItem(t *testing.T) {
	e := newEnv(t)
	id := mustSave(t, e.lib, "book", "book", bookWith("s1", fp(10)))
	if got, err := e.lib.Entry(bg, id); err != nil || got != nil {
		t.Fatalf("Entry before = %v, %v", got, err)
	}
	wantInvalid(t, e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 11}}), "total")
	wantInvalid(t, e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"percent": 1}}), "not active")
	if n := count(t, e.db, `SELECT count(*) FROM entries`); n != 0 {
		t.Fatal("invalid upsert wrote an entry")
	}
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 10}}); err != nil {
		t.Fatal(err)
	}
	if got := mustEntry(t, e.lib, id); got.Progress["pages"] != 10 {
		t.Fatalf("entry = %+v", got)
	}
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: 9999, Status: Active}); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
}

func TestClearProgress(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "c1", nil, Entry{Status: Active, Progress: map[string]float64{"season": 2, "episode": 4}}, WithProgressKeys([]string{"season", "episode"}))
	if err := e.lib.ClearProgress(bg, id, "episode"); err != nil {
		t.Fatal(err)
	}
	if got := mustEntry(t, e.lib, id); !reflect.DeepEqual(got.Progress, map[string]float64{"season": 2}) {
		t.Fatalf("entry = %+v", got)
	}
	if err := e.lib.ClearProgress(bg, id, "nothing-here"); err != nil {
		t.Fatalf("clearing an absent key: %v", err)
	}
	// a retained value of an inactive key can be cleared too
	if err := e.lib.SetActiveProgress(bg, id, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.ClearProgress(bg, id, "season"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, id); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	noEntry := mustSave(t, e.lib, "tv", "show", tvData("c2", nil))
	if err := e.lib.ClearProgress(bg, noEntry, "episode"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
	if err := e.lib.ClearProgress(bg, 9999, "episode"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveItemCascadesEntry(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "r1", nil, Entry{Status: Active, Progress: map[string]float64{"pages": 2}})
	if err := e.lib.DeleteItem(bg, id); err != nil {
		t.Fatal(err)
	}
	if count(t, e.db, `SELECT count(*) FROM entries`)+count(t, e.db, `SELECT count(*) FROM entry_progress`) != 0 {
		t.Fatal("entry rows survived")
	}
}

func TestEntryTableConstraints(t *testing.T) {
	e := newEnv(t)
	id := mustSave(t, e.lib, "book", "book", bookWith("k1", nil))
	if _, err := e.db.Exec(`INSERT INTO entries(item_id,status,updated_at) VALUES (?,?,'x')`, id, "bogus"); err == nil {
		t.Fatal("status check missing")
	}
	if _, err := e.db.Exec(`INSERT INTO entries(item_id,status,rating,updated_at) VALUES (?,?,11,'x')`, id, "active"); err == nil {
		t.Fatal("rating check missing")
	}
}

// TestInvalidEntryWritesNoEvent: a rejected save leaves neither the entry nor
// the event log changed.
func TestInvalidEntryWritesNoEvent(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-i1", fp(10), Entry{Status: Active, Progress: map[string]float64{"pages": 2}})
	wantInvalid(t, l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 11}}), "total")
	wantInvalid(t, l.UpdateEntry(bg, Entry{ItemID: id, Status: "watching"}, nil), "status")
	if n := len(readEvents(t, l, id)); n != 0 {
		t.Fatalf("invalid saves wrote %d events", n)
	}
	if got := mustEntry(t, l, id); got.Status != Active || got.Progress["pages"] != 2 {
		t.Fatalf("entry changed: %+v", got)
	}
}

// TestApplyImportUsesImportOrigin: an import write is recorded, but with the
// import origin so the activity heatmap can leave it out.
func TestApplyImportUsesImportOrigin(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-a1", fp(10), Entry{Status: Planned, Progress: map[string]float64{"pages": 2}})
	ok, err := l.ApplyImport(bg, id, Entry{Status: Completed, Progress: map[string]float64{"pages": 4}}, nil)
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	wantEvents(t, readEvents(t, l, id), []event{
		{Type: "progress", Key: "pages", OldNum: fp(2), NewNum: fp(4), Origin: OriginImport},
		{Type: "status", OldText: "planned", NewText: "completed", Origin: OriginImport},
	})
	// an import that is refused as too old writes nothing
	cur := mustEntry(t, l, id)
	older := cur.UpdatedAt.Add(-time.Hour)
	if ok, err := l.ApplyImport(bg, id, Entry{Status: Active}, &older); err != nil || ok {
		t.Fatalf("older source applied: %v %v", ok, err)
	}
	if n := len(readEvents(t, l, id)); n != 2 {
		t.Fatalf("a refused import wrote events: %d", n)
	}
}

func TestCreateEntryInsertsOnlyWhenAbsent(t *testing.T) {
	e := newEnv(t)
	id := mustSave(t, e.lib, "book", "book", bookWith("ce1", fp(10)))
	// validated against the stored item before anything is written
	wantInvalid(t, e.lib.CreateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 11}}), "total")
	wantInvalid(t, e.lib.CreateEntry(bg, Entry{ItemID: id, Status: "watching"}), "status")
	if n := count(t, e.db, `SELECT count(*) FROM entries`); n != 0 {
		t.Fatal("invalid create wrote an entry")
	}
	if err := e.lib.CreateEntry(bg, Entry{ItemID: id, Status: Active, Comment: "first", Progress: map[string]float64{"pages": 4}}); err != nil {
		t.Fatal(err)
	}
	// an existing entry is never touched
	err := e.lib.CreateEntry(bg, Entry{ItemID: id, Status: Dropped, Comment: "second", Progress: map[string]float64{"pages": 9}})
	if !errors.Is(err, ErrEntryExists) {
		t.Fatalf("err = %v, want ErrEntryExists", err)
	}
	if got := mustEntry(t, e.lib, id); got.Status != Active || got.Comment != "first" || got.Progress["pages"] != 4 {
		t.Fatalf("entry changed: %+v", got)
	}
	if err := e.lib.CreateEntry(bg, Entry{ItemID: 9999, Status: Active}); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestCreateEntryIsAtomicUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	id := mustSave(t, e.lib, "book", "book", bookWith("ce2", fp(10)))
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			errs <- e.lib.CreateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": float64(i)}})
		}(i)
	}
	ok := 0
	for i := 0; i < n; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrEntryExists):
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 || count(t, e.db, `SELECT count(*) FROM entries`) != 1 {
		t.Fatalf("%d creates succeeded", ok)
	}
}
