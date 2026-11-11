package tv

import (
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
	if len(m.Sources) != 1 || m.Sources[0].ID != SourceTVmaze || !m.Sources[0].SupportsSchedule || m.Importers[0].ResolveVia != "" {
		t.Fatalf("default sources/importers = %+v %+v", m.Sources, m.Importers)
	}
	withKey := New(Options{TMDBKey: "k"}).Manifest()
	if len(withKey.Sources) != 2 || withKey.Sources[1].ID != SourceTMDB || withKey.Sources[1].SupportsSchedule || withKey.Importers[0].ResolveVia != SourceTMDB {
		t.Fatalf("tmdb manifest = %+v %+v", withKey.Sources, withKey.Importers)
	}
	if err := plugin.Validate(withKey); err != nil {
		t.Fatalf("tmdb validate: %v", err)
	}
}
