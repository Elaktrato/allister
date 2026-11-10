package anilist

import (
	"context"
	"errors"
	"time"
)

// Source identifies where a piece of metadata came from.
type Source string

const (
	AniList Source = "anilist"
	Manual  Source = "manual"
)

// Title is one title of an anime in a single language tag (ja, ja-latn, en, ...).
type Title struct{ Lang, Text string }

// Airing is one node of a media's airing schedule: the episode number and
// its air time as a Unix timestamp in seconds.
type Airing struct {
	Episode int
	AirsAt  int64 `json:"airingAt"`
}

// Person is a character or a voice actor.
type Person struct {
	Source                  Source
	ID, Name, ImageURL, URL string
}

// CastMember pairs a character with its Japanese voice actor (nil if unknown).
type CastMember struct {
	Character Person
	Actor     *Person
	Role      string
}

// Relation links to a related work. Kind is sequel, prequel, source or other.
type Relation struct {
	Kind      string
	Source    Source
	ID        string
	Titles    []Title
	Type      string // the related item's format (TV, MANGA, ...)
	MediaType string // ANIME or MANGA
}

// Recommendation references another anime recommended for this one.
type Recommendation struct {
	Source   Source
	ID       string
	Titles   []Title
	CoverURL string
}

// SearchResult is the summary of an anime as returned by a search.
type SearchResult struct {
	Source                     Source
	ID                         string
	Titles                     []Title
	Type                       string
	Episodes                   *int
	Chapters, Volumes          *int
	Year                       *int
	Studio, Synopsis, CoverURL string
}

// Tag is a named tag with an optional relevance rank (0 to 100) and a spoiler flag.
type Tag struct {
	Name    string
	Rank    *int
	Spoiler bool
}

// AccountEntry is one entry of a user's AniList list, media included.
type AccountEntry struct {
	Status    string  `json:"status"` // CURRENT, PLANNING, COMPLETED, PAUSED, DROPPED, REPEATING
	Score     float64 `json:"score"`  // 0–10 (the query asks for POINT_10)
	Progress  int     `json:"progress"`
	UpdatedAt int64   `json:"updatedAt"` // unix seconds
	Media     struct {
		ID    int `json:"id"`
		MALID int `json:"idMal"`
		Title struct {
			Romaji, English, Native string
		} `json:"title"`
		Cover struct {
			Large string `json:"large"`
		} `json:"coverImage"`
	} `json:"media"`
}

// Media is the full detail of one media item (an anime or a manga).
type Media struct {
	SearchResult
	StartDate, EndDate          *time.Time
	SourceMaterial, ExternalURL string
	MALID                       string
	Chapters, Volumes           *int
	Format, Status              string
	Staff                       []StaffMember
	Relations                   []Relation
	Recommendations             []Recommendation
	Genres                      []string
	Tags                        []Tag
}

// StaffMember is one staff credit of a media item.
type StaffMember struct {
	Role          string
	Name          string
	ImageURL, URL string
}

// Provider is a metadata source.
type Provider interface {
	Name() Source
	Search(ctx context.Context, typ, q string) ([]SearchResult, error)
	Media(ctx context.Context, typ, id string) (Media, error)
	Cast(ctx context.Context, id string) ([]CastMember, error)
}

var (
	// ErrNotFound: AniList does not know the requested item.
	ErrNotFound = errors.New("anilist: not found")
	// ErrRateLimited: AniList throttled the request.
	ErrRateLimited = errors.New("anilist: rate limited")
	// ErrUnavailable: server error, network error, timeout or unreadable response.
	ErrUnavailable = errors.New("anilist: unavailable")
	// ErrBadResponse: AniList answered with something the client cannot use
	// and would answer the same on a retry (malformed data on a 200, a GraphQL
	// error that is not "not found", an unexpected 4xx status).
	ErrBadResponse = errors.New("anilist: bad response")
)
