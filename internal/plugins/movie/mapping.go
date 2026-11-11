package movie

import (
	"strings"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxTitleRunes = 300
	maxTextRunes  = 20000
	maxTermRunes  = 60
	maxNameRunes  = 300
	maxIDRunes    = 100
	maxGenres     = 50
	maxTags       = 100
	maxCredits    = 500
	maxRecs       = 20
)

// movieRecord is the source-neutral shape the movie sources map into.
type movieRecord struct {
	Source, ID            string
	Titles                []plugin.Title
	Fields                map[string]plugin.FieldValue
	CoverURL, ExternalURL string
	ExternalIDs           map[string]string
	Genres                []string
	Tags                  []plugin.Tag
	Credits               []plugin.Credit
	Recommendations       []plugin.ItemSummary
}

// movieData maps a source record to the contract's Movie item.
func movieData(r movieRecord) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Source:   plugin.SingleLine(r.Source, maxIDRunes),
			ID:       plugin.SingleLine(r.ID, maxIDRunes),
			Kind:     KindMovie,
			Titles:   movieTitles(r.Titles),
			Fields:   map[string]plugin.FieldValue{},
			CoverURL: plugin.HTTPSURL(r.CoverURL),
		},
		ExternalURL: plugin.HTTPSURL(r.ExternalURL),
		Genres:      cleanTerms(r.Genres, maxGenres),
		Tags:        cleanTags(r.Tags),
	}
	for key, value := range r.Fields {
		if key = plugin.SingleLine(key, 40); key == "" {
			continue
		}
		switch {
		case value.Text != "":
			if key == "synopsis" {
				d.Fields[key] = plugin.FieldValue{Text: plugin.MultiLine(value.Text, maxTextRunes)}
			} else {
				d.Fields[key] = plugin.FieldValue{Text: plugin.SingleLine(value.Text, maxTextRunes)}
			}
		case value.Num != nil:
			n := *value.Num
			d.Fields[key] = plugin.FieldValue{Num: &n}
		case value.Date != nil:
			day := plugin.DateOnly(*value.Date)
			d.Fields[key] = plugin.FieldValue{Date: &day}
		}
	}
	for typ, value := range r.ExternalIDs {
		typ, value = plugin.SingleLine(typ, 40), plugin.SingleLine(value, maxIDRunes)
		if typ == "" || value == "" {
			continue
		}
		if d.ExternalIDs == nil {
			d.ExternalIDs = map[string]string{}
		}
		d.ExternalIDs[typ] = value
	}
	return d
}

func movieTitles(ts []plugin.Title) []plugin.Title {
	var out []plugin.Title
	for _, t := range ts {
		text := plugin.SingleLine(t.Text, maxTitleRunes)
		lang := plugin.SingleLine(t.Lang, 20)
		if text == "" || lang == "" {
			continue
		}
		out = append(out, plugin.Title{Lang: lang, Text: text})
	}
	return out
}

func movieCredits(cs []plugin.Credit) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(cs))
	for _, c := range cs {
		role := plugin.SingleLine(c.Role, 40)
		name := plugin.SingleLine(c.Person.Name, maxNameRunes)
		id := plugin.SingleLine(c.Person.ID, maxIDRunes)
		if role == "" || name == "" || id == "" || len(out) == maxCredits {
			continue
		}
		credit := plugin.Credit{Role: role, Detail: plugin.SingleLine(c.Detail, 100), Person: plugin.PersonRef{
			ID: id, Name: name, ImageURL: plugin.HTTPSURL(c.Person.ImageURL), URL: plugin.HTTPSURL(c.Person.URL),
		}}
		if c.With != nil {
			wname := plugin.SingleLine(c.With.Name, maxNameRunes)
			wid := plugin.SingleLine(c.With.ID, maxIDRunes)
			if wname != "" && wid != "" {
				credit.With = &plugin.PersonRef{ID: wid, Name: wname, ImageURL: plugin.HTTPSURL(c.With.ImageURL), URL: plugin.HTTPSURL(c.With.URL)}
				credit.WithRole = plugin.SingleLine(c.WithRole, 40)
			}
		}
		out = append(out, credit)
	}
	return out
}

func movieRecommendations(rs []plugin.ItemSummary) []plugin.ItemSummary {
	out := make([]plugin.ItemSummary, 0, len(rs))
	for _, r := range rs {
		if len(out) == maxRecs {
			break
		}
		r.Source = plugin.SingleLine(r.Source, maxIDRunes)
		r.ID = plugin.SingleLine(r.ID, maxIDRunes)
		r.Kind = KindMovie
		r.Titles = movieTitles(r.Titles)
		r.CoverURL = plugin.HTTPSURL(r.CoverURL)
		if r.Source == "" || r.ID == "" || len(r.Titles) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

func cleanTerms(in []string, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		term := plugin.SingleLine(raw, maxTermRunes)
		key := strings.ToLower(term)
		if term == "" || seen[key] || len(out) == limit {
			continue
		}
		seen[key] = true
		out = append(out, term)
	}
	return out
}

func cleanTags(in []plugin.Tag) []plugin.Tag {
	var out []plugin.Tag
	seen := map[string]bool{}
	for _, t := range in {
		name := plugin.SingleLine(t.Name, maxTermRunes)
		key := strings.ToLower(name)
		if name == "" || seen[key] || len(out) == maxTags {
			continue
		}
		seen[key] = true
		tag := plugin.Tag{Name: name, Spoiler: t.Spoiler}
		if t.Rank != nil && *t.Rank >= 0 && *t.Rank <= 100 {
			r := *t.Rank
			tag.Rank = &r
		}
		out = append(out, tag)
	}
	return out
}
