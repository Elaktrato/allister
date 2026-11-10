package store

import (
	"context"
	"errors"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// UpsertListEntry inserts or replaces the list entry of e.AnimeID and sets
// updated_at. It returns an error wrapping ErrInvalid for an unknown status
// or anime, rating outside 0 to 10, negative progress, progress above a known
// episode total, or FinishedOn before StartedOn.
func (s *Store) UpsertListEntry(ctx context.Context, e ListEntry) error {
	it, err := s.lib.Item(ctx, e.AnimeID)
	if err != nil {
		return wrapf("upsert list entry", err)
	}
	sv := saved(it)
	if sv == nil {
		return invalid("anime %d does not exist", e.AnimeID)
	}
	if err := ValidateListEntry(e, sv.Episodes); err != nil {
		return err
	}
	// the library validates again inside its transaction
	err = s.lib.UpsertEntry(ctx, entry(e))
	if errors.Is(err, library.ErrNoEntry) {
		return invalid("anime %d does not exist", e.AnimeID)
	}
	return wrapf("upsert list entry", err)
}

// ValidateListEntry checks e without touching the database: known status,
// rating 0 to 10 or nil, progress between 0 and the episode total (when
// episodes is non-nil), FinishedOn not before StartedOn. Errors wrap
// ErrInvalid. The rules are the library's (ValidateEntry against the anime
// kind); only the messages keep their legacy wording.
func ValidateListEntry(e ListEntry, episodes *int) error {
	if !e.Status.valid() {
		return invalid("unknown status %q", e.Status) // the legacy vocabulary
	}
	if err := validator.ValidateEntry(animeKindDef, animeItem(episodes), entry(e)); err != nil {
		return legacyEntryError(e, episodes, err)
	}
	return nil
}

// AddToList saves the anime and its list entry in one transaction and returns
// the new anime id. e.AnimeID is ignored. An invalid entry (see
// ValidateListEntry, checked against a.Episodes) wraps ErrInvalid and saves
// nothing; an existing (source, source_id) returns ErrAlreadySaved.
func (s *Store) AddToList(ctx context.Context, a anilist.Anime, e ListEntry, opts ...SaveOption) (int64, error) {
	if err := ValidateListEntry(e, a.Episodes); err != nil {
		return 0, err
	}
	d, err := itemData(a)
	if err != nil {
		return 0, err
	}
	id, err := s.lib.AddToList(ctx, animePlugin, animeKind, d, entry(e), libOpts(opts)...)
	if err != nil {
		return 0, wrapf("add to list", err)
	}
	return id, nil
}

// GetListEntry returns the list entry or nil, nil when absent.
func (s *Store) GetListEntry(ctx context.Context, animeID int64) (*ListEntry, error) {
	e, err := s.lib.Entry(ctx, animeID)
	if err != nil || e == nil {
		return nil, wrapf("get list entry", err)
	}
	le := listEntry(*e)
	return &le, nil
}

// RemoveListEntry deletes the list entry (with its progress) only; the anime
// stays saved.
func (s *Store) RemoveListEntry(ctx context.Context, animeID int64) error {
	return s.lib.RemoveEntry(ctx, animeID)
}

// IncrementProgress adds 1 to the progress, never above a known episode
// total (no-op at the total; counts on when the total is unknown). It returns
// ErrNoEntry when the anime has no list entry.
func (s *Store) IncrementProgress(ctx context.Context, animeID int64) error {
	err := s.lib.Increment(ctx, animeID)
	if errors.Is(err, library.ErrNoEntry) {
		return ErrNoEntry
	}
	return mapErr(err)
}

// List returns all list rows, newest updated first, optionally filtered by status.
func (s *Store) List(ctx context.Context, status *Status) ([]ListRow, error) {
	return s.ListPage(ctx, status, -1, 0)
}

// ListPage is List restricted to limit rows starting at offset (limit < 0
// means no limit). The order is stable: updated_at, then id, newest first.
func (s *Store) ListPage(ctx context.Context, status *Status, limit, offset int) ([]ListRow, error) {
	var core *library.Status
	if status != nil {
		c, ok := toCore[*status]
		if !ok {
			return nil, nil // no entry has an unknown status
		}
		core = &c
	}
	rows, err := s.lib.Rows(ctx, animeQuery(core), "", limit, offset)
	if err != nil {
		return nil, wrapf("list", err)
	}
	var out []ListRow
	for _, r := range rows {
		out = append(out, listRow(r))
	}
	return out, nil
}

// GetListRow returns the list row of one anime, or nil, nil when it has no
// list entry.
func (s *Store) GetListRow(ctx context.Context, animeID int64) (*ListRow, error) {
	r, err := s.lib.Row(ctx, animeID, "")
	if err != nil || r == nil || r.Item.PluginID != animePlugin {
		return nil, wrapf("list", err)
	}
	lr := listRow(*r)
	return &lr, nil
}

// DeleteAnime removes the anime with its titles, relations, cast rows and
// list entry (foreign key cascades). People rows and cover files stay. It
// returns ErrNoEntry when the id does not exist.
func (s *Store) DeleteAnime(ctx context.Context, animeID int64) error {
	err := s.lib.DeleteItem(ctx, animeID)
	if errors.Is(err, library.ErrNoEntry) {
		return ErrNoEntry
	}
	return wrapf("delete anime", err)
}

// Counts returns the number of list entries per status (absent = 0).
func (s *Store) Counts(ctx context.Context) (map[Status]int, error) {
	m, err := s.lib.Counts(ctx, animeQuery(nil))
	if err != nil {
		return nil, wrapf("counts", err)
	}
	out := map[Status]int{}
	for st, n := range m {
		if n > 0 {
			out[fromCore(st)] = n
		}
	}
	return out, nil
}
