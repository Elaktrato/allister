package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/allister/allister/internal/plugin"
)

// ApplyProgressUpgrades applies every progress upgrade (ProgressSpec.Upgrades)
// of the installed plugins (enabled or not) that has not run on this
// database yet: each item of the plugin's kind whose active progress keys
// equal From (as a set) gets To as its active keys. Stored values stay
// untouched, so a key the upgrade activates shows the value it held while
// inactive. No events are written (the owner's activity did not change) and
// neither items.updated_at nor the entry's updated_at moves. Each upgrade runs
// in its own transaction and is recorded in progress_upgrades, so it never
// runs twice and a later switch back to From sticks. A failing upgrade is
// left unrecorded (it is tried again at the next start) and the others still
// run: it returns the number of items switched and every failure joined.
func (l *Library) ApplyProgressUpgrades(ctx context.Context) (int, error) {
	total := 0
	var errs []error
	for _, m := range l.reg.Manifests() {
		for _, k := range m.Kinds {
			for _, u := range k.Progress.Upgrades {
				n, err := l.applyProgressUpgrade(ctx, m.ID, k.Key, u)
				if err != nil {
					errs = append(errs, fmt.Errorf("progress upgrade %s/%s/%s: %w", m.ID, k.Key, u.ID, err))
					continue
				}
				total += n
			}
		}
	}
	return total, errors.Join(errs...)
}

// BackupProgressUpgrade names one progress upgrade the database applied
// (backup member "progress_upgrades").
type BackupProgressUpgrade struct {
	PluginID string `json:"plugin_id"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
}

// BackupProgressUpgrades lists the applied progress upgrades in a stable
// order (an empty, non-nil list when none ran).
func (l *Library) BackupProgressUpgrades(ctx context.Context) ([]BackupProgressUpgrade, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT plugin_id, kind, upgrade_id FROM progress_upgrades ORDER BY plugin_id, kind, upgrade_id`)
	if err != nil {
		return nil, fmt.Errorf("backup progress upgrades: %w", err)
	}
	out := []BackupProgressUpgrade{}
	for rows.Next() {
		var u BackupProgressUpgrade
		if err := rows.Scan(&u.PluginID, &u.Kind, &u.ID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("backup progress upgrades: %w", err)
		}
		out = append(out, u)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("backup progress upgrades: %w", err)
	}
	return out, nil
}

// restoredKeys are the active keys a restored item gets: the backup's keys
// with every upgrade of the kind that the backup's database had not applied
// (not in applied) run over them in declaration order, as
// ApplyProgressUpgrades would have at its next start.
func restoredKeys(def plugin.KindDef, pluginID string, keys []string, applied []BackupProgressUpgrade) []string {
	for _, u := range def.Progress.Upgrades {
		if slices.Contains(applied, BackupProgressUpgrade{PluginID: pluginID, Kind: def.Key, ID: u.ID}) {
			continue
		}
		if sameKeys(keys, u.From) {
			keys = slices.Clone(u.To)
		}
	}
	return keys
}

// sameKeys reports whether a and b hold the same keys (order ignored).
func sameKeys(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

func (l *Library) applyProgressUpgrade(ctx context.Context, pluginID, kind string, u plugin.ProgressUpgrade) (int, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM progress_upgrades WHERE plugin_id = ? AND kind = ? AND upgrade_id = ?)`,
		pluginID, kind, u.ID).Scan(&done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	to, err := json.Marshal(u.To)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, progress_keys FROM items WHERE plugin_id = ? AND kind = ?`, pluginID, kind)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var pk string
		if err := rows.Scan(&id, &pk); err != nil {
			rows.Close()
			return 0, err
		}
		var keys []string
		if err := decodeKeys(pk, &keys); err != nil {
			rows.Close()
			return 0, err
		}
		if sameKeys(keys, u.From) {
			ids = append(ids, id)
		}
	}
	if err := closeRows(rows); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET progress_keys = ? WHERE id = ?`, string(to), id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO progress_upgrades(plugin_id, kind, upgrade_id, items, applied_at) VALUES (?, ?, ?, ?, `+nowSQL+`)`,
		pluginID, kind, u.ID, len(ids)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}
