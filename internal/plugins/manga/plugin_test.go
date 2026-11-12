package manga

import (
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestNewProvidesEveryDeclaredImporter(t *testing.T) {
	p := New(Options{})
	for _, id := range []string{AccountImp, MALImp, ImporterYamtrack} {
		if p.Importer(id) == nil {
			t.Errorf("importer %q missing", id)
		}
	}
	for _, info := range p.Manifest().Importers {
		if p.Importer(info.ID) == nil {
			t.Errorf("declared importer %q is not provided", info.ID)
		}
	}
	if _, ok := p.Importer(ImporterYamtrack).(yamtrackMangaImporter); !ok {
		t.Fatalf("yamtrack importer has type %T", p.Importer(ImporterYamtrack))
	}
	if p.Importer("nope") != nil || p.Source("nope") != nil || p.Exporter("nope") != nil {
		t.Fatal("unknown ids must return nil")
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
}
