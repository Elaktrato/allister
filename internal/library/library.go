// Package library persists the generic media library: items of any plugin
// kind with titles, typed fields, external ids, relations, genres and tags,
// plus the people and credits attached to them.
//
// The library validates data against the KindDef of the item's plugin, taken
// from the registry's validated manifest snapshot. It does NOT check whether a
// plugin is enabled; that is the web layer's job.
//
// Image convention: items.cover_path and people_g.image_path initially hold
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

// SQL table names that change when the legacy tables are retired: the final
// names are "people" and "relations". Keep every use of them going through
// these constants.
const (
	tablePeople    = "people_g"
	tableRelations = "relations_g"
)

var (
	// ErrAlreadySaved: an item with the same (plugin, kind, source, source_id)
	// exists. SaveItem returns an *AlreadySavedError that matches it.
	ErrAlreadySaved = errors.New("library: item already saved")
	// ErrInvalid is wrapped by errors that describe invalid input; the error
	// text carries the human-readable reason.
	ErrInvalid = errors.New("invalid")
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
	return l.setOne(ctx, "set cover path", `UPDATE items SET cover_path = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`, path, itemID)
}

// SetSyncState sets the item's sync state.
func (l *Library) SetSyncState(ctx context.Context, itemID int64, state string) error {
	return l.setOne(ctx, "set sync state", `UPDATE items SET sync_state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`, state, itemID)
}

// SetPersonImagePath replaces the person's image (remote URL or local path).
func (l *Library) SetPersonImagePath(ctx context.Context, personID int64, path string) error {
	return l.setOne(ctx, "set person image", `UPDATE `+tablePeople+` SET image_path = ? WHERE id = ?`, path, personID)
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
