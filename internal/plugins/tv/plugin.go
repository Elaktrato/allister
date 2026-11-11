package tv

import (
	"context"
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/jsonexport"
	"github.com/allister/allister/internal/plugins/tmdb"
)

// Options configure the plugin. The URL fields exist for tests; empty means
// the production endpoint. TMDB is always declared; its key is resolved per
// request, so a key saved in the settings takes effect without a restart.
type Options struct {
	HTTP      *http.Client
	TVmazeURL string
	TMDBURL   string
	TMDBKey   tmdb.KeyFunc
}

type tvPlugin struct {
	sources   map[string]plugin.Source
	importers map[string]plugin.Importer
	exporter  plugin.Exporter
}

func New(opts Options) plugin.Plugin {
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	key := opts.TMDBKey
	if key == nil {
		key = func(context.Context) (string, error) { return "", nil }
	}
	return &tvPlugin{
		sources: map[string]plugin.Source{
			SourceTVmaze: &tvmazeSource{c: newTVmazeClient(opts.TVmazeURL, hc)},
			SourceTMDB:   &tmdbSource{c: tmdb.New(opts.TMDBURL, key, hc)},
		},
		importers: map[string]plugin.Importer{
			ImporterYamtrack: yamtrackTVImporter{},
		},
		exporter: jsonexport.New(exportFormat),
	}
}

func (p *tvPlugin) Manifest() plugin.Manifest { return manifest() }

func (p *tvPlugin) Source(id string) plugin.Source { return p.sources[id] }

func (p *tvPlugin) Importer(id string) plugin.Importer { return p.importers[id] }

func (p *tvPlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
