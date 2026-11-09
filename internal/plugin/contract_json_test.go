package plugin_test

import (
	"encoding/json"
	"reflect"
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
		ID: "full", Name: "Full", Version: "1.2.3", ContractVersion: plugin.ContractVersion, DefaultEnabled: true,
		Kinds: []plugin.KindDef{{
			Key: "k", Label: "K", LabelPlural: "Ks",
			Fields: []plugin.FieldDef{
				{Key: "n", Label: "N", Type: plugin.FieldNumber, Min: fptr(0), Max: fptr(10), Show: true, Editable: true, Filterable: true, Sortable: true, Required: true},
				{Key: "e", Label: "E", Type: plugin.FieldEnum, EnumValues: []string{"a", "b"}},
			},
			Genres:   plugin.TermDef{Enabled: true, Label: "Genres", Editable: true, Filterable: true},
			Tags:     plugin.TermDef{Enabled: true, Label: "Tags", Editable: true, Filterable: true},
			Statuses: plugin.StatusLabels{Planned: "P", Active: "A", Paused: "Pa", Completed: "C", Dropped: "D"},
			Progress: plugin.ProgressSpec{
				Eligible:     []plugin.ProgressDef{{Key: "x", Type: plugin.Count, Label: "X", TotalField: "n"}, {Key: "y", Type: plugin.Percent, Label: "Y"}},
				Defaults:     []string{"x"},
				Combinations: [][]string{{"x"}, {"y"}},
				PrimaryKey:   "x",
			},
			Sections:       []plugin.SectionDef{{Type: plugin.SectionPeople, Label: "Cast", Field: "n", Roles: []string{"actor"}}},
			TitleLanguages: []string{"en", "ja"},
		}},
		Sources:   []plugin.SourceInfo{{ID: "s", Name: "S", RequestsPerMinute: 30, ExternalIDTypes: []string{"s"}, SupportsLookup: true, LinkLabel: "View on S", URLPatterns: []string{"s.example/{id}"}}},
		Importers: []plugin.ImporterInfo{{ID: "i", Name: "I", FileHint: "csv", Accept: []string{".csv"}, ExternalIDType: "s", ResolveVia: "s", FallbackTitleLang: "en"}},
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
	roundTrip(t, "summary", summary)
	roundTrip(t, "relation", plugin.Relation{Kind: "SEQUEL", Item: summary})
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
	}
	roundTrip(t, "parsedentry", entry)
	roundTrip(t, "skipped", plugin.SkippedRow{Line: 4, Title: "T", Reason: "bad"})
	roundTrip(t, "parseresult", plugin.ParseResult{Entries: []plugin.ParsedEntry{entry}, Skipped: []plugin.SkippedRow{{Line: 1, Reason: "r"}}})
	roundTrip(t, "exportentry", plugin.ExportEntry{
		Kind: "k", Titles: []plugin.Title{{Lang: "en", Text: "T"}}, ExternalIDs: map[string]string{"a": "b"},
		Status: plugin.Dropped, Rating: iptr(7), Progress: map[string]float64{"p": 1}, StartedOn: &day, FinishedOn: &day, Comment: "c",
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
