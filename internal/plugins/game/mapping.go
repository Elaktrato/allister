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
		Genres:      plugin.CleanTerms(r.Genres, maxTermRunes, maxGenres),
		Tags:        plugin.CleanTags(r.Tags, maxTermRunes, maxTags),
	}
	gameFields(d.Fields, r.Fields)
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

// gameFields copies the source fields into out, sanitised: keys capped at 40
// runes, text single-line (the summary multi-line) and capped, a text that is
// empty after sanitising dropped, numbers copied, dates cut to the calendar
// day.
func gameFields(out, in map[string]plugin.FieldValue) {
	for key, value := range in {
		if key = plugin.SingleLine(key, 40); key == "" {
			continue
		}
		switch {
		case value.Text != "":
			text := plugin.SingleLine(value.Text, maxTextRunes)
			if key == "summary" {
				text = plugin.MultiLine(value.Text, maxTextRunes)
			}
			if text != "" {
				out[key] = plugin.FieldValue{Text: text}
			}
		case value.Num != nil:
			n := *value.Num
			out[key] = plugin.FieldValue{Num: &n}
		case value.Date != nil:
			day := plugin.DateOnly(*value.Date)
			out[key] = plugin.FieldValue{Date: &day}
		}
	}
}

// gameTitles keeps the titles that have a text and a language. A title
// without a language is dropped: the core picks titles by language, so a
// source must say which one it sends (both IGDB and Steam send "en").
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
		r.URL = plugin.HTTPSURL(r.URL)
		if len(r.Fields) > 0 {
			fields := map[string]plugin.FieldValue{}
			gameFields(fields, r.Fields)
			r.Fields = fields
		}
		if r.Source == "" || r.ID == "" || len(r.Titles) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// joinNames sanitises names (IGDB and Steam both use it) and joins them with ", ". limit <= 0 means no
// limit.
func joinNames(names []string, limit int) string {
	clean := make([]string, 0, len(names))
	for _, raw := range names {
		if limit > 0 && len(clean) == limit {
			break
		}
		if name := plugin.SingleLine(raw, maxTermRunes); name != "" {
			clean = append(clean, name)
		}
	}
	return strings.Join(clean, ", ")
}
