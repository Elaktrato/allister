package web

// The generic search page: GET /{plugin}/search?q=&source=&kind= asks one of
// the plugin's sources (the first by default) for items of one of its kinds
// (the first by default) and renders result cards from the kind's schema.

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

const (
	maxQueryRunes    = 200
	maxSynopsisRunes = 200
	maxSearchResults = 20
)

// searchCard is one result: Meta joins the kind's shown fields with " · ",
// Text is the truncated text-section field, Badge the source name (only
// when the plugin has several sources). ID is the source id of the result
// (the match dialog posts it).
type searchCard struct {
	ID, Title, Meta, Text, Cover, Href, Badge string
}

// choice is one option of a select (source or kind switch).
type choice struct {
	Value, Label string
	Selected     bool
}

// searchView is the data for search.html and search_results.html.
type searchView struct {
	// Heading ("Search anime"), Action (/{plugin}/search), Placeholder,
	// EmptyHint and ManualURL are fixed per plugin, source and kind.
	Heading, Action, Placeholder, EmptyHint, ManualURL string
	Query                                              string
	Searched                                           bool
	Results                                            []searchCard
	// Message is a user-facing error; Retry says whether to offer a retry
	// button requesting RetryURL.
	Message  string
	Retry    bool
	RetryURL string
	// Sources and Kinds are the switches, set only when there are several.
	Sources, Kinds []choice
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// cleanQuery trims the search text and caps it at maxQueryRunes.
func cleanQuery(raw string) string {
	q := strings.TrimSpace(raw)
	if rs := []rune(q); len(rs) > maxQueryRunes {
		q = strings.TrimSpace(string(rs[:maxQueryRunes]))
	}
	return q
}

// lowerFirst lower-cases the first letter of a label for use inside a
// sentence ("Anime" -> "anime"), but leaves acronyms ("TV shows") alone.
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	if next, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

// textField is the field key of the kind's first text section ("" if none).
func textField(k plugin.KindDef) string {
	for _, sec := range k.Sections {
		if sec.Type == plugin.SectionText && sec.Field != "" {
			return sec.Field
		}
	}
	return ""
}

// isTextSectionField reports whether key is shown by a text section (long
// text, never part of a one-line summary).
func isTextSectionField(k plugin.KindDef, key string) bool {
	for _, sec := range k.Sections {
		if sec.Type == plugin.SectionText && sec.Field == key {
			return true
		}
	}
	return false
}

func (s *server) search(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	qs := r.URL.Query()
	srcID, kindKey := qs.Get("source"), qs.Get("kind")
	if ok && srcID == "" && len(m.Sources) > 0 {
		srcID = m.Sources[0].ID
	}
	if ok && kindKey == "" && len(m.Kinds) > 0 {
		kindKey = m.Kinds[0].Key
	}
	si, okSrc := sourceInfo(m, srcID)
	k, okKind := kindDef(m, kindKey)
	if !ok || !okSrc || !okKind {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such search.", "")
		return
	}
	q := cleanQuery(qs.Get("q"))
	v := searchView{
		Heading:     "Search " + lowerFirst(k.LabelPlural),
		Action:      "/" + url.PathEscape(m.ID) + "/search",
		Placeholder: "Search " + si.Name + "…",
		EmptyHint:   "Type a title to search " + si.Name + ".",
		ManualURL:   "/" + url.PathEscape(m.ID) + "/manual/new",
		Query:       q,
	}
	v.RetryURL = v.Action + "?q=" + url.QueryEscape(q)
	if len(m.Sources) > 1 {
		for _, x := range m.Sources {
			v.Sources = append(v.Sources, choice{Value: x.ID, Label: x.Name, Selected: x.ID == si.ID})
		}
		if si.ID != m.Sources[0].ID {
			v.RetryURL += "&source=" + url.QueryEscape(si.ID)
		}
	}
	if len(m.Kinds) > 1 {
		for _, x := range m.Kinds {
			v.Kinds = append(v.Kinds, choice{Value: x.Key, Label: x.LabelPlural, Selected: x.Key == k.Key})
		}
		if k.Key != m.Kinds[0].Key {
			v.RetryURL += "&kind=" + url.QueryEscape(k.Key)
		}
	}
	if q != "" {
		s.runSearch(r, m, si, k, q, &v)
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "search", "search_results", v)
		return
	}
	s.render(w, r, "search", v)
}

func (s *server) runSearch(r *http.Request, m plugin.Manifest, si plugin.SourceInfo, k plugin.KindDef, q string, v *searchView) {
	v.Searched = true
	src := s.source(m.ID, si.ID)
	if src == nil {
		log.Printf("search %s/%s: %v", m.ID, si.ID, errNoSource)
		v.Message, v.Retry = "Search is unavailable right now, please try again", true
		return
	}
	res, err := src.Search(r.Context(), k.Key, q)
	switch {
	case err == nil, errors.Is(err, plugin.ErrNotFound):
	case errors.Is(err, plugin.ErrRateLimited):
		log.Printf("search %q: %v", q, err)
		v.Message, v.Retry = sourceMessage(m, si.ID, err), true
		return
	default:
		log.Printf("search %q: %v", q, err)
		v.Message, v.Retry = "Search is unavailable right now, please try again", true
		return
	}
	lang := s.deps.Settings.TitleLang(r.Context())
	text := textField(k)
	for _, it := range res {
		if len(v.Results) == maxSearchResults {
			break
		}
		if !validPathID(it.ID) {
			continue
		}
		c := searchCard{
			ID:    it.ID,
			Title: orID(library.ResolveTitle(it.Titles, lang), it.ID),
			Cover: covers.Src(httpURL(it.CoverURL)),
			// the source that was asked (matched against the manifest), not
			// what the result claims
			Href: itemURL(m, si.ID, it.ID, k.Key),
		}
		var meta []string
		for _, fd := range k.Fields {
			if !fd.Show || fd.Type == plugin.FieldURL || isTextSectionField(k, fd.Key) {
				continue
			}
			if t, _ := fieldText(fd, it.Fields[fd.Key]); t != "" {
				meta = append(meta, t)
			}
		}
		c.Meta = strings.Join(meta, " · ")
		if text != "" {
			c.Text = truncateRunes(strings.TrimSpace(it.Fields[text].Text), maxSynopsisRunes)
		}
		if len(m.Sources) > 1 {
			c.Badge = si.Name
		}
		v.Results = append(v.Results, c)
	}
}
