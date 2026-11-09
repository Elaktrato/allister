package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/allister/allister/internal/plugin"
)

// manualSource is the source of items the user created by hand.
const manualSource = "manual"

// ConvertToSource turns the manual item itemID into the source item d, in
// place and in one transaction. The item keeps its id, so its entry, progress
// and credits stay attached. Source, source id, cover and external URL are
// replaced; titles, fields, genres, tags and relations are replaced with the
// same validation and insert code as SaveItem; the external ids of the types
// present in d.ExternalIDs are replaced while ids of other types are kept;
// sync_state becomes "pending" so the sync job fetches the rest.
//
// The entry is left untouched, except that a progress value above a total of
// the new data (for example 30 episodes watched of a new total of 12) is
// clamped to that total; only then does the entry's updated_at change.
//
// Errors: ErrNoEntry (no such item), ErrInvalid (the item is not a manual
// item, d's source is manual, or d fails validation), *AlreadySavedError (the
// identity of d belongs to another item). Any failure rolls everything back.
func (l *Library) ConvertToSource(ctx context.Context, itemID int64, d plugin.ItemData) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("convert item: %w", err)
	}
	defer tx.Rollback()

	var pluginID, kindKey, source, pk string
	switch err := tx.QueryRowContext(ctx, `SELECT plugin_id, kind, source, progress_keys FROM items WHERE id = ?`, itemID).
		Scan(&pluginID, &kindKey, &source, &pk); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoEntry
	case err != nil:
		return fmt.Errorf("convert item: %w", err)
	}
	if source != manualSource {
		return invalid("only manual items can be matched to a source")
	}
	var active []string
	if err := decodeKeys(pk, &active); err != nil {
		return err
	}
	p, err := l.prepare(pluginID, kindKey, d, active)
	if err != nil {
		return err
	}
	if p.source == manualSource {
		return invalid("the match target needs a source other than manual")
	}
	var existing int64
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?`,
		pluginID, kindKey, p.source, p.sourceID).Scan(&existing); {
	case err == nil:
		return &AlreadySavedError{ExistingID: existing}
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("convert item: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE items SET source = ?, source_id = ?, cover_path = ?, external_url = ?,
		sync_state = 'pending', updated_at = `+nowSQL+` WHERE id = ?`,
		p.source, p.sourceID, d.CoverURL, d.ExternalURL, itemID); err != nil {
		return fmt.Errorf("convert item: %w", err)
	}
	for _, q := range []string{
		`DELETE FROM item_titles WHERE item_id = ?`,
		`DELETE FROM item_fields WHERE item_id = ?`,
		`DELETE FROM ` + tableRelations + ` WHERE item_id = ?`,
		`DELETE FROM item_terms WHERE item_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, itemID); err != nil {
			return fmt.Errorf("convert item: %w", err)
		}
	}
	for _, e := range p.extIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM external_ids WHERE item_id = ? AND type = ?`, itemID, e[0]); err != nil {
			return fmt.Errorf("convert item: %w", err)
		}
	}
	if err := insertParts(ctx, tx, itemID, p); err != nil {
		return err
	}
	if err := clampProgress(ctx, tx, itemID, p.def, &Item{Fields: d.Fields}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("convert item: %w", err)
	}
	return nil
}

// clampProgress lowers stored progress values (active or retained) that are
// above the total of their track in item, and bumps the entry's updated_at
// only when a value changed.
func clampProgress(ctx context.Context, tx *sql.Tx, itemID int64, kind plugin.KindDef, item *Item) error {
	var changed int64
	for _, def := range kind.Progress.Eligible {
		total, bounded := progressBound(def, item)
		if !bounded {
			continue
		}
		res, err := tx.ExecContext(ctx, `UPDATE `+tableEntryProgress+` SET value = ? WHERE item_id = ? AND key = ? AND value > ?`,
			total, itemID, def.Key, total)
		if err != nil {
			return fmt.Errorf("clamp progress: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("clamp progress: %w", err)
		}
		changed += n
	}
	if changed == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET updated_at = `+nowSQL+` WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clamp progress: %w", err)
	}
	return nil
}
