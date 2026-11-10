package plugin_test

import (
	"math"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestValidateAcceptsFixtures(t *testing.T) {
	for _, p := range []plugin.Plugin{plugintest.BookPlugin(), plugintest.TVPlugin()} {
		if err := plugin.Validate(p.Manifest()); err != nil {
			t.Errorf("%s: %v", p.Manifest().ID, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		base func() plugin.Manifest
		mod  func(m *plugin.Manifest)
		want string // substring of the error
	}{
		{"duplicate field key", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Fields = append(m.Kinds[0].Fields, plugin.FieldDef{Key: "year", Label: "Y2", Type: plugin.FieldNumber})
		}, `kinds[0].fields[3].key: duplicate "year"`},
		{"duplicate progress key", tv, func(m *plugin.Manifest) {
			e := &m.Kinds[0].Progress.Eligible
			*e = append(*e, plugin.ProgressDef{Key: "season", Type: plugin.Count, Label: "S2"})
		}, `kinds[0].progress.eligible[3].key: duplicate "season"`},
		{"unknown progress type", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Eligible[0].Type = "stars"
		}, `kinds[0].progress.eligible[0].type: unknown progress type "stars"`},
		{"total field missing", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Eligible[1].TotalField = "nope"
		}, `kinds[0].progress.eligible[1].total_field: no field "nope"`},
		{"total field not a number", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Eligible[1].TotalField = "type"
		}, `kinds[0].progress.eligible[1].total_field: field "type" is not a number field`},
		{"enum without values", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Fields[0].EnumValues = nil
		}, `kinds[0].fields[0].enum_values: enum field needs at least one value`},
		{"default not eligible", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Defaults = []string{"chapter"}
		}, `kinds[0].progress.defaults: "chapter" is not an eligible progress key`},
		{"combination uses ineligible key", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Combinations = append(m.Kinds[0].Progress.Combinations, []string{"episode", "chapter"})
		}, `kinds[0].progress.combinations[3]: "chapter" is not an eligible progress key`},
		{"default not an allowed combination", tv, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.Defaults = []string{"season"}
		}, `kinds[0].progress.defaults: not one of the allowed combinations`},
		{"primary key of type percent", book, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.PrimaryKey = "percent"
		}, `kinds[0].progress.primary_key: "percent" must be a count or pages progress`},
		{"primary key unknown", book, func(m *plugin.Manifest) {
			m.Kinds[0].Progress.PrimaryKey = "zzz"
		}, `kinds[0].progress.primary_key: "zzz" is not an eligible progress key`},
		{"section field missing", book, func(m *plugin.Manifest) {
			m.Kinds[0].Sections[0].Field = "nope"
		}, `kinds[0].sections[0].field: no field "nope"`},
		{"genres section without genres", book, func(m *plugin.Manifest) {
			m.Kinds[0].Genres.Enabled = false
		}, `kinds[0].sections[3]: genres section requires genres to be enabled`},
		{"tags section without tags", book, func(m *plugin.Manifest) {
			m.Kinds[0].Sections = append(m.Kinds[0].Sections, plugin.SectionDef{Type: plugin.SectionTags, Label: "Tags"})
		}, `tags section requires tags to be enabled`},
		{"unknown section type", book, func(m *plugin.Manifest) {
			m.Kinds[0].Sections[0].Type = "iframe"
		}, `kinds[0].sections[0].type: unknown section type "iframe"`},
		{"empty title languages", tv, func(m *plugin.Manifest) {
			m.Kinds[0].TitleLanguages = nil
		}, `kinds[0].title_languages: at least one language required`},
		{"importer resolve_via unknown", book, func(m *plugin.Manifest) {
			m.Importers[0].ResolveVia = "ghost"
		}, `importers[0].resolve_via: unknown source "ghost"`},
		{"contract major 2", tv, func(m *plugin.Manifest) {
			m.ContractVersion = 2
		}, `contract_version: plugin speaks contract 2, core speaks 1`},
		{"reserved id", tv, func(m *plugin.Manifest) {
			m.ID = "settings"
		}, `id: "settings" is reserved`},
		{"bad id slug uppercase", tv, func(m *plugin.Manifest) { m.ID = "Anime" }, `id: "Anime" is not a valid slug`},
		{"bad id slug leading digit", tv, func(m *plugin.Manifest) { m.ID = "1tv" }, `is not a valid slug`},
		{"bad id slug underscore", tv, func(m *plugin.Manifest) { m.ID = "my_tv" }, `is not a valid slug`},
		{"bad id slug too long", tv, func(m *plugin.Manifest) { m.ID = strings.Repeat("a", 33) }, `is not a valid slug`},
		{"empty id", tv, func(m *plugin.Manifest) { m.ID = "" }, `id: "" is not a valid slug`},
		{"no kinds", tv, func(m *plugin.Manifest) { m.Kinds = nil }, `kinds: at least one kind required`},
		{"duplicate kind", tv, func(m *plugin.Manifest) { m.Kinds = append(m.Kinds, m.Kinds[0]) }, `kinds[1].key: duplicate "show"`},
		{"missing status label", tv, func(m *plugin.Manifest) { m.Kinds[0].Statuses.Paused = "" }, `kinds[0].statuses.paused: label required`},
		{"unknown field type", tv, func(m *plugin.Manifest) { m.Kinds[0].Fields[1].Type = "blob" }, `kinds[0].fields[1].type: unknown field type "blob"`},
		{"min is NaN", tv, func(m *plugin.Manifest) {
			n := math.NaN()
			m.Kinds[0].Fields[1].Min = &n
		}, `kinds[0].fields[1].min: must be a finite number`},
		{"max is Inf", tv, func(m *plugin.Manifest) {
			n := math.Inf(1)
			m.Kinds[0].Fields[1].Max = &n
		}, `kinds[0].fields[1].max: must be a finite number`},
		{"min above max", tv, func(m *plugin.Manifest) {
			lo, hi := 5.0, 1.0
			m.Kinds[0].Fields[1].Min, m.Kinds[0].Fields[1].Max = &lo, &hi
		}, `kinds[0].fields[1]: min 5 is above max 1`},
		{"duplicate source", book, func(m *plugin.Manifest) { m.Sources = append(m.Sources, m.Sources[0]) }, `sources[1].id: duplicate`},
		{"source without rate limit", book, func(m *plugin.Manifest) { m.Sources[0].RequestsPerMinute = 0 }, `sources[0].requests_per_minute: must be positive`},
		{"url pattern without id", book, func(m *plugin.Manifest) { m.Sources[0].URLPatterns = []string{"example.com/x"} }, `sources[0].url_patterns[0]: must contain "{id}"`},
		{"duplicate importer", book, func(m *plugin.Manifest) { m.Importers = append(m.Importers, m.Importers[0]) }, `importers[1].id: duplicate`},
		{"exporter without extension", book, func(m *plugin.Manifest) {
			m.Exporters = []plugin.ExporterInfo{{ID: "j", Name: "J", MIME: "application/json"}}
		}, `exporters[0].extension: required`},
		{"unit with a control character", tv, func(m *plugin.Manifest) { m.Kinds[0].Fields[1].Unit = "ep\ns" },
			`kinds[0].fields[1].unit: must be a single line without control characters`},
		{"unit too long", tv, func(m *plugin.Manifest) { m.Kinds[0].Fields[1].Unit = strings.Repeat("é", 21) },
			`kinds[0].fields[1].unit: longer than 20 characters`},
		{"unit on a text field", book, func(m *plugin.Manifest) { m.Kinds[0].Fields[0].Unit = "x" },
			`kinds[0].fields[0].unit: only number fields take a unit`},
		{"progress total on percent", book, func(m *plugin.Manifest) { m.Kinds[0].Progress.Eligible[1].TotalField = "pages" }, `kinds[0].progress.eligible[1].total_field: percent progress has no total`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.base()
			tc.mod(&m)
			err := plugin.Validate(m)
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsUnit(t *testing.T) {
	m := tv()
	m.Kinds[0].Fields[1].Unit = strings.Repeat("é", 20)
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	m := tv()
	m.Kinds[0].TitleLanguages = nil
	m.Kinds[0].Progress.Defaults = []string{"chapter"}
	err := plugin.Validate(m)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"title_languages", "defaults"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q misses %q", err, want)
		}
	}
}

func TestValidateAcceptsKindWithoutProgress(t *testing.T) {
	m := book()
	m.Kinds[0].Progress = plugin.ProgressSpec{}
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
}

func TestProgressTypesAreClosedEnum(t *testing.T) {
	got := plugin.ProgressTypes()
	want := []plugin.ProgressType{plugin.Count, plugin.Pages, plugin.Percent, plugin.Duration}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || !plugin.ValidProgressType(want[i]) {
			t.Errorf("type %d: got %q", i, got[i])
		}
	}
	for _, bad := range []plugin.ProgressType{"", "stars", "Count", "chapters"} {
		if plugin.ValidProgressType(bad) {
			t.Errorf("%q must not be valid", bad)
		}
		m := tv()
		m.Kinds[0].Progress.Eligible[0].Type = bad
		if err := plugin.Validate(m); err == nil || !strings.Contains(err.Error(), "unknown progress type") {
			t.Errorf("manifest with type %q: %v", bad, err)
		}
	}
	if got := plugin.StatusClasses(); len(got) != 5 {
		t.Errorf("status classes: %v", got)
	}
}

func tv() plugin.Manifest   { return plugintest.TVPlugin().Manifest() }
func book() plugin.Manifest { return plugintest.BookPlugin().Manifest() }

// URL patterns follow the matcher's rules; a pattern the matcher would skip
// is refused with its path.
func TestValidateURLPatterns(t *testing.T) {
	for pat, want := range map[string]string{
		"books.example/work/{id}":         "",
		"www.books.example/w/{id}/info":   "",
		"books.example/x":                 `must contain "{id}"`,
		"books.example/{id}/{id}":         `must contain "{id}"`,
		"{id}.example/work/1":             "host",
		"books.example:8080/work/{id}":    "host",
		"user@books.example/work/{id}":    "host",
		"/work/{id}":                      "host",
		"https://books.example/work/{id}": "scheme",
		"//books.example/work/{id}":       "scheme",
		"books.example/work/{id}?x=1":     "query",
		"books.example/work/{id}#x":       "query",
		"books.example/work//{id}":        "empty path segment",
		"books.example/work/x{id}":        "{id}",
		"books.example/wo rk/{id}":        "space",
		"books.example\\work/{id}":        "space",
	} {
		m := book()
		m.Sources[0].URLPatterns = []string{"books.example/ok/{id}", pat}
		err := plugin.Validate(m)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", pat, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "sources[0].url_patterns[1]: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want sources[0].url_patterns[1] and %q", pat, err, want)
		}
	}
}

func TestValidateImporterExternalIDLabel(t *testing.T) {
	for label, want := range map[string]string{
		"":                      "",
		"MyAnimeList":           "",
		"My\nList":              `importers[0].external_id_label: must be a single line without control characters`,
		strings.Repeat("é", 41): `importers[0].external_id_label: longer than 40 characters`,
	} {
		m := book()
		m.Importers[0].ExternalIDLabel = label
		err := plugin.Validate(m)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("%q: %v, want %q", label, err, want)
		}
	}
}

func TestValidateImporterUnmatchedNote(t *testing.T) {
	for note, want := range map[string]string{
		"":                       "",
		"No match for #{id}.":    "",
		"No match yet.":          "", // {id} is optional
		strings.Repeat("é", 200): "",
		"Two\nlines {id}":        `importers[0].unmatched_note: must be a single line without control characters`,
		"tab\there":              `importers[0].unmatched_note: must be a single line without control characters`,
		"bad \xff utf-8":         `importers[0].unmatched_note: must be a single line without control characters`,
		strings.Repeat("é", 201): `importers[0].unmatched_note: longer than 200 characters`,
	} {
		m := book()
		m.Importers[0].UnmatchedNote = note
		err := plugin.Validate(m)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("%q: %v, want %q", note, err, want)
		}
	}
}

// Source ids name the first segment after the plugin prefix (/{plugin}/{source}/...),
// so the core's own segments there cannot be source ids.
func TestValidateReservedSourceIDs(t *testing.T) {
	for _, id := range []string{"manual", "export", "search"} {
		m := book()
		m.Sources[0].ID = id
		m.Importers[0].ResolveVia = id
		err := plugin.Validate(m)
		want := `sources[0].id: "` + id + `" is reserved by the core`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("source id %q: %v, want %q", id, err, want)
		}
	}
	for _, id := range []string{"manuals", "searcher", "exports", "books"} {
		m := book()
		m.Sources[0].ID = id
		m.Importers[0].ResolveVia = id
		if err := plugin.Validate(m); err != nil {
			t.Errorf("source id %q: %v", id, err)
		}
	}
	// only source ids: an importer or exporter may be called "export"
	m := book()
	m.Importers[0].ID = "search"
	m.Exporters = []plugin.ExporterInfo{{ID: "export", Name: "E", Extension: ".json", MIME: "application/json"}}
	if err := plugin.Validate(m); err != nil {
		t.Errorf("importer/exporter ids: %v", err)
	}
}

// An importer that resolves through a source must name an id type that
// source can look up; otherwise every row of every import would fail.
func TestValidateImporterLookupConfiguration(t *testing.T) {
	for name, c := range map[string]struct {
		mod  func(m *plugin.Manifest)
		want string // "" means valid
	}{
		"fixture as is":   {func(*plugin.Manifest) {}, ""},
		"manual-only":     {func(m *plugin.Manifest) { m.Importers[0].ResolveVia, m.Importers[0].ExternalIDType = "", "" }, ""},
		"manual-only, id": {func(m *plugin.Manifest) { m.Importers[0].ResolveVia = "" }, ""},
		"no id type": {func(m *plugin.Manifest) { m.Importers[0].ExternalIDType = "" },
			`importers[0].external_id_type: required when resolve_via is set`},
		"id type the source does not know": {func(m *plugin.Manifest) { m.Importers[0].ExternalIDType = "asin" },
			`importers[0].external_id_type: source "books" cannot look up "asin" ids`},
		"source without lookup": {func(m *plugin.Manifest) { m.Sources[0].SupportsLookup = false },
			`importers[0].resolve_via: source "books" does not support lookup`},
		"source without id types": {func(m *plugin.Manifest) { m.Sources[0].ExternalIDTypes = nil },
			`importers[0].external_id_type: source "books" cannot look up "isbn" ids`},
	} {
		t.Run(name, func(t *testing.T) {
			m := book()
			c.mod(&m)
			err := plugin.Validate(m)
			if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}
