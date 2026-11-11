package web

// The generic search page: GET /{plugin}/search?q=&source=&kind= asks one of
// the plugin's sources (the first by default) for items of one of its kinds
// (the first by default) and renders result cards from the kind's schema.

import (
	"context"
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
	// Heading ("Search books"), Action (/{plugin}/search), Placeholder,
	// EmptyHint and ManualURL are fixed per plugin, source and kind.
	Heading, Action, Placeholder, EmptyHint, ManualURL string
	Query                                              string
	Searched                                           bool
	Results                                            []searchCard
	// Message is a user-facing error; Retry says whether to offer a retry
	// button requesting RetryURL. Status is the answer's status when the
	// search fails for a reason the page itself should carry (a missing
	// credential: 503), else 0 for 200.
	Message  string
	Retry    bool
	RetryURL string
	Status   int
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
// sentence ("Books" -> "books"), but leaves acronyms ("TV shows") alone.
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

// sourceServesKind reports whether a source declares the kind (or declares
// no restriction at all).
func sourceServesKind(si plugin.SourceInfo, kind string) bool {
	if len(si.Kinds) == 0 {
		return true
	}
	for _, k := range si.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// sourcesForKind returns the sources in manifest order that serve kind.
func sourcesForKind(m plugin.Manifest, kind string) []plugin.SourceInfo {
	var out []plugin.SourceInfo
	for _, si := range m.Sources {
		if sourceServesKind(si, kind) {
			out = append(out, si)
		}
	}
	return out
}

// sourceChoiceLabel is a source's name in a source switch, marked with
// " (needs key)" while the source declares a key label and no effective key is
// configured. It never changes the heading or the result badges.
func (s *server) sourceChoiceLabel(ctx context.Context, si plugin.SourceInfo) string {
	if si.KeyLabel == "" {
		return si.Name
	}
	key, err := s.sourceKey(ctx, si.ID)
	if err != nil {
		log.Printf("source choice %s: read key: %v", si.ID, err)
	}
	if key != "" {
		return si.Name
	}
	return si.Name + " (needs key)"
}

// defaultSourceForKind is the first source serving kind that supports lookup,
// else the first one serving it. A plugin whose sources have no Kinds keeps
// the old default.
func defaultSourceForKind(m plugin.Manifest, kind string) plugin.SourceInfo {
	valid := sourcesForKind(m, kind)
	for _, si := range valid {
		if si.SupportsLookup {
			return si
		}
	}
	if len(valid) > 0 {
		return valid[0]
	}
	return plugin.SourceInfo{}
}

// chooseSourceForKind is the source a request names. An empty or non-serving
// id falls back to the kind's default; an unknown id is not a source at all.
func chooseSourceForKind(m plugin.Manifest, kind, id string) (plugin.SourceInfo, bool) {
	if id == "" {
		si := defaultSourceForKind(m, kind)
		return si, si.ID != ""
	}
	si, ok := sourceInfo(m, id)
	if !ok {
		return plugin.SourceInfo{}, false
	}
	if !sourceServesKind(si, kind) {
		si = defaultSourceForKind(m, kind)
		return si, si.ID != ""
	}
	return si, true
}

func (s *server) search(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	qs := r.URL.Query()
	kindKey := qs.Get("kind")
	if ok && kindKey == "" && len(m.Kinds) > 0 {
		kindKey = m.Kinds[0].Key
	}
	k, okKind := kindDef(m, kindKey)
	if !ok || !okKind {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such search.", "")
		return
	}
	si, okSrc := chooseSourceForKind(m, k.Key, qs.Get("source"))
	if !okSrc {
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
	validSources := sourcesForKind(m, k.Key)
	if len(validSources) > 1 {
		for _, x := range validSources {
			v.Sources = append(v.Sources, choice{Value: x.ID, Label: s.sourceChoiceLabel(r.Context(), x), Selected: x.ID == si.ID})
		}
		if si.ID != validSources[0].ID {
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
	status := http.StatusOK
	if v.Status != 0 {
		status = v.Status
	}
	if isPartialRequest(r) {
		// htmx swaps no 5xx answer outside the dialog slot, so an htmx
		// search gets the same message as a 200 partial; the full page
		// keeps the real status.
		if status == http.StatusServiceUnavailable {
			status = http.StatusOK
		}
		if r.Header.Get("HX-Target") == "search-panel" {
			s.renderPartialStatus(w, status, "search", "search_panel", v)
			return
		}
		s.renderPartialStatus(w, status, "search", "search_results", v)
		return
	}
	s.renderStatus(w, r, status, "search", v)
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
	case errors.Is(err, plugin.ErrNotConfigured):
		// the page itself carries the status: the user must set a key
		log.Printf("search %q: %v", q, err)
		v.Message, v.Retry, v.Status = sourceMessage(m, si.ID, err), true, http.StatusServiceUnavailable
		return
	case errors.Is(err, plugin.ErrRateLimited):
		log.Printf("search %q: %v", q, err)
		v.Message, v.Retry = sourceMessage(m, si.ID, err), true
		return
	default:
		log.Printf("search %q: %v", q, err)
		v.Message, v.Retry = "Search is unavailable right now, please try again", true
		return
	}
	lang := s.titleLang(r.Context(), m)
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
			Title: orID(library.ResolveTitle(it.Titles, lang, titleFallback(m, k, it.Kind)), it.ID),
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
		if len(sourcesForKind(m, k.Key)) > 1 {
			c.Badge = si.Name
		}
		v.Results = append(v.Results, c)
	}
}
