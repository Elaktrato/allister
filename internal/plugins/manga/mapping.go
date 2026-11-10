package manga

import (
	"net/url"
	"slices"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// The mapping from the AniList client's types to the plugin contract's data:
// everything is sanitised and guarded against the manifest's ranges, so a
// quirk of the source can never make an item unsaveable.

func sourceOf(s anilist.Source) string {
	if s == "" {
		return SourceID
	}
	return string(s)
}

func titles(ts []anilist.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		if text := plugin.SingleLine(t.Text, maxTitleRunes); text != "" {
			out = append(out, plugin.Title{Lang: t.Lang, Text: text})
		}
	}
	return out
}

func numField(v *int, lo, hi int) (plugin.FieldValue, bool) {
	if v == nil || *v < lo || *v > hi {
		return plugin.FieldValue{}, false
	}
	f := float64(*v)
	return plugin.FieldValue{Num: &f}, true
}

func summaryFields(r anilist.SearchResult) map[string]plugin.FieldValue {
	f := map[string]plugin.FieldValue{}
	if r.Type != "" && slices.Contains(typeValues, r.Type) {
		f["type"] = plugin.FieldValue{Text: r.Type}
	}
	if v, ok := numField(r.Chapters, 1, 100000); ok {
		f["chapters"] = v
	}
	if v, ok := numField(r.Volumes, 1, 10000); ok {
		f["volumes"] = v
	}
	if v, ok := numField(r.Year, 1900, 2100); ok {
		f["year"] = v
	}
	if s := plugin.MultiLine(r.Synopsis, maxTextRunes); s != "" {
		f["synopsis"] = plugin.FieldValue{Text: s}
	}
	return f
}

func toSummary(r anilist.SearchResult) plugin.ItemSummary {
	return plugin.ItemSummary{
		Source:   sourceOf(r.Source),
		ID:       plugin.SingleLine(r.ID, maxIDRunes),
		Kind:     KindManga,
		Titles:   titles(r.Titles),
		Fields:   summaryFields(r),
		CoverURL: plugin.HTTPSURL(r.CoverURL),
	}
}

// ItemData maps the full detail of a manga.
func ItemData(m anilist.Media) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: toSummary(m.SearchResult),
		ExternalURL: plugin.HTTPSURL(m.ExternalURL),
	}
	if mal := plugin.SingleLine(m.MALID, maxIDRunes); mal != "" {
		d.ExternalIDs = map[string]string{"mal": mal}
	}
	if a := authorOf(m.Staff); a != "" {
		d.Fields["author"] = plugin.FieldValue{Text: a}
	}
	if m.StartDate != nil {
		day := plugin.DateOnly(*m.StartDate)
		d.Fields["started_on"] = plugin.FieldValue{Date: &day}
	}
	if m.EndDate != nil {
		day := plugin.DateOnly(*m.EndDate)
		d.Fields["ended_on"] = plugin.FieldValue{Date: &day}
	}
	for _, g := range m.Genres {
		if g = plugin.SingleLine(g, maxTermRunes); g != "" && len(d.Genres) < maxGenres {
			d.Genres = append(d.Genres, g)
		}
	}
	for _, t := range m.Tags {
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
	for _, r := range m.Relations {
		id := plugin.SingleLine(r.ID, maxIDRunes)
		kind := plugin.SingleLine(r.Kind, maxNameRunes)
		if id == "" || kind == "" || len(d.Relations) >= maxRelations {
			continue
		}
		// the related item's own media type decides the kind
		itemKind := KindManga
		if r.MediaType == "ANIME" {
			itemKind = "anime"
		}
		item := plugin.ItemSummary{Source: sourceOf(r.Source), ID: id, Kind: itemKind, Titles: titles(r.Titles),
			URL: relationURL(sourceOf(r.Source), itemKind, id)}
		if ty := plugin.SingleLine(r.Type, maxNameRunes); ty != "" {
			item.Fields = map[string]plugin.FieldValue{"type": {Text: ty}}
		}
		d.Relations = append(d.Relations, plugin.Relation{Kind: kind, Item: item})
	}
	return d
}

// relationURL is the AniList page of a related item.
func relationURL(source, itemKind, id string) string {
	if source != SourceID || id == "" {
		return ""
	}
	section := "manga"
	if itemKind == "anime" {
		section = "anime"
	}
	return plugin.HTTPSURL("https://anilist.co/" + section + "/" + url.PathEscape(id))
}

// authorOf names the first Story (or Story & Art) staff member.
func authorOf(staff []anilist.StaffMember) string {
	for _, s := range staff {
		if strings.Contains(strings.ToLower(s.Role), "story") {
			if n := plugin.SingleLine(s.Name, maxNameRunes); n != "" {
				return n
			}
		}
	}
	return ""
}

// Credits maps the staff to credits: the role is story, art or story-art,
// the source's own role text stays in Detail.
func Credits(staff []anilist.StaffMember) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(staff))
	for _, s := range staff {
		name := plugin.SingleLine(s.Name, maxNameRunes)
		if name == "" || len(out) >= maxCredits {
			continue
		}
		role := "story"
		low := strings.ToLower(s.Role)
		switch {
		case strings.Contains(low, "art") && strings.Contains(low, "story"):
			role = "story-art"
		case strings.Contains(low, "art"):
			role = "art"
		}
		out = append(out, plugin.Credit{Role: role, Detail: plugin.SingleLine(s.Role, 100),
			Person: plugin.PersonRef{Name: name, ImageURL: plugin.HTTPSURL(s.ImageURL), URL: plugin.HTTPSURL(s.URL)}})
	}
	return out
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
			Source: sourceOf(r.Source), ID: sid, Kind: KindManga,
			Titles: titles(r.Titles), CoverURL: plugin.HTTPSURL(r.CoverURL),
		})
	}
	return out
}
