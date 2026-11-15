package manga

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestKindStats(t *testing.T) {
	k := manifest().Kinds[0]
	want := &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "chapter", LabelPlural: "Chapters"}}
	if !reflect.DeepEqual(k.Stats, want) {
		t.Fatalf("stats = %+v", k.Stats)
	}
}

func TestYamtrackMangaImporterInfo(t *testing.T) {
	if ImporterYamtrack != "yamtrack-manga" {
		t.Fatalf("ImporterYamtrack = %q", ImporterYamtrack)
	}
	m := manifest()
	var got plugin.ImporterInfo
	found := false
	for _, im := range m.Importers {
		if im.ID == ImporterYamtrack {
			got, found = im, true
		}
	}
	want := plugin.ImporterInfo{
		ID:                ImporterYamtrack,
		Name:              "Yamtrack Manga CSV",
		FileHint:          "Upload the Yamtrack general CSV. Manga rows are matched on AniList by MyAnimeList id; novels are skipped (they belong in Books > Light novels).",
		Accept:            []string{".csv"},
		ExternalIDType:    "mal",
		ExternalIDLabel:   "MyAnimeList",
		ResolveVia:        SourceID,
		ManualRows:        true,
		FallbackTitleLang: "ja-latn",
		UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
	}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("yamtrack importer = %+v, want %+v", got, want)
	}
}

func TestSeriesHintReleaseField(t *testing.T) {
	k := manifest().Kinds[0]
	want := &plugin.SeriesHintDef{ReleaseField: "started_on"}
	if !reflect.DeepEqual(k.SeriesHint, want) {
		t.Fatalf("series hint = %+v, want %+v", k.SeriesHint, want)
	}
	if k.SeriesFields != nil || k.SeriesLink != nil {
		t.Fatalf("series fields/link = %+v %+v", k.SeriesFields, k.SeriesLink)
	}
}
