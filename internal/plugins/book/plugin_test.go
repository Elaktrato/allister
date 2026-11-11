package book

import (
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestNewProvidesEveryDeclaredSourceImporterAndExporter(t *testing.T) {
	p := New(Options{})
	for _, id := range []string{SourceOpenLibrary, SourceGoogleBooks, SourceAniList} {
		if p.Source(id) == nil {
			t.Errorf("source %q missing", id)
		}
	}
	for _, id := range []string{ImporterGoodreads, ImporterAniList} {
		if p.Importer(id) == nil {
			t.Errorf("importer %q missing", id)
		}
	}
	if p.Exporter(ExporterJSON) == nil || p.Exporter("nope") != nil || p.Source("nope") != nil || p.Importer("nope") != nil {
		t.Fatal("unknown ids must return nil")
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
}
