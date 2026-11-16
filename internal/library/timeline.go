package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// TimelineEntry is one change of a run as the timeline shows it. Type is
// "progress", "status" or "rating" (the event types of the change log). A
// progress entry names its track in Key and the value change in From and To;
// a rating entry carries From and To (nil is unrated); a status entry carries
// OldText and NewText (the status values).
type TimelineEntry struct {
	At               time.Time
	Type, Key        string
	From, To         *float64
	OldText, NewText string
	Origin           EventOrigin
}

// Timeline returns the recorded changes of run runID, oldest first. A run
// without events (or an unknown run) has an empty timeline.
func (l *Library) Timeline(ctx context.Context, runID int64) ([]TimelineEntry, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin
		FROM `+tableEntryEvents+` WHERE run_id = ? ORDER BY occurred_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("query timeline: %w", err)
	}
	var out []TimelineEntry
	for rows.Next() {
		var (
			e        TimelineEntry
			stamp    string
			from, to sql.NullFloat64
			origin   string
		)
		if err := rows.Scan(&stamp, &e.Type, &e.Key, &from, &to, &e.OldText, &e.NewText, &origin); err != nil {
			rows.Close()
			return nil, fmt.Errorf("query timeline: %w", err)
		}
		if e.At, err = time.Parse(time.RFC3339, stamp); err != nil {
			rows.Close()
			return nil, fmt.Errorf("query timeline: bad stamp %q: %w", stamp, err)
		}
		if from.Valid {
			e.From = &from.Float64
		}
		if to.Valid {
			e.To = &to.Float64
		}
		e.Origin = EventOrigin(origin)
		out = append(out, e)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("query timeline: %w", err)
	}
	return out, nil
}
