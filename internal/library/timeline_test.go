package library

import (
	"testing"
	"time"
)

// insertRunEvent appends an event of run runID with an explicit stamp.
func insertRunEvent(t *testing.T, e *env, itemID, runID int64, occurredAt time.Time, eventType, key string,
	oldNum, newNum *float64, oldText, newText string, origin EventOrigin) {
	t.Helper()
	insertEventAt(t, e.lib, itemID, occurredAt, eventType, key, oldNum, newNum, oldText, newText, origin)
	if _, err := e.db.Exec(`UPDATE entry_events SET run_id = ? WHERE id = (SELECT MAX(id) FROM entry_events)`, runID); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineListsTheRunsEventsOldestFirst(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "tl-1", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 0}})
	run := displayRunOf(t, e, id)
	insertRunEvent(t, e, id, run, at(2026, 3, 2, 9, 0, 0), eventStatus, "", nil, nil, "active", "completed", OriginImport)
	insertRunEvent(t, e, id, run, at(2026, 3, 1, 9, 0, 0), eventProgress, "pages", fp(4), fp(7), "", "", OriginManual)
	insertRunEvent(t, e, id, run, at(2026, 3, 3, 9, 0, 0), eventRating, "", nil, fp(8), "", "", OriginRestore)
	// another run's event is not part of this run's timeline
	if _, err := e.db.Exec(`INSERT INTO entry_events(item_id, run_id, occurred_at, event_type, origin) VALUES (?, NULL, '2026-03-04T00:00:00Z', 'rating', 'manual')`, id); err != nil {
		t.Fatal(err)
	}

	got, err := e.lib.Timeline(bg, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("timeline = %+v", got)
	}
	p, s, r := got[0], got[1], got[2]
	if p.Type != "progress" || p.Key != "pages" || p.From == nil || *p.From != 4 || p.To == nil || *p.To != 7 ||
		!p.At.Equal(at(2026, 3, 1, 9, 0, 0)) || p.Origin != OriginManual {
		t.Fatalf("progress = %+v", p)
	}
	if s.Type != "status" || s.OldText != "active" || s.NewText != "completed" || s.Origin != OriginImport || s.From != nil || s.To != nil {
		t.Fatalf("status = %+v", s)
	}
	if r.Type != "rating" || r.From != nil || r.To == nil || *r.To != 8 || r.Origin != OriginRestore {
		t.Fatalf("rating = %+v", r)
	}
}

func TestTimelineOfARunWithoutEventsIsEmpty(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "tl-2", fp(100), Entry{Status: Active})
	got, err := e.lib.Timeline(bg, displayRunOf(t, e, id))
	if err != nil || len(got) != 0 {
		t.Fatalf("timeline = %+v, %v", got, err)
	}
}
