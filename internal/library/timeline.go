package library

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// TimelineEntry is one change of a run as the timeline shows it. Type is
// "progress", "status" or "rating" (the event types of the change log) or
// "note" (a unit note). A progress entry names its track in Key and the
// value change in From and To; a rating entry carries From and To (nil is
// unrated); a status entry carries OldText and NewText (the status values).
// A note entry names its track in Key and carries the note in Note.
type TimelineEntry struct {
	At               time.Time
	Type, Key        string
	From, To         *float64
	OldText, NewText string
	Origin           EventOrigin
	Note             *UnitNote
}

// Timeline returns the recorded changes of run runID with its unit notes,
// oldest first. A unit note appears right after the latest progress event
// of its track that reached its unit (from < unit <= to), at that event's
// time; a note without such an event appears at its creation time. A run
// without events or notes (or an unknown run) has an empty timeline.
func (l *Library) Timeline(ctx context.Context, runID int64) ([]TimelineEntry, error) {
	events, err := l.timelineEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	notes, err := l.UnitNotes(ctx, runID)
	if err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return events, nil
	}
	// every entry gets a time and a rank: events keep their order (rank 2i),
	// a matched note follows its event (2i+1), the others sort by time after
	// the events of the same second
	type ranked struct {
		e    TimelineEntry
		rank int
	}
	all := make([]ranked, 0, len(events)+len(notes))
	for i, e := range events {
		all = append(all, ranked{e, 2 * i})
	}
	for i := range notes {
		n := &notes[i]
		r := ranked{TimelineEntry{At: n.CreatedAt, Type: "note", Key: n.Key, Origin: OriginManual, Note: n}, 2*len(events) + 1}
		for j := len(events) - 1; j >= 0; j-- {
			if reaches(events[j], n) {
				r.e.At, r.rank = events[j].At, 2*j+1
				break
			}
		}
		all = append(all, r)
	}
	sort.SliceStable(all, func(a, b int) bool {
		if !all[a].e.At.Equal(all[b].e.At) {
			return all[a].e.At.Before(all[b].e.At)
		}
		return all[a].rank < all[b].rank
	})
	out := make([]TimelineEntry, len(all))
	for i := range all {
		out[i] = all[i].e
	}
	return out, nil
}

// reaches reports whether progress event e moved n's track onto n's unit.
func reaches(e TimelineEntry, n *UnitNote) bool {
	if e.Type != eventProgress || e.Key != n.Key || e.To == nil {
		return false
	}
	from := 0.0
	if e.From != nil {
		from = *e.From
	}
	u := float64(n.Unit)
	return from < u && u <= *e.To
}

// timelineEvents reads the change log of run runID, oldest first.
func (l *Library) timelineEvents(ctx context.Context, runID int64) ([]TimelineEntry, error) {
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
