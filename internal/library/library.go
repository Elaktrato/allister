// Package library persists the generic media library: items of any plugin
// kind with titles, typed fields, external ids, relations, genres and tags,
// plus the people and credits attached to them.
//
// The library validates data against the KindDef of the item's plugin, taken
// from the registry's validated manifest snapshot. It does NOT check whether a
// plugin is enabled; that is the web layer's job.
//
// Image convention: items.cover_path and people.image_path initially hold
// the remote URL (starting with "http") until the image has been downloaded
// and a local path replaces it via SetCoverPath / SetPersonImagePath.
//
// Dates are calendar days stored as YYYY-MM-DD text.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/plugin"
)

// nowSQL is the SQL expression for the current UTC time stamp.
const nowSQL = `strftime('%Y-%m-%dT%H:%M:%SZ','now')`

// SQL table names of people and relations (they were people_g and
// relations_g until migration 00007 retired the legacy tables). Keep every
// use of them going through these constants.
const (
	tablePeople    = "people"
	tableRelations = "relations"
)

var (
	// ErrAlreadySaved: an item with the same (plugin, kind, source, source_id)
	// exists. SaveItem returns an *AlreadySavedError that matches it.
	ErrAlreadySaved = errors.New("library: item already saved")
	// ErrInvalid is wrapped by errors that describe invalid input; the error
	// text carries the human-readable reason.
	ErrInvalid = errors.New("invalid")
	// ErrEntryExists: CreateEntry found an entry for the item already.
	ErrEntryExists = errors.New("library: item already has an entry")
	// ErrNoEntry: the item (or person) does not exist.
	ErrNoEntry = errors.New("library: no such entry")
)

// AlreadySavedError reports the item that already holds the identity.
type AlreadySavedError struct{ ExistingID int64 }

func (e *AlreadySavedError) Error() string {
	return fmt.Sprintf("%s (id %d)", ErrAlreadySaved.Error(), e.ExistingID)
}

// Is makes errors.Is(err, ErrAlreadySaved) true.
func (e *AlreadySavedError) Is(target error) bool { return target == ErrAlreadySaved }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Item is a stored item with all its parts. Credits are read separately
// (Credits, ItemPeople).
type Item struct {
	ID                                int64
	PluginID, Kind, Source, SourceID  string
	CoverPath, ExternalURL, SyncState string
	Titles                            []plugin.Title
	Fields                            map[string]plugin.FieldValue
	ExternalIDs                       map[string]string
	Relations                         []plugin.Relation
	ProgressKeys                      []string
	Genres                            []string
	Tags                              []plugin.Tag
}

// Person is a stored person (author, actor, character, ...), scoped to the
// plugin and source that named it.
type Person struct {
	ID                                                       int64
	PluginID, Source, SourceID, Name, ImagePath, ExternalURL string
}

// SavedCredit is a stored credit with its people resolved.
type SavedCredit struct {
	Role     string
	Detail   string
	Person   Person
	With     *Person
	WithRole string
	Seq      int
}

// Library is the persistence layer over *sql.DB.
type Library struct {
	db  *sql.DB
	reg *plugin.Registry
}

// New returns a Library that validates against the manifests in reg.
func New(db *sql.DB, reg *plugin.Registry) *Library { return &Library{db: db, reg: reg} }

// kindDef returns the validated KindDef of an installed plugin's kind.
func (l *Library) kindDef(pluginID, kind string) (plugin.KindDef, error) {
	m, ok := l.reg.Manifest(pluginID)
	if !ok {
		return plugin.KindDef{}, invalid("unknown plugin %q", pluginID)
	}
	for _, k := range m.Kinds {
		if k.Key == kind {
			return k, nil
		}
	}
	return plugin.KindDef{}, invalid("plugin %q has no kind %q", pluginID, kind)
}

// setOne runs an UPDATE of one item/person row and maps "no row" to ErrNoEntry.
func (l *Library) setOne(ctx context.Context, what, query string, args ...any) error {
	res, err := l.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return ErrNoEntry
	}
	return nil
}

// SetCoverPath replaces the item's cover (remote URL or local path).
func (l *Library) SetCoverPath(ctx context.Context, itemID int64, path string) error {
	if err := checkImageRef("cover", path); err != nil {
		return err
	}
	return l.setOne(ctx, "set cover path", `UPDATE items SET cover_path = ?, updated_at = `+nowSQL+` WHERE id = ?`, path, itemID)
}

// SetSyncState sets the item's sync state.
func (l *Library) SetSyncState(ctx context.Context, itemID int64, state string) error {
	if err := checkSyncState(state); err != nil {
		return err
	}
	return l.setOne(ctx, "set sync state", `UPDATE items SET sync_state = ?, updated_at = `+nowSQL+` WHERE id = ?`, state, itemID)
}

// SetPersonImagePath replaces the person's image (remote URL or local path).
func (l *Library) SetPersonImagePath(ctx context.Context, personID int64, path string) error {
	if err := checkImageRef("person image", path); err != nil {
		return err
	}
	return l.setOne(ctx, "set person image", `UPDATE `+tablePeople+` SET image_path = ? WHERE id = ?`, path, personID)
}

// ImageReferenced reports whether any item cover or person image holds the
// stored value name (for example a downloaded file's base name). Content-hash
// file names are shared, so callers check this before deleting a file. The
// empty name is never referenced.
func (l *Library) ImageReferenced(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var ok bool
	err := l.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM items WHERE cover_path = ?1)
		OR EXISTS(SELECT 1 FROM `+tablePeople+` WHERE image_path = ?1)`, name).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("image referenced: %w", err)
	}
	return ok, nil
}

// ItemsToSync returns the ids of the plugin's items whose sync state is
// pending or partial, in ascending id order.
func (l *Library) ItemsToSync(ctx context.Context, pluginID string) ([]int64, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND sync_state IN ('pending','partial') ORDER BY id`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("items to sync: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("items to sync: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("items to sync: %w", err)
	}
	return ids, nil
}

// NeedsSyncItem is an item that still needs a background sync, with its
// owning plugin (the sync job carries the plugin id for the queue's gate).
type NeedsSyncItem struct {
	ItemID   int64
	PluginID string
}

// NeedsSync returns the items of every plugin whose sync state is pending or
// partial and that have no live (pending or running) job of jobKind, in
// ascending id order. Jobs match by the integer "item_id" of their JSON
// payload; failed and done jobs are not live, rows whose payload is not
// JSON never match. The jobs table belongs to package jobs and is only read.
func (l *Library) NeedsSync(ctx context.Context, jobKind string) ([]NeedsSyncItem, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT i.id, i.plugin_id FROM items i
		WHERE i.sync_state IN ('pending','partial')
		AND NOT EXISTS (SELECT 1 FROM jobs j
			WHERE j.kind = ? AND j.state IN ('pending','running')
			AND CASE WHEN json_valid(j.payload_json) THEN json_extract(j.payload_json, '$.item_id') END = i.id)
		ORDER BY i.id`, jobKind)
	if err != nil {
		return nil, fmt.Errorf("needs sync: %w", err)
	}
	var out []NeedsSyncItem
	for rows.Next() {
		var it NeedsSyncItem
		if err := rows.Scan(&it.ItemID, &it.PluginID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("needs sync: %w", err)
		}
		out = append(out, it)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("needs sync: %w", err)
	}
	return out, nil
}

// DeleteItem removes the item and, by cascade, everything attached to it.
// People rows (and image files) stay. It returns ErrNoEntry for an unknown id.
func (l *Library) DeleteItem(ctx context.Context, id int64) error {
	res, err := l.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n == 0 {
		return ErrNoEntry
	}
	return nil
}
