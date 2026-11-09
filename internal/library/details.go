package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// DetailsUpdate is a local edit of an item's own data ("Edit details"). It
// replaces, as a whole:
//   - Titles (at least one must remain);
//   - the values of the kind's EDITABLE fields: Fields holds the new value of
//     each editable field, a field that is absent (or has no value) is
//     cleared; a key that is unknown or not editable is rejected, and fields
//     that are not editable keep their stored values;
//   - the genres when the kind enables them as editable, and the tags when
//     the kind enables them as editable (terms of a kind that does not allow
//     editing them must be empty and stay as stored);
//   - CoverPath, the stored cover value ("" clears it).
//
// Source, source id, external ids, relations, sync state, progress keys,
// credits and the list entry are never touched.
type DetailsUpdate struct {
	Titles    []plugin.Title
	Fields    map[string]plugin.FieldValue
	Genres    []string
	Tags      []plugin.Tag
	CoverPath string
}

// preparedDetails is the validated form of a DetailsUpdate.
type preparedDetails struct {
	titles       []plugin.Title
	fields       []fieldRow
	editable     []string // keys of the kind's editable fields
	terms        []termRow
	genres, tags bool // replace the stored genres / tags
}

// queryer is what both *sql.DB and *sql.Tx offer for reads.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// detailsMeta reads the item's plugin and kind and the keys of its stored
// fields. ErrNoEntry for an unknown item.
func detailsMeta(ctx context.Context, q queryer, itemID int64) (pluginID, kind string, stored []string, err error) {
	switch err := q.QueryRowContext(ctx, `SELECT plugin_id, kind FROM items WHERE id = ?`, itemID).Scan(&pluginID, &kind); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", nil, ErrNoEntry
	case err != nil:
		return "", "", nil, fmt.Errorf("details: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT key FROM item_fields WHERE item_id = ?`, itemID)
	if err != nil {
		return "", "", nil, fmt.Errorf("details: %w", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return "", "", nil, fmt.Errorf("details: %w", err)
		}
		stored = append(stored, k)
	}
	if err := closeRows(rows); err != nil {
		return "", "", nil, fmt.Errorf("details: %w", err)
	}
	return pluginID, kind, stored, nil
}

// prepareDetails validates u against the kind exactly like SaveItem and
// ValidateItem do (text hygiene, bounds, enums, URLs, term limits, required
// fields). stored are the keys of the item's stored fields: the values of
// non-editable ones stay and count for the required check.
func prepareDetails(def plugin.KindDef, u DetailsUpdate, stored []string) (*preparedDetails, error) {
	if err := checkImageRef("cover", u.CoverPath); err != nil {
		return nil, err
	}
	p := &preparedDetails{genres: def.Genres.Enabled && def.Genres.Editable, tags: def.Tags.Enabled && def.Tags.Editable}
	var err error
	if p.titles, err = normalizeTitles(u.Titles); err != nil {
		return nil, err
	}
	editable := map[string]bool{}
	known := map[string]bool{}
	for _, fd := range def.Fields {
		known[fd.Key] = true
		if fd.Editable {
			editable[fd.Key] = true
			p.editable = append(p.editable, fd.Key)
		}
	}
	for k := range u.Fields {
		if !known[k] {
			return nil, invalid("unknown field %q for kind %q", k, def.Key)
		}
		if !editable[k] {
			return nil, invalid("field %q of kind %q is not editable", k, def.Key)
		}
	}
	if p.fields, err = normalizeFields(def, u.Fields); err != nil {
		return nil, err
	}
	all := append([]fieldRow(nil), p.fields...)
	for _, k := range stored {
		if known[k] && !editable[k] {
			all = append(all, fieldRow{key: k})
		}
	}
	if miss := requiredMissing(def, all); len(miss) > 0 {
		return nil, invalid("required field(s) missing: %s", strings.Join(miss, ", "))
	}
	if len(u.Genres) > 0 && !p.genres {
		return nil, invalid("the genres of kind %q are not editable", def.Key)
	}
	if len(u.Tags) > 0 && !p.tags {
		return nil, invalid("the tags of kind %q are not editable", def.Key)
	}
	if p.terms, err = normalizeTerms(def, u.Genres, u.Tags); err != nil {
		return nil, err
	}
	return p, nil
}

// ValidateDetails runs every check of UpdateDetails without writing, so a
// caller can validate before side effects (storing an uploaded cover file).
// ErrNoEntry for an unknown item, ErrInvalid for invalid data.
func (l *Library) ValidateDetails(ctx context.Context, itemID int64, u DetailsUpdate) error {
	pluginID, kind, stored, err := detailsMeta(ctx, l.db, itemID)
	if err != nil {
		return err
	}
	def, err := l.kindDef(pluginID, kind)
	if err != nil {
		return err
	}
	_, err = prepareDetails(def, u, stored)
	return err
}

// UpdateDetails applies a local edit of the item's own data (see
// DetailsUpdate) in ONE write-locking transaction and bumps the item's
// updated_at. Everything is validated before the first write; any failure
// rolls everything back. It returns ErrNoEntry for an unknown item and
// ErrInvalid for invalid data. The edit stays until the user edits again or
// an explicit match (ConvertToSource) replaces a manual item's data.
func (l *Library) UpdateDetails(ctx context.Context, itemID int64, u DetailsUpdate) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update details: %w", err)
	}
	defer tx.Rollback()
	pluginID, kind, stored, err := detailsMeta(ctx, tx, itemID)
	if err != nil {
		return err
	}
	def, err := l.kindDef(pluginID, kind)
	if err != nil {
		return err
	}
	p, err := prepareDetails(def, u, stored)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_titles WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("update details: %w", err)
	}
	if err := insertTitles(ctx, tx, itemID, p.titles); err != nil {
		return err
	}
	if len(p.editable) > 0 {
		args := []any{itemID}
		for _, k := range p.editable {
			args = append(args, k)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_fields WHERE item_id = ? AND key IN (`+placeholders(len(p.editable))+`)`, args...); err != nil {
			return fmt.Errorf("update details: %w", err)
		}
	}
	if err := insertFields(ctx, tx, itemID, p.fields); err != nil {
		return err
	}
	for kind, replace := range map[string]bool{"genre": p.genres, "tag": p.tags} {
		if !replace {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_terms WHERE item_id = ? AND kind = ?`, itemID, kind); err != nil {
			return fmt.Errorf("update details: %w", err)
		}
	}
	if err := insertTerms(ctx, tx, itemID, p.terms); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET cover_path = ?, updated_at = `+nowSQL+` WHERE id = ?`, u.CoverPath, itemID); err != nil {
		return fmt.Errorf("update details: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update details: %w", err)
	}
	return nil
}
