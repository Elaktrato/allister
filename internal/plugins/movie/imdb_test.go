package movie

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const imdbHeader = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n"

const imdbSample = imdbHeader +
	"tt0094012,8,2026-03-03,Film One,Film One,https://x,Film,7,90,1987,Comedy,10,1987-06-24,D\n" +
	"tt1000001,,,Series One,Series One,https://x,Fernsehserie,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt1000002,5,2026-03-04,Episode One,Episode One,https://x,Fernsehepisode,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt1000003,6,2026-03-05,Odd One,Odd One,https://x,Serie animée,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0094012,8,2026-03-03,Film One,Film One,https://x,Film,7,90,1987,Comedy,10,1987-06-24,D\n" +
	"tt1000004,,,Film Two,Film Two,https://x,Film,7,90,1999,Drama,10,1999-01-01,D\n"

func TestIMDbMovieImporter(t *testing.T) {
	res, err := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Ignored != 2 {
		t.Fatalf("res = %+v", res)
	}
	e := res.Entries[0]
	if e.Kind != KindMovie || e.Title != "Film One" || e.ExternalIDs["imdb"] != "tt0094012" || e.Status != plugin.Completed ||
		e.Rating == nil || *e.Rating != 8 || e.FinishedOn == nil || e.FinishedOn.Format("2006-01-02") != "2026-03-03" ||
		e.Fields["year"].Num == nil || *e.Fields["year"].Num != 1987 {
		t.Fatalf("entry = %+v", e)
	}
	if w := res.Entries[1]; w.Status != plugin.Planned || w.Rating != nil || w.FinishedOn != nil {
		t.Fatalf("watchlist entry = %+v", w)
	}
}

func TestIMDbUnknownTypeIsSkippedWithValue(t *testing.T) {
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	found := false
	for _, s := range res.Skipped {
		if strings.Contains(s.Reason, "Serie animée") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestIMDbDuplicateConstReportedOnce(t *testing.T) {
	res, _ := imdbMovieImporter{}.Parse(context.Background(), strings.NewReader(imdbSample))
	dups := 0
	for _, s := range res.Skipped {
		if strings.Contains(s.Reason, "duplicate") {
			dups++
		}
	}
	if dups != 1 {
		t.Fatalf("skipped = %+v", res.Skipped)
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
