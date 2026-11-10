package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// ErrNotFound: no anime row with the given id.
var ErrNotFound = errors.New("store: anime not found")

// AlreadySavedError is returned by ConvertToSource when the target
// (source, source_id) already belongs to another entry. It satisfies
// errors.Is(err, ErrAlreadySaved) and carries that entry's anime id.
type AlreadySavedError struct{ ExistingID int64 }

func (e *AlreadySavedError) Error() string {
	return fmt.Sprintf("%v (anime %d)", ErrAlreadySaved, e.ExistingID)
}

// Is makes errors.Is(err, ErrAlreadySaved) true.
func (e *AlreadySavedError) Is(target error) bool { return target == ErrAlreadySaved }

// ConvertToSource turns the manual entry animeID into the saved anime a, in
// place and in one transaction (library.ConvertToSource): the anime keeps its
// id (so the list entry, which is only touched to clamp progress, see below,
// and cast links stay attached) while source, source_id, type, episodes,
// year, studio, synopsis, cover, external URL, source material and dates are
// replaced, titles and relations are replaced (same insertion code as
// SaveAnime), the MAL id is set when a.MALID is non-empty (else kept) and
// sync_state becomes "pending" so the sync job fetches the cast and downloads
// the cover.
//
// When a.Episodes is known and the entry's progress exceeds it, progress is
// clamped to a.Episodes (updated_at then changes too; otherwise the list entry
// is untouched).
//
// The cover is replaced by the remote a.CoverURL; a previously uploaded cover
// file stays on disk (content-addressed, possibly shared) and is simply no
// longer referenced. Callers must fetch a from the provider before calling, so
// that a provider failure never reaches the database.
//
// Errors: ErrNotFound (no such row), ErrInvalid (row is not a manual entry, or
// a is not a usable target), *AlreadySavedError (a belongs to another entry).
// Any failure rolls everything back.
func (s *Store) ConvertToSource(ctx context.Context, animeID int64, a anilist.Anime) error {
	if a.Source == "" || a.Source == anilist.Manual || a.ID == "" {
		return invalid("the match target needs a provider source and id")
	}
	d, err := itemData(a)
	if err != nil {
		return err
	}
	err = s.lib.ConvertToSource(ctx, animeID, d)
	var ase *library.AlreadySavedError
	switch {
	case errors.Is(err, library.ErrNoEntry):
		return ErrNotFound
	case errors.As(err, &ase):
		return &AlreadySavedError{ExistingID: ase.ExistingID}
	}
	return wrapf("convert anime", err)
}
