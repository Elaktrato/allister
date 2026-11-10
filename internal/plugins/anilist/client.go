// Package anilist is the AniList GraphQL client of the anime plugin (Client)
// together with the types it returns (Anime, SearchResult, CastMember, ...)
// and its error sentinels. Only the anime plugin uses it.
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

// Media types the client serves.
const (
	TypeAnime = "ANIME"
	TypeManga = "MANGA"
)

const searchQuery = `query ($q: String, $perPage: Int, $type: MediaType) {
  Page(page: 1, perPage: $perPage) {
    media(search: $q, type: $type, sort: SEARCH_MATCH) {
      id idMal ` + titleFields + ` format episodes chapters volumes seasonYear startDate { year }
      description(asHtml: true) coverImage { large }
      studios(isMain: true) { nodes { name } } siteUrl
    }
  }
}`

// mediaFields is the selection shared by Media and MediaByMALID, so both are
// mapped by the same code.
const mediaFields = `id idMal ` + titleFields + ` format episodes chapters volumes seasonYear
    startDate { year month day } endDate { year month day } source status
    description(asHtml: true) coverImage { large }
    studios(isMain: true) { nodes { name } } siteUrl
    staff(perPage: 10) { edges { role node { name { full } image { medium } siteUrl } } }
    genres tags { name rank isMediaSpoiler }
    relations { edges { relationType node { id type format ` + titleFields + ` } } }
    recommendations(perPage: $recs, sort: RATING_DESC) {
      nodes { mediaRecommendation { id ` + titleFields + ` coverImage { large } } }
    }`

const mediaQuery = `query ($id: Int, $recs: Int, $type: MediaType) {
  Media(id: $id, type: $type) { ` + mediaFields + ` }
}`

const mediaByMALQuery = `query ($idMal: Int, $recs: Int, $type: MediaType) {
  Media(idMal: $idMal, type: $type) { ` + mediaFields + ` }
}`

const mediaListQuery = `query ($name: String, $type: MediaType) {
  MediaListCollection(userName: $name, type: $type) {
    lists {
      entries {
        status score(format: POINT_10) progress updatedAt
        media { id idMal title { romaji english native } coverImage { large } }
      }
    }
  }
}`

const scheduleQuery = `query ($id: Int) {
  Media(id: $id, type: ANIME) {
    airingSchedule { nodes { episode airingAt } }
  }
}`

const castQuery = `query ($id: Int, $perPage: Int) {
  Media(id: $id, type: ANIME) {
    characters(perPage: $perPage, sort: ROLE) {
      edges {
        role
        node { id name { full } image { medium } siteUrl }
        voiceActors(language: JAPANESE) { id name { full } image { medium } siteUrl }
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

var _ Provider = (*Client)(nil)

// New returns a client for baseURL (use DefaultURL in production). A nil hc
// gets a client with a 15 second timeout.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, hc: hc, maxBody: maxBodyBytes}
}

// Name implements Provider.
func (c *Client) Name() Source { return AniList }

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
		return fmt.Errorf("anilist: request failed: %w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return fmt.Errorf("anilist: read response: %w: %v", ErrUnavailable, err)
	}
	if int64(len(body)) > c.maxBody {
		return fmt.Errorf("anilist: response exceeds %d bytes: %w", c.maxBody, ErrUnavailable)
	}

	var r gqlResponse
	jsonErr := json.Unmarshal(body, &r)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("anilist: http 429: %w", ErrRateLimited)
	case resp.StatusCode >= 500:
		return fmt.Errorf("anilist: http %d: %w", resp.StatusCode, ErrUnavailable)
	}
	if notFound(resp.StatusCode, r.Errors) {
		return fmt.Errorf("anilist: %w", ErrNotFound)
	}
	// From here on the answer would be the same on a retry: ErrBadResponse,
	// unless a GraphQL error itself says rate limited or server error.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anilist: unexpected http %d: %w", resp.StatusCode, ErrBadResponse)
	}
	if jsonErr != nil {
		return fmt.Errorf("anilist: malformed response: %w: %v", ErrBadResponse, jsonErr)
	}
	if len(r.Errors) > 0 {
		e := r.Errors[0]
		target := ErrBadResponse
		switch {
		case e.Status == http.StatusTooManyRequests:
			target = ErrRateLimited
		case e.Status >= 500:
			target = ErrUnavailable
		}
		return fmt.Errorf("anilist: graphql error %q: %w", e.Message, target)
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return fmt.Errorf("anilist: malformed data: %w: %v", ErrBadResponse, err)
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

func (t apiTitle) titles() []Title {
	var out []Title
	add := func(lang string, s *string) {
		if s != nil && *s != "" {
			out = append(out, Title{Lang: lang, Text: *s})
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
	Chapters    *int     `json:"chapters"`
	Volumes     *int     `json:"volumes"`
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
	SiteURL string `json:"siteUrl"`
	Status  string `json:"status"`
	Staff   struct {
		Edges []struct {
			Role string `json:"role"`
			Node struct {
				Name struct {
					Full string `json:"full"`
				} `json:"name"`
				Image struct {
					Medium string `json:"medium"`
				} `json:"image"`
				SiteURL string `json:"siteUrl"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"staff"`
	Genres []string `json:"genres"`
	Tags   []struct {
		Name           string `json:"name"`
		Rank           *int   `json:"rank"`
		IsMediaSpoiler bool   `json:"isMediaSpoiler"`
	} `json:"tags"`
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

func (m apiMedia) searchResult() SearchResult {
	r := SearchResult{
		Source:   AniList,
		ID:       strconv.Itoa(m.ID),
		Titles:   m.Title.titles(),
		Type:     m.Format,
		Episodes: m.Episodes,
		Chapters: m.Chapters,
		Volumes:  m.Volumes,
		Year:     m.SeasonYear,
		Synopsis: PlainText(m.Description),
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

// Search implements Provider. An empty query returns no results
// without contacting AniList; results are capped at 20.
func (c *Client) Search(ctx context.Context, typ, q string) ([]SearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SearchResult{}, nil
	}
	var data struct {
		Page struct {
			Media []apiMedia `json:"media"`
		} `json:"Page"`
	}
	if err := c.do(ctx, searchQuery, map[string]any{"q": q, "perPage": maxResults, "type": typ}, &data); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(data.Page.Media))
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
		return 0, fmt.Errorf("anilist: invalid id %q: %w", id, ErrNotFound)
	}
	return n, nil
}

// relationKind maps an AniList relation to a provider kind. The second result
// is false when the relation must be dropped: anime relations are all kept,
// manga/novels only as the source work. AniList labels the manga an anime was
// adapted from as ADAPTATION (and SOURCE in the reverse direction), so both
// count as source for non-anime nodes.
func relationKind(relType string) (string, bool) {
	switch relType {
	case "SOURCE":
		return "source", true
	case "ADAPTATION":
		return "adaptation", true
	case "SEQUEL":
		return "sequel", true
	case "PREQUEL":
		return "prequel", true
	case "":
		return "", false
	}
	return "other", true
}

// AiringSchedule returns the known air dates of an anime's episodes (the
// Media.airingSchedule nodes; airingAt is a Unix timestamp in seconds).
func (c *Client) AiringSchedule(ctx context.Context, id string) ([]Airing, error) {
	n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	var out struct {
		Media struct {
			AiringSchedule struct {
				Nodes []Airing
			}
		}
	}
	if err := c.do(ctx, scheduleQuery, map[string]any{"id": n}, &out); err != nil {
		return nil, err
	}
	return out.Media.AiringSchedule.Nodes, nil
}

// MediaList returns the anime list of an AniList user (all of the account's
// lists, flattened). A name AniList does not know is ErrNotFound.
func (c *Client) MediaList(ctx context.Context, typ, userName string) ([]AccountEntry, error) {
	var out struct {
		MediaListCollection struct {
			Lists []struct {
				Entries []AccountEntry `json:"entries"`
			} `json:"lists"`
		} `json:"MediaListCollection"`
	}
	if err := c.do(ctx, mediaListQuery, map[string]any{"name": userName, "type": typ}, &out); err != nil {
		return nil, err
	}
	var res []AccountEntry
	for _, l := range out.MediaListCollection.Lists {
		res = append(res, l.Entries...)
	}
	return res, nil
}

// Media implements Provider.
func (c *Client) Media(ctx context.Context, typ, id string) (Media, error) {
	n, err := parseID(id)
	if err != nil {
		return Media{}, err
	}
	return c.fetchMedia(ctx, mediaQuery, map[string]any{"id": n, "recs": recsPageSize, "type": typ}, typ+" "+strconv.Itoa(n))
}

// AnimeByMALID looks an anime up by its MyAnimeList id (AniList
// Media(idMal:, type: ANIME)) and maps it exactly like Anime. A malformed id
// (anything but 1 to 2147483647 in ASCII digits) is ErrNotFound
// without a request, as is a response without a media id.
func (c *Client) MediaByMALID(ctx context.Context, typ, malID string) (Media, error) {
	malID = strings.TrimSpace(malID)
	n, err := strconv.Atoi(malID)
	for _, r := range malID {
		if r < '0' || r > '9' {
			err = strconv.ErrSyntax
		}
	}
	if err != nil || n <= 0 || n > math.MaxInt32 {
		return Media{}, fmt.Errorf("anilist: invalid MAL id %q: %w", malID, ErrNotFound)
	}
	return c.fetchMedia(ctx, mediaByMALQuery, map[string]any{"idMal": n, "recs": recsPageSize, "type": typ}, "MAL "+typ+" "+malID)
}

// fetchAnime runs a Media query shaped like animeFields and maps the result.
func (c *Client) fetchMedia(ctx context.Context, query string, vars map[string]any, what string) (Media, error) {
	var data struct {
		Media *apiMedia `json:"Media"`
	}
	if err := c.do(ctx, query, vars, &data); err != nil {
		return Media{}, err
	}
	if data.Media == nil || data.Media.ID <= 0 {
		return Media{}, fmt.Errorf("anilist: %s: %w", what, ErrNotFound)
	}
	return data.Media.media(), nil
}

func (m *apiMedia) media() Media {
	a := Media{
		SearchResult:   m.searchResult(),
		StartDate:      m.StartDate.time(),
		EndDate:        m.EndDate.time(),
		SourceMaterial: m.Source,
		ExternalURL:    m.SiteURL,
		Status:         m.Status,
	}
	for _, e := range m.Staff.Edges {
		a.Staff = append(a.Staff, StaffMember{Role: e.Role, Name: e.Node.Name.Full,
			ImageURL: e.Node.Image.Medium, URL: e.Node.SiteURL})
	}
	if m.IDMal != nil {
		a.MALID = strconv.Itoa(*m.IDMal)
	}
	for _, g := range m.Genres {
		if g = strings.TrimSpace(g); g != "" {
			a.Genres = append(a.Genres, g)
		}
	}
	for _, t := range m.Tags {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		tag := Tag{Name: name, Spoiler: t.IsMediaSpoiler}
		if t.Rank != nil && *t.Rank >= 0 && *t.Rank <= 100 {
			r := *t.Rank
			tag.Rank = &r
		}
		a.Tags = append(a.Tags, tag)
	}
	for _, e := range m.Relations.Edges {
		kind, ok := relationKind(e.RelationType)
		if !ok {
			continue
		}
		a.Relations = append(a.Relations, Relation{
			Kind:      kind,
			Source:    AniList,
			ID:        strconv.Itoa(e.Node.ID),
			Titles:    e.Node.Title.titles(),
			Type:      e.Node.Format,
			MediaType: e.Node.Type,
		})
	}
	for _, r := range m.Recommendations.Nodes {
		if r.MediaRecommendation == nil {
			continue
		}
		a.Recommendations = append(a.Recommendations, Recommendation{
			Source:   AniList,
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
	// Image is the person photo at AniList's "medium" size (about 21 KB
	// against 87 KB at "large"): it is shown small, and every credited
	// person's photo is downloaded. Covers stay at "large".
	Image struct {
		Medium string `json:"medium"`
	} `json:"image"`
	SiteURL string `json:"siteUrl"`
}

func (p apiPerson) person() Person {
	return Person{
		Source:   AniList,
		ID:       strconv.Itoa(p.ID),
		Name:     p.Name.Full,
		ImageURL: p.Image.Medium,
		URL:      p.SiteURL,
	}
}

// Cast implements Provider: the first 25 characters sorted by ROLE
// (main before supporting), each with its first Japanese voice actor.
func (c *Client) Cast(ctx context.Context, id string) ([]CastMember, error) {
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
		return nil, fmt.Errorf("anilist: anime %d: %w", n, ErrNotFound)
	}
	out := make([]CastMember, 0, len(data.Media.Characters.Edges))
	for _, e := range data.Media.Characters.Edges {
		cm := CastMember{Character: e.Node.person(), Role: e.Role}
		if len(e.VoiceActors) > 0 {
			a := e.VoiceActors[0].person()
			cm.Actor = &a
		}
		out = append(out, cm)
	}
	return out, nil
}
