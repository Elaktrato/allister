package anilist

import (
	"regexp"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// The mapping of the AniList extras the anime, manga and light-novel kinds
// share: release status, country, adult flag, community score, popularity,
// synonyms as alternative titles, streaming links, banner and trailer. Each
// plugin calls AddExtras from its ItemData mapping; season and studios are
// anime only and mapped by the anime plugin (SeasonValue).

// Limits of the extras, inside what the library accepts.
const (
	maxCountryRunes   = 8
	maxAltTitles      = 20
	maxAltTitleRunes  = 300
	maxLinks          = 20
	maxLinkLabelRunes = 100
	maxPopularity     = 100000000
)

// ReleaseStatusValues are the values of the release_status enum field.
var ReleaseStatusValues = []string{"finished", "releasing", "upcoming", "hiatus", "cancelled"}

// SeasonValues are the values of the anime season enum field.
var SeasonValues = []string{"winter", "spring", "summer", "fall"}

// AdultValues are the values of the adult enum field.
var AdultValues = []string{"yes", "no"}

var releaseStatuses = map[string]string{
	"FINISHED": "finished", "RELEASING": "releasing", "NOT_YET_RELEASED": "upcoming",
	"HIATUS": "hiatus", "CANCELLED": "cancelled",
}

var seasons = map[string]string{"WINTER": "winter", "SPRING": "spring", "SUMMER": "summer", "FALL": "fall"}

// ReleaseStatusValue maps AniList's media status to a release_status value
// ("" for an unknown one).
func ReleaseStatusValue(s string) string { return releaseStatuses[strings.TrimSpace(s)] }

// SeasonValue maps AniList's season to a season value ("" for an unknown one).
func SeasonValue(s string) string { return seasons[strings.TrimSpace(s)] }

// AdultValue is the adult field's value of data from src: "yes" or "no"
// for AniList data, whose answer always states it, "" for any other source.
func AdultValue(src Source, adult bool) string {
	switch {
	case src != AniList:
		return ""
	case adult:
		return "yes"
	}
	return "no"
}

// AdultFields is fields with the adult value of src added (fields itself,
// or a new map when it is nil); fields is returned unchanged for another
// source.
func AdultFields(fields map[string]plugin.FieldValue, src Source, adult bool) map[string]plugin.FieldValue {
	v := AdultValue(src, adult)
	if v == "" {
		return fields
	}
	if fields == nil {
		fields = map[string]plugin.FieldValue{}
	}
	fields["adult"] = plugin.FieldValue{Text: v}
	return fields
}

// ExtraFields returns the manifest fields of the extras the three kinds
// share (release_status, country, adult, community_score, popularity). The
// adult flag is only set for data that came from AniList, whose answer
// always states it.
func ExtraFields(m Media) map[string]plugin.FieldValue {
	f := map[string]plugin.FieldValue{}
	if v := ReleaseStatusValue(m.Status); v != "" {
		f["release_status"] = plugin.FieldValue{Text: v}
	}
	if c := plugin.SingleLine(m.Country, maxCountryRunes); c != "" {
		f["country"] = plugin.FieldValue{Text: c}
	}
	f = AdultFields(f, m.Source, m.Adult)
	if v, ok := numValue(m.AverageScore, 0, 100); ok {
		f["community_score"] = v
	}
	if v, ok := numValue(m.Popularity, 0, maxPopularity); ok {
		f["popularity"] = v
	}
	return f
}

func numValue(v *int, lo, hi int) (plugin.FieldValue, bool) {
	if v == nil || *v < lo || *v > hi {
		return plugin.FieldValue{}, false
	}
	n := float64(*v)
	return plugin.FieldValue{Num: &n}, true
}

// AltTitles maps synonyms to alternative titles (plugin.AltTitleLang),
// cleaned, dropping those equal to a title in have or to an earlier synonym,
// at most 20.
func AltTitles(synonyms []string, have []plugin.Title) []plugin.Title {
	seen := map[string]bool{}
	for _, t := range have {
		seen[t.Text] = true
	}
	var out []plugin.Title
	for _, s := range synonyms {
		text := plugin.SingleLine(s, maxAltTitleRunes)
		if text == "" || seen[text] || len(out) == maxAltTitles {
			continue
		}
		seen[text] = true
		out = append(out, plugin.Title{Lang: plugin.AltTitleLang, Text: text})
	}
	return out
}

// StreamingLinks maps the STREAMING links (labelled with the site) to item
// links; links without a label or an https URL are dropped, at most 20.
func StreamingLinks(ls []LinkRef) []plugin.Link {
	var out []plugin.Link
	for _, l := range ls {
		if l.Type != "STREAMING" || len(out) == maxLinks {
			continue
		}
		label, u := plugin.SingleLine(l.Site, maxLinkLabelRunes), httpsOnly(l.URL)
		if label == "" || u == "" {
			continue
		}
		out = append(out, plugin.Link{Label: label, URL: u})
	}
	return out
}

var trailerID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Trailer builds the trailer link from AniList's video id: YouTube and
// Dailymotion only, the id checked, nil otherwise. The thumbnail is kept
// only as an https URL.
func Trailer(t *TrailerRef) *plugin.Trailer {
	if t == nil || !trailerID.MatchString(t.ID) {
		return nil
	}
	var link string
	switch strings.ToLower(strings.TrimSpace(t.Site)) {
	case "youtube":
		link = "https://www.youtube.com/watch?v=" + t.ID
	case "dailymotion":
		link = "https://www.dailymotion.com/video/" + t.ID
	default:
		return nil
	}
	return &plugin.Trailer{URL: link, ThumbnailURL: httpsOnly(t.Thumbnail)}
}

// AddExtras adds the shared extras of m to d: the fields of ExtraFields,
// the synonyms as alternative titles, streaming links, banner and trailer.
func AddExtras(d *plugin.ItemData, m Media) {
	if d.Fields == nil {
		d.Fields = map[string]plugin.FieldValue{}
	}
	for k, v := range ExtraFields(m) {
		d.Fields[k] = v
	}
	d.Titles = append(d.Titles, AltTitles(m.Synonyms, d.Titles)...)
	d.Links = StreamingLinks(m.StreamingLinks)
	d.BannerURL = httpsOnly(m.BannerURL)
	d.Trailer = Trailer(m.Trailer)
}

// httpsOnly is plugin.HTTPSURL limited to https (the library stores only
// https URLs for links, banners and trailers).
func httpsOnly(s string) string {
	if u := plugin.HTTPSURL(s); strings.HasPrefix(u, "https://") {
		return u
	}
	return ""
}
