package plugin_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestErrNotConfigured(t *testing.T) {
	if plugin.ErrNotConfigured == nil || errors.Is(plugin.ErrNotConfigured, plugin.ErrNotFound) {
		t.Fatal("bad sentinel")
	}
}

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
		{"reserved id lists", tv, func(m *plugin.Manifest) {
			m.ID = "lists"
		}, `id: "lists" is reserved`},
		{"bad id slug uppercase", tv, func(m *plugin.Manifest) { m.ID = "Anime" }, `id: "Anime" is not a valid slug`},
		{"bad id slug leading digit", tv, func(m *plugin.Manifest) { m.ID = "1tv" }, `is not a valid slug`},
		{"bad id slug underscore", tv, func(m *plugin.Manifest) { m.ID = "my_tv" }, `is not a valid slug`},
		{"bad id slug too long", tv, func(m *plugin.Manifest) { m.ID = strings.Repeat("a", 33) }, `is not a valid slug`},
		{"key help without label", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyLabel = ""
			m.Sources[0].KeyHelp = "Get one at example.test."
		}, `sources[0].key_help: requires key_label`},
		{"key label with newline", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyLabel = "API\nkey"
		}, `sources[0].key_label: must be a single line without control characters`},
		{"key label too long", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyLabel = strings.Repeat("k", 41)
		}, `sources[0].key_label: longer than 40 characters`},
		{"key fields without group label", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyLabel = ""
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "client_id", Label: "Client ID"}}
		}, `sources[0].key_fields: key_label is the group label`},
		{"key field bad name", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "Client ID", Label: "Client ID"}}
		}, `sources[0].key_fields[0].name`},
		{"key field name leading digit", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "1id", Label: "ID"}}
		}, `sources[0].key_fields[0].name: "1id" is not a valid key`},
		{"key field name leading underscore", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "_id", Label: "ID"}}
		}, `sources[0].key_fields[0].name: "_id" is not a valid key`},
		{"key field invalid name repeated", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "Bad", Label: "A"}, {Name: "Bad", Label: "B"}}
		}, `sources[0].key_fields[1].name: "Bad" is not a valid key`},
		{"key field duplicate", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: "A"}, {Name: "id", Label: "B"}}
		}, `sources[0].key_fields[1].name: duplicate`},
		{"key field label too long", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: strings.Repeat("L", 41)}}
		}, `sources[0].key_fields[0].label: longer than 40 characters`},
		{"too many key fields", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{
				{Name: "a", Label: "A"}, {Name: "b", Label: "B"}, {Name: "c", Label: "C"},
				{Name: "d", Label: "D"}, {Name: "e", Label: "E"},
			}
		}, `sources[0].key_fields: at most 4 fields`},
		{"key field without label", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id"}}
		}, `sources[0].key_fields[0].label: required`},
		{"key field label with newline", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: "Client\nID"}}
		}, `sources[0].key_fields[0].label: must be a single line without control characters`},
		{"key help too long", tv, func(m *plugin.Manifest) {
			m.Sources[0].KeyLabel = "API key"
			m.Sources[0].KeyHelp = strings.Repeat("h", 201)
		}, `sources[0].key_help: longer than 200 characters`},
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
		{"unknown source kind", book, func(m *plugin.Manifest) { m.Sources[0].Kinds = []string{"nope"} }, `sources[0].kinds[0]: unknown kind "nope"`},
		{"duplicate source kind", book, func(m *plugin.Manifest) { m.Sources[0].Kinds = []string{"book", "book"} }, `sources[0].kinds[1]: duplicate "book"`},
		{"duplicate importer", book, func(m *plugin.Manifest) { m.Importers = append(m.Importers, m.Importers[0]) }, `importers[1].id: duplicate`},
		{"fallback unknown source", book, func(m *plugin.Manifest) { m.Importers[0].ResolveViaFallback = []string{"ghost"} }, `importers[0].resolve_via_fallback[0]: unknown source "ghost"`},
		{"fallback duplicates primary", book, func(m *plugin.Manifest) { m.Importers[0].ResolveViaFallback = []string{"books"} }, `importers[0].resolve_via_fallback[0]: duplicate source "books"`},
		{"fallback without primary", book, func(m *plugin.Manifest) {
			m.Importers[0].ResolveVia = ""
			m.Importers[0].ResolveViaFallback = []string{"books"}
		}, `importers[0].resolve_via_fallback: requires resolve_via`},
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

func TestValidateAcceptsKeyFields(t *testing.T) {
	m := plugintest.TVPlugin(plugintest.WithSourceKeyFields(
		plugin.KeyField{Name: "client_id", Label: "Client ID"},
		plugin.KeyField{Name: "client_secret", Label: "Client Secret", Secret: true},
	)).Manifest()
	if got := m.Sources[0].KeyFields; len(got) != 2 || !got[1].Secret {
		t.Fatalf("option did not set the fields: %+v", got)
	}
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
}

// TestValidateKeyFieldInvalidNameIsNotAlsoADuplicate: a repeated invalid
// field name is reported as invalid at each position, never also as a
// duplicate (only valid names are checked for duplicates).
func TestValidateKeyFieldInvalidNameIsNotAlsoADuplicate(t *testing.T) {
	m := tv()
	m.Sources[0].KeyFields = []plugin.KeyField{{Name: "Bad", Label: "A"}, {Name: "Bad", Label: "B"}}
	err := plugin.Validate(m)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`key_fields[0].name: "Bad" is not a valid key`, `key_fields[1].name: "Bad" is not a valid key`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("invalid names also reported as duplicates: %v", err)
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

// TestValidateIcon: the optional icon is an icon NAME (data only):
// lower-case letters, digits and "_", 1 to 40 characters; empty means the
// core's default icon.
func TestValidateIcon(t *testing.T) {
	for _, ok := range []string{"", "live_tv", "menu_book", "a", "3d_rotation", strings.Repeat("x", 40)} {
		m := tv()
		m.Icon = ok
		if err := plugin.Validate(m); err != nil {
			t.Errorf("icon %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"Live_tv", "live-tv", "live tv", strings.Repeat("x", 41), "<b>", "tv\n", "ä", " tv"} {
		m := tv()
		m.Icon = bad
		err := plugin.Validate(m)
		if err == nil || !strings.Contains(err.Error(), "icon: ") {
			t.Errorf("icon %q: err = %v", bad, err)
		}
	}
}

// TestValidateTitleFallback: a kind's optional title fallback order lists
// declared title languages, each once.
func TestValidateTitleFallback(t *testing.T) {
	for _, ok := range [][]string{nil, {"en"}, {"original", "en"}} {
		m := tv()
		m.Kinds[0].TitleFallback = ok
		if err := plugin.Validate(m); err != nil {
			t.Errorf("fallback %q refused: %v", ok, err)
		}
	}
	for _, c := range []struct {
		fallback []string
		want     string
	}{
		{[]string{"de"}, `kinds[0].title_fallback[0]: "de" is not one of the title languages`},
		{[]string{"en", "en"}, `kinds[0].title_fallback[1]: duplicate "en"`},
		{[]string{""}, `kinds[0].title_fallback[0]: "" is not one of the title languages`},
	} {
		m := tv()
		m.Kinds[0].TitleFallback = c.fallback
		err := plugin.Validate(m)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("fallback %q: err = %v, want %s", c.fallback, err, c.want)
		}
	}
}

// TestValidateColumns: a kind's optional table columns name its fields or
// the closed set of core columns, each once, with a bounded single-line
// label and a known screen-width hint; the title column is always shown and
// cannot be declared.
func TestValidateColumns(t *testing.T) {
	good := []plugin.ColumnDef{
		{Key: "type", Default: true, Hide: "sm"},
		{Key: "year", Label: "Released", Default: true, Hide: "md"},
		{Key: "episodes", Hide: "lg"},
		{Key: "progress", Default: true},
		{Key: "rating", Label: "Score", Default: true},
		{Key: "status", Default: true},
		{Key: "updated", Hide: "lg"},
	}
	m := tv()
	m.Kinds[0].Columns = good
	if err := plugin.Validate(m); err != nil {
		t.Fatalf("good columns refused: %v", err)
	}
	for _, c := range []struct {
		name string
		cols []plugin.ColumnDef
		mod  func(k *plugin.KindDef)
		want string
	}{
		{"unknown key", []plugin.ColumnDef{{Key: "studio"}}, nil, `kinds[0].columns[0].key: "studio" is neither a field of the kind nor a core column`},
		{"empty key", []plugin.ColumnDef{{Key: ""}}, nil, `kinds[0].columns[0].key: "" is neither a field`},
		{"duplicate", []plugin.ColumnDef{{Key: "year"}, {Key: "year"}}, nil, `kinds[0].columns[1].key: duplicate "year"`},
		{"title", []plugin.ColumnDef{{Key: "title"}}, nil, `kinds[0].columns[0].key: "title" is always shown`},
		{"bad hide", []plugin.ColumnDef{{Key: "year", Hide: "xl"}}, nil, `kinds[0].columns[0].hide: "xl" is not one of "", "sm", "md", "lg"`},
		{"long label", []plugin.ColumnDef{{Key: "year", Label: strings.Repeat("x", 41)}}, nil, `kinds[0].columns[0].label: longer than 40 characters`},
		{"control in label", []plugin.ColumnDef{{Key: "year", Label: "a\nb"}}, nil, `kinds[0].columns[0].label: must be a single line without control characters`},
		{"blank label", []plugin.ColumnDef{{Key: "year", Label: "   "}}, nil, `kinds[0].columns[0].label: must not be blank`},
		{"no-break blank label", []plugin.ColumnDef{{Key: "year", Label: "\u00a0 \u3000"}}, nil, `kinds[0].columns[0].label: must not be blank`},
		{"label padded past 40", []plugin.ColumnDef{{Key: "year", Label: " " + strings.Repeat("x", 41) + " "}}, nil, `kinds[0].columns[0].label: longer than 40 characters`},
		{"field shadowed by a core column", []plugin.ColumnDef{{Key: "status"}}, func(k *plugin.KindDef) {
			k.Fields = append(k.Fields, plugin.FieldDef{Key: "status", Label: "Status", Type: plugin.FieldText})
		}, `kinds[0].columns[0].key: "status" names both a field and a core column`},
	} {
		m := tv()
		if c.mod != nil {
			c.mod(&m.Kinds[0])
		}
		m.Kinds[0].Columns = c.cols
		err := plugin.Validate(m)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.want)
		}
	}
	// a label of exactly 40 characters (runes) is fine, also with padding
	// (trimmed first); fields need not be Show to be columns
	m = tv()
	m.Kinds[0].Columns = []plugin.ColumnDef{{Key: "year", Label: " " + strings.Repeat("ä", 40) + " "}, {Key: "episodes"}}
	if err := plugin.Validate(m); err != nil {
		t.Errorf("40-rune label: %v", err)
	}
}

// TestCoreColumnsAreAClosedSet: the core columns a kind may offer by key.
func TestCoreColumnsAreAClosedSet(t *testing.T) {
	got := plugin.CoreColumns()
	want := []string{"progress", "rating", "status", "updated"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("core columns = %v", got)
	}
	got[0] = "x"
	if plugin.CoreColumns()[0] != "progress" {
		t.Fatal("CoreColumns must return a fresh slice")
	}
}

// TestValidateStats: a kind's optional stats declaration names a time source
// (duration progress, completed number field, or units with a per-unit number
// field) and/or a units track (a count or pages progress with a plural label).
func TestValidateStats(t *testing.T) {
	withStats := func(s plugin.StatsDef) plugin.Manifest {
		m := tv()
		m.Kinds[0].Stats = &s
		return m
	}
	valid := []plugin.StatsDef{
		{Time: &plugin.StatsTimeDef{Label: "Time", DurationKey: "position"}},
		{Time: &plugin.StatsTimeDef{Label: "Time", CompletedField: "episodes"}},
		{Time: &plugin.StatsTimeDef{Label: "Time", UnitsKey: "episode", PerUnitField: "episodes"}},
		{Units: &plugin.StatsUnitsDef{Key: "season", LabelPlural: "Seasons"}},
		{
			Time:  &plugin.StatsTimeDef{Label: "Time", DurationKey: "position", CompletedField: "episodes", UnitsKey: "episode", PerUnitField: "episodes"},
			Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
		},
		{Time: &plugin.StatsTimeDef{Label: strings.Repeat("é", 40), DurationKey: "position"}},
	}
	for i, s := range valid {
		if err := plugin.Validate(withStats(s)); err != nil {
			t.Errorf("valid stats %d refused: %v", i, err)
		}
	}
	if err := plugin.Validate(tv()); err != nil {
		t.Errorf("nil stats refused: %v", err)
	}
	for _, c := range []struct {
		name  string
		stats plugin.StatsDef
		want  string
	}{
		{"empty stats", plugin.StatsDef{}, `kinds[0].stats: needs a time or a units declaration`},
		{"time without source", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time"}},
			`kinds[0].stats.time: needs duration_key, completed_field, or units_key with per_unit_field`},
		{"time label missing", plugin.StatsDef{Time: &plugin.StatsTimeDef{DurationKey: "position"}},
			`kinds[0].stats.time.label: required`},
		{"time label with newline", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Ti\nme", DurationKey: "position"}},
			`kinds[0].stats.time.label: must be a single line without control characters`},
		{"time label too long", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: strings.Repeat("T", 41), DurationKey: "position"}},
			`kinds[0].stats.time.label: longer than 40 characters`},
		{"units key without per unit field", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", UnitsKey: "episode"}},
			`kinds[0].stats.time.per_unit_field: required with units_key`},
		{"per unit field without units key", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", DurationKey: "position", PerUnitField: "episodes"}},
			`kinds[0].stats.time.units_key: required with per_unit_field`},
		{"duration key not a duration", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", DurationKey: "season"}},
			`kinds[0].stats.time.duration_key: "season" must be a duration progress, not count`},
		{"duration key unknown", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", DurationKey: "zzz"}},
			`kinds[0].stats.time.duration_key: "zzz" is not an eligible progress key`},
		{"units key not count or pages", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", UnitsKey: "position", PerUnitField: "episodes"}},
			`kinds[0].stats.time.units_key: "position" must be a count or pages progress, not duration`},
		{"units key unknown", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", UnitsKey: "zzz", PerUnitField: "episodes"}},
			`kinds[0].stats.time.units_key: "zzz" is not an eligible progress key`},
		{"completed field missing", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", CompletedField: "nope"}},
			`kinds[0].stats.time.completed_field: no field "nope"`},
		{"completed field not a number", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", CompletedField: "type"}},
			`kinds[0].stats.time.completed_field: field "type" is not a number field`},
		{"per unit field not a number", plugin.StatsDef{Time: &plugin.StatsTimeDef{Label: "Time", UnitsKey: "episode", PerUnitField: "type"}},
			`kinds[0].stats.time.per_unit_field: field "type" is not a number field`},
		{"units key missing", plugin.StatsDef{Units: &plugin.StatsUnitsDef{LabelPlural: "Episodes"}},
			`kinds[0].stats.units.key: required`},
		{"units key not count or pages", plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "position", LabelPlural: "Positions"}},
			`kinds[0].stats.units.key: "position" must be a count or pages progress, not duration`},
		{"units key unknown", plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "zzz", LabelPlural: "Things"}},
			`kinds[0].stats.units.key: "zzz" is not an eligible progress key`},
		{"units label plural missing", plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "episode"}},
			`kinds[0].stats.units.label_plural: required`},
		{"units label plural with newline", plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Ep\nisodes"}},
			`kinds[0].stats.units.label_plural: must be a single line without control characters`},
		{"units label plural too long", plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: strings.Repeat("E", 41)}},
			`kinds[0].stats.units.label_plural: longer than 40 characters`},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := plugin.Validate(withStats(c.stats))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestValidateSeriesHint(t *testing.T) {
	tests := []struct {
		name string
		hint plugin.SeriesHintDef
		want string // empty: valid
	}{
		{"valid", plugin.SeriesHintDef{TitleField: "author", PositionField: "pages"}, ""},
		{"no position", plugin.SeriesHintDef{TitleField: "author"}, ""},
		{"empty hint", plugin.SeriesHintDef{}, "kinds[0].series_hint: name at least one field"},
		{"release only", plugin.SeriesHintDef{ReleaseField: "released"}, ""},
		{"all three", plugin.SeriesHintDef{TitleField: "author", PositionField: "pages", ReleaseField: "released"}, ""},
		{"unknown release", plugin.SeriesHintDef{ReleaseField: "nope"}, `kinds[0].series_hint.release_field: no field "nope"`},
		{"release not date", plugin.SeriesHintDef{ReleaseField: "pages"}, `kinds[0].series_hint.release_field: field "pages" is not a date field`},
		{"unknown title field", plugin.SeriesHintDef{TitleField: "nope"}, `kinds[0].series_hint.title_field: no field "nope"`},
		{"title not text", plugin.SeriesHintDef{TitleField: "pages"}, `kinds[0].series_hint.title_field: field "pages" is not a text field`},
		{"unknown position", plugin.SeriesHintDef{TitleField: "author", PositionField: "nope"}, `kinds[0].series_hint.position_field: no field "nope"`},
		{"position not number", plugin.SeriesHintDef{TitleField: "author", PositionField: "isbn"}, `kinds[0].series_hint.position_field: field "isbn" is not a number field`},
	}
	for _, tc := range tests {
		m := book()
		m.Kinds[0].Fields = append(m.Kinds[0].Fields, plugin.FieldDef{Key: "released", Label: "Released", Type: plugin.FieldDate})
		h := tc.hint
		m.Kinds[0].SeriesHint = &h
		err := plugin.Validate(m)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateSeriesFields(t *testing.T) {
	ok := plugin.FieldDef{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Editable: true}
	tests := []struct {
		name   string
		fields []plugin.FieldDef
		want   string
	}{
		{"valid", []plugin.FieldDef{ok, {Key: "writer", Label: "Writer", Type: plugin.FieldText, Editable: true}}, ""},
		{"required", []plugin.FieldDef{{Key: "x", Label: "X", Type: plugin.FieldText, Editable: true, Required: true}}, "kinds[0].series_fields[0].required: series fields cannot be required"},
		{"not editable", []plugin.FieldDef{ok, {Key: "x", Label: "X", Type: plugin.FieldText}}, "kinds[0].series_fields[1].editable: series fields must be editable"},
		{"reserved description", []plugin.FieldDef{{Key: "description", Label: "D", Type: plugin.FieldText}}, `kinds[0].series_fields[0].key: "description" is reserved`},
		{"reserved parts_total", []plugin.FieldDef{ok, {Key: "parts_total", Label: "P", Type: plugin.FieldNumber}}, `kinds[0].series_fields[1].key: "parts_total" is reserved`},
		{"duplicate", []plugin.FieldDef{ok, ok}, `kinds[0].series_fields[1].key: duplicate "volumes"`},
		{"bad key", []plugin.FieldDef{{Key: "Bad Key", Label: "B", Type: plugin.FieldText}}, "not a valid key"},
		{"no label", []plugin.FieldDef{{Key: "x", Type: plugin.FieldText}}, "kinds[0].series_fields[0].label: required"},
		{"bad type", []plugin.FieldDef{{Key: "x", Label: "X", Type: "stars"}}, "unknown field type"},
		{"enum without values", []plugin.FieldDef{{Key: "x", Label: "X", Type: plugin.FieldEnum}}, "enum field needs at least one value"},
	}
	for _, tc := range tests {
		m := book()
		m.Kinds[0].SeriesFields = tc.fields
		err := plugin.Validate(m)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateSeriesLink(t *testing.T) {
	tests := []struct {
		name string
		link plugin.SeriesLinkDef
		want string
	}{
		{"valid", plugin.SeriesLinkDef{Kind: "novel", DescriptionField: "blurb", StartField: "begun", EndField: "ended", PartsField: "count"}, ""},
		{"kind only", plugin.SeriesLinkDef{Kind: "novel"}, ""},
		{"empty kind", plugin.SeriesLinkDef{}, "kinds[0].series_link.kind: required"},
		{"unknown kind", plugin.SeriesLinkDef{Kind: "nope"}, `kinds[0].series_link.kind: no kind "nope"`},
		{"unknown field", plugin.SeriesLinkDef{Kind: "novel", DescriptionField: "nope"}, `kinds[0].series_link.description_field: no field "nope" on kind "novel"`},
		{"description not text", plugin.SeriesLinkDef{Kind: "novel", DescriptionField: "count"}, `kinds[0].series_link.description_field: field "count" is not a text field`},
		{"start not date", plugin.SeriesLinkDef{Kind: "novel", StartField: "blurb"}, `kinds[0].series_link.start_field: field "blurb" is not a date field`},
		{"end not date", plugin.SeriesLinkDef{Kind: "novel", EndField: "count"}, `kinds[0].series_link.end_field: field "count" is not a date field`},
		{"parts not number", plugin.SeriesLinkDef{Kind: "novel", PartsField: "begun"}, `kinds[0].series_link.parts_field: field "begun" is not a number field`},
		{"same-named field of the same type", plugin.SeriesLinkDef{Kind: "novel"}, ""},
		{"same-named field of another type", plugin.SeriesLinkDef{Kind: "novel"}, `kinds[0].series_fields[1].type: the linked kind "novel" has a field "pages" of type number`},
	}
	for _, tc := range tests {
		m := book()
		other := m.Kinds[0]
		other.Key, other.Label, other.LabelPlural = "novel", "Novel", "Novels"
		other.SeriesHint, other.SeriesFields, other.SeriesLink = nil, nil, nil
		other.Columns = nil
		other.Fields = []plugin.FieldDef{
			{Key: "blurb", Label: "Blurb", Type: plugin.FieldText},
			{Key: "begun", Label: "Begun", Type: plugin.FieldDate},
			{Key: "ended", Label: "Ended", Type: plugin.FieldDate},
			{Key: "count", Label: "Count", Type: plugin.FieldNumber},
			{Key: "pages", Label: "Pages", Type: plugin.FieldNumber},
		}
		m.Kinds = append(m.Kinds, other)
		l := tc.link
		m.Kinds[0].SeriesLink = &l
		// series fields named like fields of the linked kind take their values
		m.Kinds[0].SeriesFields = []plugin.FieldDef{{Key: "count", Label: "Count", Type: plugin.FieldNumber, Editable: true}}
		if tc.name == "same-named field of another type" {
			m.Kinds[0].SeriesFields = append(m.Kinds[0].SeriesFields, plugin.FieldDef{Key: "pages", Label: "Pages", Type: plugin.FieldText, Editable: true})
		}
		err := plugin.Validate(m)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateRefusesSeriesSourceID(t *testing.T) {
	m := book()
	m.Sources[0].ID = "series"
	m.Importers[0].ResolveVia = "series"
	err := plugin.Validate(m)
	if err == nil || !strings.Contains(err.Error(), "reserved by the core") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateResolveByTitle(t *testing.T) {
	for name, c := range map[string]struct {
		mod  func(m *plugin.Manifest)
		want string // "" means valid
	}{
		"without resolve_via": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveVia, m.Importers[0].ResolveByTitle = "", true
		}, "importers[0].resolve_by_title: resolve_by_title needs resolve_via"},
		"with resolve_via": {func(m *plugin.Manifest) { m.Importers[0].ResolveByTitle = true }, ""},
		"id type the source does not list (R1)": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle, m.Importers[0].ExternalIDType = true, "asin"
		}, ""},
		"no id type, source without lookup": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle, m.Importers[0].ExternalIDType = true, ""
			m.Sources[0].SupportsLookup = false
		}, ""},
		"listed id type, source without lookup": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle = true
			m.Sources[0].SupportsLookup = false
		}, `importers[0].resolve_via: source "books" does not support lookup`},
		"unknown fallback": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle = true
			m.Importers[0].ResolveViaFallback = []string{"ghost"}
		}, `importers[0].resolve_via_fallback[0]: unknown source "ghost"`},
		"fallback lists the id type, no lookup": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle = true
			fb := m.Sources[0]
			fb.ID, fb.SupportsLookup = "books2", false
			m.Sources = append(m.Sources, fb)
			m.Importers[0].ResolveViaFallback = []string{"books2"}
		}, `importers[0].resolve_via_fallback[0]: source "books2" does not support lookup`},
		"unknown source still refused": {func(m *plugin.Manifest) {
			m.Importers[0].ResolveByTitle, m.Importers[0].ResolveVia = true, "ghost"
		}, `unknown source "ghost"`},
		"without the flag the id rule stays": {func(m *plugin.Manifest) { m.Importers[0].ExternalIDType = "asin" },
			`source "books" cannot look up "asin" ids`},
	} {
		m := book()
		c.mod(&m)
		err := plugin.Validate(m)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestValidateKeyOptional(t *testing.T) {
	m := book()
	m.Sources[0].KeyLabel, m.Sources[0].KeyHelp, m.Sources[0].KeyFields = "", "", nil
	m.Sources[0].KeyOptional = true
	err := plugin.Validate(m)
	if err == nil || !strings.Contains(err.Error(), "sources[0].key_optional: key_optional needs a key") {
		t.Errorf("no key: %v", err)
	}
	m.Sources[0].KeyLabel = "API key"
	if err := plugin.Validate(m); err != nil {
		t.Errorf("with key_label: %v", err)
	}
	m.Sources[0].KeyLabel = "Creds"
	m.Sources[0].KeyFields = []plugin.KeyField{{Name: "id", Label: "ID"}}
	if err := plugin.Validate(m); err != nil {
		t.Errorf("with key_fields: %v", err)
	}
}

func TestKindRepeatLabels(t *testing.T) {
	var k plugin.KindDef
	if k.RepeatAction() != "Start again" || k.RepeatMarker() != "Again" {
		t.Fatalf("defaults: %q %q", k.RepeatAction(), k.RepeatMarker())
	}
	k.Repeat = &plugin.RepeatDef{Action: "Rewatch", Marker: "Rewatching"}
	if k.RepeatAction() != "Rewatch" || k.RepeatMarker() != "Rewatching" {
		t.Fatalf("declared: %q %q", k.RepeatAction(), k.RepeatMarker())
	}
}

func TestValidateRepeat(t *testing.T) {
	long := strings.Repeat("é", 41)
	for _, c := range []struct {
		name string
		r    plugin.RepeatDef
		want string
	}{
		{"empty action", plugin.RepeatDef{Marker: "M"}, `kinds[0].repeat.action: required`},
		{"empty marker", plugin.RepeatDef{Action: "A"}, `kinds[0].repeat.marker: required`},
		{"multi line", plugin.RepeatDef{Action: "A\nB", Marker: "M"}, `kinds[0].repeat.action:`},
		{"too long", plugin.RepeatDef{Action: "A", Marker: long}, `kinds[0].repeat.marker:`},
	} {
		m := plugintest.TVPlugin().Manifest()
		m.Kinds = append([]plugin.KindDef(nil), m.Kinds...)
		r := c.r
		m.Kinds[0].Repeat = &r
		err := plugin.Validate(m)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	m := plugintest.TVPlugin().Manifest()
	m.Kinds = append([]plugin.KindDef(nil), m.Kinds...)
	m.Kinds[0].Repeat = &plugin.RepeatDef{Action: strings.Repeat("é", 40), Marker: "M"}
	if err := plugin.Validate(m); err != nil {
		t.Errorf("valid repeat refused: %v", err)
	}
}

func TestValidateLength(t *testing.T) {
	tests := []struct {
		name   string
		length []plugin.LengthDef
		want   string // empty: valid
	}{
		{"field", []plugin.LengthDef{{Key: "total", Label: "Total", Field: "year"}}, ""},
		{"units and per unit", []plugin.LengthDef{{Key: "total", Label: "Total", UnitsField: "episodes", PerUnitField: "year"}}, ""},
		{"units alone", []plugin.LengthDef{{Key: "eps", Label: "Episodes", UnitsField: "episodes"}}, ""},
		{"unknown field", []plugin.LengthDef{{Key: "a", Label: "A", Field: "nope"}}, `kinds[0].length[0].field: no field "nope"`},
		{"non number field", []plugin.LengthDef{{Key: "a", Label: "A", Field: "type"}}, `kinds[0].length[0].field: field "type" is not a number field`},
		{"unknown units field", []plugin.LengthDef{{Key: "a", Label: "A", UnitsField: "nope"}}, `kinds[0].length[0].units_field: no field "nope"`},
		{"non number per unit", []plugin.LengthDef{{Key: "a", Label: "A", UnitsField: "episodes", PerUnitField: "type"}}, `kinds[0].length[0].per_unit_field: field "type" is not a number field`},
		{"field with units", []plugin.LengthDef{{Key: "a", Label: "A", Field: "year", UnitsField: "episodes"}}, `kinds[0].length[0]: field cannot be combined with units_field or per_unit_field`},
		{"per unit without units", []plugin.LengthDef{{Key: "a", Label: "A", PerUnitField: "year"}}, `kinds[0].length[0].per_unit_field: needs units_field`},
		{"nothing set", []plugin.LengthDef{{Key: "a", Label: "A"}}, `kinds[0].length[0]: needs field, or units_field (with an optional per_unit_field)`},
		{"duplicate key", []plugin.LengthDef{{Key: "a", Label: "A", Field: "year"}, {Key: "a", Label: "B", Field: "year"}}, `kinds[0].length[1].key: duplicate "a"`},
		{"bad key", []plugin.LengthDef{{Key: "A b", Label: "A", Field: "year"}}, `kinds[0].length[0].key: "A b" is not a valid key`},
		{"empty label", []plugin.LengthDef{{Key: "a", Field: "year"}}, `kinds[0].length[0].label: required`},
	}
	for _, tc := range tests {
		m := tv()
		m.Kinds[0].Length = tc.length
		err := plugin.Validate(m)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateGroupKeyNeedsPrimaryKey(t *testing.T) {
	m := tv()
	m.Kinds[0].Progress.PrimaryKey = ""
	m.Kinds[0].Progress.GroupKey = "season"
	err := plugin.Validate(m)
	want := `kinds[0].progress.group_key: needs a primary_key`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%v, want %q", err, want)
	}
	m.Kinds[0].Progress.GroupKey = ""
	if err := plugin.Validate(m); err != nil {
		t.Fatalf("no primary and no group key is valid: %v", err)
	}
}

func TestValidateGroupKey(t *testing.T) {
	tests := []struct {
		name, key, want string
	}{
		{"valid", "season", ""},
		{"none", "", ""},
		{"unknown track", "nope", `kinds[0].progress.group_key: "nope" is not an eligible progress key`},
		{"same as primary", "episode", `kinds[0].progress.group_key: must differ from the primary key`},
		{"not a count", "position", `kinds[0].progress.group_key: "position" must be a count progress`},
	}
	for _, tc := range tests {
		m := tv()
		m.Kinds[0].Progress.GroupKey = tc.key
		err := plugin.Validate(m)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestLengthInMinutes(t *testing.T) {
	for _, tc := range []struct {
		d    plugin.LengthDef
		want bool
	}{
		{plugin.LengthDef{Field: "a"}, true},
		{plugin.LengthDef{UnitsField: "a", PerUnitField: "b"}, true},
		{plugin.LengthDef{UnitsField: "a"}, false},
		{plugin.LengthDef{}, false},
	} {
		if got := tc.d.InMinutes(); got != tc.want {
			t.Errorf("%+v: InMinutes = %v", tc.d, got)
		}
	}
}
