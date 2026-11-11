package book

import (
	"net/http"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/plugins/jsonexport"
)

// Options configure the plugin. The URL fields exist for tests; empty means
// the production endpoint.
type Options struct {
	HTTP           *http.Client
	OpenLibraryURL string
	GoogleBooksURL string
	AniListURL     string
	GoogleBooksKey string
}

type booksPlugin struct {
	sources   map[string]plugin.Source
	importers map[string]plugin.Importer
	exporter  plugin.Exporter
}

// New returns the Books plugin. The source and importer implementations are
// added by the files in this package; this constructor only wires the ids the
// manifest declares.
func New(opts Options) plugin.Plugin {
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	anilistURL := opts.AniListURL
	if anilistURL == "" {
		anilistURL = anilist.DefaultURL
	}
	ac := anilist.New(anilistURL, hc)
	return &booksPlugin{
		sources: map[string]plugin.Source{
			SourceOpenLibrary: &openLibrarySource{c: newOpenLibraryClient(opts.OpenLibraryURL, hc)},
			SourceGoogleBooks: &googleBooksSource{c: newGoogleBooksClient(opts.GoogleBooksURL, opts.GoogleBooksKey, hc)},
			SourceAniList:     &anilistSource{c: ac},
		},
		importers: map[string]plugin.Importer{
			ImporterGoodreads: goodreadsImporter{},
			ImporterAniList:   anilistAccountImporter{c: ac},
		},
		exporter: jsonexport.New(exportFormat),
	}
}

func (p *booksPlugin) Manifest() plugin.Manifest { return manifest() }

func (p *booksPlugin) Source(id string) plugin.Source {
	return p.sources[id]
}

func (p *booksPlugin) Importer(id string) plugin.Importer {
	return p.importers[id]
}

func (p *booksPlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterJSON {
		return p.exporter
	}
	return nil
}
