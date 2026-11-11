package game

import (
	"strings"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxTitleRunes = 300
	maxTextRunes  = 20000
	maxTermRunes  = 60
	maxIDRunes    = 100
	maxGenres     = 50
	maxTags       = 100
	maxRecs       = 20
)

// gameRecord is the source-neutral shape the game sources map into.
type gameRecord struct {
	Source, ID            string
	Titles                []plugin.Title
	Fields                map[string]plugin.FieldValue
	CoverURL, ExternalURL string
	ExternalIDs           map[string]string
	Genres                []string
	Tags                  []plugin.Tag
	Recommendations       []plugin.ItemSummary
}

// gameData maps a source record to the contract's Game item.
func gameData(r gameRecord) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Source:   plugin.SingleLine(r.Source, maxIDRunes),
			ID:       plugin.SingleLine(r.ID, maxIDRunes),
			Kind:     KindGame,
			Titles:   gameTitles(r.Titles),
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
			if key == "summary" {
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

func gameTitles(ts []plugin.Title) []plugin.Title {
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

func gameRecommendations(rs []plugin.ItemSummary) []plugin.ItemSummary {
	out := make([]plugin.ItemSummary, 0, len(rs))
	for _, r := range rs {
		if len(out) == maxRecs {
			break
		}
		r.Source = plugin.SingleLine(r.Source, maxIDRunes)
		r.ID = plugin.SingleLine(r.ID, maxIDRunes)
		r.Kind = KindGame
		r.Titles = gameTitles(r.Titles)
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
