package tv

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const imdbTVSample = "Const,Your Rating,Date Rated,Title,Original Title,URL,Title Type,IMDb Rating,Runtime (mins),Year,Genres,Num Votes,Release Date,Directors\n" +
	"tt1000001,9,2026-03-03,Series One,Series One,https://x,Fernsehserie,7,90,2020,Drama,10,2020-01-01,D\n" +
	"tt0094012,8,2026-03-03,Film One,Film One,https://x,Film,7,90,1987,Comedy,10,1987-06-24,D\n" +
	"tt1000002,,,Mini One,Mini One,https://x,Miniserie,7,90,2021,Drama,10,2021-01-01,D\n"

func TestIMDbTVImporter(t *testing.T) {
	res, err := imdbTVImporter{}.Parse(context.Background(), strings.NewReader(imdbTVSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Ignored != 1 || len(res.Skipped) != 0 {
		t.Fatalf("res = %+v", res)
	}
	e := res.Entries[0]
	if e.Kind != KindShow || e.ExternalIDs["imdb"] != "tt1000001" || e.Status != plugin.Completed || *e.Rating != 9 || e.FinishedOn == nil {
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
