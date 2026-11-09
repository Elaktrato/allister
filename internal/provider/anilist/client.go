// Package anilist is a provider.Provider backed by the AniList GraphQL API.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/provider"
)

// DefaultURL is the production AniList GraphQL endpoint.
const DefaultURL = "https://graphql.anilist.co"

const (
	maxResults = 20
	// castPageSize: the first 25 characters sorted by ROLE (main characters
	// first) are plenty for a detail page and keep the response small.
	castPageSize = 25
	recsPageSize = 10
	// maxBodyBytes bounds how much of a response is read, so a misbehaving
	// server or wrong base URL cannot exhaust memory.
	maxBodyBytes = 8 << 20
)

const titleFields = `title { romaji english native }`

const searchQuery = `query ($q: String, $perPage: Int) {
  Page(page: 1, perPage: $perPage) {
    media(search: $q, type: ANIME, sort: SEARCH_MATCH) {
      id idMal ` + titleFields + ` format episodes seasonYear startDate { year }
      description(asHtml: true) coverImage { large }
      studios(isMain: true) { nodes { name } } siteUrl
    }
  }
}`

// animeFields is the selection shared by Anime and AnimeByMALID, so both are
// mapped by the same code.
const animeFields = `id idMal ` + titleFields + ` format episodes seasonYear
    startDate { year month day } endDate { year month day } source
    description(asHtml: true) coverImage { large }
    studios(isMain: true) { nodes { name } } siteUrl
    relations { edges { relationType node { id type format ` + titleFields + ` } } }
    recommendations(perPage: $recs, sort: RATING_DESC) {
      nodes { mediaRecommendation { id ` + titleFields + ` coverImage { large } } }
    }`

const animeQuery = `query ($id: Int, $recs: Int) {
  Media(id: $id, type: ANIME) { ` + animeFields + ` }
}`

const animeByMALQuery = `query ($idMal: Int, $recs: Int) {
  Media(idMal: $idMal, type: ANIME) { ` + animeFields + ` }
}`

const castQuery = `query ($id: Int, $perPage: Int) {
  Media(id: $id, type: ANIME) {
    characters(perPage: $perPage, sort: ROLE) {
      edges {
        role
        node { id name { full } image { large } siteUrl }
        voiceActors(language: JAPANESE) { id name { full } image { large } siteUrl }
      }
    }
  }
}`

// Client talks to AniList.
type Client struct {
	baseURL string
	hc      *http.Client
	maxBody int64
}

var _ provider.Provider = (*Client)(nil)

// New returns a client for baseURL (use DefaultURL in production). A nil hc
// gets a client with a 15 second timeout.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, hc: hc, maxBody: maxBodyBytes}
}

// Name implements provider.Provider.
func (c *Client) Name() provider.Source { return provider.AniList }

type gqlError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// do posts a GraphQL request and decodes the data field into out.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("anilist: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("anilist: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("anilist: request: %w", ctx.Err())
		}
		return fmt.Errorf("anilist: request failed: %w: %v", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return fmt.Errorf("anilist: read response: %w: %v", provider.ErrUnavailable, err)
	}
	if int64(len(body)) > c.maxBody {
		return fmt.Errorf("anilist: response exceeds %d bytes: %w", c.maxBody, provider.ErrUnavailable)
	}

	var r gqlResponse
	jsonErr := json.Unmarshal(body, &r)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("anilist: http 429: %w", provider.ErrRateLimited)
	case resp.StatusCode >= 500:
		return fmt.Errorf("anilist: http %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	if notFound(resp.StatusCode, r.Errors) {
		return fmt.Errorf("anilist: %w", provider.ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anilist: unexpected http %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	if jsonErr != nil {
		return fmt.Errorf("anilist: malformed response: %w: %v", provider.ErrUnavailable, jsonErr)
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("anilist: graphql error %q: %w", r.Errors[0].Message, provider.ErrUnavailable)
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return fmt.Errorf("anilist: malformed data: %w: %v", provider.ErrUnavailable, err)
	}
	return nil
}

func notFound(status int, errs []gqlError) bool {
	if status == http.StatusNotFound {
		return true
	}
	for _, e := range errs {
		if e.Status == http.StatusNotFound || strings.HasPrefix(e.Message, "Not Found") {
			return true
		}
	}
	return false
}

type apiTitle struct {
	Romaji  *string `json:"romaji"`
	English *string `json:"english"`
	Native  *string `json:"native"`
}

func (t apiTitle) titles() []provider.Title {
	var out []provider.Title
	add := func(lang string, s *string) {
		if s != nil && *s != "" {
			out = append(out, provider.Title{Lang: lang, Text: *s})
		}
	}
	add("ja", t.Native)
	add("ja-latn", t.Romaji)
	add("en", t.English)
	return out
}

type apiDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

// time returns the date, or nil unless year, month and day are all known.
func (d apiDate) time() *time.Time {
	if d.Year == nil || d.Month == nil || d.Day == nil {
		return nil
	}
	t := time.Date(*d.Year, time.Month(*d.Month), *d.Day, 0, 0, 0, 0, time.UTC)
	return &t
}

type apiMedia struct {
	ID          int      `json:"id"`
	IDMal       *int     `json:"idMal"`
	Title       apiTitle `json:"title"`
	Format      string   `json:"format"`
	Episodes    *int     `json:"episodes"`
	SeasonYear  *int     `json:"seasonYear"`
	StartDate   apiDate  `json:"startDate"`
	EndDate     apiDate  `json:"endDate"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	CoverImage  struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	Studios struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"studios"`
	SiteURL   string `json:"siteUrl"`
	Relations struct {
		Edges []struct {
			RelationType string `json:"relationType"`
			Node         struct {
				ID     int      `json:"id"`
				Type   string   `json:"type"`
				Format string   `json:"format"`
				Title  apiTitle `json:"title"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"relations"`
	Recommendations struct {
		Nodes []struct {
			MediaRecommendation *struct {
				ID         int      `json:"id"`
				Title      apiTitle `json:"title"`
				CoverImage struct {
					Large string `json:"large"`
				} `json:"coverImage"`
			} `json:"mediaRecommendation"`
		} `json:"nodes"`
	} `json:"recommendations"`
}

func (m apiMedia) searchResult() provider.SearchResult {
	r := provider.SearchResult{
		Source:   provider.AniList,
		ID:       strconv.Itoa(m.ID),
		Titles:   m.Title.titles(),
		Type:     m.Format,
		Episodes: m.Episodes,
		Year:     m.SeasonYear,
		Synopsis: provider.PlainText(m.Description),
		CoverURL: m.CoverImage.Large,
	}
	if r.Year == nil {
		r.Year = m.StartDate.Year
	}
	if len(m.Studios.Nodes) > 0 {
		r.Studio = m.Studios.Nodes[0].Name
	}
	return r
}

// Search implements provider.Provider. An empty query returns no results
// without contacting AniList; results are capped at 20.
func (c *Client) Search(ctx context.Context, q string) ([]provider.SearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []provider.SearchResult{}, nil
	}
	var data struct {
		Page struct {
			Media []apiMedia `json:"media"`
		} `json:"Page"`
	}
	if err := c.do(ctx, searchQuery, map[string]any{"q": q, "perPage": maxResults}, &data); err != nil {
		return nil, err
	}
	out := make([]provider.SearchResult, 0, len(data.Page.Media))
	for _, m := range data.Page.Media {
		if len(out) == maxResults {
			break
		}
		out = append(out, m.searchResult())
	}
	return out, nil
}

func parseID(id string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("anilist: invalid id %q: %w", id, provider.ErrNotFound)
	}
	return n, nil
}

// relationKind maps an AniList relation to a provider kind. The second result
// is false when the relation must be dropped: anime relations are all kept,
// manga/novels only as the source work. AniList labels the manga an anime was
// adapted from as ADAPTATION (and SOURCE in the reverse direction), so both
// count as source for non-anime nodes.
func relationKind(relType, mediaType string) (string, bool) {
	isSource := relType == "SOURCE" || (relType == "ADAPTATION" && mediaType == "MANGA")
	if mediaType != "ANIME" && !isSource {
		return "", false
	}
	switch {
	case isSource:
		return "source", true
	case relType == "SEQUEL":
		return "sequel", true
	case relType == "PREQUEL":
		return "prequel", true
	}
	return "other", true
}

// Anime implements provider.Provider.
func (c *Client) Anime(ctx context.Context, id string) (provider.Anime, error) {
	n, err := parseID(id)
	if err != nil {
		return provider.Anime{}, err
	}
	return c.fetchAnime(ctx, animeQuery, map[string]any{"id": n, "recs": recsPageSize}, "anime "+strconv.Itoa(n))
}

// AnimeByMALID looks an anime up by its MyAnimeList id (AniList
// Media(idMal:, type: ANIME)) and maps it exactly like Anime. A malformed id
// (anything but 1 to 2147483647 in ASCII digits) is provider.ErrNotFound
// without a request, as is a response without a media id.
func (c *Client) AnimeByMALID(ctx context.Context, malID string) (provider.Anime, error) {
	malID = strings.TrimSpace(malID)
	n, err := strconv.Atoi(malID)
	for _, r := range malID {
		if r < '0' || r > '9' {
			err = strconv.ErrSyntax
		}
	}
	if err != nil || n <= 0 || n > math.MaxInt32 {
		return provider.Anime{}, fmt.Errorf("anilist: invalid MAL id %q: %w", malID, provider.ErrNotFound)
	}
	return c.fetchAnime(ctx, animeByMALQuery, map[string]any{"idMal": n, "recs": recsPageSize}, "MAL anime "+malID)
}

// fetchAnime runs a Media query shaped like animeFields and maps the result.
func (c *Client) fetchAnime(ctx context.Context, query string, vars map[string]any, what string) (provider.Anime, error) {
	var data struct {
		Media *apiMedia `json:"Media"`
	}
	if err := c.do(ctx, query, vars, &data); err != nil {
		return provider.Anime{}, err
	}
	if data.Media == nil || data.Media.ID <= 0 {
		return provider.Anime{}, fmt.Errorf("anilist: %s: %w", what, provider.ErrNotFound)
	}
	return data.Media.anime(), nil
}

func (m *apiMedia) anime() provider.Anime {
	a := provider.Anime{
		SearchResult:   m.searchResult(),
		StartDate:      m.StartDate.time(),
		EndDate:        m.EndDate.time(),
		SourceMaterial: m.Source,
		ExternalURL:    m.SiteURL,
	}
	if m.IDMal != nil {
		a.MALID = strconv.Itoa(*m.IDMal)
	}
	for _, e := range m.Relations.Edges {
		kind, ok := relationKind(e.RelationType, e.Node.Type)
		if !ok {
			continue
		}
		a.Relations = append(a.Relations, provider.Relation{
			Kind:   kind,
			Source: provider.AniList,
			ID:     strconv.Itoa(e.Node.ID),
			Titles: e.Node.Title.titles(),
			Type:   e.Node.Format,
		})
	}
	for _, r := range m.Recommendations.Nodes {
		if r.MediaRecommendation == nil {
			continue
		}
		a.Recommendations = append(a.Recommendations, provider.Recommendation{
			Source:   provider.AniList,
			ID:       strconv.Itoa(r.MediaRecommendation.ID),
			Titles:   r.MediaRecommendation.Title.titles(),
			CoverURL: r.MediaRecommendation.CoverImage.Large,
		})
	}
	return a
}

type apiPerson struct {
	ID   int `json:"id"`
	Name struct {
		Full string `json:"full"`
	} `json:"name"`
	Image struct {
		Large string `json:"large"`
	} `json:"image"`
	SiteURL string `json:"siteUrl"`
}

func (p apiPerson) person() provider.Person {
	return provider.Person{
		Source:   provider.AniList,
		ID:       strconv.Itoa(p.ID),
		Name:     p.Name.Full,
		ImageURL: p.Image.Large,
		URL:      p.SiteURL,
	}
}

// Cast implements provider.Provider: the first 25 characters sorted by ROLE
// (main before supporting), each with its first Japanese voice actor.
func (c *Client) Cast(ctx context.Context, id string) ([]provider.CastMember, error) {
	n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	var data struct {
		Media *struct {
			Characters struct {
				Edges []struct {
					Role        string      `json:"role"`
					Node        apiPerson   `json:"node"`
					VoiceActors []apiPerson `json:"voiceActors"`
				} `json:"edges"`
			} `json:"characters"`
		} `json:"Media"`
	}
	if err := c.do(ctx, castQuery, map[string]any{"id": n, "perPage": castPageSize}, &data); err != nil {
		return nil, err
	}
	if data.Media == nil {
		return nil, fmt.Errorf("anilist: anime %d: %w", n, provider.ErrNotFound)
	}
	out := make([]provider.CastMember, 0, len(data.Media.Characters.Edges))
	for _, e := range data.Media.Characters.Edges {
		cm := provider.CastMember{Character: e.Node.person(), Role: e.Role}
		if len(e.VoiceActors) > 0 {
			a := e.VoiceActors[0].person()
			cm.Actor = &a
		}
		out = append(out, cm)
	}
	return out, nil
}
