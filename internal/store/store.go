// Package store persists saved anime, their cast and the user's list entries.
//
// Image convention: anime.cover_path and people.image_path initially hold the
// remote URL (starting with "http") until the image has been downloaded and a
// local path replaces it via SetAnimeCoverPath / SetPersonImagePath.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

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
	AnimeID   int64
	Titles    []provider.Title
	CoverPath string
	Character string
}

// Store is the persistence layer over *sql.DB.
type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

const dateLayout = "2006-01-02"

func fmtDate(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(dateLayout) // calendar day in the input's own zone
}

func parseDate(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func intArg(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
