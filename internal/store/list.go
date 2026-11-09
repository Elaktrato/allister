package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/provider"
)

const nowSQL = `strftime('%Y-%m-%dT%H:%M:%SZ','now')`

// UpsertListEntry inserts or replaces the list entry of e.AnimeID and sets
// updated_at. It returns an error wrapping ErrInvalid for an unknown status
// or anime, rating outside 0 to 10, negative progress, progress above a known
// episode total, or FinishedOn before StartedOn.
func (s *Store) UpsertListEntry(ctx context.Context, e ListEntry) error {
	var eps sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT episodes FROM anime WHERE id = ?`, e.AnimeID).Scan(&eps)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid("anime %d does not exist", e.AnimeID)
	}
	if err != nil {
		return fmt.Errorf("upsert list entry: %w", err)
	}
	if err := ValidateListEntry(e, nullInt(eps)); err != nil {
		return err
	}
	return upsertListEntry(ctx, s.db, e)
}

// ValidateListEntry checks e without touching the database: known status,
// rating 0 to 10 or nil, progress between 0 and the episode total (when
// episodes is non-nil), FinishedOn not before StartedOn. Errors wrap ErrInvalid.
func ValidateListEntry(e ListEntry, episodes *int) error {
	if !e.Status.valid() {
		return invalid("unknown status %q", e.Status)
	}
	if e.Rating != nil && (*e.Rating < 0 || *e.Rating > 10) {
		return invalid("rating must be between 0 and 10, got %d", *e.Rating)
	}
	if e.Progress < 0 {
		return invalid("progress must not be negative, got %d", e.Progress)
	}
	if e.StartedOn != nil && e.FinishedOn != nil && fmtDate(e.FinishedOn).(string) < fmtDate(e.StartedOn).(string) {
		return invalid("finish date is before start date")
	}
	if episodes != nil && e.Progress > *episodes {
		return invalid("progress %d is above the episode total %d", e.Progress, *episodes)
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func upsertListEntry(ctx context.Context, x execer, e ListEntry) error {
	_, err := x.ExecContext(ctx, `INSERT INTO list_entries
		(anime_id, status, rating, progress, started_on, finished_on, comment, updated_at)
		VALUES (?,?,?,?,?,?,?,`+nowSQL+`)
		ON CONFLICT(anime_id) DO UPDATE SET status=excluded.status, rating=excluded.rating,
		  progress=excluded.progress, started_on=excluded.started_on, finished_on=excluded.finished_on,
		  comment=excluded.comment, updated_at=excluded.updated_at`,
		e.AnimeID, string(e.Status), intArg(e.Rating), e.Progress, fmtDate(e.StartedOn), fmtDate(e.FinishedOn), e.Comment)
	if err != nil {
		return fmt.Errorf("upsert list entry: %w", err)
	}
	return nil
}

// AddToList saves the anime and its list entry in one transaction and returns
// the new anime id. e.AnimeID is ignored. An invalid entry (see
// ValidateListEntry, checked against a.Episodes) wraps ErrInvalid and saves
// nothing; an existing (source, source_id) returns ErrAlreadySaved.
func (s *Store) AddToList(ctx context.Context, a provider.Anime, e ListEntry, opts ...SaveOption) (int64, error) {
	o := saveOpts{syncState: "pending"}
	for _, f := range opts {
		f(&o)
	}
	if err := ValidateListEntry(e, a.Episodes); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("add to list: %w", err)
	}
	defer tx.Rollback()
	id, err := saveAnimeTx(ctx, tx, a, o)
	if err != nil {
		return 0, err
	}
	e.AnimeID = id
	if err := upsertListEntry(ctx, tx, e); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("add to list: %w", err)
	}
	return id, nil
}

// GetListEntry returns the list entry or nil, nil when absent.
func (s *Store) GetListEntry(ctx context.Context, animeID int64) (*ListEntry, error) {
	var (
		e          ListEntry
		st         string
		rating     sql.NullInt64
		start, end sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT anime_id, status, rating, progress, started_on, finished_on, comment
		FROM list_entries WHERE anime_id = ?`, animeID).Scan(&e.AnimeID, &st, &rating, &e.Progress, &start, &end, &e.Comment)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get list entry: %w", err)
	}
	e.Status, e.Rating, e.StartedOn, e.FinishedOn = Status(st), nullInt(rating), parseDate(start), parseDate(end)
	return &e, nil
}

// RemoveListEntry deletes the list entry only; the anime stays saved.
func (s *Store) RemoveListEntry(ctx context.Context, animeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM list_entries WHERE anime_id = ?`, animeID)
	return err
}

// IncrementProgress adds 1 to the progress, never above a known episode
// total (no-op at the total; counts on when the total is unknown). It returns
// ErrNoEntry when the anime has no list entry.
func (s *Store) IncrementProgress(ctx context.Context, animeID int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE list_entries SET progress = progress + 1, updated_at = `+nowSQL+`
		WHERE anime_id = ? AND (
		  (SELECT episodes FROM anime WHERE id = list_entries.anime_id) IS NULL
		  OR progress < (SELECT episodes FROM anime WHERE id = list_entries.anime_id))`, animeID)
	if err != nil {
		return fmt.Errorf("increment progress: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM list_entries WHERE anime_id = ?`, animeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoEntry
	}
	if err != nil {
		return fmt.Errorf("increment progress: %w", err)
	}
	return nil // at the total already
}

// List returns list rows, newest updated first, optionally filtered by status.
func (s *Store) List(ctx context.Context, status *Status) ([]ListRow, error) {
	q := `SELECT a.id, a.cover_path, a.type, a.episodes, a.sync_state,
		l.status, l.rating, l.progress, l.started_on, l.finished_on, l.comment
		FROM list_entries l JOIN anime a ON a.id = l.anime_id`
	var args []any
	if status != nil {
		q += ` WHERE l.status = ?`
		args = append(args, string(*status))
	}
	q += ` ORDER BY l.updated_at DESC, a.id DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	var out []ListRow
	for rows.Next() {
		var (
			r          ListRow
			eps, rat   sql.NullInt64
			st         string
			start, end sql.NullString
		)
		if err := rows.Scan(&r.AnimeID, &r.CoverPath, &r.Type, &eps, &r.SyncState,
			&st, &rat, &r.Entry.Progress, &start, &end, &r.Entry.Comment); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list: %w", err)
		}
		r.Episodes = nullInt(eps)
		r.Entry.AnimeID, r.Entry.Status, r.Entry.Rating = r.AnimeID, Status(st), nullInt(rat)
		r.Entry.StartedOn, r.Entry.FinishedOn = parseDate(start), parseDate(end)
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close() // release the connection before the title lookups
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	for i := range out {
		if out[i].Titles, err = s.titles(ctx, out[i].AnimeID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Counts returns the number of list entries per status (absent = 0).
func (s *Store) Counts(ctx context.Context) (map[Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM list_entries GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("counts: %w", err)
	}
	defer rows.Close()
	out := map[Status]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("counts: %w", err)
		}
		out[Status(st)] = n
	}
	return out, rows.Err()
}
