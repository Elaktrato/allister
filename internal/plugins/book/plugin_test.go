package book

import (
	"bytes"
	"context"
	"strings"
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
	if ol := p.Source(SourceOpenLibrary).(*openLibrarySource); ol.c.hc.Timeout <= 0 {
		t.Fatal("Open Library client has no default timeout")
	}
	if gb := p.Source(SourceGoogleBooks).(*googleBooksSource); gb.c.hc.Timeout <= 0 {
		t.Fatal("Google Books client has no default timeout")
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestBooksJSONExport(t *testing.T) {
	p := New(Options{})
	var b bytes.Buffer
	err := p.Exporter(ExporterJSON).Export(context.Background(), []plugin.ExportEntry{{
		Kind: KindSeries, Titles: []plugin.Title{{Lang: "en", Text: "Sword Art Online"}},
		Status: plugin.Active, Progress: map[string]float64{"volume": 1, "chapter": 5},
	}}, &b)
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `"format":"allister.book.export"`) || !strings.Contains(out, `"kind":"series"`) ||
		!strings.Contains(out, `"chapter":5`) || !strings.Contains(out, "Sword Art Online") {
		t.Fatalf("export = %s", out)
	}
}
