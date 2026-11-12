package book

import (
	"reflect"
	"slices"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestBooksManifest(t *testing.T) {
	m := New(Options{}).Manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.ID != PluginID || m.Name != "Books" || m.DefaultEnabled || m.Icon != "auto_stories" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Kinds) != 2 || m.Kinds[0].Key != KindBook || m.Kinds[1].Key != KindSeries {
		t.Fatalf("kinds = %+v", m.Kinds)
	}
	book := m.Kinds[0]
	if book.Label != "Book" || book.LabelPlural != "Books" || !book.Genres.Enabled || !book.Tags.Enabled {
		t.Fatalf("book kind = %+v", book)
	}
	for _, key := range []string{"author", "pages", "year", "publisher", "isbn", "series", "series_number", "synopsis", "published_on"} {
		if !hasField(book, key) {
			t.Errorf("book field %q missing", key)
		}
	}
	wantBook := [][]string{{"pages"}, {"percent"}}
	if !equalCombos(book.Progress.Combinations, wantBook) || book.Progress.PrimaryKey != "pages" ||
		!slices.Equal(book.Progress.Defaults, []string{"pages"}) {
		t.Fatalf("book progress = %+v", book.Progress)
	}
	series := m.Kinds[1]
	if series.Label != "Series" || series.LabelPlural != "Series" {
		t.Fatalf("series kind = %+v", series)
	}
	for _, key := range []string{"author", "volumes", "chapters", "year", "synopsis", "published_on"} {
		if !hasField(series, key) {
			t.Errorf("series field %q missing", key)
		}
	}
	wantSeries := [][]string{{"volume", "chapter"}, {"chapter"}, {"volume"}}
	if !equalCombos(series.Progress.Combinations, wantSeries) || series.Progress.PrimaryKey != "chapter" ||
		!slices.Equal(series.Progress.Defaults, []string{"volume", "chapter"}) {
		t.Fatalf("series progress = %+v", series.Progress)
	}

	if len(m.Sources) != 3 {
		t.Fatalf("sources = %+v", m.Sources)
	}
	for id, wantKinds := range map[string][]string{
		SourceOpenLibrary: {KindBook},
		SourceGoogleBooks: {KindBook},
		SourceAniList:     {KindSeries},
	} {
		si, ok := sourceInfo(m, id)
		if !ok || !slices.Equal(si.Kinds, wantKinds) || si.RequestsPerMinute != 30 {
			t.Errorf("source %s = %+v ok=%v", id, si, ok)
		}
	}
	if si, _ := sourceInfo(m, SourceOpenLibrary); !slices.Equal(si.URLPatterns, []string{"openlibrary.org/works/{id}"}) {
		t.Errorf("open library patterns = %v", si.URLPatterns)
	}
	if si, _ := sourceInfo(m, SourceAniList); !slices.Equal(si.ExternalIDTypes, []string{"anilist", "mal"}) {
		t.Errorf("anilist ids = %v", si.ExternalIDTypes)
	}

	if len(m.Importers) != 3 {
		t.Fatalf("importers = %+v", m.Importers)
	}
	goodreads := m.Importers[0]
	if goodreads.ID != ImporterGoodreads || goodreads.ExternalIDType != "isbn" ||
		goodreads.ResolveVia != SourceOpenLibrary || !slices.Equal(goodreads.ResolveViaFallback, []string{SourceGoogleBooks}) ||
		!goodreads.ManualRows {
		t.Fatalf("goodreads = %+v", goodreads)
	}
	anilist := m.Importers[1]
	if anilist.ID != ImporterAniList || anilist.ExternalIDType != "anilist" || anilist.ResolveVia != SourceAniList {
		t.Fatalf("anilist = %+v", anilist)
	}
	wantYamtrack := plugin.ImporterInfo{
		ID:                ImporterYamtrack,
		Name:              "Yamtrack Novel CSV",
		FileHint:          "Upload the Yamtrack general CSV. Only manga rows that AniList reports as novels are imported into Series.",
		Accept:            []string{".csv"},
		ExternalIDType:    "mal",
		ExternalIDLabel:   "MyAnimeList",
		ResolveVia:        SourceAniList,
		ManualRows:        false,
		FallbackTitleLang: "ja-latn",
		UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
	}
	if yamtrack := m.Importers[2]; !reflect.DeepEqual(yamtrack, wantYamtrack) {
		t.Fatalf("yamtrack = %+v, want %+v", yamtrack, wantYamtrack)
	}
	if len(m.Exporters) != 1 || m.Exporters[0].ID != ExporterJSON {
		t.Fatalf("exporters = %+v", m.Exporters)
	}
}

func hasField(k plugin.KindDef, key string) bool {
	for _, f := range k.Fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

func sourceInfo(m plugin.Manifest, id string) (plugin.SourceInfo, bool) {
	for _, si := range m.Sources {
		if si.ID == id {
			return si, true
		}
	}
	return plugin.SourceInfo{}, false
}

func equalCombos(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestBooksKindStats(t *testing.T) {
	m := New(Options{}).Manifest()
	wantBook := &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "pages", LabelPlural: "Pages"}}
	if !reflect.DeepEqual(m.Kinds[0].Stats, wantBook) {
		t.Fatalf("book stats = %+v", m.Kinds[0].Stats)
	}
	if m.Kinds[1].Stats != nil {
		t.Fatalf("series stats = %+v, want nil", m.Kinds[1].Stats)
	}
}
