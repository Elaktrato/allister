package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/allister/allister/internal/provider"
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
// place and in one transaction: the anime row keeps its id (so the list entry,
// which is only touched to clamp progress, see below, and cast links stay attached) while source,
// source_id, type, episodes, year, studio, synopsis, cover, external URL,
// source material and dates are replaced, titles and relations are replaced
// (same insertion code as SaveAnime), mal_id is set when a.MALID is non-empty
// (else kept) and sync_state becomes "pending" so the sync job fetches the
// cast and downloads the cover.
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
func (s *Store) ConvertToSource(ctx context.Context, animeID int64, a provider.Anime) error {
	if a.Source == "" || a.Source == provider.Manual || a.ID == "" {
		return invalid("the match target needs a provider source and id")
	}
	var mal any // nil keeps the current mal_id (COALESCE below)
	if a.MALID != "" {
		n, err := strconv.ParseInt(a.MALID, 10, 64)
		if err != nil {
			return invalid("MAL id %q is not a number", a.MALID)
		}
		mal = n
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("convert anime: %w", err)
	}
	defer tx.Rollback()

	var cur string
	switch err := tx.QueryRowContext(ctx, `SELECT source FROM anime WHERE id = ?`, animeID).Scan(&cur); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("convert anime: %w", err)
	}
	if provider.Source(cur) != provider.Manual {
		return invalid("only manual entries can be matched")
	}
	var existing int64
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM anime WHERE source = ? AND source_id = ?`,
		string(a.Source), a.ID).Scan(&existing); {
	case err == nil:
		return &AlreadySavedError{ExistingID: existing}
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("convert anime: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE anime SET source = ?, source_id = ?, type = ?, episodes = ?, year = ?,
		studio = ?, synopsis = ?, cover_path = ?, external_url = ?, source_material = ?, started_on = ?, ended_on = ?,
		mal_id = COALESCE(?, mal_id), sync_state = 'pending' WHERE id = ?`,
		string(a.Source), a.ID, a.Type, intArg(a.Episodes), intArg(a.Year), a.Studio, a.Synopsis, a.CoverURL,
		a.ExternalURL, a.SourceMaterial, fmtDate(a.StartDate), fmtDate(a.EndDate), mal, animeID); err != nil {
		return fmt.Errorf("convert anime: %w", err)
	}
	for _, q := range []string{`DELETE FROM anime_titles WHERE anime_id = ?`, `DELETE FROM anime_relations WHERE anime_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, animeID); err != nil {
			return fmt.Errorf("convert anime: %w", err)
		}
	}
	if err := insertTitlesAndRelations(ctx, tx, animeID, a); err != nil {
		return err
	}
	// A known episode count below the entry's progress would show 30/12, hide
	// +1 and make every edit fail validation: clamp (and only then touch
	// updated_at).
	if a.Episodes != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE list_entries SET progress = ?, updated_at = `+nowSQL+`
			WHERE anime_id = ? AND progress > ?`, *a.Episodes, animeID, *a.Episodes); err != nil {
			return fmt.Errorf("convert anime: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("convert anime: %w", err)
	}
	return nil
}
