package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// ExportEntries returns every list entry of the plugin's items (only those of
// kind when kind is not ""), ordered by item id, in the exporter's shape:
// status, rating, ALL stored progress values (also those of keys that are not
// active any more), calendar dates, comment, titles in their stored order,
// external ids and the kind. Items without an entry are left out. An unknown
// plugin or a kind the plugin does not declare is ErrInvalid; the plugin need
// not be enabled.
//
// The whole result is held in memory (four queries, no per-item query): a
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
	from := ` FROM items i JOIN ` + tableEntries + ` e ON e.item_id = i.id WHERE ` + where

	var out []plugin.ExportEntry
	byID := map[int64]int{}
	rows, err := l.db.QueryContext(ctx, `SELECT i.id, i.kind, e.status, e.rating, e.comment, e.started_on, e.finished_on`+from+` ORDER BY i.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("export entries: %w", err)
	}
	for rows.Next() {
		var id int64
		var x plugin.ExportEntry
		var status string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := rows.Scan(&id, &x.Kind, &status, &rating, &x.Comment, &started, &finished); err != nil {
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
	prows, err := l.db.QueryContext(ctx, `SELECT p.item_id, p.key, p.value FROM `+tableEntryProgress+` p WHERE p.item_id IN (SELECT i.id`+from+`)`, args...)
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
