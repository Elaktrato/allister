// Package provider defines the types shared with metadata providers.
package provider

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
	Kind   string
	Source Source
	ID     string
	Titles []Title
	Type   string
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
	Year                       *int
	Studio, Synopsis, CoverURL string
}

// Anime is the full detail of an anime.
type Anime struct {
	SearchResult
	StartDate, EndDate          *time.Time
	SourceMaterial, ExternalURL string
	MALID                       string
	Relations                   []Relation
	Recommendations             []Recommendation
}

// Provider is a metadata source.
type Provider interface {
	Name() Source
	Search(ctx context.Context, q string) ([]SearchResult, error)
	Anime(ctx context.Context, id string) (Anime, error)
	Cast(ctx context.Context, id string) ([]CastMember, error)
}

var (
	// ErrNotFound: the provider does not know the requested item.
	ErrNotFound = errors.New("provider: not found")
	// ErrRateLimited: the provider throttled the request.
	ErrRateLimited = errors.New("provider: rate limited")
	// ErrUnavailable: server error, network error, timeout or unreadable response.
	ErrUnavailable = errors.New("provider: unavailable")
)
