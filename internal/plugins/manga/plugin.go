package manga

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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
}

type mangaPlugin struct{ source *source }

// New returns the manga plugin.
func New(opts Options) plugin.Plugin {
	if opts.Provider != nil {
		return &mangaPlugin{source: &source{c: opts.Provider}}
	}
	url := opts.AniListURL
	if url == "" {
		url = anilist.DefaultURL
	}
	return &mangaPlugin{source: &source{c: anilist.New(url, opts.HTTP)}}
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
type source struct{ c anilist.Provider }

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
	return ItemData(m), nil
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
	return ItemData(m), nil
}
