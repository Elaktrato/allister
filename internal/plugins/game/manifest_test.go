package game

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestIDs(t *testing.T) {
	if PluginID != "game" || KindGame != "game" ||
		SourceIGDB != "igdb" || SourceSteam != "steam" ||
		ImporterYamtrack != "yamtrack-game" || ImporterSteam != "steam-account" ||
		ExporterJSON != "json" || exportFormat != "allister.game.export" {
		t.Fatalf("ids = %q %q %q %q %q %q %q %q",
			PluginID, KindGame, SourceIGDB, SourceSteam, ImporterYamtrack, ImporterSteam, ExporterJSON, exportFormat)
	}
}

func TestManifest(t *testing.T) {
	m := manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
	if m.ID != PluginID || m.Name != "Games" || m.DefaultEnabled || m.Icon != "sports_esports" ||
		len(m.Kinds) != 1 || m.Kinds[0].Key != KindGame {
		t.Fatalf("%+v", m)
	}
	k := m.Kinds[0]
	if k.Label != "Game" || k.LabelPlural != "Games" {
		t.Fatalf("kind labels = %q %q", k.Label, k.LabelPlural)
	}
	if !k.Genres.Enabled || !k.Tags.Enabled || len(k.Sections) != 7 {
		t.Fatalf("%+v", k)
	}
	if !slices.Equal(k.Progress.Defaults, []string{"playtime"}) || k.Progress.PrimaryKey != "" {
		t.Fatalf("progress = %+v", k.Progress)
	}
	if len(m.Sources) != 2 || m.Sources[0].ID != SourceIGDB || m.Sources[1].ID != SourceSteam {
		t.Fatalf("sources = %+v", m.Sources)
	}
	if len(m.Importers) != 2 || m.Importers[0].ID != ImporterYamtrack || m.Importers[1].ID != ImporterSteam {
		t.Fatalf("importers = %+v", m.Importers)
	}
	if len(m.Exporters) != 1 || m.Exporters[0].ID != ExporterJSON {
		t.Fatalf("exporters = %+v", m.Exporters)
	}
}

func TestGameKindFields(t *testing.T) {
	k := gameKind()
	want := []plugin.FieldDef{
		{Key: "year", Label: "Year", Type: plugin.FieldNumber, Min: num(1950), Max: num(2100), Show: true, Editable: true, Filterable: true, Sortable: true},
		{Key: "released_on", Label: "Released", Type: plugin.FieldDate, Show: true, Editable: true},
		{Key: "platforms", Label: "Platforms", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "developer", Label: "Developer", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "time_main", Label: "Main story", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
		{Key: "time_extra", Label: "Main + extras", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
		{Key: "time_complete", Label: "Completionist", Type: plugin.FieldNumber, Unit: "min", Show: true, Editable: true, Sortable: true},
		{Key: "summary", Label: "Summary", Type: plugin.FieldText, Editable: true},
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

func TestGameKindStatusesProgressAndTerms(t *testing.T) {
	k := gameKind()
	wantStatuses := plugin.StatusLabels{
		Planned: "Want to play", Active: "Playing", Paused: "Paused", Completed: "Played", Dropped: "Dropped",
	}
	if k.Statuses != wantStatuses {
		t.Fatalf("statuses = %+v", k.Statuses)
	}
	wantProgress := plugin.ProgressSpec{
		Eligible:     []plugin.ProgressDef{{Key: "playtime", Type: plugin.Duration, Label: "Playtime"}},
		Defaults:     []string{"playtime"},
		Combinations: [][]string{{"playtime"}},
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

func TestGameKindSectionsAndTitles(t *testing.T) {
	k := gameKind()
	wantTypes := []plugin.SectionType{
		plugin.SectionInfo, plugin.SectionText, plugin.SectionTitles,
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
	if k.Sections[0].Label != "Details" || k.Sections[1].Field != "summary" || k.Sections[1].Label != "Summary" {
		t.Errorf("info/text sections = %+v %+v", k.Sections[0], k.Sections[1])
	}
	if !slices.Equal(k.TitleLanguages, []string{"en"}) || !slices.Equal(k.TitleFallback, []string{"en"}) {
		t.Errorf("titles = %+v fallback %+v", k.TitleLanguages, k.TitleFallback)
	}
}

func TestGameKindColumns(t *testing.T) {
	k := gameKind()
	var defaults, optional []string
	for _, c := range k.Columns {
		if c.Default {
			defaults = append(defaults, c.Key)
		} else {
			optional = append(optional, c.Key)
		}
	}
	if !slices.Equal(defaults, []string{"year", plugin.ColumnProgress, plugin.ColumnRating, plugin.ColumnStatus, plugin.ColumnUpdated}) {
		t.Fatalf("default columns = %+v", defaults)
	}
	if !slices.Equal(optional, []string{"platforms", "developer", "publisher", "released_on", "time_main", "time_extra", "time_complete"}) {
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

func TestManifestIGDBSource(t *testing.T) {
	m := manifest()
	want := plugin.SourceInfo{
		ID:                SourceIGDB,
		Name:              "IGDB",
		RequestsPerMinute: 60,
		ExternalIDTypes:   []string{"igdb", "steam"},
		SupportsLookup:    true,
		Kinds:             []string{KindGame},
		LinkLabel:         "View on IGDB",
		URLPatterns:       []string{"igdb.com/games/{id}"},
		KeyLabel:          "Twitch app credentials",
		KeyHelp:           "Create a free Twitch application at dev.twitch.tv/console/apps.",
		KeyFields: []plugin.KeyField{
			{Name: "client_id", Label: "Client ID"},
			{Name: "client_secret", Label: "Client Secret", Secret: true},
		},
	}
	if !reflect.DeepEqual(m.Sources[0], want) {
		t.Fatalf("source = %+v", m.Sources[0])
	}
}

func TestManifestSteamSource(t *testing.T) {
	m := manifest()
	want := plugin.SourceInfo{
		ID:                SourceSteam,
		Name:              "Steam",
		RequestsPerMinute: 60,
		ExternalIDTypes:   []string{"steam"},
		SupportsLookup:    true,
		Kinds:             []string{KindGame},
		LinkLabel:         "View on Steam",
		URLPatterns:       []string{"store.steampowered.com/app/{id}"},
		KeyLabel:          "Steam Web API key",
		KeyHelp:           "Create one at steamcommunity.com/dev/apikey; used for the Steam account import.",
	}
	if !reflect.DeepEqual(m.Sources[1], want) {
		t.Fatalf("source = %+v", m.Sources[1])
	}
	for _, s := range m.Sources {
		if s.SupportsSchedule {
			t.Errorf("source %q declares a schedule", s.ID)
		}
	}
}

func TestManifestImportersAndExporter(t *testing.T) {
	m := manifest()
	if len(m.Importers) != 2 {
		t.Fatalf("importers = %+v", m.Importers)
	}
	wantYamtrack := plugin.ImporterInfo{
		ID:                ImporterYamtrack,
		Name:              "Yamtrack Game CSV",
		Accept:            []string{".csv"},
		ExternalIDType:    "igdb",
		ExternalIDLabel:   "IGDB",
		ResolveVia:        SourceIGDB,
		ManualRows:        true,
		FallbackTitleLang: "en",
		UnmatchedNote:     "Imported from Yamtrack; IGDB had no match for #{id}.",
	}
	wantSteam := plugin.ImporterInfo{
		ID:                 ImporterSteam,
		Name:               "Steam account",
		AccountLabel:       "SteamID64",
		ExternalIDType:     "steam",
		ExternalIDLabel:    "Steam AppID",
		ResolveVia:         SourceIGDB,
		ResolveViaFallback: []string{SourceSteam},
		ManualRows:         true,
		FallbackTitleLang:  "en",
		UnmatchedNote:      "Imported from Steam; IGDB and the Steam store had no match for #{id}.",
	}
	for _, c := range []struct {
		idx  int
		want plugin.ImporterInfo
	}{{0, wantYamtrack}, {1, wantSteam}} {
		got := m.Importers[c.idx]
		if got.FileHint == "" && got.AccountLabel == "" {
			t.Errorf("importer %q has neither a file hint nor an account label", got.ID)
		}
		got.FileHint = ""
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("importer = %+v, want %+v", m.Importers[c.idx], c.want)
		}
	}
	if !strings.Contains(m.Importers[1].AccountLabel, "SteamID64") {
		t.Errorf("account label = %q", m.Importers[1].AccountLabel)
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

func TestGameKindStats(t *testing.T) {
	k := gameKind()
	want := &plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Playtime", DurationKey: "playtime"}}
	if !reflect.DeepEqual(k.Stats, want) {
		t.Fatalf("stats = %+v", k.Stats)
	}
}
