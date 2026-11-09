package anime

import (
	"context"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/animemap"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/provider/anilist"
)

// Limits of the importer (the source's limits live in animemap).
const (
	maxTitleRunes = animemap.MaxTitleRunes
	// maxCommentRunes mirrors the library's entry comment limit
	// (library.maxCommentRunes, internal/library/entries.go).
	maxCommentRunes = 2000
)

// source adapts the legacy AniList client to plugin.Source. Everything it
// returns goes through animemap, which sanitises it, so a quirk of the
// AniList data can never make an item unsaveable in the library.
type source struct {
	c *anilist.Client
}

func checkKind(kind string) error {
	if kind != KindAnime {
		return fmt.Errorf("anime: unknown kind %q: %w", kind, plugin.ErrNotFound)
	}
	return nil
}

// mapErr maps a legacy provider error onto the plugin sentinels while keeping
// the cause: errors.Is matches both. Context errors pass through unchanged.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, provider.ErrNotFound):
		target = plugin.ErrNotFound
	case errors.Is(err, provider.ErrRateLimited):
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
		out = append(out, animemap.Summary(r))
	}
	return out, nil
}

func (s *source) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if err := checkKind(kind); err != nil {
		return plugin.ItemData{}, err
	}
	a, err := s.c.Anime(ctx, id)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return animemap.ItemData(a), nil
}

// Recommendations reads the recommendations of the legacy Anime call, so it
// costs the same single request as Details until a later task splits the
// query.
func (s *source) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	a, err := s.c.Anime(ctx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return animemap.Recommendations(a.Recommendations), nil
}

// People returns the characters with their first Japanese voice actor.
func (s *source) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	cast, err := s.c.Cast(ctx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return animemap.Credits(cast), nil
}

// Lookup finds an anime by external id; only the MyAnimeList id ("mal") is
// supported.
func (s *source) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if err := checkKind(kind); err != nil {
		return plugin.ItemData{}, err
	}
	if idType != "mal" {
		return plugin.ItemData{}, fmt.Errorf("anime: cannot look up by %q: %w", idType, plugin.ErrNotFound)
	}
	a, err := s.c.AnimeByMALID(ctx, value)
	if err != nil {
		return plugin.ItemData{}, mapErr(err)
	}
	return animemap.ItemData(a), nil
}
