package library

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// mustSeries starts a series with the item as its first part or fails the test.
func mustSeries(t *testing.T, e *env, itemID int64, title string) Series {
	t.Helper()
	s, err := e.lib.CreateSeries(bg, itemID, title)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mustAddPart appends the items to the series (no position hint) or fails the
// test.
func mustAddPart(t *testing.T, e *env, seriesID int64, items ...int64) {
	t.Helper()
	for _, id := range items {
		if err := e.lib.AddPart(bg, seriesID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateSeries(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "1", fp(300), Entry{Status: Planned})
	s, err := e.lib.CreateSeries(bg, id, "  Dune  ")
	if err != nil {
		t.Fatal(err)
	}
	if s.UID == "" || s.PluginID != "book" || s.Kind != "book" || s.Title != "Dune" || s.ID == 0 {
		t.Fatalf("series = %+v", s)
	}
	got, pos, err := e.lib.SeriesOfItem(bg, id)
	if err != nil || got == nil || got.ID != s.ID || pos != 1 {
		t.Fatalf("SeriesOfItem = %+v %d %v", got, pos, err)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		t.Fatalf("stamps not set: %+v", s)
	}
	other := addBook(t, e.lib, "2", fp(1), Entry{Status: Planned})
	if got, pos, err := e.lib.SeriesOfItem(bg, other); got != nil || pos != 0 || err != nil {
		t.Fatalf("no series: %+v %d %v", got, pos, err)
	}
}

func TestCreateSeriesRefuses(t *testing.T) {
	e := newEnv(t)
	noEntry := mustSave(t, e.lib, "book", "book", bookData("x"))
	if _, err := e.lib.CreateSeries(bg, noEntry, "S"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("no entry: %v", err)
	}
	id := addBook(t, e.lib, "1", fp(300), Entry{Status: Planned})
	if _, err := e.lib.CreateSeries(bg, id, "S"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.CreateSeries(bg, id, "T"); !errors.Is(err, ErrAlreadyInSeries) {
		t.Fatalf("again: %v", err)
	}
	fresh := addBook(t, e.lib, "2", fp(300), Entry{Status: Planned})
	for _, title := range []string{"  ", "a\x00b", strings.Repeat("x", 301)} {
		if _, err := e.lib.CreateSeries(bg, fresh, title); !errors.Is(err, ErrInvalid) {
			t.Fatalf("title %q: %v", title, err)
		}
	}
	if n := count(t, e.db, `SELECT count(*) FROM series`); n != 1 {
		t.Fatalf("series rows = %d", n)
	}
}

func TestSeriesByUIDOtherPlugin(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "1", fp(300), Entry{Status: Planned})
	s, err := e.lib.CreateSeries(bg, id, "S")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.SeriesByUID(bg, "book", s.UID)
	if err != nil || got.ID != s.ID {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := e.lib.SeriesByUID(bg, "book2", s.UID); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("other plugin: %v", err)
	}
	if _, err := e.lib.SeriesByUID(bg, "book", "nope"); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("missing: %v", err)
	}
}

func TestRenameSeries(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "1", fp(300), Entry{Status: Planned})
	s, err := e.lib.CreateSeries(bg, id, "Old")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour).Format(stampLayout)
	if _, err := e.db.Exec(`UPDATE series SET updated_at = ? WHERE id = ?`, old, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RenameSeries(bg, s.ID, " New "); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.SeriesByUID(bg, "book", s.UID)
	if err != nil || got.Title != "New" {
		t.Fatalf("got %+v %v", got, err)
	}
	if !got.UpdatedAt.After(s.UpdatedAt.Add(-time.Hour)) || got.UpdatedAt.Format(stampLayout) == old {
		t.Fatalf("UpdatedAt not advanced: %v", got.UpdatedAt)
	}
	for _, title := range []string{"", "a\x00b", strings.Repeat("x", 301)} {
		if err := e.lib.RenameSeries(bg, s.ID, title); !errors.Is(err, ErrInvalid) {
			t.Fatalf("title %q: %v", title, err)
		}
	}
	if err := e.lib.RenameSeries(bg, 9999, "X"); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("missing: %v", err)
	}
}

// numbered adds a book with a series_number field value stored directly (the
// fixture manifest does not declare the field; AddPart reads item_fields).
func numbered(t *testing.T, e *env, id string, n *float64) int64 {
	t.Helper()
	item := addBook(t, e.lib, id, fp(100), Entry{Status: Planned})
	if n != nil {
		if _, err := e.db.Exec(`INSERT INTO item_fields(item_id, key, vtype, num, seq) VALUES (?, 'series_number', 'number', ?, 0)`, item, *n); err != nil {
			t.Fatal(err)
		}
	}
	return item
}

// partIDs returns the item ids of a series in position order, checking that
// positions are dense.
func partIDs(t *testing.T, e *env, seriesID int64) []int64 {
	t.Helper()
	rows, err := e.db.Query(`SELECT item_id, position FROM series_parts WHERE series_id = ? ORDER BY position`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		var pos int
		if err := rows.Scan(&id, &pos); err != nil {
			t.Fatal(err)
		}
		if pos != len(out)+1 {
			t.Fatalf("position %d at index %d: not dense", pos, len(out))
		}
		out = append(out, id)
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAddPartPlacesByHint(t *testing.T) {
	e := newEnv(t)
	one := numbered(t, e, "1", fp(1))
	three := numbered(t, e, "3", fp(3))
	s, err := e.lib.CreateSeries(bg, one, "S")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddPart(bg, s.ID, three, "series_number"); err != nil {
		t.Fatal(err)
	}
	two := numbered(t, e, "2", fp(2))
	if err := e.lib.AddPart(bg, s.ID, two, "series_number"); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{one, two, three}) {
		t.Fatalf("by number: %v", got)
	}
	none := numbered(t, e, "n", nil)
	if err := e.lib.AddPart(bg, s.ID, none, "series_number"); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{one, two, three, none}) {
		t.Fatalf("no number: %v", got)
	}
	// An unnumbered part does not stop the scan.
	four := numbered(t, e, "4", fp(4))
	if err := e.lib.AddPart(bg, s.ID, four, "series_number"); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{one, two, three, none, four}) {
		t.Fatalf("after unnumbered: %v", got)
	}
	// Without a position field the item is always appended.
	zero := numbered(t, e, "0", fp(0))
	if err := e.lib.AddPart(bg, s.ID, zero, ""); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{one, two, three, none, four, zero}) {
		t.Fatalf("no hint: %v", got)
	}
}

func TestAddPartRefuses(t *testing.T) {
	e := newEnv(t)
	first := addBook(t, e.lib, "1", fp(1), Entry{Status: Planned})
	s, err := e.lib.CreateSeries(bg, first, "S")
	if err != nil {
		t.Fatal(err)
	}
	show := addShow(t, e.lib, "1", fp(12), Entry{Status: Planned})
	if err := e.lib.AddPart(bg, s.ID, show, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other kind/plugin: %v", err)
	}
	otherPlugin, err := e.lib.AddToList(bg, "book2", "book", bookData("o"), Entry{Status: Planned})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddPart(bg, s.ID, otherPlugin, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other plugin: %v", err)
	}
	noEntry := mustSave(t, e.lib, "book", "book", bookData("x"))
	if err := e.lib.AddPart(bg, s.ID, noEntry, ""); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("no entry: %v", err)
	}
	if err := e.lib.AddPart(bg, 9999, first, ""); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("no series: %v", err)
	}
}

func TestAddPartAlreadyInSeries(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	sa, err := e.lib.CreateSeries(bg, a, "A")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := e.lib.CreateSeries(bg, b, "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddPart(bg, sb.ID, a, ""); !errors.Is(err, ErrAlreadyInSeries) {
		t.Fatalf("other series: %v", err)
	}
	if err := e.lib.AddPart(bg, sa.ID, a, ""); !errors.Is(err, ErrAlreadyInSeries) {
		t.Fatalf("same series: %v", err)
	}
}

// A concurrent insert that slips between the check and the insert surfaces as
// a UNIQUE failure; it must read as ErrAlreadyInSeries. The race itself cannot
// be scheduled deterministically, so the mapping is tested against the real
// SQLite error.
func TestMapPartInsertErr(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	mustSeries(t, e, a, "A")
	sb := mustSeries(t, e, b, "B")
	_, err := e.db.Exec(`INSERT INTO series_parts(series_id, item_id, position) VALUES (?, ?, 9)`, sb.ID, a)
	if err == nil {
		t.Fatal("expected a UNIQUE failure")
	}
	if got := mapPartInsertErr(err); !errors.Is(got, ErrAlreadyInSeries) {
		t.Fatalf("mapped = %v", got)
	}
	other := errors.New("boom")
	if got := mapPartInsertErr(other); got != other {
		t.Fatalf("other error changed: %v", got)
	}
}

func TestMovePart(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c)
	if err := e.lib.MovePart(bg, s.ID, b, -1); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{b, a, c}) {
		t.Fatalf("b up: %v", got)
	}
	if err := e.lib.MovePart(bg, s.ID, b, -1); err != nil {
		t.Fatalf("past start: %v", err)
	}
	if err := e.lib.MovePart(bg, s.ID, c, 1); err != nil {
		t.Fatalf("past end: %v", err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{b, a, c}) {
		t.Fatalf("no-ops changed order: %v", got)
	}
	if err := e.lib.MovePart(bg, s.ID, b, 1); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{a, b, c}) {
		t.Fatalf("b down: %v", got)
	}
	stranger := addBook(t, e.lib, "z", fp(1), Entry{Status: Planned})
	if err := e.lib.MovePart(bg, s.ID, stranger, 1); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("not a part: %v", err)
	}
}

// Only a single step is allowed: any other delta is refused and moves nothing.
func TestMovePartRefusesOtherDeltas(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c)
	for _, delta := range []int{0, 2, -2, 100} {
		if err := e.lib.MovePart(bg, s.ID, b, delta); !errors.Is(err, ErrInvalid) {
			t.Fatalf("delta %d: %v", delta, err)
		}
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{a, b, c}) {
		t.Fatalf("order changed: %v", got)
	}
}

func TestRemovePartKeepsEntry(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c)
	if err := e.lib.RemovePart(bg, s.ID, b); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{a, c}) {
		t.Fatalf("after remove: %v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM `+tableEntries+` WHERE item_id = ?`, b); n != 1 {
		t.Fatalf("entry gone: %d", n)
	}
	if err := e.lib.RemovePart(bg, s.ID, b); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("not a part: %v", err)
	}
	for _, id := range []int64{a, c} {
		if err := e.lib.RemovePart(bg, s.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.lib.SeriesByUID(bg, "book", s.UID); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("series should be gone: %v", err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM `+tableEntries); n != 3 {
		t.Fatalf("entries = %d", n)
	}
}

func TestDeleteSeries(t *testing.T) {
	e := newEnv(t)
	mk := func(p string) (Series, []int64) {
		a := addBook(t, e.lib, p+"a", fp(1), Entry{Status: Planned})
		b := addBook(t, e.lib, p+"b", fp(1), Entry{Status: Planned})
		s := mustSeries(t, e, a, "S"+p)
		mustAddPart(t, e, s.ID, b)
		return s, []int64{a, b}
	}
	s, parts := mk("k")
	if err := e.lib.DeleteSeries(bg, s.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range parts {
		if n := count(t, e.db, `SELECT count(*) FROM `+tableEntries+` WHERE item_id = ?`, id); n != 1 {
			t.Fatalf("entry %d removed", id)
		}
		if got, _, _ := e.lib.SeriesOfItem(bg, id); got != nil {
			t.Fatalf("item %d still grouped", id)
		}
	}
	s, parts = mk("r")
	if err := e.lib.DeleteSeries(bg, s.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range parts {
		if n := count(t, e.db, `SELECT count(*) FROM items WHERE id = ?`, id); n != 0 {
			t.Fatalf("item %d kept", id)
		}
	}
	if n := count(t, e.db, `SELECT count(*) FROM series`); n != 0 {
		t.Fatalf("series left: %d", n)
	}
	if err := e.lib.DeleteSeries(bg, s.ID, false); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("again: %v", err)
	}
}

func TestDeleteItemRenumbersAndDropsEmptySeries(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s := mustSeries(t, e, a, "S")
	mustAddPart(t, e, s.ID, b, c)
	if err := e.lib.DeleteItem(bg, b); err != nil {
		t.Fatal(err)
	}
	if got := partIDs(t, e, s.ID); !sameIDs(got, []int64{a, c}) {
		t.Fatalf("after delete: %v", got)
	}
	if err := e.lib.DeleteItem(bg, a); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.DeleteItem(bg, c); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM series`); n != 0 {
		t.Fatalf("empty series kept: %d", n)
	}
	if n := count(t, e.db, `SELECT count(*) FROM series_parts`); n != 0 {
		t.Fatalf("parts kept: %d", n)
	}
	if err := e.lib.DeleteItem(bg, 99999); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestDeriveSeriesStatus(t *testing.T) {
	for _, c := range []struct {
		parts []Status
		want  Status
	}{
		{[]Status{Dropped, Dropped}, Dropped},
		{[]Status{Completed, Completed, Dropped}, Completed},
		{[]Status{Planned, Planned}, Planned},
		{[]Status{Completed, Active, Planned}, Active},
		{[]Status{Completed, Paused}, Paused},
		{[]Status{Completed, Planned}, Active},
	} {
		if got := DeriveSeriesStatus(c.parts); got != c.want {
			t.Errorf("%v = %s, want %s", c.parts, got, c.want)
		}
	}
}

// seriesOf builds a series titled title from one new book per id, in order.
func seriesOf(t *testing.T, e *env, plug, title string, ids ...string) *Series {
	t.Helper()
	var s *Series
	for i, id := range ids {
		item := addBook(t, e.lib, id, fp(100), Entry{Status: Planned})
		if i == 0 {
			sv, err := e.lib.CreateSeries(bg, item, title)
			if err != nil {
				t.Fatal(err)
			}
			s = &sv
			continue
		}
		if err := e.lib.AddPart(bg, s.ID, item, ""); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func setStatus(t *testing.T, e *env, series int64, pos int, st Status, updated string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE entries SET status = ?, updated_at = ? WHERE item_id = (SELECT item_id FROM series_parts WHERE series_id = ? AND position = ?)`, string(st), updated, series, pos); err != nil {
		t.Fatal(err)
	}
}

func TestListSeries(t *testing.T) {
	e := newEnv(t)
	a := seriesOf(t, e, "book", "Alpha", "a1", "a2", "a3")
	b := seriesOf(t, e, "book", "Beta", "b1")
	setStatus(t, e, a.ID, 1, Completed, "2026-01-01T00:00:00Z")
	setStatus(t, e, a.ID, 2, Completed, "2026-03-01T00:00:00Z")
	setStatus(t, e, a.ID, 3, Planned, "2026-02-01T00:00:00Z")
	setStatus(t, e, b.ID, 1, Paused, "2026-05-01T00:00:00Z")
	if _, err := e.db.Exec(`UPDATE items SET cover_path = 'covers/a1.jpg' WHERE source_id = 'a1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET cover_path = '' WHERE source_id = 'b1'`); err != nil {
		t.Fatal(err)
	}
	// a series of another plugin must not show up
	other, err := e.lib.AddToList(bg, "book2", "book", bookWith("o1", fp(1)), Entry{Status: Planned})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.CreateSeries(bg, other, "Other"); err != nil {
		t.Fatal(err)
	}

	pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book"})
	if err != nil {
		t.Fatal(err)
	}
	if pg.Total != 2 || pg.Page != 1 || pg.Pages != 1 || len(pg.Rows) != 2 {
		t.Fatalf("page = %+v", pg)
	}
	if pg.Rows[0].Series.Title != "Beta" || pg.Rows[1].Series.Title != "Alpha" {
		t.Fatalf("newest first: %q, %q", pg.Rows[0].Series.Title, pg.Rows[1].Series.Title)
	}
	ra := pg.Rows[1]
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if ra.Parts != 3 || ra.Completed != 2 || ra.Status != Active || ra.Cover != "covers/a1.jpg" || !ra.Updated.Equal(want) {
		t.Fatalf("alpha row = %+v", ra)
	}
	if rb := pg.Rows[0]; rb.Parts != 1 || rb.Completed != 0 || rb.Status != Paused || rb.Cover != "" {
		t.Fatalf("beta row = %+v", rb)
	}
	pg, err = e.lib.ListSeries(bg, SeriesQuery{PluginID: "book", Sort: "title"})
	if err != nil || pg.Rows[0].Series.Title != "Alpha" || pg.Rows[1].Series.Title != "Beta" {
		t.Fatalf("title sort: %+v %v", pg, err)
	}
	pg, err = e.lib.ListSeries(bg, SeriesQuery{PluginID: "book", PageSize: 1, Page: 9})
	if err != nil || pg.Page != 2 || pg.Pages != 2 || pg.Total != 2 || len(pg.Rows) != 1 {
		t.Fatalf("clamped page: %+v %v", pg, err)
	}
}

func TestListSeriesText(t *testing.T) {
	e := newEnv(t)
	s := seriesOf(t, e, "book", "The Expanse", "x1", "x2")
	seriesOf(t, e, "book", "Other", "y1")
	if _, err := e.db.Exec(`UPDATE item_titles SET text = 'Leviathan Wakes' WHERE lang = 'en' AND item_id = (SELECT item_id FROM series_parts WHERE series_id = ? AND position = 2)`, s.ID); err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]int{"EXPANSE": 1, "leviathan": 1, "  wakes ": 1, "dune": 2, "zzz": 0, "100%": 0} {
		pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book", Text: text})
		if err != nil || pg.Total != want || len(pg.Rows) != want || pg.Pages != 1 {
			t.Errorf("text %q: %+v %v, want %d", text, pg, err, want)
		}
	}
}

func TestListSeriesStatusAndCounts(t *testing.T) {
	e := newEnv(t)
	a := seriesOf(t, e, "book", "A", "a1", "a2")
	b := seriesOf(t, e, "book", "B", "b1")
	seriesOf(t, e, "book", "C", "c1")
	setStatus(t, e, a.ID, 1, Completed, "2026-01-01T00:00:00Z")
	setStatus(t, e, a.ID, 2, Dropped, "2026-01-01T00:00:00Z")
	setStatus(t, e, b.ID, 1, Active, "2026-01-01T00:00:00Z")
	pg, err := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book", Status: Completed})
	if err != nil || pg.Total != 1 || pg.Rows[0].Series.Title != "A" {
		t.Fatalf("completed: %+v %v", pg, err)
	}
	counts, err := e.lib.SeriesCounts(bg, SeriesQuery{PluginID: "book", Status: Completed})
	if err != nil {
		t.Fatal(err)
	}
	if counts[Completed] != 1 || counts[Active] != 1 || counts[Planned] != 1 || counts[Paused] != 0 || counts[Dropped] != 0 || len(counts) != 5 {
		t.Fatalf("counts = %v", counts)
	}
	all, _ := e.lib.ListSeries(bg, SeriesQuery{PluginID: "book"})
	sum := 0
	for _, n := range counts {
		sum += n
	}
	if sum != all.Total {
		t.Fatalf("sum %d != total %d", sum, all.Total)
	}
}

func TestSeriesPartsAndHasSeries(t *testing.T) {
	e := newEnv(t)
	if ok, err := e.lib.HasSeries(bg, "book"); err != nil || ok {
		t.Fatalf("empty: %v %v", ok, err)
	}
	s := seriesOf(t, e, "book", "S", "p1", "p2", "p3")
	last := partIDs(t, e, s.ID)[2]
	for range 2 {
		if err := e.lib.MovePart(bg, s.ID, last, -1); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.lib.SeriesParts(bg, s.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	ids := partIDs(t, e, s.ID)
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	for i, r := range rows {
		if r.Item.ID != ids[i] || r.Title != "Dune" || r.Entry.ItemID != ids[i] {
			t.Fatalf("row %d = %+v want item %d", i, r, ids[i])
		}
	}
	if ok, err := e.lib.HasSeries(bg, "book"); err != nil || !ok {
		t.Fatalf("has: %v %v", ok, err)
	}
	if ok, _ := e.lib.HasSeries(bg, "book2"); ok {
		t.Fatal("book2 has none")
	}
}

func TestSeriesCandidates(t *testing.T) {
	e := newEnv(t)
	titles := func(rows []Row) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	mk := func(id, title, author string) int64 {
		d := bookData(id)
		d.Titles = []plugin.Title{{Lang: "en", Text: title}}
		d.Fields = map[string]plugin.FieldValue{"author": {Text: author}}
		itemID, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Planned})
		if err != nil {
			t.Fatal(err)
		}
		return itemID
	}
	taken := mk("1", "Taken", "Le Guin")
	mk("2", "Zeta", "Other")
	mk("3", "Alpha", "Other")
	mk("4", "Mid", "  LE GUIN ")
	mk("5", "Beta%", "Other")
	s, err := e.lib.CreateSeries(bg, taken, "Earthsea")
	if err != nil {
		t.Fatal(err)
	}
	// hint match first (trimmed, case-insensitive), then by title; members out
	got, err := e.lib.SeriesCandidates(bg, "book", "book", "", "author", "le guin", "en", 20)
	if err != nil || strings.Join(titles(got), ",") != "Mid,Alpha,Beta%,Zeta" {
		t.Fatalf("hinted = %v %v", titles(got), err)
	}
	// no hint field or empty series title: plain title order
	got, _ = e.lib.SeriesCandidates(bg, "book", "book", "", "", "le guin", "en", 20)
	if strings.Join(titles(got), ",") != "Alpha,Beta%,Mid,Zeta" {
		t.Fatalf("plain = %v", titles(got))
	}
	// the limit cuts after the ordering
	got, _ = e.lib.SeriesCandidates(bg, "book", "book", "", "author", "le guin", "en", 2)
	if strings.Join(titles(got), ",") != "Mid,Alpha" {
		t.Fatalf("limit = %v", titles(got))
	}
	// the text filter is the list's: contains, case-insensitive, LIKE-escaped
	got, _ = e.lib.SeriesCandidates(bg, "book", "book", "ET", "author", "x", "en", 20)
	if strings.Join(titles(got), ",") != "Beta%,Zeta" {
		t.Fatalf("text = %v", titles(got))
	}
	if got, _ = e.lib.SeriesCandidates(bg, "book", "book", "%", "", "", "en", 20); strings.Join(titles(got), ",") != "Beta%" {
		t.Fatalf("escaped = %v", titles(got))
	}
	// parts of the series leave the candidates as they join
	if err := e.lib.AddPart(bg, s.ID, got[0].Item.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.lib.SeriesCandidates(bg, "book", "book", "beta", "", "", "en", 20); len(got) != 0 {
		t.Fatalf("member offered: %v", titles(got))
	}
	if other, err := e.lib.SeriesCandidates(bg, "tv", "show", "", "", "", "en", 20); err != nil || len(other) != 0 {
		t.Fatalf("other kind = %v %v", other, err)
	}
	if none, err := e.lib.SeriesCandidates(bg, "book", "book", "", "", "", "en", 0); err != nil || none != nil {
		t.Fatalf("limit 0 = %v %v", none, err)
	}
}

func TestSeriesOfItems(t *testing.T) {
	e := newEnv(t)
	s := seriesOf(t, e, "book", "Saga", "1", "2")
	free := addBook(t, e.lib, "9", fp(1), Entry{Status: Planned})
	ids := partIDs(t, e, s.ID)
	got, err := e.lib.SeriesOfItems(bg, append([]int64{free}, ids...))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for i, id := range ids {
		if g := got[id]; g.UID != s.UID || g.Title != "Saga" || g.Position != i+1 {
			t.Fatalf("part %d = %+v", i, g)
		}
	}
	if m, err := e.lib.SeriesOfItems(bg, nil); err != nil || len(m) != 0 {
		t.Fatalf("empty: %v %v", m, err)
	}
}

func TestRemoveEntryLeavesTheSeries(t *testing.T) {
	e := newEnv(t)
	s := seriesOf(t, e, "book", "Saga", "1", "2", "3")
	ids := partIDs(t, e, s.ID)
	if err := e.lib.RemoveEntry(bg, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got, _, err := e.lib.SeriesOfItem(bg, ids[0]); err != nil || got != nil {
		t.Fatalf("removed entry still a part: %+v %v", got, err)
	}
	for i, id := range ids[1:] {
		if _, pos, err := e.lib.SeriesOfItem(bg, id); err != nil || pos != i+1 {
			t.Fatalf("part %d at %d (%v)", id, pos, err)
		}
	}
	// the last part takes the series with it
	for _, id := range ids[1:] {
		if err := e.lib.RemoveEntry(bg, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.lib.SeriesByUID(bg, "book", s.UID); !errors.Is(err, ErrNoSeries) {
		t.Fatalf("series kept: %v", err)
	}
}

func TestRestoreSeriesHostileInput(t *testing.T) {
	e := newEnv(t)
	addBook(t, e.lib, "1", fp(1), Entry{Status: Planned})
	addBook(t, e.lib, "2", fp(1), Entry{Status: Planned})
	addBook(t, e.lib, "3", fp(1), Entry{Status: Planned})
	taken := addBook(t, e.lib, "4", fp(1), Entry{Status: Planned})
	if _, err := e.lib.CreateSeries(bg, taken, "Taken"); err != nil {
		t.Fatal(err)
	}
	addShow(t, e.lib, "9", fp(1), Entry{Status: Planned})
	ref := func(source, id string) BackupPartRef {
		return BackupPartRef{PluginID: "book", Kind: "book", Source: source, SourceID: id}
	}
	count := func(q string) (n int) {
		if err := e.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	good := func(uid string, parts ...BackupPartRef) BackupSeries {
		return BackupSeries{UID: uid, PluginID: "book", Kind: "book", Title: "T " + uid, Parts: parts}
	}
	bad := func(mod func(*BackupSeries)) BackupSeries {
		bs := good("u-x", ref("books", "1"))
		mod(&bs)
		return bs
	}
	invalidCases := map[string]BackupSeries{
		"control title": bad(func(s *BackupSeries) { s.Title = "a\x07b" }),
		"empty title":   bad(func(s *BackupSeries) { s.Title = " \t " }),
		"empty uid":     bad(func(s *BackupSeries) { s.UID = "" }),
		"long uid":      bad(func(s *BackupSeries) { s.UID = strings.Repeat("u", 101) }),
		"control uid":   bad(func(s *BackupSeries) { s.UID = "u\nx" }),
		"empty kind":    bad(func(s *BackupSeries) { s.Kind = "" }),
	}
	seriesN, partsN := count(`SELECT count(*) FROM series`), count(`SELECT count(*) FROM series_parts`)
	items := count(`SELECT count(*) FROM items`)
	for name, bs := range invalidCases {
		if created, _, err := e.lib.RestoreSeries(bg, bs); created || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: created=%v err=%v", name, created, err)
		}
	}
	// nothing resolvable: not created, the rejects are counted
	unresolvable := map[string]struct {
		bs     BackupSeries
		reason string
	}{
		"other plugin":      {good("u-p", BackupPartRef{PluginID: "tv", Kind: "show", Source: "tvdata", SourceID: "9"}), LeftOutOtherKind},
		"other kind":        {good("u-k", BackupPartRef{PluginID: "book", Kind: "show", Source: "books", SourceID: "1"}), LeftOutOtherKind},
		"in another series": {good("u-t", ref("books", "4")), LeftOutInSeries},
		"unknown item":      {good("u-g", ref("books", "nope")), LeftOutMissing},
	}
	for name, c := range unresolvable {
		created, left, err := e.lib.RestoreSeries(bg, c.bs)
		if created || err != nil || len(left) != 1 || left[0].Reason != c.reason || left[0].Ref != c.bs.Parts[0] {
			t.Errorf("%s: created=%v left=%+v err=%v", name, created, left, err)
		}
	}
	if count(`SELECT count(*) FROM series`) != seriesN || count(`SELECT count(*) FROM series_parts`) != partsN || count(`SELECT count(*) FROM items`) != items {
		t.Fatal("a refused series changed the store")
	}
	// the same part twice and a mix: only the resolvable, first occurrence stays
	created, left, err := e.lib.RestoreSeries(bg, good("u-mix", ref("books", "2"), ref("books", "2"), ref("books", "4"),
		BackupPartRef{PluginID: "tv", Kind: "show", Source: "tvdata", SourceID: "9"}, ref("books", "3")))
	if err != nil || !created || len(left) != 3 {
		t.Fatalf("mix: created=%v left=%+v err=%v", created, left, err)
	}
	if left[0].Reason != LeftOutDuplicate || left[1].Reason != LeftOutInSeries || left[1].Title == "" ||
		left[2].Reason != LeftOutOtherKind {
		t.Fatalf("reasons = %+v", left)
	}
	if n := count(`SELECT count(*) FROM series_parts p JOIN series s ON s.id = p.series_id WHERE s.uid = 'u-mix'`); n != 2 {
		t.Fatalf("parts of the mix = %d", n)
	}
	if count(`SELECT count(*) FROM series`) != seriesN+1 || count(`SELECT count(*) FROM items`) != items {
		t.Fatal("unexpected series or item count")
	}
}

// TestActiveSeriesMatchesDerivedStatus: for every combination of up to three
// part statuses, ActiveSeries returns the series exactly when
// DeriveSeriesStatus says active, newest part update first, cut at limit.
func TestActiveSeriesMatchesDerivedStatus(t *testing.T) {
	e := newEnv(t)
	all := Statuses()
	var combos [][]Status
	for i, a := range all {
		combos = append(combos, []Status{a})
		for j := i; j < len(all); j++ {
			combos = append(combos, []Status{a, all[j]})
			for k := j; k < len(all); k++ {
				combos = append(combos, []Status{a, all[j], all[k]})
			}
		}
	}
	n := 0
	titleOf := map[int64]string{}
	want := map[string]bool{}
	for _, c := range combos {
		var ids []int64
		for range c {
			n++
			ids = append(ids, addShow(t, e.lib, "x"+fmt.Sprint(n), nil, Entry{Status: Planned}))
		}
		title := "S" + fmt.Sprint(n)
		s := mustSeries(t, e, ids[0], title)
		mustAddPart(t, e, s.ID, ids[1:]...)
		for i, st := range c {
			if _, err := e.db.Exec(`UPDATE entries SET status = ? WHERE item_id = ?`, string(st), ids[i]); err != nil {
				t.Fatal(err)
			}
		}
		titleOf[s.ID] = title
		want[title] = DeriveSeriesStatus(c) == Active
	}
	got, err := e.lib.ActiveSeries(bg, []string{"tv"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range got {
		if r.Status != Active || r.Parts == 0 {
			t.Fatalf("%+v", r)
		}
		have[r.Series.Title] = true
	}
	for title, w := range want {
		if have[title] != w {
			t.Errorf("%s: active=%v, ActiveSeries has it=%v", title, w, have[title])
		}
	}
	if lim, _ := e.lib.ActiveSeries(bg, []string{"tv"}, 3); len(lim) != 3 {
		t.Fatalf("limit: %d", len(lim))
	}
	if none, _ := e.lib.ActiveSeries(bg, []string{"book"}, 10); len(none) != 0 {
		t.Fatalf("other plugin: %d", len(none))
	}
	if none, _ := e.lib.ActiveSeries(bg, nil, 10); len(none) != 0 {
		t.Fatalf("no plugins: %d", len(none))
	}
}

// TestActiveSeriesOrderAndCounts: newest part update first, with the
// completed/parts counts and the first part's cover.
func TestActiveSeriesOrderAndCounts(t *testing.T) {
	e := newEnv(t)
	a1 := addShow(t, e.lib, "o1", nil, Entry{Status: Completed})
	a2 := addShow(t, e.lib, "o2", nil, Entry{Status: Active})
	b1 := addShow(t, e.lib, "o3", nil, Entry{Status: Active})
	sa := mustSeries(t, e, a1, "Older")
	mustAddPart(t, e, sa.ID, a2)
	sb := mustSeries(t, e, b1, "Newer")
	stamp := func(id int64, at string) {
		if _, err := e.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	stamp(a1, "2025-01-01T00:00:00Z")
	stamp(a2, "2025-01-02T00:00:00Z")
	stamp(b1, "2025-02-01T00:00:00Z")
	got, err := e.lib.ActiveSeries(bg, []string{"tv"}, 10)
	if err != nil || len(got) != 2 || got[0].Series.ID != sb.ID || got[1].Series.ID != sa.ID {
		t.Fatalf("%+v %v", got, err)
	}
	if got[1].Parts != 2 || got[1].Completed != 1 || !got[1].Updated.Equal(time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", got[1])
	}
}
