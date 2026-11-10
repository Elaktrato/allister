// Package anime is the anime plugin of Allister. It is data (a manifest) plus
// three thin adapters: an AniList source over its AniList client (package
// anilist), a Yamtrack CSV importer over its parser (package yamtrack) and a
// JSON exporter.
package anime

import (
	"net/http"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// Options configures the plugin. The zero value talks to the real AniList.
type Options struct {
	// AniListURL is the AniList GraphQL endpoint; empty means anilist.DefaultURL.
	AniListURL string
	// HTTP is the client used for AniList; nil gets the client's default.
	HTTP *http.Client
	// Provider, when set, replaces the AniList client (AniListURL and HTTP
	// are then ignored): tests inject a fake this way. Lookup needs a
	// provider with an AnimeByMALID method.
	Provider anilist.Provider
}

type animePlugin struct {
	source *source
}

// New returns the anime plugin.
func New(opts Options) plugin.Plugin {
	if opts.Provider != nil {
		return &animePlugin{source: &source{c: opts.Provider}}
	}
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
	switch id {
	case ImporterID:
		return yamtrackImporter{}
	case AniListImporter:
		return accountImporter{c: p.source.c}
	case MALImporter:
		return malXMLImporter{}
	}
	return nil
}

func (p *animePlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterID {
		return jsonExporter{}
	}
	return nil
}
