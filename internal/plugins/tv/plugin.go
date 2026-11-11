package tv

import (
	"net/http"

	"github.com/allister/allister/internal/plugin"
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

func New(opts Options) plugin.Plugin { return &tvPlugin{opts: opts} }

func (p *tvPlugin) Manifest() plugin.Manifest { return manifest(p.opts) }

func (p *tvPlugin) Source(id string) plugin.Source { return p.sources[id] }

func (p *tvPlugin) Importer(id string) plugin.Importer { return p.importers[id] }

func (p *tvPlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
