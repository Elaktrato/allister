package book

import (
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// Limits that keep source data inside the library's ranges.
const (
	maxTitleRunes  = 300
	maxTextRunes   = 20000
	maxTermRunes   = 60
	maxNameRunes   = 300
	maxIDRunes     = 100
	maxGenres      = 50
	maxTags        = 100
	maxCredits     = 500
	maxRoleRunes   = 100
	maxDetailRunes = 100
)

// bookRecord is the source-neutral shape both book sources produce.
type bookRecord struct {
	Source, ID, Title, Subtitle, AltTitle, AltLanguage, Author, ISBN, Publisher, Language, Synopsis string
	CoverURL, ExternalURL, Series                                                                   string
	Pages, Year                                                                                     int
	PublishedOn                                                                                     *time.Time
	Genres                                                                                          []string
	Tags                                                                                            []plugin.Tag
	Credits                                                                                         []plugin.Credit
	ExternalIDs                                                                                     map[string]string
}

// bookData maps a source record to the book contract. Empty or out-of-range
// values are omitted; text and links are sanitised; lists are capped.
func bookData(r bookRecord) plugin.ItemData {
	lang := plugin.SingleLine(r.Language, 20)
	if lang == "" {
		lang = "en"
	}
	title := plugin.SingleLine(r.Title, maxTitleRunes)
	if sub := plugin.SingleLine(r.Subtitle, maxTitleRunes); sub != "" {
		if title == "" {
			title = sub
		} else {
			title = plugin.SingleLine(title+": "+sub, maxTitleRunes)
		}
	}
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{
			Source:   plugin.SingleLine(r.Source, maxIDRunes),
			ID:       plugin.SingleLine(r.ID, maxIDRunes),
			Kind:     KindBook,
			CoverURL: plugin.HTTPSURL(r.CoverURL),
		},
		ExternalURL: plugin.HTTPSURL(r.ExternalURL),
	}
	if title != "" {
		d.Titles = []plugin.Title{{Lang: lang, Text: title}}
	}
	if alt := plugin.SingleLine(r.AltTitle, maxTitleRunes); alt != "" && alt != title {
		altLang := plugin.SingleLine(r.AltLanguage, 20)
		if altLang == "" {
			altLang = lang
		}
		d.Titles = append(d.Titles, plugin.Title{Lang: altLang, Text: alt})
	}
	d.Fields = map[string]plugin.FieldValue{}
	if v := plugin.SingleLine(r.Author, maxNameRunes); v != "" {
		d.Fields["author"] = plugin.FieldValue{Text: v}
	}
	if r.Pages > 0 {
		pages := float64(r.Pages)
		d.Fields["pages"] = plugin.FieldValue{Num: &pages}
	}
	if r.Year > 0 {
		year := float64(r.Year)
		d.Fields["year"] = plugin.FieldValue{Num: &year}
	}
	if v := plugin.SingleLine(r.Publisher, maxNameRunes); v != "" {
		d.Fields["publisher"] = plugin.FieldValue{Text: v}
	}
	if v := plugin.SingleLine(r.ISBN, maxIDRunes); v != "" {
		d.Fields["isbn"] = plugin.FieldValue{Text: v}
	}
	if v := plugin.SingleLine(r.Series, maxNameRunes); v != "" {
		d.Fields["series"] = plugin.FieldValue{Text: v}
	}
	if v := plugin.MultiLine(r.Synopsis, maxTextRunes); v != "" {
		d.Fields["synopsis"] = plugin.FieldValue{Text: v}
	}
	if r.PublishedOn != nil {
		day := plugin.DateOnly(*r.PublishedOn)
		d.Fields["published_on"] = plugin.FieldValue{Date: &day}
	}
	d.Genres = cleanTerms(r.Genres, maxGenres)
	d.Tags = cleanTags(r.Tags)
	for typ, value := range r.ExternalIDs {
		typ = plugin.SingleLine(typ, 40)
		value = plugin.SingleLine(value, maxIDRunes)
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

// bookCredits sanitises the source credits; a credit needs both a person id
// and a name for the library's people table.
func bookCredits(r bookRecord) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(r.Credits))
	for _, c := range r.Credits {
		role := plugin.SingleLine(c.Role, maxRoleRunes)
		name := plugin.SingleLine(c.Person.Name, maxNameRunes)
		id := plugin.SingleLine(c.Person.ID, maxIDRunes)
		if role == "" || name == "" || id == "" || len(out) == maxCredits {
			continue
		}
		out = append(out, plugin.Credit{
			Role:   role,
			Detail: plugin.SingleLine(c.Detail, maxDetailRunes),
			Person: plugin.PersonRef{
				ID:       id,
				Name:     name,
				ImageURL: plugin.HTTPSURL(c.Person.ImageURL),
				URL:      plugin.HTTPSURL(c.Person.URL),
			},
		})
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
