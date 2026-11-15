package imdb

import (
	"errors"
	"strings"
	"testing"
)

const ratingsHeader = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n"
const watchlistHeader = "Position,Const,Created,Modified,Description,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors,Your Rating,Date Rated\n"

func TestParseRatingsLayout(t *testing.T) {
	csv := ratingsHeader +
		"tt0094012,7,2026-03-03,Title A,Orig,https://x,Film,7.1,90,1987,Comedy,100,1987-06-24,Dir\n" +
		"tt1234567,,,Title B,Orig,https://x,Fernsehserie,7.1,90,,Comedy,100,1987-06-24,Dir\n" +
		"bad,5,2026-03-03,Title C,Orig,https://x,Film,7.1,90,1990,Comedy,100,1987-06-24,Dir\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	a := rows[0]
	if a.Const != "tt0094012" || a.Title != "Title A" || a.TypeRaw != "Film" || a.TypeKey != "film" || a.Year != 1987 ||
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
		"1,tt12345678,2026-01-01,2026-01-01,,Title A,Orig,https://x,Fernsehserie,7.1,90,2020,Drama,100,2020-01-01,Dir,,\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Const != "tt12345678" || rows[0].Rating != nil || rows[0].RatedOn != nil || rows[0].Year != 2020 || rows[0].TypeKey != "fernsehserie" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestParseMissingRequiredColumn(t *testing.T) {
	_, err := Parse(strings.NewReader("Const,Title,Year\ntt0094012,A,1987\n"))
	if !errors.Is(err, ErrBadFormat) {
		t.Fatalf("err = %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Film": TypeMovie, "Movie": TypeMovie, "Video": TypeMovie, "TV Movie": TypeMovie, "Fernsehfilm": TypeMovie,
		"Short": TypeMovie, "Kurzfilm": TypeMovie, "TV Special": TypeMovie,
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
