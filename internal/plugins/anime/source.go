package anime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/provider/anilist"
)

// Limits that keep plugin output inside what the library accepts.
const (
	maxTitleRunes = 300
	maxTextRunes  = 20000
	maxTermRunes  = 60
	maxNameRunes  = 300
	maxIDRunes    = 100
	maxGenres     = 50
	maxTags       = 100
	maxRelations  = 200
	maxCredits    = 500
)

// source adapts the legacy AniList client to plugin.Source. Everything it
// returns is sanitised (plugin.SingleLine, MultiLine, HTTPSURL), so a quirk
// of the AniList data can never make an item unsaveable in the library.
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
		out = append(out, summary(r))
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
	return itemData(a), nil
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
	out := make([]plugin.ItemSummary, 0, len(a.Recommendations))
	for _, r := range a.Recommendations {
		sid := plugin.SingleLine(r.ID, maxIDRunes)
		if sid == "" || len(out) == maxRelations {
			continue
		}
		out = append(out, plugin.ItemSummary{
			Source: SourceID, ID: sid, Kind: KindAnime,
			Titles: mapTitles(r.Titles), CoverURL: plugin.HTTPSURL(r.CoverURL),
		})
	}
	return out, nil
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
	out := make([]plugin.Credit, 0, len(cast))
	for _, m := range cast {
		ch, ok := personRef(m.Character)
		if !ok || len(out) == maxCredits {
			continue
		}
		c := plugin.Credit{Role: "character", Person: ch}
		if m.Actor != nil {
			if a, ok := personRef(*m.Actor); ok {
				c.With, c.WithRole = &a, "Voice actor"
			}
		}
		out = append(out, c)
	}
	return out, nil
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
	return itemData(a), nil
}

func personRef(p provider.Person) (plugin.PersonRef, bool) {
	r := plugin.PersonRef{
		ID:       plugin.SingleLine(p.ID, maxIDRunes),
		Name:     plugin.SingleLine(p.Name, maxNameRunes),
		ImageURL: plugin.HTTPSURL(p.ImageURL),
		URL:      plugin.HTTPSURL(p.URL),
	}
	return r, r.ID != "" && r.Name != ""
}

func mapTitles(ts []provider.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		if text := plugin.SingleLine(t.Text, maxTitleRunes); text != "" {
			out = append(out, plugin.Title{Lang: t.Lang, Text: text})
		}
	}
	return out
}

// numField returns a number field value, or false when v is outside the range
// the manifest declares (so the value never makes the item unsaveable).
func numField(v *int, lo, hi int) (plugin.FieldValue, bool) {
	if v == nil || *v < lo || *v > hi {
		return plugin.FieldValue{}, false
	}
	f := float64(*v)
	return plugin.FieldValue{Num: &f}, true
}

func summaryFields(r provider.SearchResult) map[string]plugin.FieldValue {
	f := map[string]plugin.FieldValue{}
	if slices.Contains(typeValues, r.Type) {
		f["type"] = plugin.FieldValue{Text: r.Type}
	}
	if v, ok := numField(r.Episodes, 1, 10000); ok {
		f["episodes"] = v
	}
	if v, ok := numField(r.Year, 1900, 2100); ok {
		f["year"] = v
	}
	if s := plugin.SingleLine(r.Studio, maxNameRunes); s != "" {
		f["studio"] = plugin.FieldValue{Text: s}
	}
	if s := plugin.MultiLine(r.Synopsis, maxTextRunes); s != "" {
		f["synopsis"] = plugin.FieldValue{Text: s}
	}
	return f
}

func summary(r provider.SearchResult) plugin.ItemSummary {
	return plugin.ItemSummary{
		Source:   SourceID,
		ID:       plugin.SingleLine(r.ID, maxIDRunes),
		Kind:     KindAnime,
		Titles:   mapTitles(r.Titles),
		Fields:   summaryFields(r),
		CoverURL: plugin.HTTPSURL(r.CoverURL),
	}
}

func itemData(a provider.Anime) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: summary(a.SearchResult),
		ExternalURL: plugin.HTTPSURL(a.ExternalURL),
	}
	if mal := plugin.SingleLine(a.MALID, maxIDRunes); mal != "" {
		d.ExternalIDs = map[string]string{"mal": mal}
	}
	if s := plugin.SingleLine(a.SourceMaterial, maxNameRunes); s != "" {
		d.Fields["source_material"] = plugin.FieldValue{Text: s}
	}
	if a.StartDate != nil {
		day := plugin.DateOnly(*a.StartDate)
		d.Fields["started_on"] = plugin.FieldValue{Date: &day}
	}
	if a.EndDate != nil {
		day := plugin.DateOnly(*a.EndDate)
		d.Fields["ended_on"] = plugin.FieldValue{Date: &day}
	}
	for _, g := range a.Genres {
		if g = plugin.SingleLine(g, maxTermRunes); g != "" && len(d.Genres) < maxGenres {
			d.Genres = append(d.Genres, g)
		}
	}
	for _, t := range a.Tags {
		name := plugin.SingleLine(t.Name, maxTermRunes)
		if name == "" || len(d.Tags) >= maxTags {
			continue
		}
		tag := plugin.Tag{Name: name, Spoiler: t.Spoiler}
		if t.Rank != nil && *t.Rank >= 0 && *t.Rank <= 100 {
			r := *t.Rank
			tag.Rank = &r
		}
		d.Tags = append(d.Tags, tag)
	}
	for _, r := range a.Relations {
		id := plugin.SingleLine(r.ID, maxIDRunes)
		if id == "" || len(d.Relations) >= maxRelations {
			continue
		}
		kind, itemKind := r.Kind, KindAnime
		if r.Kind == "source" {
			itemKind = "manga"
		}
		item := plugin.ItemSummary{Source: SourceID, ID: id, Kind: itemKind, Titles: mapTitles(r.Titles)}
		if ty := plugin.SingleLine(r.Type, maxNameRunes); ty != "" {
			item.Fields = map[string]plugin.FieldValue{"type": {Text: ty}}
		}
		d.Relations = append(d.Relations, plugin.Relation{Kind: plugin.SingleLine(kind, maxNameRunes), Item: item})
	}
	return d
}
