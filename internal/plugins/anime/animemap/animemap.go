// Package animemap is the ONE mapping from the legacy provider types
// (internal/provider) to the plugin contract's data for the anime plugin. For
// data from a source it sanitises everything it returns (plugin.SingleLine,
// MultiLine, HTTPSURL), guards fields against the manifest's ranges and caps
// lists, so a quirk of the data can never make an item or a cast unsaveable
// in the library. For what a user typed (UserItemData) the same mapping runs
// without any rewriting or dropping: the library validates and rejects.
//
// Both the anime plugin's AniList source and the legacy store adapter
// (internal/store) use it. It imports only provider, plugin and animedef, so
// the store can use it without an import cycle.
package animemap

import (
	"net/url"
	"slices"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime/animedef"
	"github.com/allister/allister/internal/provider"
)

// Limits that keep the output inside what the library accepts.
const (
	MaxTitleRunes  = 300
	MaxTextRunes   = 20000
	MaxTermRunes   = 60
	MaxNameRunes   = 300
	MaxIDRunes     = 100
	MaxDetailRunes = 100
	MaxGenres      = 50
	MaxTags        = 100
	MaxRelations   = 200
	MaxRecs        = 200
	MaxCredits     = 500
)

// Credit roles of the anime plugin.
const (
	RoleCharacter  = "character"
	RoleVoiceActor = "Voice actor"
)

// cleaner holds the text and value rules of one mapping mode.
type cleaner struct {
	single func(s string, maxRunes int) string
	multi  func(s string, maxRunes int) string
	url    func(s string) string
	guard  bool // drop values outside the manifest's ranges and enum
}

var (
	fromSource = cleaner{single: plugin.SingleLine, multi: plugin.MultiLine, url: plugin.HTTPSURL, guard: true}
	fromUser   = cleaner{single: keep, multi: keep, url: func(s string) string { return s }}
)

func keep(s string, _ int) string { return s }

// sourceOf is the item source: the provider's source, AniList when unset.
func sourceOf(s provider.Source) string {
	if s == "" {
		return animedef.SourceID
	}
	return string(s)
}

// Titles cleans the titles and drops the empty ones (nil when none is left).
func Titles(ts []provider.Title) []plugin.Title { return fromSource.titles(ts) }

func (c cleaner) titles(ts []provider.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		if text := c.single(t.Text, MaxTitleRunes); text != "" {
			out = append(out, plugin.Title{Lang: t.Lang, Text: text})
		}
	}
	return out
}

// numField returns a number field value, or false when v is outside the range
// the manifest declares (so the value never makes the item unsaveable).
func (c cleaner) numField(v *int, lo, hi int) (plugin.FieldValue, bool) {
	if v == nil || c.guard && (*v < lo || *v > hi) {
		return plugin.FieldValue{}, false
	}
	f := float64(*v)
	return plugin.FieldValue{Num: &f}, true
}

func (c cleaner) summaryFields(r provider.SearchResult) map[string]plugin.FieldValue {
	f := map[string]plugin.FieldValue{}
	if r.Type != "" && (!c.guard || slices.Contains(animedef.TypeValues(), r.Type)) {
		f["type"] = plugin.FieldValue{Text: r.Type}
	}
	if v, ok := c.numField(r.Episodes, 1, 10000); ok {
		f["episodes"] = v
	}
	if v, ok := c.numField(r.Year, 1900, 2100); ok {
		f["year"] = v
	}
	if s := c.single(r.Studio, MaxNameRunes); s != "" {
		f["studio"] = plugin.FieldValue{Text: s}
	}
	if s := c.multi(r.Synopsis, MaxTextRunes); s != "" {
		f["synopsis"] = plugin.FieldValue{Text: s}
	}
	return f
}

// Summary maps a search result.
func Summary(r provider.SearchResult) plugin.ItemSummary { return fromSource.summary(r) }

func (c cleaner) summary(r provider.SearchResult) plugin.ItemSummary {
	return plugin.ItemSummary{
		Source:   sourceOf(r.Source),
		ID:       c.single(r.ID, MaxIDRunes),
		Kind:     animedef.KindAnime,
		Titles:   c.titles(r.Titles),
		Fields:   c.summaryFields(r),
		CoverURL: c.url(r.CoverURL),
	}
}

// ItemData maps the full detail of an anime: summary fields, source material,
// dates, the MAL id (external id "mal"), genres, tags and relations (a
// "source" relation points at a manga, every other one at an anime;
// relations without an id or a kind are dropped). Recommendations are not
// part of it (see Recommendations).
func ItemData(a provider.Anime) plugin.ItemData { return fromSource.itemData(a) }

// UserItemData is ItemData for what a user typed (a manual entry): the same
// mapping, but nothing is rewritten, truncated or dropped, so the library
// rejects invalid input instead of the input being silently changed.
func UserItemData(a provider.Anime) plugin.ItemData { return fromUser.itemData(a) }

func (c cleaner) itemData(a provider.Anime) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: c.summary(a.SearchResult),
		ExternalURL: c.url(a.ExternalURL),
	}
	if mal := c.single(a.MALID, MaxIDRunes); mal != "" {
		d.ExternalIDs = map[string]string{"mal": mal}
	}
	if s := c.single(a.SourceMaterial, MaxNameRunes); s != "" {
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
		if g = c.single(g, MaxTermRunes); g != "" && (!c.guard || len(d.Genres) < MaxGenres) {
			d.Genres = append(d.Genres, g)
		}
	}
	for _, t := range a.Tags {
		name := c.single(t.Name, MaxTermRunes)
		if name == "" || c.guard && len(d.Tags) >= MaxTags {
			continue
		}
		tag := plugin.Tag{Name: name, Spoiler: t.Spoiler}
		if t.Rank != nil && (!c.guard || *t.Rank >= 0 && *t.Rank <= 100) {
			r := *t.Rank
			tag.Rank = &r
		}
		d.Tags = append(d.Tags, tag)
	}
	for _, r := range a.Relations {
		id := c.single(r.ID, MaxIDRunes)
		kind := c.single(r.Kind, MaxNameRunes)
		if c.guard && (id == "" || kind == "" || len(d.Relations) >= MaxRelations) {
			continue
		}
		itemKind := animedef.KindAnime
		if kind == "source" {
			itemKind = "manga"
		}
		item := plugin.ItemSummary{Source: sourceOf(r.Source), ID: id, Kind: itemKind, Titles: c.titles(r.Titles),
			URL: relationURL(sourceOf(r.Source), itemKind, id)}
		if ty := c.single(r.Type, MaxNameRunes); ty != "" {
			item.Fields = map[string]plugin.FieldValue{"type": {Text: ty}}
		}
		d.Relations = append(d.Relations, plugin.Relation{Kind: kind, Item: item})
	}
	return d
}

// relationURL is the AniList page of a related item ("" for another source or
// no id): /manga/{id} for a manga, /anime/{id} otherwise. The id is path
// escaped and the result cleaned by plugin.HTTPSURL.
func relationURL(source, itemKind, id string) string {
	if source != animedef.SourceID || id == "" {
		return ""
	}
	section := "anime"
	if itemKind == "manga" {
		section = "manga"
	}
	return plugin.HTTPSURL("https://anilist.co/" + section + "/" + url.PathEscape(id))
}

// Recommendations maps recommendations, dropping those without an id.
func Recommendations(rs []provider.Recommendation) []plugin.ItemSummary {
	out := make([]plugin.ItemSummary, 0, len(rs))
	for _, r := range rs {
		sid := plugin.SingleLine(r.ID, MaxIDRunes)
		if sid == "" || len(out) == MaxRecs {
			continue
		}
		out = append(out, plugin.ItemSummary{
			Source: sourceOf(r.Source), ID: sid, Kind: animedef.KindAnime,
			Titles: Titles(r.Titles), CoverURL: plugin.HTTPSURL(r.CoverURL),
		})
	}
	return out
}

// PersonRef maps a person; ok is false when it has no usable id or name. URL
// and image are cleaned with plugin.HTTPSURL ("" when not an http(s) URL).
func PersonRef(p provider.Person) (plugin.PersonRef, bool) {
	r := plugin.PersonRef{
		ID:       plugin.SingleLine(p.ID, MaxIDRunes),
		Name:     plugin.SingleLine(p.Name, MaxNameRunes),
		ImageURL: plugin.HTTPSURL(p.ImageURL),
		URL:      plugin.HTTPSURL(p.URL),
	}
	return r, r.ID != "" && r.Name != ""
}

// Credits maps a cast: one credit per character (role "character", the cast
// role such as MAIN as Detail) with its voice actor as the counterpart
// ("Voice actor"). A character without a usable id or name is dropped; an
// unusable voice actor only loses the counterpart. At most MaxCredits.
func Credits(cast []provider.CastMember) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cast))
	for _, m := range cast {
		ch, ok := PersonRef(m.Character)
		if !ok || len(out) == MaxCredits {
			continue
		}
		c := plugin.Credit{Role: RoleCharacter, Detail: plugin.SingleLine(m.Role, MaxDetailRunes), Person: ch}
		if m.Actor != nil {
			if a, ok := PersonRef(*m.Actor); ok {
				c.With, c.WithRole = &a, RoleVoiceActor
			}
		}
		out = append(out, c)
	}
	return out
}
