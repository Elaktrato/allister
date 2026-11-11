package movie

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func TestNewWiresDeclaredParts(t *testing.T) {
	p := New(Options{})
	m := p.Manifest()
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
	// Every part the manifest declares must be served by the implementation.
	for _, s := range m.Sources {
		if p.Source(s.ID) == nil {
			t.Fatalf("declared source %q is not provided", s.ID)
		}
	}
	for _, i := range m.Importers {
		if p.Importer(i.ID) == nil {
			t.Fatalf("declared importer %q is not provided", i.ID)
		}
	}
	for _, e := range m.Exporters {
		if p.Exporter(e.ID) == nil {
			t.Fatalf("declared exporter %q is not provided", e.ID)
		}
	}
	if p.Source(SourceTMDB) == nil || p.Importer(ImporterYamtrack) == nil ||
		p.Exporter(ExporterJSON) == nil || p.Source("nope") != nil ||
		p.Importer("nope") != nil || p.Exporter("nope") != nil {
		t.Fatal("wiring")
	}
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(New(Options{})); err == nil {
		t.Fatal("duplicate registration")
	}
}

// TestNewKeyProviderIsLive resolves the key on every request: a key saved in
// the settings later must take effect without a new plugin. A nil key provider
// means the source is not configured.
func TestNewKeyProviderIsLive(t *testing.T) {
	var mu sync.Mutex
	var key string
	keyFunc := func(context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return key, nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("api_key") != "key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(movieDetailsBody))
	}))
	t.Cleanup(srv.Close)

	p := New(Options{HTTP: srv.Client(), TMDBURL: srv.URL, TMDBKey: keyFunc})
	src := p.Source(SourceTMDB)
	if _, err := src.Details(context.Background(), KindMovie, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("no-key details err = %v, want ErrNotConfigured", err)
	}

	mu.Lock()
	key = "key"
	mu.Unlock()
	d, err := src.Details(context.Background(), KindMovie, "1")
	if err != nil || d.ID != "1" || d.Kind != KindMovie || len(d.Titles) == 0 || d.Titles[0].Text != "The Matrix" {
		t.Fatalf("%+v %v", d, err)
	}
}

// TestNewNilKeyIsNotConfigured covers the fallback key provider New installs
// when Options.TMDBKey is nil.
func TestNewNilKeyIsNotConfigured(t *testing.T) {
	src := New(Options{}).Source(SourceTMDB)
	if _, err := src.Details(context.Background(), KindMovie, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("details err = %v, want ErrNotConfigured", err)
	}
}
