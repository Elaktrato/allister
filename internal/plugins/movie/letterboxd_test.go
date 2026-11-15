package movie

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func lbZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func lbParse(t *testing.T, files map[string]string) (plugin.ParseResult, error) {
	t.Helper()
	return letterboxdImporter{}.Parse(context.Background(), bytes.NewReader(lbZip(t, files)))
}

func lbByTitle(res plugin.ParseResult, title string) *plugin.ParsedEntry {
	for i := range res.Entries {
		if res.Entries[i].Title == title {
			return &res.Entries[i]
		}
	}
	return nil
}

func TestLetterboxdMerge(t *testing.T) {
	res, err := lbParse(t, map[string]string{
		"watched.csv": "Date,Name,Year,Letterboxd URI\n2026-01-01,Film One,2001,https://boxd.it/test1\n2026-01-02,Film Three,2003,https://boxd.it/test3\n",
		"diary.csv": "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Tags,Watched Date\n" +
			"2026-02-01,Film One,2001,https://boxd.it/entry1,4.5,,,2026-01-30\n" +
			"2026-03-01,Film One,2001,https://boxd.it/entry2,4.5,Yes,,2026-02-27\n",
		"ratings.csv":   "Date,Name,Year,Letterboxd URI,Rating\n2026-01-05,Film One,2001,https://boxd.it/test1,4.5\n",
		"reviews.csv":   "Date,Name,Year,Letterboxd URI,Rating,Rewatch,Review,Tags,Watched Date\n2026-02-01,Film One,2001,https://boxd.it/r1,4.5,,Old take,,2026-01-30\n2026-03-01,Film One,2001,https://boxd.it/r2,4.5,Yes,\"New take,\nsecond line\",,2026-02-27\n",
		"watchlist.csv": "Date,Name,Year,Letterboxd URI\n2026-01-09,Film Two,1999,https://boxd.it/test2\n2026-01-10,Film Three,2003,https://boxd.it/test3\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("entries = %+v skipped = %+v", res.Entries, res.Skipped)
	}
	e := lbByTitle(res, "Film One")
	if e == nil || e.Kind != KindMovie || e.Status != plugin.Completed || e.Rating == nil || *e.Rating != 9 ||
		e.FinishedOn == nil || e.FinishedOn.Format("2006-01-02") != "2026-02-27" ||
		e.Comment != "New take,\nsecond line" ||
		e.Fields["year"].Num == nil || *e.Fields["year"].Num != 2001 ||
		e.ExternalIDs["letterboxd"] != "test1" {
		t.Fatalf("film one = %+v", e)
	}
	if len(e.Notes) != 1 || e.Notes[0] != "watched 2 times" {
		t.Fatalf("notes = %v", e.Notes)
	}
	if p := lbByTitle(res, "Film Two"); p == nil || p.Status != plugin.Planned || p.Rating != nil || p.FinishedOn != nil || p.ExternalIDs["letterboxd"] != "test2" {
		t.Fatalf("film two = %+v", p)
	}
	if w := lbByTitle(res, "Film Three"); w == nil || w.Status != plugin.Completed {
		t.Fatalf("film three = %+v", w)
	}
}

func TestLetterboxdHalfStars(t *testing.T) {
	res, err := lbParse(t, map[string]string{
		"watched.csv": "Date,Name,Year,Letterboxd URI\n2026-01-01,Low,2001,https://boxd.it/a\n2026-01-01,High,2002,https://boxd.it/b\n2026-01-01,None,2003,https://boxd.it/c\n",
		"ratings.csv": "Date,Name,Year,Letterboxd URI,Rating\n2026-01-05,Low,2001,https://boxd.it/a,0.5\n2026-01-05,High,2002,https://boxd.it/b,5\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	for title, want := range map[string]int{"Low": 1, "High": 10} {
		if e := lbByTitle(res, title); e == nil || e.Rating == nil || *e.Rating != want {
			t.Fatalf("%s = %+v", title, e)
		}
	}
	if e := lbByTitle(res, "None"); e == nil || e.Rating != nil {
		t.Fatalf("None = %+v", e)
	}
}

func TestLetterboxdColumnsCaseInsensitive(t *testing.T) {
	res, err := lbParse(t, map[string]string{
		"watched.csv": "\xef\xbb\xbfdate,NAME,year,letterboxd uri\n2026-01-01,Film One,2001,https://boxd.it/test1\n",
	})
	if err != nil || len(res.Entries) != 1 || res.Entries[0].Title != "Film One" || res.Entries[0].ExternalIDs["letterboxd"] != "test1" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLetterboxdBadInput(t *testing.T) {
	if _, err := (letterboxdImporter{}).Parse(context.Background(), strings.NewReader("not a zip")); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("not a zip: err = %v", err)
	}
	if _, err := lbParse(t, map[string]string{"ratings.csv": "Date,Name,Year,Letterboxd URI,Rating\n", "reviews.csv": "Date,Name\n"}); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("no watched/diary/watchlist: err = %v", err)
	}
}

func TestLetterboxdRefusesOversizeAndPaths(t *testing.T) {
	big := "Date,Name,Year,Letterboxd URI\n" + strings.Repeat("x", 10<<20+1)
	if _, err := lbParse(t, map[string]string{"watched.csv": big}); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("oversize: err = %v", err)
	}
	// Nested and traversing names are ignored, so nothing usable is left.
	good := "Date,Name,Year,Letterboxd URI\n2026-01-01,Film One,2001,https://boxd.it/test1\n"
	if _, err := lbParse(t, map[string]string{"../watched.csv": good, "sub/watched.csv": good, "Watched.csv": good}); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("paths: err = %v", err)
	}
	res, err := lbParse(t, map[string]string{"watched.csv": good, "sub/diary.csv": good, "other.csv": big})
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLetterboxdSkipsRowsWithoutName(t *testing.T) {
	res, err := lbParse(t, map[string]string{
		"watched.csv": "Date,Name,Year,Letterboxd URI\n2026-01-01,,2001,https://boxd.it/a\n2026-01-01,Film One,2001,https://boxd.it/test1\n",
	})
	if err != nil || len(res.Entries) != 1 || len(res.Skipped) != 1 || res.Skipped[0].Line != 2 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLetterboxdManifest(t *testing.T) {
	imp := New(Options{}).Importer(ImporterLetterboxd)
	if imp == nil {
		t.Fatal("letterboxd importer not registered")
	}
	m := manifest()
	found := false
	for _, i := range m.Importers {
		if i.ID == ImporterLetterboxd {
			found = true
			if !i.ResolveByTitle || i.ResolveVia != SourceTMDB || i.ExternalIDType != "letterboxd" || len(i.Accept) != 1 || i.Accept[0] != ".zip" {
				t.Fatalf("importer = %+v", i)
			}
		}
	}
	if !found {
		t.Fatal("no manifest entry")
	}
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
}
