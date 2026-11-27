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
	Adult     bool   // AniList's isAdult
}

// Recommendation references another anime recommended for this one.
type Recommendation struct {
	Source   Source
	ID       string
	Titles   []Title
	CoverURL string
	Adult    bool // AniList's isAdult
}

// SearchResult is the summary of an anime as returned by a search.
type SearchResult struct {
	Source                     Source
	ID                         string
	Titles                     []Title
	Type                       string
	Episodes                   *int
	Duration                   *int // minutes per episode
	Chapters, Volumes          *int
	Year                       *int
	Studio, Synopsis, CoverURL string
	// Adult is AniList's isAdult (see AdultValue).
	Adult bool
}

// Tag is a named tag with an optional relevance rank (0 to 100) and a spoiler flag.
type Tag struct {
	Name    string
	Rank    *int
	Spoiler bool
}

// AccountEntry is one entry of a user's AniList list, media included.
type AccountEntry struct {
	Status          string  `json:"status"` // CURRENT, PLANNING, COMPLETED, PAUSED, DROPPED, REPEATING
	Score           float64 `json:"score"`  // 0–10 (the query asks for POINT_10)
	Progress        int     `json:"progress"`
	ProgressVolumes int     `json:"progressVolumes"`
	UpdatedAt       int64   `json:"updatedAt"` // unix seconds
	Media           struct {
		ID     int    `json:"id"`
		MALID  int    `json:"idMal"`
		Format string `json:"format"`
		Title  struct {
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
	// Season is the airing season (WINTER, SPRING, SUMMER, FALL), Country
	// the country of origin as AniList gives it (an ISO code such as JP).
	Season, Country string
	// AverageScore is AniList's community average (0 to 100), Popularity
	// the number of users with the media on a list; nil when unknown.
	AverageScore, Popularity *int
	Synonyms                 []string
	// Studios are the media's animation studios (producers and licensors
	// left out), main studios first.
	Studios []string
	// StreamingLinks are the media's external links as given, each with
	// its AniList type (STREAMING, INFO, SOCIAL); the plugins keep the
	// STREAMING ones.
	StreamingLinks []LinkRef
	BannerURL      string
	Trailer        *TrailerRef
}

// LinkRef is one external link of a media: the site's name, the URL and
// AniList's link type.
type LinkRef struct{ Site, URL, Type string }

// TrailerRef is a media's trailer as AniList gives it: the video id at a
// site (youtube, dailymotion) and a thumbnail URL.
type TrailerRef struct{ ID, Site, Thumbnail string }

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
