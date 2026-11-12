package library

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// event is one entry_events row as the tests read it.
type event struct {
	Type, Key        string
	OldNum, NewNum   *float64
	OldText, NewText string
	Origin           EventOrigin
}

// readEvents returns the item's events in insertion order.
func readEvents(t *testing.T, l *Library, itemID int64) []event {
	t.Helper()
	rows, err := l.db.QueryContext(bg, `SELECT event_type, progress_key, old_num, new_num, old_text, new_text, origin
		FROM entry_events WHERE item_id = ? ORDER BY id`, itemID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var out []event
	for rows.Next() {
		var ev event
		var oldNum, newNum sql.NullFloat64
		var origin string
		if err := rows.Scan(&ev.Type, &ev.Key, &oldNum, &newNum, &ev.OldText, &ev.NewText, &origin); err != nil {
			rows.Close()
			t.Fatalf("read events: %v", err)
		}
		if oldNum.Valid {
			ev.OldNum = fp(oldNum.Float64)
		}
		if newNum.Valid {
			ev.NewNum = fp(newNum.Float64)
		}
		ev.Origin = EventOrigin(origin)
		out = append(out, ev)
	}
	if err := closeRows(rows); err != nil {
		t.Fatalf("read events: %v", err)
	}
	return out
}

func numText(n *float64) string {
	if n == nil {
		return "NULL"
	}
	return fmt.Sprint(*n)
}

// showEvents formats events without pointer addresses for failure messages.
func showEvents(evs []event) string {
	if len(evs) == 0 {
		return "[]"
	}
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "\n  %s %q old=%s new=%s %q->%q %s", ev.Type, ev.Key,
			numText(ev.OldNum), numText(ev.NewNum), ev.OldText, ev.NewText, ev.Origin)
	}
	return b.String()
}

func sameNum(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameEvent(a, b event) bool {
	return a.Type == b.Type && a.Key == b.Key && a.OldText == b.OldText && a.NewText == b.NewText &&
		a.Origin == b.Origin && sameNum(a.OldNum, b.OldNum) && sameNum(a.NewNum, b.NewNum)
}

func wantEvents(t *testing.T, got, want []event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %s, want %s", showEvents(got), showEvents(want))
	}
	for i := range want {
		if !sameEvent(got[i], want[i]) {
			t.Fatalf("events = %s, want %s", showEvents(got), showEvents(want))
		}
	}
}

func TestUpsertEntryRecordsChanges(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-u1", fp(10), Entry{Status: Planned, Progress: map[string]float64{"pages": 2}})
	if n := len(readEvents(t, l, id)); n != 0 {
		t.Fatalf("creating the entry wrote %d events", n)
	}
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Rating: ip(6), Progress: map[string]float64{"pages": 5}}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id), []event{
		{Type: "progress", Key: "pages", OldNum: fp(2), NewNum: fp(5), Origin: OriginManual},
		{Type: "status", OldText: "planned", NewText: "active", Origin: OriginManual},
		{Type: "rating", NewNum: fp(6), Origin: OriginManual},
	})
	// saving the same values again writes nothing
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Rating: ip(6), Progress: map[string]float64{"pages": 5}}); err != nil {
		t.Fatal(err)
	}
	if n := len(readEvents(t, l, id)); n != 3 {
		t.Fatalf("an unchanged save wrote %d events", n-3)
	}
	// decreases are recorded; a rating of 0 is a value, not "unrated"
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Rating: ip(0), Progress: map[string]float64{"pages": 3}}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id)[3:], []event{
		{Type: "progress", Key: "pages", OldNum: fp(5), NewNum: fp(3), Origin: OriginManual},
		{Type: "rating", OldNum: fp(6), NewNum: fp(0), Origin: OriginManual},
	})
	// clearing the rating records NULL as the new value
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 3}}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id)[5:], []event{
		{Type: "rating", OldNum: fp(0), Origin: OriginManual},
	})
}

func TestUpsertEntryCreationWritesNoEvents(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	// an upsert that creates the entry is a baseline
	item := mustSave(t, l, "book", "book", bookWith("ev-c1", fp(10)))
	if err := l.UpsertEntry(bg, Entry{ItemID: item, Status: Active, Rating: ip(4), Progress: map[string]float64{"pages": 7}}); err != nil {
		t.Fatal(err)
	}
	if n := len(readEvents(t, l, item)); n != 0 {
		t.Fatalf("creating entry by upsert wrote %d events", n)
	}
	// CreateEntry is a baseline too
	create := mustSave(t, l, "book", "book", bookWith("ev-c2", nil))
	if err := l.CreateEntry(bg, Entry{ItemID: create, Status: Planned, Progress: map[string]float64{"pages": 2}}); err != nil {
		t.Fatal(err)
	}
	if n := len(readEvents(t, l, create)); n != 0 {
		t.Fatalf("CreateEntry wrote %d events", n)
	}
	// AddToList writes the item and its entry together, without events
	listed := addBook(t, l, "ev-c3", nil, Entry{Status: Active, Progress: map[string]float64{"pages": 5}})
	if n := len(readEvents(t, l, listed)); n != 0 {
		t.Fatalf("AddToList wrote %d events", n)
	}
}

func TestEntryEventConstraints(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "ev-k1", nil, Entry{Status: Planned})
	ins := `INSERT INTO entry_events(item_id, occurred_at, event_type, origin) VALUES (?, '2026-01-01T00:00:00Z', ?, ?)`
	if _, err := e.db.Exec(ins, id, "bogus", "manual"); err == nil {
		t.Fatal("event type check missing")
	}
	if _, err := e.db.Exec(ins, id, "progress", "bogus"); err == nil {
		t.Fatal("origin check missing")
	}
	if _, err := e.db.Exec(ins, id, "status", "manual"); err != nil {
		t.Fatal(err)
	}
	// events cascade with their item
	if err := e.lib.DeleteItem(bg, id); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events`); n != 0 {
		t.Fatalf("%d events survived the item", n)
	}
}
