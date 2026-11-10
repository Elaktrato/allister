package anime

import (
	"context"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// Limits of the importer (the source's limits live in mapping.go).
const (
	// maxCommentRunes mirrors the library's entry comment limit
	// (library.MaxCommentRunes, internal/library/entries.go).
	maxCommentRunes = 2000
)

// source adapts the AniList client to plugin.Source. Everything it
// returns goes through the mapping (mapping.go), which sanitises it, so a quirk of the
// AniList data can never make an item unsaveable in the library.
type source struct {
	c anilist.Provider
}

// malLookup is the optional MAL id lookup of a provider (anilist.Client).
type malLookup interface {
	AnimeByMALID(ctx context.Context, malID string) (anilist.Anime, error)
}

// maxIDDigits is the longest AniList id accepted.
const maxIDDigits = 12

// checkID refuses an id that is not a run of 1 to 12 ASCII digits without a
// request: AniList ids are numbers.
func checkID(id string) error {
	if id == "" || len(id) > maxIDDigits {
		return fmt.Errorf("anime: bad id %q: %w", id, plugin.ErrNotFound)
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return fmt.Errorf("anime: bad id %q: %w", id, plugin.ErrNotFound)
		}
	}
	return nil
}

func checkKind(kind string) error {
	if kind != KindAnime {
		return fmt.Errorf("anime: unknown kind %q: %w", kind, plugin.ErrNotFound)
	}
	return nil
}

// mapErr maps an AniList client error onto the plugin sentinels while keeping
// the cause: errors.Is matches both. Context errors pass through unchanged.
// anilist.ErrBadResponse (an answer that a retry would not change) stays a
// plain error, so an import records the row as failed and moves on instead
// of retrying it as unavailable.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, anilist.ErrBadResponse):
		return fmt.Errorf("anime: %w", err)
	case errors.Is(err, anilist.ErrNotFound):
		target = plugin.ErrNotFound
	case errors.Is(err, anilist.ErrRateLimited):
		target = plugin.ErrRateLimited
	default:
		target = plugin.ErrUnavailable
	}
	return fmt.Errorf("%w: %w", target, err)
}

func (s *source) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	res, err := s.c.Search(ctx, q)
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
	a, err := s.c.Anime(ctx, id)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return ItemData(a), nil
}

// Recommendations reads the recommendations of the client's Anime call: one
// request of its own (the overview loads recommendations separately, late).
func (s *source) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	if err := checkID(id); err != nil {
		return nil, err
	}
	a, err := s.c.Anime(ctx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return toRecommendations(a.Recommendations), nil
}

// People returns the characters with their first Japanese voice actor.
func (s *source) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	if err := checkID(id); err != nil {
		return nil, err
	}
	cast, err := s.c.Cast(ctx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return Credits(cast), nil
}

// Lookup finds an anime by external id; only the MyAnimeList id ("mal") is
// supported.
func (s *source) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if err := checkKind(kind); err != nil {
		return plugin.ItemData{}, err
	}
	// An id type the source cannot look up fails alike for every row: a plain
	// error (the import fails the row), neither not found nor unavailable.
	if idType != "mal" {
		return plugin.ItemData{}, fmt.Errorf("anime: cannot look up an anime by %q ids", idType)
	}
	ml, ok := s.c.(malLookup)
	if !ok {
		return plugin.ItemData{}, errors.New("anime: the provider cannot look up MAL ids")
	}
	a, err := ml.AnimeByMALID(ctx, value)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return ItemData(a), nil
}
