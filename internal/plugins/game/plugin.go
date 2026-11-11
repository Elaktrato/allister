package game

import (
	"context"
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/jsonexport"
)

// Options configure the plugin. The URL fields exist for tests; empty means
// the production endpoint. The credentials are resolved per request, so a key
// saved in the settings takes effect without a restart; a nil key func means
// no credential (the sources then answer plugin.ErrNotConfigured).
type Options struct {
	HTTP *http.Client

	IGDBURL       string
	IGDBTokenURL  string
	SteamAPIURL   string
	SteamStoreURL string

	IGDBClientID     func(context.Context) (string, error)
	IGDBClientSecret func(context.Context) (string, error)
	SteamKey         func(context.Context) (string, error)
}

type gamePlugin struct {
	sources   map[string]plugin.Source
	importers map[string]plugin.Importer
	exporter  plugin.Exporter
}

func New(opts Options) plugin.Plugin {
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	clientID := keyFunc(opts.IGDBClientID)
	clientSecret := keyFunc(opts.IGDBClientSecret)
	steamKey := keyFunc(opts.SteamKey)
	steam := newSteamClient(opts.SteamAPIURL, opts.SteamStoreURL, steamKey, hc)
	return &gamePlugin{
		sources: map[string]plugin.Source{
			SourceIGDB:  &igdbSource{c: newIGDBClient(opts.IGDBURL, opts.IGDBTokenURL, clientID, clientSecret, hc)},
			SourceSteam: &steamSource{c: steam},
		},
		importers: map[string]plugin.Importer{
			ImporterYamtrack: yamtrackGameImporter{},
			ImporterSteam:    steamAccountImporter{c: steam},
		},
		exporter: jsonexport.New(exportFormat),
	}
}

// keyFunc keeps a missing resolver as an empty credential: the source reports
// plugin.ErrNotConfigured instead of panicking.
func keyFunc(f func(context.Context) (string, error)) func(context.Context) (string, error) {
	if f == nil {
		return func(context.Context) (string, error) { return "", nil }
	}
	return f
}

func (p *gamePlugin) Manifest() plugin.Manifest { return manifest() }

func (p *gamePlugin) Source(id string) plugin.Source { return p.sources[id] }

func (p *gamePlugin) Importer(id string) plugin.Importer { return p.importers[id] }

func (p *gamePlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
