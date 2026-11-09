// Package store persists saved anime, their cast and the user's list entries.
//
// It is a thin adapter over the generic library (internal/library): every
// anime is an item of plugin "anime", kind "anime"; list entries, progress
// (key "episode"), cast (credits) and people live in the library's tables.
// The legacy API and its observable behaviour are kept; validation and
// persistence are delegated to the library (see adapter.go for the mapping).
//
// Image convention: items.cover_path and people.image_path initially hold the
// remote URL (starting with "http") until the image has been downloaded and a
// local file name replaces it via SetAnimeCoverPath / SetPersonImagePath.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/provider"
)

var (
	// ErrAlreadySaved: an anime with the same (source, source_id) exists.
	ErrAlreadySaved = errors.New("store: anime already saved")
	// ErrInvalid is wrapped by errors that describe invalid input; the
	// error text carries the human-readable reason.
	ErrInvalid = errors.New("invalid")
	// ErrNoEntry: the anime has no list entry.
	ErrNoEntry = errors.New("store: no list entry")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Status is the watch status of a list entry.
type Status string

const (
	Watching    Status = "watching"
	PlanToWatch Status = "plan_to_watch"
	Paused      Status = "paused"
	Completed   Status = "completed"
	Dropped     Status = "dropped"
)

func (s Status) valid() bool {
	switch s {
	case Watching, PlanToWatch, Paused, Completed, Dropped:
		return true
	}
	return false
}

// ListEntry is the user's tracking data for one saved anime. Rating nil means
// unrated (0 to 10 are valid). Dates are date-only, UTC midnight.
type ListEntry struct {
	AnimeID               int64
	Status                Status
	Rating                *int
	Progress              int
	StartedOn, FinishedOn *time.Time
	Comment               string
}

// Saved is a stored anime. Titles are in the embedded Anime's
// SearchResult.Titles, Relations in Anime.Relations; Recommendations is
// always empty. MALID is "" when unknown.
type Saved struct {
	ID int64
	provider.Anime
	CoverPath, SyncState string
}

// ListRow is one row of the list view.
type ListRow struct {
	AnimeID   int64
	Titles    []provider.Title
	CoverPath string
	Type      string
	Episodes  *int
	Entry     ListEntry
	SyncState string
	// Source and SourceID identify the anime; MALID is "" when unknown.
	Source   provider.Source
	SourceID string
	MALID    string
}

// Person is a stored character or voice actor.
type Person struct {
	ID                                     int64
	Source                                 provider.Source
	SourceID, Name, ImagePath, ExternalURL string
}

// PersonAppearance is a saved anime a person appears in.
type PersonAppearance struct {
	AnimeID int64
	// Source and SourceID identify the anime at its provider.
	Source    provider.Source
	SourceID  string
	Titles    []provider.Title
	CoverPath string
	// Character is the voiced character's name when AsActor, else the
	// person's own name (the person is the character).
	Character string
	// AsActor is true when the person is the voice actor of Character.
	AsActor bool
}

// Store is the persistence layer over *sql.DB.
type Store struct {
	db  *sql.DB
	lib *library.Library
}

// New returns a Store over db. It builds its own library with a registry that
// holds the anime plugin's manifest (see adapter.go).
func New(db *sql.DB) *Store { return &Store{db: db, lib: library.New(db, animeRegistry)} }

// DB returns the database the store works on (the web layer builds its
// generic library over the same database).
func (s *Store) DB() *sql.DB { return s.db }

const dateLayout = "2006-01-02"
