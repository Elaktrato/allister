package manga

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/plugins/jsonexport"
)

// Limits that keep the output inside what the library accepts.
const (
	maxTitleRunes = 300
	maxTextRunes  = 20000
	maxNameRunes  = 300
	maxIDRunes    = 100
	maxTermRunes  = 60
	maxGenres     = 50
	maxTags       = 100
	maxRelations  = 200
	maxRecs       = 200
	maxCredits    = 500
)

// Options configure the plugin (tests inject a provider).
type Options struct {
	// Provider, when set, replaces the AniList client.
	Provider anilist.Provider
	// AniListURL and HTTP configure the client when Provider is nil.
	AniListURL string
	HTTP       *http.Client
	// MALClientID returns the MyAnimeList Client ID ("" = not configured);
	// it is read per request. MALURL (default malDefaultURL) and
	// MALInterval (default one second between requests) are for tests.
	MALClientID func(context.Context) (string, error)
	// Credential, when MALClientID is nil, resolves a named credential field
	// of a source of this plugin (the core's resolver).
	Credential  func(ctx context.Context, sourceID, field string) (string, error)
	MALURL      string
	MALInterval time.Duration
}

type mangaPlugin struct{ source *source }

func newMAL(opts Options) *malClient {
	id := opts.MALClientID
	if id == nil && opts.Credential != nil {
		id = func(ctx context.Context) (string, error) { return opts.Credential(ctx, SourceID, MALClientIDField) }
	}
	if id == nil {
		return nil
	}
	return newMALClient(opts.MALURL, &http.Client{Timeout: malTimeout}, id, opts.MALInterval)
}

// New returns the manga plugin.
func New(opts Options) plugin.Plugin {
	if opts.Provider != nil {
		return &mangaPlugin{source: &source{c: opts.Provider, mal: newMAL(opts)}}
	}
	url := opts.AniListURL
	if url == "" {
		url = anilist.DefaultURL
	}
	return &mangaPlugin{source: &source{c: anilist.New(url, opts.HTTP), mal: newMAL(opts)}}
}

func (p *mangaPlugin) Manifest() plugin.Manifest { return manifest() }

func (p *mangaPlugin) Source(id string) plugin.Source {
	if id == SourceID {
		return p.source
	}
	return nil
}

func (p *mangaPlugin) Importer(id string) plugin.Importer {
	switch id {
	case AccountImp:
		return accountImporter{c: p.source.c}
	case MALImp:
		return malXMLImporter{}
	case ImporterYamtrack:
		return yamtrackMangaImporter{c: p.source.c}
	}
	return nil
}

func (p *mangaPlugin) Exporter(id string) plugin.Exporter {
	if id == ExporterID {
		return jsonexport.New("allister.manga.export")
	}
	return nil
}

// source adapts the shared AniList client to plugin.Source for manga.
type source struct {
	c   anilist.Provider
	mal *malClient // nil when no Client ID source is wired
}

func checkKind(kind string) error {
	if kind != KindManga {
		return fmt.Errorf("manga: unknown kind %q: %w", kind, plugin.ErrNotFound)
	}
	return nil
}

func checkID(id string) error {
	if id == "" || len(id) > 12 {
		return fmt.Errorf("manga: bad id %q: %w", id, plugin.ErrNotFound)
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return fmt.Errorf("manga: bad id %q: %w", id, plugin.ErrNotFound)
		}
	}
	return nil
}

// mapErr maps an AniList client error onto the plugin sentinels, keeping the
// cause (context errors pass through).
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, anilist.ErrBadResponse):
		return fmt.Errorf("%w: %w", plugin.ErrBadResponse, err)
	case errors.Is(err, anilist.ErrNotFound):
		return fmt.Errorf("%w: %w", plugin.ErrNotFound, err)
	case errors.Is(err, anilist.ErrRateLimited):
		return fmt.Errorf("%w: %w", plugin.ErrRateLimited, err)
	default:
		return fmt.Errorf("%w: %w", plugin.ErrUnavailable, err)
	}
}

func (s *source) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	res, err := s.c.Search(ctx, anilist.TypeManga, q)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]plugin.ItemSummary, 0, len(res))
	for _, r := range res {
		out = append(out, toSummary(r))
	}
	return out, nil
}

func (s *source) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if err := checkKind(kind); err != nil {
		return plugin.ItemData{}, err
	}
	if err := checkID(id); err != nil {
		return plugin.ItemData{}, err
	}
	m, err := s.c.Media(ctx, anilist.TypeManga, id)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return s.withMagazine(ctx, ItemData(m)), nil
}

// withMagazine adds the magazine from MyAnimeList when the item has a MAL id
// and a Client ID is configured. A failure never fails the details.
func (s *source) withMagazine(ctx context.Context, d plugin.ItemData) plugin.ItemData {
	mal := d.ExternalIDs["mal"]
	if s.mal == nil || mal == "" {
		return d
	}
	names, err := s.mal.Serialization(ctx, mal)
	if err != nil {
		// not found and an unset Client ID are normal and stay silent; the
		// error strings never contain the Client ID
		if !errors.Is(err, plugin.ErrNotFound) && !errors.Is(err, errMALNotSet) && ctx.Err() == nil {
			log.Printf("manga: magazine from MyAnimeList skipped: %v", err)
		}
		return d
	}
	if m := plugin.SingleLine(strings.Join(names, ", "), maxNameRunes); m != "" {
		d.Fields["magazine"] = plugin.FieldValue{Text: m}
	}
	return d
}

func (s *source) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	if err := checkID(id); err != nil {
		return nil, err
	}
	m, err := s.c.Media(ctx, anilist.TypeManga, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return toRecommendations(m.Recommendations), nil
}

// People returns the staff credits (story, art, ...).
func (s *source) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	if err := checkID(id); err != nil {
		return nil, err
	}
	m, err := s.c.Media(ctx, anilist.TypeManga, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return Credits(m.Staff), nil
}

// Lookup finds a manga by its MyAnimeList id ("mal") or its AniList id.
func (s *source) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if err := checkKind(kind); err != nil {
		return plugin.ItemData{}, err
	}
	if idType == "anilist" {
		return s.Details(ctx, kind, value)
	}
	if idType != "mal" {
		return plugin.ItemData{}, fmt.Errorf("manga: cannot look up a manga by %q ids", idType)
	}
	ml, ok := s.c.(interface {
		MediaByMALID(ctx context.Context, typ, malID string) (anilist.Media, error)
	})
	if !ok {
		return plugin.ItemData{}, errors.New("manga: the provider cannot look up MAL ids")
	}
	m, err := ml.MediaByMALID(ctx, anilist.TypeManga, value)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return s.withMagazine(ctx, ItemData(m)), nil
}
