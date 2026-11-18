package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// ExportEntries returns every list entry of the plugin's items (only those of
// kind when kind is not ""), ordered by item id, in the exporter's shape:
// the display run's status, rating, ALL stored progress values (also those of
// keys that are not active any more) and calendar dates, the entry's notes,
// every run (oldest first, each with all its stored progress values), the
// own tags and custom lists (names ordered by key), titles in their stored
// order, external ids and the kind. Items without an entry are left out. An unknown
// plugin or a kind the plugin does not declare is ErrInvalid; the plugin need
// not be enabled.
//
// The whole result is held in memory (a fixed number of queries, no per-item query): a
// list of a few thousand entries is a few MB at most. That is fine for a
// single-user list; a much larger library would need a streaming exporter
// contract.
func (l *Library) ExportEntries(ctx context.Context, pluginID, kind string) ([]plugin.ExportEntry, error) {
	if kind != "" {
		if _, err := l.kindDef(pluginID, kind); err != nil {
			return nil, err
		}
	} else if _, ok := l.reg.Manifest(pluginID); !ok {
		return nil, invalid("unknown plugin %q", pluginID)
	}
	where := `i.plugin_id = ?`
	args := []any{pluginID}
	if kind != "" {
		where += ` AND i.kind = ?`
		args = append(args, kind)
	}
	from := ` FROM items i JOIN ` + viewEntryState + ` e ON e.item_id = i.id WHERE ` + where

	var out []plugin.ExportEntry
	byID := map[int64]int{}
	rows, err := l.db.QueryContext(ctx, `SELECT i.id, i.kind, e.status, e.rating, e.notes, e.started_on, e.finished_on`+from+` ORDER BY i.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("export entries: %w", err)
	}
	for rows.Next() {
		var id int64
		var x plugin.ExportEntry
		var status string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := rows.Scan(&id, &x.Kind, &status, &rating, &x.Notes, &started, &finished); err != nil {
			rows.Close()
			return nil, fmt.Errorf("export entries: %w", err)
		}
		x.Status = plugin.StatusClass(status)
		if rating.Valid {
			r := int(rating.Int64)
			x.Rating = &r
		}
		if x.StartedOn, err = exportDay(started); err != nil {
			rows.Close()
			return nil, err
		}
		if x.FinishedOn, err = exportDay(finished); err != nil {
			rows.Close()
			return nil, err
		}
		byID[id] = len(out)
		out = append(out, x)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("export entries: %w", err)
	}
	if len(out) == 0 {
		return out, nil
	}

	if err := l.exportParts(ctx, `SELECT t.item_id, t.lang, t.text FROM item_titles t WHERE t.item_id IN (SELECT i.id`+from+`) ORDER BY t.item_id, t.rowid`,
		args, func(id int64, a, b string) {
			if i, ok := byID[id]; ok {
				out[i].Titles = append(out[i].Titles, plugin.Title{Lang: a, Text: b})
			}
		}); err != nil {
		return nil, err
	}
	if err := l.exportParts(ctx, `SELECT x.item_id, x.type, x.value FROM external_ids x WHERE x.item_id IN (SELECT i.id`+from+`)`,
		args, func(id int64, a, b string) {
			if i, ok := byID[id]; ok {
				if out[i].ExternalIDs == nil {
					out[i].ExternalIDs = map[string]string{}
				}
				out[i].ExternalIDs[a] = b
			}
		}); err != nil {
		return nil, err
	}
	prows, err := l.db.QueryContext(ctx, `SELECT p.item_id, p.key, p.value FROM `+viewDisplayProgress+` p WHERE p.item_id IN (SELECT i.id`+from+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("export entries: %w", err)
	}
	for prows.Next() {
		var id int64
		var k string
		var v float64
		if err := prows.Scan(&id, &k, &v); err != nil {
			prows.Close()
			return nil, fmt.Errorf("export entries: %w", err)
		}
		if i, ok := byID[id]; ok {
			if out[i].Progress == nil {
				out[i].Progress = map[string]float64{}
			}
			out[i].Progress[k] = v
		}
	}
	if err := closeRows(prows); err != nil {
		return nil, fmt.Errorf("export entries: %w", err)
	}
	if err := l.exportRuns(ctx, from, args, out, byID); err != nil {
		return nil, err
	}
	// own tags and custom lists, each ordered by name key
	for _, q := range []struct {
		sql string
		add func(x *plugin.ExportEntry, name string)
	}{
		{`SELECT x.item_id, u.name, u.name_key FROM entry_user_tags x JOIN user_tags u ON u.id = x.tag_id
			WHERE x.item_id IN (SELECT i.id` + from + `) ORDER BY x.item_id, u.name_key`,
			func(x *plugin.ExportEntry, name string) { x.UserTags = append(x.UserTags, name) }},
		{`SELECT li.item_id, c.name, c.name_key FROM list_items li JOIN lists c ON c.id = li.list_id
			WHERE li.item_id IN (SELECT i.id` + from + `) ORDER BY li.item_id, c.name_key, c.id`,
			func(x *plugin.ExportEntry, name string) { x.Lists = append(x.Lists, name) }},
	} {
		if err := l.exportParts(ctx, q.sql, args, func(id int64, name, _ string) {
			if i, ok := byID[id]; ok {
				q.add(&out[i], name)
			}
		}); err != nil {
			return nil, err
		}
	}
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	for chunk := range slices.Chunk(ids, 500) { // stay below SQLite's variable limit
		memberships, err := l.SeriesOfItems(ctx, chunk)
		if err != nil {
			return nil, err
		}
		for id, m := range memberships {
			out[byID[id]].Series, out[byID[id]].SeriesPosition = m.Title, m.Position
		}
	}
	return out, nil
}

// exportParts runs a query of (item_id, text, text) rows and hands each to add.
func (l *Library) exportParts(ctx context.Context, query string, args []any, add func(id int64, a, b string)) error {
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("export entries: %w", err)
	}
	for rows.Next() {
		var id int64
		var a, b string
		if err := rows.Scan(&id, &a, &b); err != nil {
			rows.Close()
			return fmt.Errorf("export entries: %w", err)
		}
		add(id, a, b)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("export entries: %w", err)
	}
	return nil
}

// exportDay reads a stored calendar day (YYYY-MM-DD) as UTC midnight.
func exportDay(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, ns.String)
	if err != nil {
		return nil, fmt.Errorf("export entries: stored date %q: %w", ns.String, err)
	}
	return &t, nil
}

// exportRuns fills the runs of the exported entries, oldest first, with every
// stored progress value of each run.
func (l *Library) exportRuns(ctx context.Context, from string, args []any, out []plugin.ExportEntry, byID map[int64]int) error {
	//nolint:gosec // G202: from is constant SQL of this package; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT r.item_id, r.id, r.number, r.status, r.rating, r.review, r.started_on, r.finished_on
		FROM `+tableRuns+` r WHERE r.item_id IN (SELECT i.id`+from+`) ORDER BY r.item_id, r.number`, args...)
	if err != nil {
		return fmt.Errorf("export runs: %w", err)
	}
	type place struct{ entry, run int }
	runs := map[int64]place{}
	for rows.Next() {
		var itemID, runID int64
		var r plugin.ExportRun
		var status string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := rows.Scan(&itemID, &runID, &r.Number, &status, &rating, &r.Review, &started, &finished); err != nil {
			rows.Close()
			return fmt.Errorf("export runs: %w", err)
		}
		r.Status = plugin.StatusClass(status)
		if rating.Valid {
			v := int(rating.Int64)
			r.Rating = &v
		}
		if r.StartedOn, err = exportDay(started); err != nil {
			rows.Close()
			return err
		}
		if r.FinishedOn, err = exportDay(finished); err != nil {
			rows.Close()
			return err
		}
		if i, ok := byID[itemID]; ok {
			runs[runID] = place{i, len(out[i].Runs)}
			out[i].Runs = append(out[i].Runs, r)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("export runs: %w", err)
	}
	//nolint:gosec // G202: from is constant SQL of this package; values are bound
	prows, err := l.db.QueryContext(ctx, `SELECT p.run_id, p.key, p.value FROM `+tableRunProgress+` p JOIN `+tableRuns+` r ON r.id = p.run_id
		WHERE r.item_id IN (SELECT i.id`+from+`)`, args...)
	if err != nil {
		return fmt.Errorf("export runs: %w", err)
	}
	for prows.Next() {
		var runID int64
		var k string
		var v float64
		if err := prows.Scan(&runID, &k, &v); err != nil {
			prows.Close()
			return fmt.Errorf("export runs: %w", err)
		}
		if p, ok := runs[runID]; ok {
			r := &out[p.entry].Runs[p.run]
			if r.Progress == nil {
				r.Progress = map[string]float64{}
			}
			r.Progress[k] = v
		}
	}
	if err := closeRows(prows); err != nil {
		return fmt.Errorf("export runs: %w", err)
	}
	return nil
}
