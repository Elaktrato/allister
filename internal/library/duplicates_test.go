package library

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func dupBook(src, id, title string, ext map[string]string) plugin.ItemData {
	d := bookData(id)
	d.Source = src
	d.Titles = []plugin.Title{{Lang: "en", Text: title}}
	d.ExternalIDs = ext
	return d
}

func dupShow(id, title string, year *float64) plugin.ItemData {
	d := tvData(id, nil)
	d.Titles = []plugin.Title{{Lang: "en", Text: title}}
	if year != nil {
		d.Fields["year"] = plugin.FieldValue{Num: year}
	}
	return d
}

func dupAdd(t *testing.T, l *Library, pluginID, kind string, d plugin.ItemData, inLib bool) int64 {
	t.Helper()
	if inLib {
		id, err := l.AddToList(bg, pluginID, kind, d, Entry{Status: Planned})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return mustSave(t, l, pluginID, kind, d)
}

func TestNormalTitle(t *testing.T) {
	for in, want := range map[string]string{
		"  Dune: Part  One! ": "dune part one",
		"ÄRGER":               "ärger",
		"a--b__c":             "a b c",
		"!!!":                 "",
	} {
		if got := normalTitle(in); got != want {
			t.Errorf("normalTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDuplicatesByExternalID(t *testing.T) {
	l := newLib(t)
	a := dupAdd(t, l, "book", "book", dupBook("ol", "1", "Alpha", map[string]string{"isbn": "123", "oclc": "9"}), true)
	b := dupAdd(t, l, "book", "book", dupBook("gb", "2", "Beta", map[string]string{"isbn": "123", "oclc": "9"}), false)
	dupAdd(t, l, "book", "book", dupBook("gb", "3", "Gamma", map[string]string{"isbn": "999"}), true)
	// same value, other type: no pair
	dupAdd(t, l, "book", "book", dupBook("gb", "4", "Delta", map[string]string{"issn": "999"}), true)
	got, err := l.Duplicates(bg, "book")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (DuplicatePair{A: a, B: b, Reason: "id:isbn"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDuplicatesNeitherInLibraryExcluded(t *testing.T) {
	l := newLib(t)
	dupAdd(t, l, "book", "book", dupBook("ol", "1", "Alpha", map[string]string{"isbn": "1"}), false)
	dupAdd(t, l, "book", "book", dupBook("gb", "2", "Alpha", map[string]string{"isbn": "1"}), false)
	got, err := l.Duplicates(bg, "book")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDuplicatesByTitleAndYear(t *testing.T) {
	l := newLib(t)
	y := func(f float64) *float64 { return &f }
	a := dupAdd(t, l, "tv", "show", dupShow("1", "The  Show!", y(2000)), true)
	b := dupAdd(t, l, "tv", "show", dupShow("2", "the show", y(2000)), false)
	dupAdd(t, l, "tv", "show", dupShow("3", "the show", y(2001)), true)
	// no year never matches by title
	dupAdd(t, l, "tv", "show", dupShow("4", "No Year", nil), true)
	dupAdd(t, l, "tv", "show", dupShow("5", "No Year", nil), true)
	got, err := l.Duplicates(bg, "tv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (DuplicatePair{A: a, B: b, Reason: "title"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDuplicatesKindWithoutYearMatchesByTitle(t *testing.T) {
	l := newLib(t)
	a := dupAdd(t, l, "book", "book", dupBook("ol", "1", "Dune", nil), true)
	b := dupAdd(t, l, "book", "book", dupBook("gb", "2", "DUNE.", nil), true)
	got, err := l.Duplicates(bg, "book")
	if err != nil || len(got) != 1 || got[0] != (DuplicatePair{A: a, B: b, Reason: "title"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDuplicatesIDReasonWinsAndDismiss(t *testing.T) {
	l := newLib(t)
	a := dupAdd(t, l, "book", "book", dupBook("ol", "1", "Dune", map[string]string{"isbn": "1"}), true)
	b := dupAdd(t, l, "book", "book", dupBook("gb", "2", "Dune", map[string]string{"isbn": "1"}), true)
	got, _ := l.Duplicates(bg, "book")
	if len(got) != 1 || got[0].Reason != "id:isbn" {
		t.Fatalf("got %+v", got)
	}
	if err := l.DismissDuplicate(bg, b, a); err != nil { // either order
		t.Fatal(err)
	}
	if err := l.DismissDuplicate(bg, a, b); err != nil { // twice is a no-op
		t.Fatal(err)
	}
	if got, _ := l.Duplicates(bg, "book"); len(got) != 0 {
		t.Fatalf("dismissed pair listed: %+v", got)
	}
	if err := l.DismissDuplicate(bg, a, a); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same id err = %v", err)
	}
	if err := l.DismissDuplicate(bg, a, 99999); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown id err = %v", err)
	}
}

func TestDuplicatesPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("slow guard")
	}
	l := newLib(t)
	tx, err := l.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3000 {
		res, err := tx.Exec(`INSERT INTO items(plugin_id, kind, source, source_id) VALUES ('book','book','s',?)`, fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES (?, 'en', ?)`, id, fmt.Sprintf("Title %d", i/2)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO external_ids(item_id, type, value) VALUES (?, 'isbn', ?)`, id, fmt.Sprint(i/3)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO entries(item_id, added_at, updated_at) VALUES (?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := l.Duplicates(bg, "book")
	d := time.Since(start)
	t.Logf("Duplicates over 3000 items: %v (%d pairs)", d, len(got))
	if err != nil || len(got) == 0 {
		t.Fatalf("got %d pairs, %v", len(got), err)
	}
	if d > time.Second {
		t.Fatalf("took %v, want under 1s", d)
	}
}
