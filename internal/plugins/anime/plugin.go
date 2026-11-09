// Package anime is the anime plugin of Allister. It is data (a manifest) plus
// three thin adapters that wrap the legacy AniList client and Yamtrack parser
// (they move into this package in a later task): an AniList source, a Yamtrack
// CSV importer and a JSON exporter.
package anime

import (
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/animedef"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/provider/anilist"
)

// Plugin and source ids and the one kind key of this plugin.
const (
	PluginID   = animedef.PluginID
	KindAnime  = animedef.KindAnime
	SourceID   = animedef.SourceID
	ImporterID = animedef.ImporterID
	ExporterID = animedef.ExporterID
)

// Options configures the plugin. The zero value talks to the real AniList.
type Options struct {
	// AniListURL is the AniList GraphQL endpoint; empty means anilist.DefaultURL.
	AniListURL string
	// HTTP is the client used for AniList; nil gets the client's default.
	HTTP *http.Client
	// Provider, when set, replaces the AniList client (AniListURL and HTTP
	// are then ignored): the server shares its one client this way, and tests
	// inject a fake. Lookup needs a provider with an AnimeByMALID method.
	Provider provider.Provider

	// now is the clock of the source's one-shot Details reuse (tests);
	// nil means time.Now.
	now func() time.Time
}

type animePlugin struct {
	source *source
}

// New returns the anime plugin.
func New(opts Options) plugin.Plugin {
	now := opts.now
	if now == nil {
		now = time.Now
	}
	if opts.Provider != nil {
		return &animePlugin{source: &source{c: opts.Provider, now: now}}
	}
	url := opts.AniListURL
	if url == "" {
		url = anilist.DefaultURL
	}
	return &animePlugin{source: &source{c: anilist.New(url, opts.HTTP), now: now}}
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
