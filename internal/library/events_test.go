package library

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// insertEventAt appends an event with an explicit occurred_at (UTC).
func insertEventAt(t *testing.T, l *Library, itemID int64, occurredAt time.Time, eventType, key string,
	oldNum, newNum *float64, oldText, newText string, origin EventOrigin) {
	t.Helper()
	var oldV, newV any
	if oldNum != nil {
		oldV = *oldNum
	}
	if newNum != nil {
		newV = *newNum
	}
	if _, err := l.db.ExecContext(bg, `INSERT INTO entry_events(item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		itemID, occurredAt.UTC().Format("2006-01-02T15:04:05Z"), eventType, key, oldV, newV, oldText, newText, string(origin)); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func at(y int, m time.Month, d, h, min, s int) time.Time {
	return time.Date(y, m, d, h, min, s, 0, time.UTC)
}

func wantDays(t *testing.T, got map[string]int, want map[string]int) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("days = %v, want %v", got, want)
	}
}

func TestActivityDaysCountsProgressAndCompletions(t *testing.T) {
	l := newLib(t)
	id := addBook(t, l, "act-1", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 0}})
	insertEventAt(t, l, id, at(2026, 3, 1, 10, 0, 0), eventProgress, "pages", fp(0), fp(5), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 1, 11, 0, 0), eventStatus, "", nil, nil, "active", "completed", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 1, 12, 0, 0), eventProgress, "pages", fp(5), fp(3), "", "", OriginManual)
	got, err := l.ActivityDays(bg, day(2026, 3, 1), day(2026, 3, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantDays(t, got, map[string]int{"2026-03-01": 2})
}

func TestActivityDaysExcludesImportsAndDecreases(t *testing.T) {
	l := newLib(t)
	id := addBook(t, l, "act-2", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 9}})
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 0, 0), eventProgress, "pages", fp(0), fp(5), "", "", OriginImport)
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 1, 0), eventStatus, "", nil, nil, "active", "completed", OriginRestore)
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 2, 0), eventProgress, "pages", fp(9), fp(4), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 3, 0), eventStatus, "", nil, nil, "completed", "completed", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 4, 0), eventStatus, "", nil, nil, "active", "planned", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 2, 9, 5, 0), eventRating, "", nil, fp(8), "", "", OriginManual)
	got, err := l.ActivityDays(bg, day(2026, 3, 2), day(2026, 3, 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantDays(t, got, map[string]int{})
}

func TestActivityDaysFiltersScope(t *testing.T) {
	l := newLib(t)
	a := addBook(t, l, "act-3", fp(100), Entry{Status: Active})
	b := mustSave(t, l, "book2", "book", bookWith("act-4", nil))
	insertEventAt(t, l, a, at(2026, 3, 3, 8, 0, 0), eventProgress, "pages", fp(0), fp(1), "", "", OriginManual)
	insertEventAt(t, l, b, at(2026, 3, 3, 8, 0, 0), eventProgress, "pages", fp(0), fp(1), "", "", OriginManual)
	from, to := day(2026, 3, 3), day(2026, 3, 4)
	for _, tc := range []struct {
		name  string
		scope []KindRef
		want  int
	}{
		{"every kind", nil, 2},
		{"first plugin", []KindRef{{PluginID: "book", Kind: "book"}}, 1},
		{"second plugin", []KindRef{{PluginID: "book2", Kind: "book"}}, 1},
		{"kind mismatch", []KindRef{{PluginID: "book", Kind: "tv"}}, 0},
		{"both", []KindRef{{PluginID: "book", Kind: "book"}, {PluginID: "book2", Kind: "book"}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := l.ActivityDays(bg, from, to, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			if got["2026-03-03"] != tc.want {
				t.Fatalf("count = %d, want %d (%v)", got["2026-03-03"], tc.want, got)
			}
		})
	}
}

func TestActivityDaysGroupsByUTCDayAndRange(t *testing.T) {
	l := newLib(t)
	id := addBook(t, l, "act-5", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 0}})
	insertEventAt(t, l, id, at(2026, 3, 3, 23, 59, 59), eventProgress, "pages", fp(0), fp(1), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 4, 23, 59, 59), eventProgress, "pages", fp(1), fp(2), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 5, 0, 0, 0), eventProgress, "pages", fp(2), fp(3), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 5, 12, 0, 0), eventProgress, "pages", fp(3), fp(4), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 3, 6, 0, 0, 0), eventProgress, "pages", fp(4), fp(5), "", "", OriginManual)
	got, err := l.ActivityDays(bg, day(2026, 3, 4), day(2026, 3, 6), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantDays(t, got, map[string]int{"2026-03-04": 1, "2026-03-05": 2})
}

func TestFirstActivityDay(t *testing.T) {
	l := newLib(t)
	if first, ok, err := l.FirstActivityDay(bg); err != nil || ok || first != "" {
		t.Fatalf("empty log: FirstActivityDay = %q, %v, %v", first, ok, err)
	}
	id := addBook(t, l, "act-6", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 0}})
	insertEventAt(t, l, id, at(2026, 1, 1, 10, 0, 0), eventProgress, "pages", fp(0), fp(1), "", "", OriginImport)
	if first, ok, err := l.FirstActivityDay(bg); err != nil || ok || first != "" {
		t.Fatalf("only imports: FirstActivityDay = %q, %v, %v", first, ok, err)
	}
	insertEventAt(t, l, id, at(2026, 3, 1, 10, 0, 0), eventProgress, "pages", fp(1), fp(2), "", "", OriginManual)
	insertEventAt(t, l, id, at(2026, 2, 10, 23, 0, 0), eventRating, "", nil, fp(7), "", "", OriginManual)
	if first, ok, err := l.FirstActivityDay(bg); err != nil || !ok || first != "2026-02-10" {
		t.Fatalf("FirstActivityDay = %q, %v, %v, want 2026-02-10", first, ok, err)
	}
}

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
