package tv

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestManifest(t *testing.T) {
	m := New(Options{}).Manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.ID != PluginID || m.Name != "TV" || m.DefaultEnabled || m.Icon != "live_tv" || len(m.Kinds) != 1 || m.Kinds[0].Key != KindShow {
		t.Fatalf("manifest = %+v", m)
	}
	k := m.Kinds[0]
	if !k.Genres.Enabled || !k.Tags.Enabled || len(k.Sections) != 8 {
		t.Fatalf("kind = %+v", k)
	}
	if !slices.Equal(k.Progress.Defaults, []string{"season", "episode", "position"}) || k.Progress.PrimaryKey != "episode" {
		t.Fatalf("progress = %+v", k.Progress)
	}
	for _, pd := range k.Progress.Eligible {
		if pd.Key == "position" && !pd.ResetOnPrimary {
			t.Fatalf("position must reset on +1")
		}
	}
	if len(m.Sources) != 2 || m.Sources[0].ID != SourceTVmaze || len(m.Importers) != 1 || len(m.Exporters) != 1 {
		t.Fatalf("sources/importers/exporters = %+v %+v %+v", m.Sources, m.Importers, m.Exporters)
	}
}

func TestManifestAlwaysDeclaresTMDB(t *testing.T) {
	m := New(Options{}).Manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 2 || m.Sources[1].ID != SourceTMDB || m.Sources[1].KeyLabel != "API key" || m.Sources[1].SupportsSchedule {
		t.Fatalf("sources = %+v", m.Sources)
	}
	if m.Importers[0].ResolveVia != SourceTMDB || m.Importers[0].ExternalIDType != "tmdb" || m.Importers[0].UnmatchedNote == "" {
		t.Fatalf("importer = %+v", m.Importers[0])
	}
	if got := New(Options{TMDBKey: func(context.Context) (string, error) { return "k", nil }}).Manifest(); !reflect.DeepEqual(got, m) {
		t.Fatalf("the key must not change the manifest")
	}
}

func TestKindStats(t *testing.T) {
	k := manifest().Kinds[0]
	wantStats := &plugin.StatsDef{
		Time:  &plugin.StatsTimeDef{Label: "Watchtime", UnitsKey: "episode", PerUnitField: "runtime"},
		Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"},
	}
	if !reflect.DeepEqual(k.Stats, wantStats) {
		t.Fatalf("stats = %+v", k.Stats)
	}
	for _, f := range k.Fields {
		if f.Key == "runtime" && f.Unit != "min" {
			t.Fatalf("runtime unit = %q, want min", f.Unit)
		}
	}
}
