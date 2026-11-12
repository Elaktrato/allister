package movie

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestIDs(t *testing.T) {
	if PluginID != "movie" || KindMovie != "movie" || SourceTMDB != "tmdb" ||
		ImporterYamtrack != "yamtrack-movie" || ExporterJSON != "json" ||
		exportFormat != "allister.movie.export" {
		t.Fatalf("ids = %q %q %q %q %q %q", PluginID, KindMovie, SourceTMDB, ImporterYamtrack, ExporterJSON, exportFormat)
	}
}

func TestManifest(t *testing.T) {
	m := manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
	if m.ID != PluginID || m.Name != "Movies" || m.DefaultEnabled || m.Icon != "movie" || len(m.Kinds) != 1 || m.Kinds[0].Key != KindMovie {
		t.Fatalf("%+v", m)
	}
	k := m.Kinds[0]
	if k.Label != "Movie" || k.LabelPlural != "Movies" {
		t.Fatalf("kind labels = %q %q", k.Label, k.LabelPlural)
	}
	if !k.Genres.Enabled || !k.Tags.Enabled || len(k.Sections) != 8 {
		t.Fatalf("%+v", k)
	}
	if !slices.Equal(k.Progress.Defaults, []string{"position"}) || k.Progress.PrimaryKey != "" {
		t.Fatalf("progress = %+v", k.Progress)
	}
	if len(m.Sources) != 1 || m.Sources[0].ID != SourceTMDB || m.Sources[0].KeyLabel == "" || m.Sources[0].SupportsSchedule {
		t.Fatalf("sources = %+v", m.Sources)
	}
	if m.Importers[0].ID != ImporterYamtrack || m.Importers[0].ResolveVia != SourceTMDB || !strings.Contains(m.Importers[0].UnmatchedNote, "could not resolve") {
		t.Fatalf("importer = %+v", m.Importers[0])
	}
	if len(m.Exporters) != 1 || m.Exporters[0].ID != ExporterJSON {
		t.Fatalf("exporters = %+v", m.Exporters)
	}
}

func TestMovieKindFields(t *testing.T) {
	k := movieKind()
	want := []plugin.FieldDef{
		{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1900), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
		{Key: "runtime", Label: "Runtime", Type: plugin.FieldNumber, Min: num(1), Max: num(10000), Unit: "min", Show: true, Editable: true},
		{Key: "director", Label: "Director", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "studio", Label: "Studio", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "country", Label: "Country", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "language", Label: "Language", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "collection", Label: "Collection", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "release_status", Label: "Release status", Type: plugin.FieldEnum, EnumValues: []string{"released", "upcoming", "other"}, Show: true, Editable: true, Filterable: true},
		{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Editable: true},
		{Key: "released_on", Label: "Released", Type: plugin.FieldDate, Show: true, Editable: true},
	}
	if len(k.Fields) != len(want) {
		t.Fatalf("fields = %+v", k.Fields)
	}
	for _, w := range want {
		f, ok := fieldByKey(k.Fields, w.Key)
		if !ok {
			t.Fatalf("missing field %q", w.Key)
		}
		if !reflect.DeepEqual(f, w) {
			t.Errorf("field %q = %+v, want %+v", w.Key, f, w)
		}
	}
}

func TestMovieKindStatusesProgressAndTerms(t *testing.T) {
	k := movieKind()
	wantStatuses := plugin.StatusLabels{
		Planned: "Want to watch", Active: "Watching", Paused: "Paused", Completed: "Seen", Dropped: "Dropped",
	}
	if k.Statuses != wantStatuses {
		t.Fatalf("statuses = %+v", k.Statuses)
	}
	wantProgress := plugin.ProgressSpec{
		Eligible:     []plugin.ProgressDef{{Key: "position", Type: plugin.Duration, Label: "Position"}},
		Defaults:     []string{"position"},
		Combinations: [][]string{{"position"}},
	}
	if !reflect.DeepEqual(k.Progress, wantProgress) {
		t.Fatalf("progress = %+v", k.Progress)
	}
	for name, term := range map[string]plugin.TermDef{"genres": k.Genres, "tags": k.Tags} {
		if !term.Enabled || !term.Editable || !term.Filterable || term.Label == "" {
			t.Errorf("%s = %+v", name, term)
		}
	}
}

func TestMovieKindSectionsAndTitles(t *testing.T) {
	k := movieKind()
	wantTypes := []plugin.SectionType{
		plugin.SectionInfo, plugin.SectionText, plugin.SectionTitles, plugin.SectionPeople,
		plugin.SectionGenres, plugin.SectionTags, plugin.SectionRecommendations, plugin.SectionLinks,
	}
	if len(k.Sections) != len(wantTypes) {
		t.Fatalf("sections = %+v", k.Sections)
	}
	for i, want := range wantTypes {
		if k.Sections[i].Type != want {
			t.Errorf("section[%d] = %q, want %q", i, k.Sections[i].Type, want)
		}
	}
	if k.Sections[0].Label != "Details" || k.Sections[1].Field != "synopsis" || k.Sections[1].Label != "Summary" {
		t.Errorf("info/text sections = %+v %+v", k.Sections[0], k.Sections[1])
	}
	if !slices.Equal(k.Sections[3].Roles, []string{"actor", "director", "writer"}) {
		t.Errorf("people roles = %+v", k.Sections[3].Roles)
	}
	if !slices.Equal(k.TitleLanguages, []string{"en", "original"}) || !slices.Equal(k.TitleFallback, []string{"en"}) {
		t.Errorf("titles = %+v fallback %+v", k.TitleLanguages, k.TitleFallback)
	}
}

func TestMovieKindColumns(t *testing.T) {
	k := movieKind()
	var defaults, optional []string
	for _, c := range k.Columns {
		if c.Default {
			defaults = append(defaults, c.Key)
		} else {
			optional = append(optional, c.Key)
		}
	}
	if !slices.Equal(defaults, []string{"year", plugin.ColumnRating, plugin.ColumnStatus, plugin.ColumnUpdated}) {
		t.Fatalf("default columns = %+v", defaults)
	}
	if !slices.Equal(optional, []string{plugin.ColumnProgress, "runtime", "director", "studio", "country", "language", "collection", "released_on"}) {
		t.Fatalf("optional columns = %+v", optional)
	}
	for _, c := range k.Columns {
		switch c.Key {
		case "year":
			if c.Hide != "sm" {
				t.Errorf("year hide = %q", c.Hide)
			}
		case plugin.ColumnUpdated:
			if c.Hide != "lg" {
				t.Errorf("updated hide = %q", c.Hide)
			}
		case plugin.ColumnRating:
			if c.Label != "Score" {
				t.Errorf("rating label = %q", c.Label)
			}
		}
	}
}

func TestManifestTMDB(t *testing.T) {
	m := manifest()
	if len(m.Sources) != 1 {
		t.Fatalf("sources = %+v", m.Sources)
	}
	want := plugin.SourceInfo{
		ID:                SourceTMDB,
		Name:              "TMDB",
		RequestsPerMinute: 30,
		ExternalIDTypes:   []string{"tmdb", "imdb"},
		SupportsLookup:    true,
		Kinds:             []string{KindMovie},
		LinkLabel:         "View on TMDB",
		URLPatterns:       []string{"themoviedb.org/movie/{id}"},
		KeyLabel:          "API key",
		KeyHelp:           "Create a free key at themoviedb.org under Settings → API (v3 key or v4 read token).",
	}
	if !reflect.DeepEqual(m.Sources[0], want) {
		t.Fatalf("source = %+v", m.Sources[0])
	}
}

func TestManifestImporterAndExporter(t *testing.T) {
	m := manifest()
	if len(m.Importers) != 1 {
		t.Fatalf("importers = %+v", m.Importers)
	}
	wantImporter := plugin.ImporterInfo{
		ID:                ImporterYamtrack,
		Name:              "Yamtrack Movie CSV",
		Accept:            []string{".csv"},
		ExternalIDType:    "tmdb",
		ExternalIDLabel:   "TMDB",
		ResolveVia:        SourceTMDB,
		ManualRows:        true,
		FallbackTitleLang: "en",
		UnmatchedNote:     "Imported from Yamtrack; TMDB could not resolve #{id}.",
	}
	got := m.Importers[0]
	if got.FileHint == "" {
		t.Fatalf("importer file hint is empty")
	}
	got.FileHint = ""
	if !reflect.DeepEqual(got, wantImporter) {
		t.Fatalf("importer = %+v", m.Importers[0])
	}
	if len(m.Exporters) != 1 {
		t.Fatalf("exporters = %+v", m.Exporters)
	}
	wantExporter := plugin.ExporterInfo{ID: ExporterJSON, Name: "JSON", Extension: ".json", MIME: "application/json"}
	if !reflect.DeepEqual(m.Exporters[0], wantExporter) {
		t.Fatalf("exporter = %+v", m.Exporters[0])
	}
}

func fieldByKey(fields []plugin.FieldDef, key string) (plugin.FieldDef, bool) {
	for _, f := range fields {
		if f.Key == key {
			return f, true
		}
	}
	return plugin.FieldDef{}, false
}

func TestMovieKindStats(t *testing.T) {
	k := movieKind()
	want := &plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Watchtime", CompletedField: "runtime", DurationKey: "position"}}
	if !reflect.DeepEqual(k.Stats, want) {
		t.Fatalf("stats = %+v", k.Stats)
	}
}
