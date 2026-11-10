package library

import (
	"context"
	"fmt"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// scheduleLayout is how stored schedule timestamps are written and read
// (the same layout the job queue uses).
const scheduleLayout = "2006-01-02T15:04:05Z"

// Airing is one stored release air date of an item.
type Airing struct {
	Number int
	AirsAt time.Time
}

// ScheduledEntry is one stored airing of an active entry with what the
// calendar feed needs to label it.
type ScheduledEntry struct {
	ItemID           int64
	PluginID, Kind   string
	Source, SourceID string
	Titles           []plugin.Title
	Number           int
	AirsAt           time.Time
}

// StaleSchedule names an active entry whose schedule was never stored or is
// older than the caller's cutoff, with the plugin and source to ask.
type StaleSchedule struct {
	ItemID   int64
	PluginID string
	Source   string
}

// SaveSchedule replaces the stored airing schedule of itemID and records
// when it was fetched. An empty schedule is recorded too (the marker says the
// item was asked), so an item without upcoming releases is not fetched on
// every calendar poll.
func (l *Library) SaveSchedule(ctx context.Context, itemID int64, airings []Airing, syncedAt time.Time) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM airings WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	for _, a := range airings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO airings(item_id, number, airs_at) VALUES (?, ?, ?)`,
			itemID, a.Number, a.AirsAt.UTC().Format(scheduleLayout)); err != nil {
			return fmt.Errorf("save schedule: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_schedules(item_id, synced_at) VALUES (?, ?)
		ON CONFLICT(item_id) DO UPDATE SET synced_at = excluded.synced_at`,
		itemID, syncedAt.UTC().Format(scheduleLayout)); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	return nil
}

// ScheduleEntries returns the stored airings of every active entry, titles
// included (rowid order), ordered by air time. The caller filters by source
// capability: the library does not know the manifests.
func (l *Library) ScheduleEntries(ctx context.Context) ([]ScheduledEntry, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT a.item_id, i.plugin_id, i.kind, i.source, i.source_id, a.number, a.airs_at
		FROM airings a
		JOIN items i ON i.id = a.item_id
		JOIN entries e ON e.item_id = a.item_id
		WHERE e.status = ?
		ORDER BY a.airs_at, a.item_id, a.number`, string(Active))
	if err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	var out []ScheduledEntry
	ids := map[int64]bool{}
	for rows.Next() {
		var e ScheduledEntry
		var airs string
		if err := rows.Scan(&e.ItemID, &e.PluginID, &e.Kind, &e.Source, &e.SourceID, &e.Number, &airs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		if e.AirsAt, err = time.Parse(scheduleLayout, airs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		ids[e.ItemID] = true
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]any, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	trows, err := l.db.QueryContext(ctx, `SELECT item_id, lang, text FROM item_titles
		WHERE item_id IN (`+placeholders(len(list))+`) ORDER BY item_id, rowid`, list...)
	if err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	defer trows.Close()
	titles := map[int64][]plugin.Title{}
	for trows.Next() {
		var id int64
		var t plugin.Title
		if err := trows.Scan(&id, &t.Lang, &t.Text); err != nil {
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		titles[id] = append(titles[id], t)
	}
	if err := trows.Err(); err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	for i := range out {
		out[i].Titles = titles[out[i].ItemID]
	}
	return out, nil
}

// StaleSchedules returns the active entries whose schedule was never stored
// or is older than the cutoff, with the plugin and source to ask.
func (l *Library) StaleSchedules(ctx context.Context, olderThan time.Time) ([]StaleSchedule, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT e.item_id, i.plugin_id, i.source
		FROM entries e
		JOIN items i ON i.id = e.item_id
		LEFT JOIN item_schedules s ON s.item_id = e.item_id
		WHERE e.status = ? AND (s.synced_at IS NULL OR s.synced_at < ?)
		ORDER BY e.item_id`, string(Active), olderThan.UTC().Format(scheduleLayout))
	if err != nil {
		return nil, fmt.Errorf("stale schedules: %w", err)
	}
	defer rows.Close()
	var out []StaleSchedule
	for rows.Next() {
		var s StaleSchedule
		if err := rows.Scan(&s.ItemID, &s.PluginID, &s.Source); err != nil {
			return nil, fmt.Errorf("stale schedules: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
