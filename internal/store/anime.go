package store

import (
	"context"
	"fmt"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// SaveOption customises SaveAnime.
type SaveOption func(*saveOpts)

type saveOpts struct{ syncState string }

// WithSyncState sets the initial sync_state (default "pending"). Manual
// entries use WithSyncState("done") since there is nothing to sync.
func WithSyncState(state string) SaveOption {
	return func(o *saveOpts) { o.syncState = state }
}

func libOpts(opts []SaveOption) []library.SaveOption {
	o := saveOpts{syncState: "pending"}
	for _, f := range opts {
		f(&o)
	}
	return []library.SaveOption{library.WithSyncState(o.syncState)}
}

// SaveAnime stores the anime with all titles (de-duplicated) and relations in
// one transaction and returns the new id. It returns ErrAlreadySaved when
// (source, source_id) exists. The cover URL is stored in cover_path (see the
// package comment). The library's transaction is IMMEDIATE (see db.Open), so
// the existence check and the insert cannot race.
func (s *Store) SaveAnime(ctx context.Context, a anilist.Anime, opts ...SaveOption) (int64, error) {
	d, err := itemData(a)
	if err != nil {
		return 0, err
	}
	id, err := s.lib.SaveItem(ctx, animePlugin, animeKind, d, libOpts(opts)...)
	if err != nil {
		return 0, wrapf("save anime", err)
	}
	return id, nil
}

// FindBySource returns the saved anime with titles and relations, or nil, nil.
func (s *Store) FindBySource(ctx context.Context, source anilist.Source, id string) (*Saved, error) {
	it, err := s.lib.ItemBySource(ctx, animePlugin, animeKind, string(source), id)
	if err != nil {
		return nil, wrapf("find anime", err)
	}
	return saved(it), nil
}

// FindByMALID returns the saved anime whose MAL id matches, or nil, nil.
func (s *Store) FindByMALID(ctx context.Context, malID string) (*Saved, error) {
	mal, ok := canonicalMAL(malID)
	if !ok {
		return nil, nil // empty or non-numeric ids cannot match
	}
	it, err := s.lib.ItemByExternalID(ctx, animePlugin, animeKind, extMAL, mal)
	if err != nil {
		return nil, wrapf("find anime", err)
	}
	return saved(it), nil
}

// GetByID returns the saved anime with the given row id, or nil, nil.
func (s *Store) GetByID(ctx context.Context, id int64) (*Saved, error) {
	it, err := s.lib.Item(ctx, id)
	if err != nil {
		return nil, wrapf("find anime", err)
	}
	return saved(it), nil
}

// SetAnimeCoverPath replaces the cover (remote URL or local file name). An
// unknown id is a no-op.
func (s *Store) SetAnimeCoverPath(ctx context.Context, animeID int64, path string) error {
	return ignoreMissing(s.lib.SetCoverPath(ctx, animeID, path))
}

// SetAnimeCoverPathIf sets the cover to path only while it still holds
// expected (library.SetCoverPathIf); false means the cover changed meanwhile
// (or the anime is gone).
func (s *Store) SetAnimeCoverPathIf(ctx context.Context, animeID int64, expected, path string) (bool, error) {
	ok, err := s.lib.SetCoverPathIf(ctx, animeID, expected, path)
	return ok, wrapf("set cover", err)
}

// SetSyncState sets the anime's sync_state (pending, partial, done). An
// unknown id is a no-op.
func (s *Store) SetSyncState(ctx context.Context, animeID int64, state string) error {
	return ignoreMissing(s.lib.SetSyncState(ctx, animeID, state))
}

// CoverReferenced reports whether any anime cover or person image uses the
// stored value (a covers-dir file name). Content-hash file names are shared,
// so callers check this before deleting a file.
func (s *Store) CoverReferenced(ctx context.Context, name string) (bool, error) {
	ok, err := s.lib.ImageReferenced(ctx, name)
	return ok, wrapf("cover referenced", err)
}

// NeedsSync returns the ids of anime whose sync_state is pending or partial
// and that have no job of the given kind in state pending or running (jobs
// match by payload anime_id). Failed and done jobs do not count as live.
func (s *Store) NeedsSync(ctx context.Context, jobKind string) ([]int64, error) {
	ids, err := s.lib.ItemsToSync(ctx, animePlugin)
	if err != nil || len(ids) == 0 {
		return nil, wrapf("needs sync", err)
	}
	live, err := s.liveJobAnime(ctx, jobKind)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, id := range ids {
		if !live[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// liveJobAnime returns the payload anime ids of the pending or running jobs of
// the kind. The jobs table belongs to internal/jobs, not to the library, so it
// is read here as before.
func (s *Store) liveJobAnime(ctx context.Context, jobKind string) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json_extract(payload_json, '$.anime_id') FROM jobs
		WHERE kind = ? AND state IN ('pending','running')`, jobKind)
	if err != nil {
		return nil, fmt.Errorf("needs sync: %w", err)
	}
	defer rows.Close()
	live := map[int64]bool{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("needs sync: %w", err)
		}
		switch n := v.(type) { // a JSON number, as the former SQL comparison required
		case int64:
			live[n] = true
		case float64:
			if n == float64(int64(n)) {
				live[int64(n)] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("needs sync: %w", err)
	}
	return live, nil
}
