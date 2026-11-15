package tv

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const imdbTVSample = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n" +
	"tt0000002,9,2026-03-03,Series One,Series One,https://x,Fernsehserie,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0000001,8,2026-03-03,Film One,Film One,https://x,Film,7,90,2001,Comedy,10,2001-01-01,D\n" +
	"tt0000003,,,Mini One,Mini One,https://x,Miniserie,7,90,2021,Drama,10,2021-01-01,D\n"

func TestIMDbTVImporter(t *testing.T) {
	res, err := imdbTVImporter{}.Parse(context.Background(), strings.NewReader(imdbTVSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Ignored != 1 || len(res.Skipped) != 0 {
		t.Fatalf("res = %+v", res)
	}
	e := res.Entries[0]
	if e.Kind != KindShow || e.ExternalIDs["imdb"] != "tt0000002" || e.Status != plugin.Completed || *e.Rating != 9 || e.FinishedOn == nil {
		t.Fatalf("entry = %+v", e)
	}
	if res.Entries[1].Status != plugin.Planned {
		t.Fatalf("entry = %+v", res.Entries[1])
	}
}

func TestIMDbTVImporterRegistered(t *testing.T) {
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
	if im.Name != "IMDb CSV" || im.ExternalIDType != "imdb" || im.ResolveVia != SourceTVmaze || !reflect.DeepEqual(im.ResolveViaFallback, []string{SourceTMDB}) ||
		!im.ManualRows || im.UnmatchedNote != "Imported from IMDb; TVmaze and TMDB had no match for {id}." || im.FileHint == "" {
		t.Fatalf("importer = %+v", im)
	}
}

func TestIMDbTVEdgeRows(t *testing.T) {
	csv := imdbTVSample[:strings.Index(imdbTVSample, "\n")+1] +
		"tt0000002,9,2001-01-01,Series One,x,u,Fernsehserie,1,1,2020,g,1,2020-01-01,d\n" +
		"tt0000002,3,2001-01-02,Series Dup,x,u,Fernsehserie,1,1,2020,g,1,2020-01-01,d\n" +
		"bogus,5,2001-01-01,Bad Show,x,u,Fernsehserie,1,1,2020,g,1,2020-01-01,d\n" +
		"bogus,5,2001-01-01,Bad Film,x,u,Film,1,1,2020,g,1,2020-01-01,d\n" +
		"tt0000009,5,2001-01-01,Odd,x,u,Serie animée,1,1,2020,g,1,2020-01-01,d\n"
	res, _ := imdbTVImporter{}.Parse(context.Background(), strings.NewReader(csv))
	want := []plugin.SkippedRow{
		{Line: 3, Title: "Series Dup", Reason: "duplicate IMDb id tt0000002"},
		{Line: 4, Title: "Bad Show", Reason: "invalid IMDb id"},
		{Line: 6, Title: "Odd", Reason: `unknown Title Type "Serie animée"`},
	}
	if len(res.Entries) != 1 || *res.Entries[0].Rating != 9 || res.Ignored != 1 || !reflect.DeepEqual(res.Skipped, want) {
		t.Fatalf("res = %+v", res)
	}
}
