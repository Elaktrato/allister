package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/allister/allister/internal/provider"
)

// SaveOption customises SaveAnime.
type SaveOption func(*saveOpts)

type saveOpts struct{ syncState string }

// WithSyncState sets the initial sync_state (default "pending"). Manual
// entries use WithSyncState("done") since there is nothing to sync.
func WithSyncState(state string) SaveOption {
	return func(o *saveOpts) { o.syncState = state }
}

// SaveAnime stores the anime row, all titles (de-duplicated) and relations in
// one transaction and returns the new id. It returns ErrAlreadySaved when
// (source, source_id) exists. The cover URL is stored in cover_path (see the
// package comment). The transaction is IMMEDIATE (see db.Open), so the
// existence check and the insert cannot race.
func (s *Store) SaveAnime(ctx context.Context, a provider.Anime, opts ...SaveOption) (int64, error) {
	o := saveOpts{syncState: "pending"}
	for _, f := range opts {
		f(&o)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("save anime: %w", err)
	}
	defer tx.Rollback()
	id, err := saveAnimeTx(ctx, tx, a, o)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("save anime: %w", err)
	}
	return id, nil
}

// saveAnimeTx does the work of SaveAnime inside the caller's transaction.
func saveAnimeTx(ctx context.Context, tx *sql.Tx, a provider.Anime, o saveOpts) (int64, error) {
	var mal any
	if a.MALID != "" {
		n, err := strconv.ParseInt(a.MALID, 10, 64)
		if err != nil {
			return 0, invalid("MAL id %q is not a number", a.MALID)
		}
		mal = n
	}
	var existing int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM anime WHERE source = ? AND source_id = ?`, string(a.Source), a.ID).Scan(&existing)
	if err == nil {
		return 0, ErrAlreadySaved
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("save anime: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO anime
		(source, source_id, type, episodes, year, studio, synopsis, cover_path, external_url,
		 source_material, started_on, ended_on, mal_id, sync_state)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(a.Source), a.ID, a.Type, intArg(a.Episodes), intArg(a.Year), a.Studio, a.Synopsis,
		a.CoverURL, a.ExternalURL, a.SourceMaterial, fmtDate(a.StartDate), fmtDate(a.EndDate), mal, o.syncState)
	if err != nil {
		return 0, fmt.Errorf("save anime: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save anime: %w", err)
	}
	for _, t := range a.Titles {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO anime_titles(anime_id, lang, text) VALUES (?,?,?)`, id, t.Lang, t.Text); err != nil {
			return 0, fmt.Errorf("save titles: %w", err)
		}
	}
	for _, r := range a.Relations {
		tj, err := json.Marshal(r.Titles)
		if err != nil {
			return 0, fmt.Errorf("save relations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO anime_relations
			(anime_id, kind, rel_source, rel_source_id, titles_json, type) VALUES (?,?,?,?,?,?)`,
			id, r.Kind, string(r.Source), r.ID, string(tj), r.Type); err != nil {
			return 0, fmt.Errorf("save relations: %w", err)
		}
	}
	return id, nil
}

// FindBySource returns the saved anime with titles and relations, or nil, nil.
func (s *Store) FindBySource(ctx context.Context, source provider.Source, id string) (*Saved, error) {
	return s.findOne(ctx, `a.source = ? AND a.source_id = ?`, string(source), id)
}

// FindByMALID returns the saved anime whose mal_id matches, or nil, nil.
func (s *Store) FindByMALID(ctx context.Context, malID string) (*Saved, error) {
	n, err := strconv.ParseInt(malID, 10, 64)
	if err != nil {
		return nil, nil // empty or non-numeric ids cannot match
	}
	return s.findOne(ctx, `a.mal_id = ?`, n)
}

// GetByID returns the saved anime with the given row id, or nil, nil.
func (s *Store) GetByID(ctx context.Context, id int64) (*Saved, error) {
	return s.findOne(ctx, `a.id = ?`, id)
}

func (s *Store) findOne(ctx context.Context, where string, args ...any) (*Saved, error) {
	var (
		sv             Saved
		src            string
		eps, year, mal sql.NullInt64
		started, ended sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT a.id, a.source, a.source_id, a.type, a.episodes, a.year,
		a.studio, a.synopsis, a.cover_path, a.external_url, a.source_material, a.started_on, a.ended_on,
		a.mal_id, a.sync_state FROM anime a WHERE `+where+` LIMIT 1`, args...).Scan(
		&sv.ID, &src, &sv.Anime.ID, &sv.Type, &eps, &year, &sv.Studio, &sv.Synopsis, &sv.CoverPath,
		&sv.ExternalURL, &sv.SourceMaterial, &started, &ended, &mal, &sv.SyncState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find anime: %w", err)
	}
	sv.Source = provider.Source(src)
	sv.Episodes, sv.Year = nullInt(eps), nullInt(year)
	sv.StartDate, sv.EndDate = parseDate(started), parseDate(ended)
	sv.CoverURL = sv.CoverPath
	if mal.Valid {
		sv.MALID = strconv.FormatInt(mal.Int64, 10)
	}
	if sv.Titles, err = s.titles(ctx, sv.ID); err != nil {
		return nil, err
	}
	if sv.Relations, err = s.relations(ctx, sv.ID); err != nil {
		return nil, err
	}
	return &sv, nil
}

func (s *Store) titles(ctx context.Context, animeID int64) ([]provider.Title, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lang, text FROM anime_titles WHERE anime_id = ? ORDER BY rowid`, animeID)
	if err != nil {
		return nil, fmt.Errorf("load titles: %w", err)
	}
	defer rows.Close()
	out := []provider.Title{}
	for rows.Next() {
		var t provider.Title
		if err := rows.Scan(&t.Lang, &t.Text); err != nil {
			return nil, fmt.Errorf("load titles: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) relations(ctx context.Context, animeID int64) ([]provider.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, rel_source, rel_source_id, titles_json, type
		FROM anime_relations WHERE anime_id = ? ORDER BY rowid`, animeID)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()
	var out []provider.Relation
	for rows.Next() {
		var r provider.Relation
		var src, tj string
		if err := rows.Scan(&r.Kind, &src, &r.ID, &tj, &r.Type); err != nil {
			return nil, fmt.Errorf("load relations: %w", err)
		}
		r.Source = provider.Source(src)
		if err := json.Unmarshal([]byte(tj), &r.Titles); err != nil {
			return nil, fmt.Errorf("load relations: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetAnimeCoverPath replaces the cover (remote URL or local path).
func (s *Store) SetAnimeCoverPath(ctx context.Context, animeID int64, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE anime SET cover_path = ? WHERE id = ?`, path, animeID)
	return err
}

// SetSyncState sets anime.sync_state (pending, done, ...).
func (s *Store) SetSyncState(ctx context.Context, animeID int64, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE anime SET sync_state = ? WHERE id = ?`, state, animeID)
	return err
}

// CoverReferenced reports whether any anime cover or person image uses the
// stored value (a covers-dir file name). Content-hash file names are shared,
// so callers check this before deleting a file.
func (s *Store) CoverReferenced(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM anime WHERE cover_path = ?) +
		(SELECT COUNT(*) FROM people WHERE image_path = ?)`, name, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("cover referenced: %w", err)
	}
	return n > 0, nil
}
