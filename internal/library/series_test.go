package library

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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
	sa, _ := e.lib.CreateSeries(bg, a, "A")
	sb, _ := e.lib.CreateSeries(bg, b, "B")
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
	_ = sa
}

func TestMovePart(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s, _ := e.lib.CreateSeries(bg, a, "S")
	for _, id := range []int64{b, c} {
		if err := e.lib.AddPart(bg, s.ID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
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

func TestRemovePartKeepsEntry(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "a", fp(1), Entry{Status: Planned})
	b := addBook(t, e.lib, "b", fp(1), Entry{Status: Planned})
	c := addBook(t, e.lib, "c", fp(1), Entry{Status: Planned})
	s, _ := e.lib.CreateSeries(bg, a, "S")
	for _, id := range []int64{b, c} {
		if err := e.lib.AddPart(bg, s.ID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
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
		s, _ := e.lib.CreateSeries(bg, a, "S"+p)
		if err := e.lib.AddPart(bg, s.ID, b, ""); err != nil {
			t.Fatal(err)
		}
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
	s, _ := e.lib.CreateSeries(bg, a, "S")
	for _, id := range []int64{b, c} {
		if err := e.lib.AddPart(bg, s.ID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
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
