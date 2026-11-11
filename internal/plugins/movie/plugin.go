package movie

import (
	"context"
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/jsonexport"
	"github.com/allister/allister/internal/plugins/tmdb"
)

// Options configure the plugin. The URL field exists for tests; empty means
// the production endpoint. The TMDB key is resolved per request, so a key
// saved in the settings takes effect without a restart.
type Options struct {
	HTTP    *http.Client
	TMDBURL string
	TMDBKey tmdb.KeyFunc
}

type moviePlugin struct {
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
	return &moviePlugin{
		sources: map[string]plugin.Source{
			SourceTMDB: &tmdbSource{c: tmdb.New(opts.TMDBURL, key, hc)},
		},
		importers: map[string]plugin.Importer{
			ImporterYamtrack: yamtrackMovieImporter{},
		},
		exporter: jsonexport.New(exportFormat),
	}
}

func (p *moviePlugin) Manifest() plugin.Manifest { return manifest() }

func (p *moviePlugin) Source(id string) plugin.Source { return p.sources[id] }

func (p *moviePlugin) Importer(id string) plugin.Importer { return p.importers[id] }

func (p *moviePlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
