package tv

import (
	"context"
	"errors"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestNewProvidesDeclaredSourcesAndImporter(t *testing.T) {
	p := New(Options{})
	if p.Source(SourceTVmaze) == nil || p.Source(SourceTMDB) == nil || p.Importer(ImporterYamtrack) == nil ||
		p.Exporter(ExporterJSON) == nil || p.Source("nope") != nil || p.Importer("nope") != nil {
		t.Fatal("default wiring")
	}
	withKey := New(Options{TMDBKey: func(context.Context) (string, error) { return "k", nil }})
	if withKey.Source(SourceTMDB) == nil || withKey.Source(SourceTVmaze) == nil {
		t.Fatal("tmdb wiring")
	}
	if _, err := p.Source(SourceTMDB).Details(context.Background(), KindShow, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("no-key details err = %v, want ErrNotConfigured", err)
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(withKey); err == nil {
		t.Fatal("second tv registration should be refused")
	}
}
