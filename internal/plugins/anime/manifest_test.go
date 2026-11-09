package anime

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestManifestIsValid(t *testing.T) {
	if err := plugin.Validate(New(Options{}).Manifest()); err != nil {
		t.Fatal(err)
	}
}

func TestManifestMatchesBaseline(t *testing.T) {
	m := New(Options{}).Manifest()
	if m.ID != "anime" || m.Name != "Anime" || m.Version != "1.0.0" || m.ContractVersion != plugin.ContractVersion || !m.DefaultEnabled {
		t.Errorf("header = %+v", m)
	}
	if len(m.Kinds) != 1 {
		t.Fatalf("kinds = %d", len(m.Kinds))
	}
	k := m.Kinds[0]
	if k.Key != "anime" || k.Label != "Anime" || k.LabelPlural != "Anime" {
		t.Errorf("kind = %+v", k)
	}
	if !reflect.DeepEqual(k.TitleLanguages, []string{"ja", "ja-latn", "en"}) {
		t.Errorf("title languages = %v", k.TitleLanguages)
	}
	if want := (plugin.StatusLabels{Planned: "Plan to watch", Active: "Watching", Paused: "Paused", Completed: "Completed", Dropped: "Dropped"}); k.Statuses != want {
		t.Errorf("statuses = %+v", k.Statuses)
	}
	if want := (plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true}); k.Genres != want {
		t.Errorf("genres = %+v", k.Genres)
	}
	if want := (plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true}); k.Tags != want {
		t.Errorf("tags = %+v", k.Tags)
	}
	f := map[string]plugin.FieldDef{}
	var keys []string
	for _, d := range k.Fields {
		f[d.Key] = d
		keys = append(keys, d.Key)
	}
	if want := []string{"type", "episodes", "year", "studio", "synopsis", "source_material", "started_on", "ended_on"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("fields = %v", keys)
	}
	if ty := f["type"]; ty.Type != plugin.FieldEnum || ty.Label != "Type" ||
		!reflect.DeepEqual(ty.EnumValues, []string{"TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC"}) {
		t.Errorf("type = %+v", ty)
	}
	if ep := f["episodes"]; ep.Type != plugin.FieldNumber || ep.Min == nil || *ep.Min != 1 || ep.Max == nil || *ep.Max != 10000 {
		t.Errorf("episodes = %+v", ep)
	}
	if y := f["year"]; y.Type != plugin.FieldNumber || y.Min == nil || *y.Min != 1900 || y.Max == nil || *y.Max != 2100 {
		t.Errorf("year = %+v", y)
	}
	for _, d := range []struct {
		key                                  string
		typ                                  plugin.FieldType
		show, editable, filterable, sortable bool
		unit                                 string
		required                             bool
	}{
		// type is required: a manual entry must choose one (Task 10)
		{"type", plugin.FieldEnum, true, true, true, false, "", true},
		{"episodes", plugin.FieldNumber, true, true, false, false, "eps", false},
		{"year", plugin.FieldNumber, true, true, true, true, "", false},
		{"studio", plugin.FieldText, true, true, true, false, "", false},
		{"synopsis", plugin.FieldText, false, true, false, false, "", false},
		{"source_material", plugin.FieldText, true, true, false, false, "", false},
		{"started_on", plugin.FieldDate, true, true, false, false, "", false},
		{"ended_on", plugin.FieldDate, true, true, false, false, "", false},
	} {
		g := f[d.key]
		if g.Type != d.typ || g.Show != d.show || g.Editable != d.editable || g.Filterable != d.filterable || g.Sortable != d.sortable || g.Required != d.required || g.Unit != d.unit {
			t.Errorf("%s flags = %+v, want %+v", d.key, g, d)
		}
	}
	wantProgress := plugin.ProgressSpec{
		Eligible:     []plugin.ProgressDef{{Key: "episode", Type: plugin.Count, Label: "Episode", TotalField: "episodes"}},
		Defaults:     []string{"episode"},
		Combinations: [][]string{{"episode"}},
		PrimaryKey:   "episode",
	}
	if !reflect.DeepEqual(k.Progress, wantProgress) {
		t.Errorf("progress = %+v", k.Progress)
	}
	wantSections := []plugin.SectionDef{
		{Type: plugin.SectionInfo, Label: "Information"},
		{Type: plugin.SectionText, Field: "synopsis", Label: "Synopsis"},
		{Type: plugin.SectionTitles, Label: "Titles"},
		{Type: plugin.SectionGenres, Label: "Genres"},
		{Type: plugin.SectionTags, Label: "Tags"},
		{Type: plugin.SectionRelated, Label: "Related"},
		{Type: plugin.SectionPeople, Label: "Characters", Roles: []string{"character"}},
		{Type: plugin.SectionRecommendations, Label: "Recommendations"},
		{Type: plugin.SectionLinks},
	}
	if !reflect.DeepEqual(k.Sections, wantSections) {
		t.Errorf("sections = %+v", k.Sections)
	}
	wantSource := plugin.SourceInfo{ID: "anilist", Name: "AniList", RequestsPerMinute: 30, ExternalIDTypes: []string{"mal"},
		SupportsLookup: true, LinkLabel: "View on AniList", URLPatterns: []string{"anilist.co/anime/{id}"}}
	if len(m.Sources) != 1 || !reflect.DeepEqual(m.Sources[0], wantSource) {
		t.Errorf("sources = %+v", m.Sources)
	}
	if len(m.Importers) != 1 {
		t.Fatalf("importers = %+v", m.Importers)
	}
	im := m.Importers[0]
	if im.ID != "yamtrack" || im.Name != "Yamtrack CSV" || !reflect.DeepEqual(im.Accept, []string{".csv"}) || im.ExternalIDType != "mal" ||
		im.ResolveVia != "anilist" || im.FallbackTitleLang != "ja-latn" || im.FileHint == "" {
		t.Errorf("importer = %+v", im)
	}
	if want := (plugin.ExporterInfo{ID: "json", Name: "JSON", Extension: ".json", MIME: "application/json"}); len(m.Exporters) != 1 || m.Exporters[0] != want {
		t.Errorf("exporters = %+v", m.Exporters)
	}
}

func TestManifestIsFreshPerCall(t *testing.T) {
	p := New(Options{})
	m := p.Manifest()
	m.Kinds[0].Fields[0].EnumValues[0] = "X"
	m.Kinds[0].TitleLanguages[0] = "X"
	if p.Manifest().Kinds[0].Fields[0].EnumValues[0] != "TV" || p.Manifest().Kinds[0].TitleLanguages[0] != "ja" {
		t.Error("mutating a returned manifest must not leak into the next one")
	}
}

func TestPluginRegistersInRegistry(t *testing.T) {
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	if got, ok := reg.Get("anime"); !ok || got == nil {
		t.Fatal("anime not registered")
	}
	if !reg.IsEnabled("anime") {
		t.Error("anime must be enabled by default")
	}
	if len(reg.Problems()) != 0 {
		t.Errorf("problems = %v", reg.Problems())
	}
	if m, ok := reg.Manifest("anime"); !ok || m.Kinds[0].Key != "anime" {
		t.Errorf("manifest = %+v", m)
	}
}
