package imdb

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

const ratingsHeader = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n"
const watchlistHeader = "Position,Const,Created,Modified,Description,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors,Your Rating,Date Rated\n"

func TestParseRatingsLayout(t *testing.T) {
	csv := ratingsHeader +
		"tt0000001,7,2026-03-03,Title A,Orig,https://x,Film,7.1,90,2001,Comedy,100,2001-01-01,Dir\n" +
		"tt0000006,,,Title B,Orig,https://x,Fernsehserie,7.1,90,,Comedy,100,2001-01-01,Dir\n" +
		"bad,5,2026-03-03,Title C,Orig,https://x,Film,7.1,90,1990,Comedy,100,2001-01-01,Dir\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	a := rows[0]
	if a.Const != "tt0000001" || a.Title != "Title A" || a.TypeRaw != "Film" || a.TypeKey != "film" || a.Year != 2001 ||
		a.Rating == nil || *a.Rating != 7 || a.RatedOn == nil || a.RatedOn.Format("2006-01-02") != "2026-03-03" || a.RatedOn.Location().String() != "UTC" {
		t.Fatalf("a = %+v", a)
	}
	b := rows[1]
	if b.Rating != nil || b.RatedOn != nil || b.Year != 0 || b.Line != 3 {
		t.Fatalf("b = %+v", b)
	}
	if rows[2].Const != "" || rows[2].Invalid != "invalid IMDb id" {
		t.Fatalf("c = %+v", rows[2])
	}
}

func TestParseWatchlistLayout(t *testing.T) {
	csv := watchlistHeader +
		"1,tt00000068,2026-01-01,2026-01-01,,Title A,Orig,https://x,Fernsehserie,7.1,90,2020,Drama,100,2020-01-01,Dir,,\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Const != "tt00000068" || rows[0].Rating != nil || rows[0].RatedOn != nil || rows[0].Year != 2020 || rows[0].TypeKey != "fernsehserie" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestParseMissingRequiredColumn(t *testing.T) {
	_, err := Parse(strings.NewReader("Const,Title,Year\ntt0000001,A,2001\n"))
	if !errors.Is(err, ErrBadFormat) {
		t.Fatalf("err = %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Film": TypeMovie, "Movie": TypeMovie, "Video": TypeMovie, "TV Movie": TypeMovie, "Fernsehfilm": TypeMovie,
		"Short": TypeMovie, "Kurzfilm": TypeMovie, "TV Special": TypeMovie, "TV Short": TypeMovie,
		"Fernsehserie": TypeTV, "TV Series": TypeTV, "Miniserie": TypeTV, "TV Mini Series": TypeTV,
		"Fernsehepisode": TypeIgnored, "TV Episode": TypeIgnored, "Video Game": TypeIgnored, "Videospiel": TypeIgnored,
		"Podcast Series": TypeIgnored, "Music Video": TypeIgnored,
		"Serie animée": TypeUnknown,
	}
	for in, want := range cases {
		if got := Classify(NormaliseType(in)); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
}

func one(t *testing.T, cells string) Row {
	t.Helper()
	rows, err := Parse(strings.NewReader(ratingsHeader + cells + "\n"))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
	return rows[0]
}

func TestRatingCellValidation(t *testing.T) {
	for _, raw := range []string{"0", "11", "x"} {
		r := one(t, "tt0000001,"+raw+",2001-01-01,T,T,u,Film,1,1,2001,g,1,2001-01-01,d")
		if r.Rating != nil || !r.Rated || len(r.Notes) != 1 || r.Notes[0] != `IMDb rating "`+raw+`" is not 1–10; not imported` {
			t.Errorf("%q: %+v", raw, r)
		}
	}
	for _, raw := range []string{"1", "10"} {
		r := one(t, "tt0000001,"+raw+",2001-01-01,T,T,u,Film,1,1,2001,g,1,2001-01-01,d")
		if r.Rating == nil || strconv.Itoa(*r.Rating) != raw || len(r.Notes) != 0 {
			t.Errorf("%q: %+v", raw, r)
		}
	}
	r := one(t, "tt0000001,,,T,T,u,Film,1,1,2001,g,1,2001-01-01,d")
	if r.Rated || r.Rating != nil || len(r.Notes) != 0 {
		t.Errorf("empty: %+v", r)
	}
}

func TestDateRatedNotUnderstood(t *testing.T) {
	r := one(t, "tt0000001,7,03/03/2026,T,T,u,Film,1,1,2001,g,1,2001-01-01,d")
	if r.RatedOn != nil || r.Rating == nil || len(r.Notes) != 1 || r.Notes[0] != `IMDb date "03/03/2026" not understood` {
		t.Fatalf("%+v", r)
	}
}

func TestParseRobustness(t *testing.T) {
	bom := string(rune(0xFEFF)) + ratingsHeader
	rows, err := Parse(strings.NewReader(bom + "tt0000001,7,2001-01-01,T,T,u,Film,1,1,2001,g,1,2001-01-01,d\n"))
	if err != nil || len(rows) != 1 || rows[0].Const != "tt0000001" {
		t.Fatalf("bom: %+v %v", rows, err)
	}
	crlf := strings.ReplaceAll(ratingsHeader+"tt0000001,7,2001-01-01,T,T,u,Film,1,1,2001,g,1,2001-01-01,d\ntt0000002,,,U,U,u,Film,1,1,2002,g,1,2001-01-01,d\n", "\n", "\r\n")
	rows, err = Parse(strings.NewReader(crlf))
	if err != nil || len(rows) != 2 || rows[0].TypeKey != "film" || rows[1].Year != 2002 {
		t.Fatalf("crlf: %+v %v", rows, err)
	}
	r := one(t, `tt0000001,7,2001-01-01,"A, B",T,u,Film,1,1,2001,g,1,2001-01-01,d`)
	if r.Title != "A, B" {
		t.Fatalf("quoted comma: %+v", r)
	}
	// A non-numeric or out-of-range year is dropped (Year 0, no note): the row
	// still imports by its IMDb id, it just has no year and is never title-searched.
	for _, y := range []string{"abc", "1500", "9999"} {
		r := one(t, "tt0000001,7,2001-01-01,T,T,u,Film,1,1,"+y+",g,1,2001-01-01,d")
		if r.Year != 0 || len(r.Notes) != 0 {
			t.Errorf("year %q: %+v", y, r)
		}
	}
}
