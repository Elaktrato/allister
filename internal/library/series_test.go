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
