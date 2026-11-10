package anime

import (
	"net/url"
	"slices"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// This file is the ONE mapping from the AniList client's types (package
// anilist) to the plugin contract's data. It sanitises everything it returns
// (plugin.SingleLine, MultiLine, HTTPSURL), guards fields against the
// manifest's ranges and caps lists, so a quirk of the AniList data can never
// make an item or a cast unsaveable in the library.

// Limits that keep the output inside what the library accepts.
const (
	maxTitleRunes  = 300
	maxTextRunes   = 20000
	maxTermRunes   = 60
	maxNameRunes   = 300
	maxIDRunes     = 100
	maxDetailRunes = 100
	maxGenres      = 50
	maxTags        = 100
	maxRelations   = 200
	maxRecs        = 200
	maxCredits     = 500
)

// Credit roles of the anime plugin.
const (
	roleCharacter  = "character"
	roleVoiceActor = "Voice actor"
)

// cleaner holds the text and value rules of one mapping mode.
type cleaner struct {
	single func(s string, maxRunes int) string
	multi  func(s string, maxRunes int) string
	url    func(s string) string
	guard  bool // drop values outside the manifest's ranges and enum
}

// fromSource is the mapping mode of source data: clean and guard everything.
var fromSource = cleaner{single: plugin.SingleLine, multi: plugin.MultiLine, url: plugin.HTTPSURL, guard: true}

// sourceOf is the item source: the provider's source, AniList when unset.
func sourceOf(s anilist.Source) string {
	if s == "" {
		return SourceID
	}
	return string(s)
}

// sourceTitles cleans the titles and drops the empty ones (nil when none is left).
func sourceTitles(ts []anilist.Title) []plugin.Title { return fromSource.titles(ts) }

func (c cleaner) titles(ts []anilist.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		if text := c.single(t.Text, maxTitleRunes); text != "" {
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

func (c cleaner) summaryFields(r anilist.SearchResult) map[string]plugin.FieldValue {
	f := map[string]plugin.FieldValue{}
	if r.Type != "" && (!c.guard || slices.Contains(typeValues, r.Type)) {
		f["type"] = plugin.FieldValue{Text: r.Type}
	}
	if v, ok := c.numField(r.Episodes, 1, 10000); ok {
		f["episodes"] = v
	}
	if v, ok := c.numField(r.Year, 1900, 2100); ok {
		f["year"] = v
	}
	if s := c.single(r.Studio, maxNameRunes); s != "" {
		f["studio"] = plugin.FieldValue{Text: s}
	}
	if s := c.multi(r.Synopsis, maxTextRunes); s != "" {
		f["synopsis"] = plugin.FieldValue{Text: s}
	}
	return f
}

// toSummary maps a search result.
func toSummary(r anilist.SearchResult) plugin.ItemSummary { return fromSource.summary(r) }

func (c cleaner) summary(r anilist.SearchResult) plugin.ItemSummary {
	return plugin.ItemSummary{
		Source:   sourceOf(r.Source),
		ID:       c.single(r.ID, maxIDRunes),
		Kind:     KindAnime,
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
func ItemData(a anilist.Media) plugin.ItemData { return fromSource.itemData(a) }

func (c cleaner) itemData(a anilist.Media) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: c.summary(a.SearchResult),
		ExternalURL: c.url(a.ExternalURL),
	}
	if mal := c.single(a.MALID, maxIDRunes); mal != "" {
		d.ExternalIDs = map[string]string{"mal": mal}
	}
	if s := c.single(a.SourceMaterial, maxNameRunes); s != "" {
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
		if g = c.single(g, maxTermRunes); g != "" && (!c.guard || len(d.Genres) < maxGenres) {
			d.Genres = append(d.Genres, g)
		}
	}
	for _, t := range a.Tags {
		name := c.single(t.Name, maxTermRunes)
		if name == "" || c.guard && len(d.Tags) >= maxTags {
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
		id := c.single(r.ID, maxIDRunes)
		kind := c.single(r.Kind, maxNameRunes)
		if c.guard && (id == "" || kind == "" || len(d.Relations) >= maxRelations) {
			continue
		}
		itemKind := KindAnime
		if kind == "source" {
			itemKind = "manga"
		}
		item := plugin.ItemSummary{Source: sourceOf(r.Source), ID: id, Kind: itemKind, Titles: c.titles(r.Titles),
			URL: relationURL(sourceOf(r.Source), itemKind, id)}
		if ty := c.single(r.Type, maxNameRunes); ty != "" {
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
	if source != SourceID || id == "" {
		return ""
	}
	section := "anime"
	if itemKind == "manga" {
		section = "manga"
	}
	return plugin.HTTPSURL("https://anilist.co/" + section + "/" + url.PathEscape(id))
}

// toRecommendations maps recommendations, dropping those without an id.
func toRecommendations(rs []anilist.Recommendation) []plugin.ItemSummary {
	out := make([]plugin.ItemSummary, 0, len(rs))
	for _, r := range rs {
		sid := plugin.SingleLine(r.ID, maxIDRunes)
		if sid == "" || len(out) == maxRecs {
			continue
		}
		out = append(out, plugin.ItemSummary{
			Source: sourceOf(r.Source), ID: sid, Kind: KindAnime,
			Titles: sourceTitles(r.Titles), CoverURL: plugin.HTTPSURL(r.CoverURL),
		})
	}
	return out
}

// toPersonRef maps a person; ok is false when it has no usable id or name. URL
// and image are cleaned with plugin.HTTPSURL ("" when not an http(s) URL).
func toPersonRef(p anilist.Person) (plugin.PersonRef, bool) {
	r := plugin.PersonRef{
		ID:       plugin.SingleLine(p.ID, maxIDRunes),
		Name:     plugin.SingleLine(p.Name, maxNameRunes),
		ImageURL: plugin.HTTPSURL(p.ImageURL),
		URL:      plugin.HTTPSURL(p.URL),
	}
	return r, r.ID != "" && r.Name != ""
}

// Credits maps a cast: one credit per character (role "character", the cast
// role such as MAIN as Detail) with its voice actor as the counterpart
// ("Voice actor"). A character without a usable id or name is dropped; an
// unusable voice actor only loses the counterpart. At most maxCredits.
func Credits(cast []anilist.CastMember) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cast))
	for _, m := range cast {
		ch, ok := toPersonRef(m.Character)
		if !ok || len(out) == maxCredits {
			continue
		}
		c := plugin.Credit{Role: roleCharacter, Detail: plugin.SingleLine(m.Role, maxDetailRunes), Person: ch}
		if m.Actor != nil {
			if a, ok := toPersonRef(*m.Actor); ok {
				c.With, c.WithRole = &a, roleVoiceActor
			}
		}
		out = append(out, c)
	}
	return out
}
