// Package anime is the anime plugin of Allister. It is data (a manifest) plus
// three thin adapters that wrap the legacy AniList client and Yamtrack parser
// (they move into this package in a later task): an AniList source, a Yamtrack
// CSV importer and a JSON exporter.
package anime

import (
	"net/http"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/provider/anilist"
)

// Plugin and source ids and the one kind key of this plugin.
const (
	PluginID   = "anime"
	KindAnime  = "anime"
	SourceID   = "anilist"
	ImporterID = "yamtrack"
	ExporterID = "json"
)

// Options configures the plugin. The zero value talks to the real AniList.
type Options struct {
	// AniListURL is the AniList GraphQL endpoint; empty means anilist.DefaultURL.
	AniListURL string
	// HTTP is the client used for AniList; nil gets the client's default.
	HTTP *http.Client
}

type animePlugin struct {
	source *source
}

// New returns the anime plugin.
func New(opts Options) plugin.Plugin {
	url := opts.AniListURL
	if url == "" {
		url = anilist.DefaultURL
	}
	return &animePlugin{source: &source{c: anilist.New(url, opts.HTTP)}}
}

func (p *animePlugin) Manifest() plugin.Manifest { return manifest() }

func (p *animePlugin) Source(id string) plugin.Source {
	if id == SourceID {
		return p.source
	}
	return nil
}

func (p *animePlugin) Importer(id string) plugin.Importer {
	if id == ImporterID {
		return yamtrackImporter{}
	}
	return nil
}

func (p *animePlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterID {
		return jsonExporter{}
	}
	return nil
}
