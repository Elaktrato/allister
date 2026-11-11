package tv

import (
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestNewProvidesDeclaredSourcesAndImporter(t *testing.T) {
	p := New(Options{})
	if p.Source(SourceTVmaze) == nil || p.Source(SourceTMDB) != nil || p.Importer(ImporterYamtrack) == nil ||
		p.Exporter(ExporterJSON) == nil || p.Source("nope") != nil || p.Importer("nope") != nil {
		t.Fatal("default wiring")
	}
	withKey := New(Options{TMDBKey: "k"})
	if withKey.Source(SourceTMDB) == nil || withKey.Source(SourceTVmaze) == nil {
		t.Fatal("tmdb wiring")
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(New(Options{TMDBKey: "k"})); err == nil {
		t.Fatal("second tv registration should be refused")
	}
}
