package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/titles"
)

const (
	maxQueryRunes    = 200
	maxSynopsisRunes = 200
)

type searchCard struct {
	ID, Title, Type, Studio, Synopsis, CoverURL string
	Episodes, Year                              string
}

// searchView is the data for search.html and search_results.html.
type searchView struct {
	Query    string
	Searched bool
	Results  []searchCard
	// Message is a user-facing error; Retry says whether to offer a retry button.
	Message string
	Retry   bool
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

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := cleanQuery(r.URL.Query().Get("q"))
	v := searchView{Query: q}
	if q != "" {
		s.runSearch(r, q, &v)
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "search", "search_results", v)
		return
	}
	s.render(w, r, "search", v)
}

func (s *server) runSearch(r *http.Request, q string, v *searchView) {
	v.Searched = true
	p := s.deps.Providers[provider.AniList]
	if p == nil {
		log.Printf("search: no AniList provider configured")
		v.Message = "Search is unavailable right now, please try again"
		return
	}
	res, err := p.Search(r.Context(), q)
	switch {
	case err == nil, errors.Is(err, provider.ErrNotFound):
	case errors.Is(err, provider.ErrRateLimited):
		log.Printf("search %q: %v", q, err)
		v.Message = "AniList is rate limiting requests, please wait a moment and try again"
		v.Retry = true
		return
	default:
		log.Printf("search %q: %v", q, err)
		v.Message = "Search is unavailable right now, please try again"
		v.Retry = true
		return
	}
	lang := s.deps.Settings.TitleLang(r.Context())
	for _, it := range res {
		c := searchCard{
			ID:       it.ID,
			Title:    titles.Resolve(it.Titles, lang),
			Type:     it.Type,
			Studio:   it.Studio,
			Synopsis: truncateRunes(it.Synopsis, maxSynopsisRunes),
			CoverURL: it.CoverURL,
		}
		if it.Episodes != nil {
			c.Episodes = strconv.Itoa(*it.Episodes)
		}
		if it.Year != nil {
			c.Year = strconv.Itoa(*it.Year)
		}
		v.Results = append(v.Results, c)
	}
}
