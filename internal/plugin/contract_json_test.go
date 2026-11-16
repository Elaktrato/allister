package plugin_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func roundTrip[T any](t *testing.T, name string, in T) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: unmarshal: %v", name, err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("%s: round trip changed value\n in: %#v\nout: %#v\njson: %s", name, in, out, b)
	}
}

func fptr(f float64) *float64 { return &f }
func iptr(i int) *int         { return &i }

func TestManifestJSONRoundTrip(t *testing.T) {
	for _, p := range []plugin.Plugin{plugintest.BookPlugin(), plugintest.TVPlugin()} {
		m := p.Manifest()
		roundTrip(t, m.ID+" manifest", m)
	}
	full := plugin.Manifest{
		ID: "full", Name: "Full", Version: "1.2.3", ContractVersion: plugin.ContractVersion, DefaultEnabled: true, Icon: "menu_book",
		Kinds: []plugin.KindDef{{
			Key: "k", Label: "K", LabelPlural: "Ks",
			Fields: []plugin.FieldDef{
				{Key: "n", Label: "N", Type: plugin.FieldNumber, Unit: "pcs", Min: fptr(0), Max: fptr(10), Show: true, Editable: true, Filterable: true, Sortable: true, Required: true},
				{Key: "e", Label: "E", Type: plugin.FieldEnum, EnumValues: []string{"a", "b"}},
			},
			Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
			Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
			Statuses: plugin.StatusLabels{Planned: "P", Active: "A", Paused: "Pa", Completed: "C", Dropped: "D"},
			Progress: plugin.ProgressSpec{
				Eligible:     []plugin.ProgressDef{{Key: "x", Type: plugin.Count, Label: "X", LabelPlural: "Xes", TotalField: "n"}, {Key: "y", Type: plugin.Percent, Label: "Y", ResetOnPrimary: true}, {Key: "d", Type: plugin.Duration, Label: "D"}},
				Defaults:     []string{"x"},
				Combinations: [][]string{{"x"}, {"y"}},
				PrimaryKey:   "x",
			},
			Sections:       []plugin.SectionDef{{Type: plugin.SectionPeople, Label: "Cast", Field: "n", Roles: []string{"actor"}}},
			TitleLanguages: []string{"en", "ja"},
			TitleFallback:  []string{"ja", "en"},
			Columns:        []plugin.ColumnDef{{Key: "n", Label: "Number", Default: true, Hide: "md"}, {Key: "rating"}},
			SeriesHint:     &plugin.SeriesHintDef{TitleField: "e", PositionField: "n"},
			Repeat:         &plugin.RepeatDef{Action: "Redo", Marker: "Redoing"},
			Stats: &plugin.StatsDef{
				Time:  &plugin.StatsTimeDef{Label: "Time", DurationKey: "d", CompletedField: "n", UnitsKey: "x", PerUnitField: "n"},
				Units: &plugin.StatsUnitsDef{Key: "x", LabelPlural: "Units"},
			},
		}},
		Sources: []plugin.SourceInfo{{ID: "s", Name: "S", RequestsPerMinute: 30, ExternalIDTypes: []string{"s"}, SupportsLookup: true, LinkLabel: "View on S", URLPatterns: []string{"s.example/{id}"}, Kinds: []string{"k"}, KeyLabel: "API key", KeyHelp: "Get one at example.test.", KeyOptional: true,
			KeyFields: []plugin.KeyField{{Name: "client_id", Label: "Client ID"}, {Name: "client_secret", Label: "Client Secret", Secret: true}}}},
		Importers: []plugin.ImporterInfo{{ID: "i", Name: "I", FileHint: "csv", Accept: []string{".csv"}, ExternalIDType: "s", ExternalIDLabel: "S id", ResolveVia: "s", ResolveViaFallback: []string{"s2"}, ResolveByTitle: true, ManualRows: true, FallbackTitleLang: "en", UnmatchedNote: "No S match for #{id}."}},
		Exporters: []plugin.ExporterInfo{{ID: "x", Name: "X", Extension: ".json", MIME: "application/json"}},
	}
	roundTrip(t, "full manifest", full)
}

func TestRuntimeDataJSONRoundTrip(t *testing.T) {
	day := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	voice := plugin.PersonRef{ID: "9", Name: "Actor", ImageURL: "http://i/9", URL: "http://u/9"}
	summary := plugin.ItemSummary{
		Source: "s", ID: "1", Kind: "k", CoverURL: "http://c",
		Titles: []plugin.Title{{Lang: "en", Text: "T"}},
		Fields: map[string]plugin.FieldValue{
			"a": {Text: "x"}, "b": {Num: fptr(2.5)}, "c": {Date: &day}, "d": {List: []string{"p", "q"}},
		},
	}
	roundTrip(t, "title", plugin.Title{Lang: "ja", Text: "テスト"})
	roundTrip(t, "fieldvalue", plugin.FieldValue{Text: "x", Num: fptr(1), Date: &day, List: []string{"a"}})
	roundTrip(t, "person", voice)
	roundTrip(t, "credit without counterpart", plugin.Credit{Role: "author", Person: plugin.PersonRef{ID: "1", Name: "A"}})
	roundTrip(t, "credit with counterpart", plugin.Credit{Role: "main", Person: plugin.PersonRef{ID: "1", Name: "Char"}, With: &voice, WithRole: "Japanese"})
	roundTrip(t, "credit with detail", plugin.Credit{Role: "character", Detail: "MAIN", Person: plugin.PersonRef{ID: "1", Name: "Char"}})
	roundTrip(t, "summary", summary)
	roundTrip(t, "relation", plugin.Relation{Kind: "SEQUEL", Item: summary})
	withURL := summary
	withURL.URL = "https://s.example/1"
	roundTrip(t, "summary with url", withURL)
	roundTrip(t, "relation with url", plugin.Relation{Kind: "SEQUEL", Item: withURL})
	roundTrip(t, "tag", plugin.Tag{Name: "Time Travel", Rank: iptr(0), Spoiler: true})
	roundTrip(t, "tag plain", plugin.Tag{Name: "Plain"})
	roundTrip(t, "itemdata", plugin.ItemData{
		ItemSummary: summary, ExternalURL: "http://e", ExternalIDs: map[string]string{"s": "1"},
		Relations: []plugin.Relation{{Kind: "PREQUEL", Item: summary}},
		Genres:    []string{"Drama"}, Tags: []plugin.Tag{{Name: "T", Rank: iptr(80)}},
	})
	entry := plugin.ParsedEntry{
		Kind: "k", Title: "T", ExternalIDs: map[string]string{"mal": "5"}, Status: plugin.Completed, Rating: iptr(0),
		Progress: map[string]float64{"episode": 12, "position": 90}, StartedOn: &day, FinishedOn: &day,
		Comment: "c", CoverURL: "http://c", Notes: []string{"n"},
		Fields: map[string]plugin.FieldValue{"pages": {Num: fptr(300)}},
	}
	roundTrip(t, "parsedentry", entry)
	roundTrip(t, "skipped", plugin.SkippedRow{Line: 4, Title: "T", Reason: "bad"})
	roundTrip(t, "airing", plugin.Airing{Season: 2, Number: 5, AirsAt: day})
	roundTrip(t, "parseresult", plugin.ParseResult{Entries: []plugin.ParsedEntry{entry}, Skipped: []plugin.SkippedRow{{Line: 1, Reason: "r"}}, Ignored: 2})
	roundTrip(t, "exportentry", plugin.ExportEntry{
		Kind: "k", Titles: []plugin.Title{{Lang: "en", Text: "T"}}, ExternalIDs: map[string]string{"a": "b"},
		Status: plugin.Dropped, Rating: iptr(7), Progress: map[string]float64{"p": 1}, StartedOn: &day, FinishedOn: &day, Notes: "c",
		Runs: []plugin.ExportRun{{Number: 1, Status: plugin.Completed, Rating: iptr(6), Review: "r", Progress: map[string]float64{"p": 3}, StartedOn: &day, FinishedOn: &day},
			{Number: 2, Status: plugin.Dropped}},
		UserTags: []string{"comfort"}, Lists: []string{"Favourites"}, Series: "S", SeriesPosition: 2,
	})
}

func TestJSONIgnoresUnknownFields(t *testing.T) {
	var m plugin.Manifest
	in := `{"id":"x","name":"X","future_field":{"a":1},"kinds":[{"key":"k","brand_new":true}]}`
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unknown fields must be ignored: %v", err)
	}
	if m.ID != "x" || len(m.Kinds) != 1 || m.Kinds[0].Key != "k" {
		t.Fatalf("known fields lost: %+v", m)
	}
	var c plugin.Credit
	if err := json.Unmarshal([]byte(`{"role":"r","person":{"id":"1","name":"n","extra":1},"zzz":2}`), &c); err != nil || c.Person.Name != "n" {
		t.Fatalf("credit: %v %+v", err, c)
	}
}

func TestItemSummaryURLJSON(t *testing.T) {
	b, err := json.Marshal(plugin.ItemSummary{Source: "s", ID: "1", Kind: "k"})
	if err != nil || strings.Contains(string(b), "url") {
		t.Fatalf("empty url must be omitted: %s %v", b, err)
	}
	b, err = json.Marshal(plugin.ItemSummary{Source: "s", ID: "1", Kind: "k", URL: "https://s.example/1"})
	if err != nil || !strings.Contains(string(b), `"url":"https://s.example/1"`) {
		t.Fatalf("url missing: %s %v", b, err)
	}
	var got plugin.ItemSummary
	if err := json.Unmarshal(b, &got); err != nil || got.URL != "https://s.example/1" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestCreditDetailJSON(t *testing.T) {
	b, err := json.Marshal(plugin.Credit{Role: "character", Person: plugin.PersonRef{ID: "1", Name: "n"}})
	if err != nil || strings.Contains(string(b), "detail") {
		t.Fatalf("empty detail must be omitted: %s %v", b, err)
	}
	b, err = json.Marshal(plugin.Credit{Role: "character", Detail: "MAIN", Person: plugin.PersonRef{ID: "1", Name: "n"}})
	if err != nil || !strings.Contains(string(b), `"detail":"MAIN"`) {
		t.Fatalf("detail missing: %s %v", b, err)
	}
	var c plugin.Credit
	if err := json.Unmarshal(b, &c); err != nil || c.Detail != "MAIN" {
		t.Fatalf("round trip: %+v %v", c, err)
	}
}

func TestFieldUnitJSON(t *testing.T) {
	f := plugin.FieldDef{Key: "episodes", Label: "Episodes", Type: plugin.FieldNumber, Unit: "eps"}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"unit":"eps"`) {
		t.Fatalf("json %s", b)
	}
	roundTrip(t, "field with unit", f)
	f.Unit = ""
	if b, _ = json.Marshal(f); strings.Contains(string(b), "unit") {
		t.Fatalf("empty unit not omitted: %s", b)
	}
}

func TestImporterUnmatchedNoteJSON(t *testing.T) {
	im := plugin.ImporterInfo{ID: "i", Name: "I", ExternalIDType: "s", UnmatchedNote: "No S match for #{id}."}
	b, err := json.Marshal(im)
	if err != nil || !strings.Contains(string(b), `"unmatched_note":"No S match for #{id}."`) {
		t.Fatalf("json %s %v", b, err)
	}
	roundTrip(t, "importer with unmatched note", im)
	im.UnmatchedNote = ""
	if b, _ = json.Marshal(im); strings.Contains(string(b), "unmatched_note") {
		t.Fatalf("empty unmatched note not omitted: %s", b)
	}
}

// TestManifestIconJSON: the icon travels as "icon" and is omitted when empty.
func TestManifestIconJSON(t *testing.T) {
	b, err := json.Marshal(plugin.Manifest{ID: "x", Icon: "live_tv"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"icon":"live_tv"`) {
		t.Errorf("json: %s", b)
	}
	b, _ = json.Marshal(plugin.Manifest{ID: "x"})
	if strings.Contains(string(b), `"icon"`) {
		t.Errorf("empty icon not omitted: %s", b)
	}
}

// TestKindTitleFallbackJSON: the fallback order travels as "title_fallback"
// and is omitted when empty.
func TestKindTitleFallbackJSON(t *testing.T) {
	k := plugin.KindDef{Key: "k", TitleLanguages: []string{"en", "ja"}, TitleFallback: []string{"ja", "en"}}
	b, err := json.Marshal(k)
	if err != nil || !strings.Contains(string(b), `"title_fallback":["ja","en"]`) {
		t.Fatalf("json %s %v", b, err)
	}
	roundTrip(t, "kind with fallback", k)
	k.TitleFallback = nil
	if b, _ = json.Marshal(k); strings.Contains(string(b), "title_fallback") {
		t.Fatalf("empty fallback not omitted: %s", b)
	}
}

// TestKindStatsJSON: the stats declaration travels as "stats" and is omitted
// when unset.
func TestKindStatsJSON(t *testing.T) {
	k := plugin.KindDef{Key: "k", TitleLanguages: []string{"en"}, Stats: &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "pages", LabelPlural: "Pages"}}}
	b, err := json.Marshal(k)
	if err != nil || !strings.Contains(string(b), `"stats":{"units":{"key":"pages","label_plural":"Pages"}}`) {
		t.Fatalf("json %s %v", b, err)
	}
	roundTrip(t, "kind with stats", k)
	k.Stats = nil
	if b, _ = json.Marshal(k); strings.Contains(string(b), "stats") {
		t.Fatalf("nil stats not omitted: %s", b)
	}
}

// TestKindColumnsJSON: columns travel as "columns" (omitted when empty);
// a column's label, default and hide are omitted when unset.
func TestKindColumnsJSON(t *testing.T) {
	k := plugin.KindDef{Key: "k", TitleLanguages: []string{"en"},
		Columns: []plugin.ColumnDef{{Key: "year", Label: "Year", Default: true, Hide: "md"}, {Key: "status"}}}
	b, err := json.Marshal(k)
	if err != nil || !strings.Contains(string(b), `"columns":[{"key":"year","label":"Year","default":true,"hide":"md"},{"key":"status"}]`) {
		t.Fatalf("json %s %v", b, err)
	}
	roundTrip(t, "kind with columns", k)
	k.Columns = nil
	if b, _ = json.Marshal(k); strings.Contains(string(b), "columns") {
		t.Fatalf("empty columns not omitted: %s", b)
	}
}

func TestKindSeriesHintJSON(t *testing.T) {
	k := plugin.KindDef{Key: "k", SeriesHint: &plugin.SeriesHintDef{TitleField: "series", PositionField: "series_number"}}
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"series_hint":{"title_field":"series","position_field":"series_number"}`) {
		t.Fatalf("json = %s", b)
	}
	roundTrip(t, "kind with hint", k)
	b, _ = json.Marshal(plugin.KindDef{Key: "k"})
	if strings.Contains(string(b), "series_hint") {
		t.Fatalf("hint not omitted: %s", b)
	}
	b, _ = json.Marshal(plugin.KindDef{Key: "k", SeriesHint: &plugin.SeriesHintDef{TitleField: "s"}})
	if strings.Contains(string(b), "position_field") {
		t.Fatalf("position not omitted: %s", b)
	}
}

func TestExportEntrySeriesJSON(t *testing.T) {
	b, err := json.Marshal(plugin.ExportEntry{Kind: "k", Series: "The Expanse", SeriesPosition: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"series":"The Expanse","series_position":3`) {
		t.Fatalf("json = %s", b)
	}
	b, _ = json.Marshal(plugin.ExportEntry{Kind: "k"})
	if strings.Contains(string(b), "series") {
		t.Fatalf("zero values not omitted: %s", b)
	}
}

func TestImporterResolveByTitleJSON(t *testing.T) {
	on, _ := json.Marshal(plugin.ImporterInfo{ID: "i", Name: "I", ResolveByTitle: true})
	if !strings.Contains(string(on), `"resolve_by_title":true`) {
		t.Errorf("set: %s", on)
	}
	off, _ := json.Marshal(plugin.ImporterInfo{ID: "i", Name: "I"})
	if strings.Contains(string(off), "resolve_by_title") {
		t.Errorf("unset must be omitted: %s", off)
	}
	roundTrip(t, "resolve by title", plugin.ImporterInfo{ID: "i", Name: "I", ResolveByTitle: true})
}

func TestSourceKeyOptionalJSON(t *testing.T) {
	on, _ := json.Marshal(plugin.SourceInfo{ID: "s", KeyLabel: "API key", KeyOptional: true})
	if !strings.Contains(string(on), `"key_optional":true`) {
		t.Errorf("set: %s", on)
	}
	off, _ := json.Marshal(plugin.SourceInfo{ID: "s", KeyLabel: "API key"})
	if strings.Contains(string(off), "key_optional") {
		t.Errorf("unset must be omitted: %s", off)
	}
	roundTrip(t, "key optional", plugin.SourceInfo{ID: "s", KeyLabel: "API key", KeyOptional: true})
}

func TestProgressDefPluralLabel(t *testing.T) {
	if got := (plugin.ProgressDef{Label: "Page"}).PluralLabel(); got != "Pages" {
		t.Fatalf("default plural = %q", got)
	}
	if got := (plugin.ProgressDef{Label: "Match", LabelPlural: "Matches"}).PluralLabel(); got != "Matches" {
		t.Fatalf("declared plural = %q", got)
	}
}

func TestExportEntryRunsTagsListsJSON(t *testing.T) {
	b, err := json.Marshal(plugin.ExportEntry{Kind: "k", Notes: "n", Runs: []plugin.ExportRun{{Number: 1, Status: plugin.Active}},
		UserTags: []string{"t"}, Lists: []string{"L"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"notes":"n"`, `"runs":[{"number":1,"status":"active"}]`, `"user_tags":["t"]`, `"lists":["L"]`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("json lacks %s: %s", want, b)
		}
	}
	b, _ = json.Marshal(plugin.ExportEntry{Kind: "k"})
	for _, not := range []string{"notes", "runs", "user_tags", "lists", "comment"} {
		if strings.Contains(string(b), not) {
			t.Fatalf("empty entry has %s: %s", not, b)
		}
	}
}
