package tv

import (
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/jsonexport"
)

// Options configure the plugin. The URL fields exist for tests; empty means
// the production endpoint. TMDB is only declared when its key is set.
type Options struct {
	HTTP      *http.Client
	TVmazeURL string
	TMDBURL   string
	TMDBKey   string
}

type tvPlugin struct {
	opts      Options
	sources   map[string]plugin.Source
	importers map[string]plugin.Importer
	exporter  plugin.Exporter
}

func New(opts Options) plugin.Plugin {
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	sources := map[string]plugin.Source{
		SourceTVmaze: &tvmazeSource{c: newTVmazeClient(opts.TVmazeURL, hc)},
	}
	if opts.TMDBKey != "" {
		sources[SourceTMDB] = &tmdbSource{c: newTMDBClient(opts.TMDBURL, opts.TMDBKey, hc)}
	}
	return &tvPlugin{
		opts:    opts,
		sources: sources,
		importers: map[string]plugin.Importer{
			ImporterYamtrack: yamtrackTVImporter{},
		},
		exporter: jsonexport.New(exportFormat),
	}
}

func (p *tvPlugin) Manifest() plugin.Manifest { return manifest(p.opts) }

func (p *tvPlugin) Source(id string) plugin.Source { return p.sources[id] }

func (p *tvPlugin) Importer(id string) plugin.Importer { return p.importers[id] }

func (p *tvPlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
