package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// maxProgressValue keeps stored values exactly representable as whole numbers.
const maxProgressValue = 1e12

func decodeKeys(s string, out *[]string) error {
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("item progress keys: %w", err)
	}
	return nil
}

func progressDef(kind plugin.KindDef, key string) (plugin.ProgressDef, bool) {
	for _, d := range kind.Progress.Eligible {
		if d.Key == key {
			return d, true
		}
	}
	return plugin.ProgressDef{}, false
}

// progressBound returns the total of a bounded track. Percent is always
// bounded at 100. Count, pages and duration are bounded only when the track
// names a total field and the item has a total greater than 0 there; a missing,
// zero or negative total means unbounded (sources report 0 for "unknown").
func progressBound(def plugin.ProgressDef, item *Item) (total float64, bounded bool) {
	if def.Type == plugin.Percent {
		return 100, true
	}
	if def.TotalField == "" {
		return 0, false
	}
	if v, ok := item.Fields[def.TotalField]; ok && v.Num != nil && *v.Num > 0 {
		return *v.Num, true
	}
	return 0, false
}

func checkProgressValue(def plugin.ProgressDef, v, total float64, bounded bool) error {
	what := def.Key
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return invalid("progress %q is not a finite number", what)
	}
	if v < 0 {
		return invalid("progress %q must not be negative", what)
	}
	if v != math.Trunc(v) {
		return invalid("progress %q must be a whole number", what)
	}
	if v > maxProgressValue {
		return invalid("progress %q is too large", what)
	}
	if bounded && v > total {
		if def.Type == plugin.Percent {
			return invalid("progress %q (percent) must be at most 100", what)
		}
		return invalid("progress %q (%v) is above the total of %v", what, v, total)
	}
	return nil
}

// activeTrack is one active, eligible progress track of an item.
type activeTrack struct {
	key     string
	total   float64
	bounded bool
}

func activeTracks(kind plugin.KindDef, item *Item) []activeTrack {
	var out []activeTrack
	for _, k := range item.ProgressKeys {
		def, ok := progressDef(kind, k)
		if !ok {
			continue
		}
		total, bounded := progressBound(def, item)
		out = append(out, activeTrack{k, total, bounded})
	}
	return out
}

// Finished reports whether progress alone makes the item finished: every
// ACTIVE bounded track (percent, or a track with a numeric total) is at its
// total. Unbounded tracks do not count, and an item with no bounded active
// track is never finished by progress. Values of inactive keys are ignored.
func Finished(kind plugin.KindDef, item *Item, e Entry) bool {
	any := false
	for _, t := range activeTracks(kind, item) {
		if !t.bounded {
			continue
		}
		any = true
		if e.Progress[t.key] < t.total {
			return false
		}
	}
	return any
}

// NotStarted reports whether every ACTIVE track is zero or absent (also true
// without active tracks). Values of inactive keys are ignored.
func NotStarted(kind plugin.KindDef, item *Item, e Entry) bool {
	for _, t := range activeTracks(kind, item) {
		if e.Progress[t.key] != 0 {
			return false
		}
	}
	return true
}

// itemMeta reads what the progress operations need of an item inside tx.
func itemMeta(ctx context.Context, tx *sql.Tx, itemID int64) (pluginID, kind string, active []string, hasEntry bool, err error) {
	var pk string
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id, i.kind, i.progress_keys,
		EXISTS(SELECT 1 FROM `+tableEntries+` e WHERE e.item_id = i.id) FROM items i WHERE i.id = ?`, itemID).
		Scan(&pluginID, &kind, &pk, &hasEntry)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil, false, ErrNoEntry
	}
	if err != nil {
		return "", "", nil, false, fmt.Errorf("load item: %w", err)
	}
	if err := decodeKeys(pk, &active); err != nil {
		return "", "", nil, false, err
	}
	return pluginID, kind, active, hasEntry, nil
}

// Increment adds 1 to the item's primary progress (the kind's
// Progress.PrimaryKey, a count or pages track) with one atomic upsert: the
// row is created at 1 when absent, and the value never rises above the total
// when the item has one (a no-op at the total). It returns ErrNoEntry when
// the item or its entry does not exist. It is a documented no-op (nil) when the
// kind has no primary key or the primary key is not active for the item.
func (l *Library) Increment(ctx context.Context, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("increment progress: %w", err)
	}
	defer tx.Rollback()
	pluginID, kindKey, active, hasEntry, err := itemMeta(ctx, tx, itemID)
	if err != nil {
		return err
	}
	if !hasEntry {
		return ErrNoEntry
	}
	def, err := l.kindDef(pluginID, kindKey)
	if err != nil {
		return err
	}
	key := def.Progress.PrimaryKey
	pd, ok := progressDef(def, key)
	if key == "" || !ok || !slices.Contains(active, key) || (pd.Type != plugin.Count && pd.Type != plugin.Pages) {
		return nil
	}
	const total = `(SELECT f.num FROM item_fields f WHERE f.item_id = ?1 AND f.key = ?3 AND f.num > 0 ORDER BY f.seq LIMIT 1)`
	res, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntryProgress+`(item_id, key, value)
		SELECT ?1, ?2, 1 WHERE `+total+` IS NULL OR `+total+` >= 1
		ON CONFLICT(item_id, key) DO UPDATE SET value = value + 1
		WHERE `+total+` IS NULL OR value < `+total, itemID, key, pd.TotalField)
	if err != nil {
		return fmt.Errorf("increment progress: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET updated_at = `+nowSQL+` WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("increment progress: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("increment progress: %w", err)
	}
	return nil
}

// txItem builds the Item that entry validation needs (active keys and the
// numeric total fields) from reads inside tx, and returns its KindDef.
func (l *Library) txItem(ctx context.Context, tx *sql.Tx, itemID int64) (*Item, plugin.KindDef, error) {
	pluginID, kindKey, active, _, err := itemMeta(ctx, tx, itemID)
	if err != nil {
		return nil, plugin.KindDef{}, err
	}
	def, err := l.kindDef(pluginID, kindKey)
	if err != nil {
		return nil, def, err
	}
	it := &Item{ID: itemID, PluginID: pluginID, Kind: kindKey, ProgressKeys: active}
	for _, d := range def.Progress.Eligible {
		if d.TotalField == "" || !slices.Contains(active, d.Key) {
			continue
		}
		var n float64
		err := tx.QueryRowContext(ctx, `SELECT num FROM item_fields WHERE item_id = ? AND key = ? AND num IS NOT NULL ORDER BY seq LIMIT 1`,
			itemID, d.TotalField).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, def, fmt.Errorf("load total: %w", err)
		}
		if it.Fields == nil {
			it.Fields = map[string]plugin.FieldValue{}
		}
		it.Fields[d.TotalField] = plugin.FieldValue{Num: &n}
	}
	return it, def, nil
}

// SetActiveProgress sets the item's active progress keys. keys must equal
// (as a set) one of the kind's allowed combinations. Values of keys that stop
// being active are retained and reappear when the key is activated again.
// It returns ErrNoEntry for an unknown item. It bumps items.updated_at but
// not entries.updated_at: the entry's own data did not change, and the list
// order ("recently updated") should follow what the user did to the entry.
func (l *Library) SetActiveProgress(ctx context.Context, itemID int64, keys []string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set active progress: %w", err)
	}
	defer tx.Rollback()
	pluginID, kindKey, _, _, err := itemMeta(ctx, tx, itemID)
	if err != nil {
		return err
	}
	def, err := l.kindDef(pluginID, kindKey)
	if err != nil {
		return err
	}
	if keys == nil {
		keys = []string{}
	}
	norm, err := normalizeProgress(def, keys)
	if err != nil {
		return err
	}
	pk, err := json.Marshal(norm)
	if err != nil {
		return fmt.Errorf("set active progress: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET progress_keys = ?, updated_at = `+nowSQL+` WHERE id = ?`, string(pk), itemID); err != nil {
		return fmt.Errorf("set active progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set active progress: %w", err)
	}
	return nil
}

// ClearProgress deletes the stored values of the given keys (active or
// retained). Absent keys are fine. It returns ErrNoEntry when the item has no
// entry.
func (l *Library) ClearProgress(ctx context.Context, itemID int64, keys ...string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clear progress: %w", err)
	}
	defer tx.Rollback()
	if _, _, _, hasEntry, err := itemMeta(ctx, tx, itemID); err != nil {
		return err
	} else if !hasEntry {
		return ErrNoEntry
	}
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			return invalid("a progress key must not be empty")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+tableEntryProgress+` WHERE item_id = ? AND key = ?`, itemID, k); err != nil {
			return fmt.Errorf("clear progress: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET updated_at = `+nowSQL+` WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("clear progress: %w", err)
	}
	return nil
}

// UpdateEntry replaces the existing entry of e.ItemID (like UpsertEntry) and,
// when activeKeys is not nil, first makes activeKeys the item's active
// progress combination (like SetActiveProgress: it must equal, as a set, one
// of the kind's allowed combinations; values of keys that stop being active
// are retained). Everything happens in ONE write-locking transaction: e is
// validated against the item's NEW active keys and totals, and on any error
// neither the combination nor the entry changes. A nil activeKeys keeps the
// combination, and an unchanged combination (in any order) leaves the item
// row untouched. It returns ErrNoEntry when the item or its entry does not
// exist; it never creates an entry.
func (l *Library) UpdateEntry(ctx context.Context, e Entry, activeKeys []string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update entry: %w", err)
	}
	defer tx.Rollback()
	pluginID, kindKey, active, hasEntry, err := itemMeta(ctx, tx, e.ItemID)
	if err != nil {
		return err
	}
	if !hasEntry {
		return ErrNoEntry
	}
	if activeKeys != nil {
		def, err := l.kindDef(pluginID, kindKey)
		if err != nil {
			return err
		}
		norm, err := normalizeProgress(def, activeKeys)
		if err != nil {
			return err
		}
		if CombinationIndex(def, norm) != CombinationIndex(def, active) {
			pk, err := json.Marshal(norm)
			if err != nil {
				return fmt.Errorf("update entry: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE items SET progress_keys = ?, updated_at = `+nowSQL+` WHERE id = ?`, string(pk), e.ItemID); err != nil {
				return fmt.Errorf("update entry: %w", err)
			}
		}
	}
	it, def, err := l.txItem(ctx, tx, e.ItemID)
	if err != nil {
		return err
	}
	if err := l.ValidateEntry(def, it, e); err != nil {
		return err
	}
	if err := writeEntry(ctx, tx, e.ItemID, e); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update entry: %w", err)
	}
	return nil
}

// ItemPluginID returns the plugin id of item id (with or without an entry),
// or ErrNoEntry when there is no such item. The web layer uses it to guard
// the entry routes by the item's plugin before anything else happens.
func (l *Library) ItemPluginID(ctx context.Context, id int64) (string, error) {
	var p string
	err := l.db.QueryRowContext(ctx, `SELECT plugin_id FROM items WHERE id = ?`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoEntry
	}
	if err != nil {
		return "", fmt.Errorf("item plugin: %w", err)
	}
	return p, nil
}

// StoredProgress returns every stored progress value of the item: the
// active ones and those retained for keys that are not active (see
// SetActiveProgress). An unknown item or one without values yields an empty
// map. The edit dialog shows retained values next to their inactive tracks.
func (l *Library) StoredProgress(ctx context.Context, itemID int64) (map[string]float64, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT key, value FROM `+tableEntryProgress+` WHERE item_id = ?`, itemID)
	if err != nil {
		return nil, fmt.Errorf("stored progress: %w", err)
	}
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stored progress: %w", err)
		}
		out[k] = v
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("stored progress: %w", err)
	}
	return out, nil
}
