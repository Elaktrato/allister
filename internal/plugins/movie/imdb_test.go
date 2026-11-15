package movie

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const imdbHeader = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n"

const imdbSample = imdbHeader +
	"tt0000001,8,2026-03-03,Film One,Film One,https://x,Film,7,90,2001,Comedy,10,2001-01-01,D\n" +
	"tt0000002,,,Series One,Series One,https://x,Fernsehserie,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0000003,5,2026-03-04,Episode One,Episode One,https://x,Fernsehepisode,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0000004,6,2026-03-05,Odd One,Odd One,https://x,Serie animée,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0000001,8,2026-03-03,Film One,Film One,https://x,Film,7,90,2001,Comedy,10,2001-01-01,D\n" +
	"tt0000005,,,Film Two,Film Two,https://x,Film,7,90,1999,Drama,10,1999-01-01,D\n"

func TestIMDbMovieImporter(t *testing.T) {
	res, err := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Ignored != 2 {
		t.Fatalf("res = %+v", res)
	}
	e := res.Entries[0]
	if e.Kind != KindMovie || e.Title != "Film One" || e.ExternalIDs["imdb"] != "tt0000001" || e.Status != plugin.Completed ||
		e.Rating == nil || *e.Rating != 8 || e.FinishedOn == nil || e.FinishedOn.Format("2006-01-02") != "2026-03-03" ||
		e.Fields["year"].Num == nil || *e.Fields["year"].Num != 2001 {
		t.Fatalf("entry = %+v", e)
	}
	if w := res.Entries[1]; w.Status != plugin.Planned || w.Rating != nil || w.FinishedOn != nil {
		t.Fatalf("watchlist entry = %+v", w)
	}
}

func TestIMDbUnknownTypeIsSkippedWithValue(t *testing.T) {
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	var got []plugin.SkippedRow
	for _, s := range res.Skipped {
		if strings.Contains(s.Reason, "Serie") {
			got = append(got, s)
		}
	}
	want := []plugin.SkippedRow{{Line: 5, Title: "Odd One", Reason: `unknown Title Type "Serie animée"`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestIMDbDuplicateConstReportedOnce(t *testing.T) {
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	want := plugin.SkippedRow{Line: 6, Title: "Film One", Reason: "duplicate IMDb id tt0000001"}
	if res.Skipped[1] != want {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	if res.Entries[0].Title != "Film One" || *res.Entries[0].Rating != 8 {
		t.Fatalf("first row must win: %+v", res.Entries[0])
	}
}

func TestIMDbMovieBadFormat(t *testing.T) {
	_, err := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader("a,b\n1,2\n"))
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestIMDbMovieImporterRegistered(t *testing.T) {
	p := New(Options{})
	if p.Importer(ImporterIMDb) == nil {
		t.Fatal("not registered")
	}
	var im plugin.ImporterInfo
	for _, i := range p.Manifest().Importers {
		if i.ID == ImporterIMDb {
			im = i
		}
	}
	if im.Name != "IMDb CSV" || im.ExternalIDType != "imdb" || im.ExternalIDLabel != "IMDb" || im.ResolveVia != SourceTMDB ||
		!im.ManualRows || im.FallbackTitleLang != "en" || im.UnmatchedNote != "Imported from IMDb; TMDB had no match for {id}." ||
		len(im.Accept) != 1 || im.Accept[0] != ".csv" || im.FileHint == "" {
		t.Fatalf("importer = %+v", im)
	}
}

func TestIMDbInvalidConstSkippedOnlyWhereTypeWanted(t *testing.T) {
	csv := imdbHeader +
		"bogus,5,2001-01-01,Bad Film,x,u,Film,1,1,2001,g,1,2001-01-01,d\n" +
		"bogus,5,2001-01-01,Bad Show,x,u,Fernsehserie,1,1,2001,g,1,2001-01-01,d\n"
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(csv))
	want := []plugin.SkippedRow{{Line: 2, Title: "Bad Film", Reason: "invalid IMDb id"}}
	if len(res.Entries) != 0 || res.Ignored != 1 || !reflect.DeepEqual(res.Skipped, want) {
		t.Fatalf("res = %+v", res)
	}
}

func TestIMDbBadRatingStaysWatched(t *testing.T) {
	csv := imdbHeader + "tt0000001,11,03/03/2026,F,F,u,Film,1,1,2001,g,1,2001-01-01,d\n"
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(csv))
	if len(res.Entries) != 1 {
		t.Fatalf("res = %+v", res)
	}
	e := res.Entries[0]
	wantNotes := []string{`IMDb rating "11" is not 1–10; not imported`, `IMDb date "03/03/2026" not understood`}
	if e.Status != plugin.Completed || e.Rating != nil || e.FinishedOn != nil || !reflect.DeepEqual(e.Notes, wantNotes) {
		t.Fatalf("entry = %+v", e)
	}
}
