package library

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func unitNotesOf(t *testing.T, e *env, runID int64) []UnitNote {
	t.Helper()
	got, err := e.lib.UnitNotes(bg, runID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSetUnitNoteStoresUpdatesAndDeletes(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "un-1", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 5}})
	run := displayRunOf(t, e, id)
	if err := e.lib.SetUnitNote(bg, run, "pages", 5, "a **bold** page"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(bg, run, "pages", 2, "earlier"); err != nil {
		t.Fatal(err)
	}
	got := unitNotesOf(t, e, run)
	if len(got) != 2 || got[0].Unit != 2 || got[1].Unit != 5 || got[1].Body != "a **bold** page" || got[1].Key != "pages" ||
		got[1].RunID != run || got[1].CreatedAt.IsZero() || got[1].UpdatedAt.IsZero() {
		t.Fatalf("notes = %+v", got)
	}
	created := got[1].CreatedAt
	if err := e.lib.SetUnitNote(bg, run, "pages", 5, "changed"); err != nil {
		t.Fatal(err)
	}
	got = unitNotesOf(t, e, run)
	if len(got) != 2 || got[1].Body != "changed" || !got[1].CreatedAt.Equal(created) {
		t.Fatalf("after update = %+v", got)
	}
	// an empty (or blank) body deletes the note; deleting a missing one is fine
	if err := e.lib.SetUnitNote(bg, run, "pages", 5, " \n "); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(bg, run, "pages", 9, ""); err != nil {
		t.Fatal(err)
	}
	if got = unitNotesOf(t, e, run); len(got) != 1 || got[0].Unit != 2 {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestSetUnitNoteRefusals(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "un-2", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 5}})
	run := displayRunOf(t, e, id)
	unbounded := addBook(t, e.lib, "un-3", nil, Entry{Status: Active})
	urun := displayRunOf(t, e, unbounded)
	long := strings.Repeat("あ", MaxUnitNoteRunes)
	cases := []struct {
		name string
		run  int64
		key  string
		unit int
		body string
		want string // "" accepts
	}{
		{"2000 runes", run, "pages", 1, long, ""},
		{"2001 runes", run, "pages", 1, long + "あ", "2000"},
		{"control character", run, "pages", 1, "bad\x07", "control character"},
		{"newline and tab are fine", run, "pages", 1, "a\n\tb", ""},
		{"percent track", run, "percent", 1, "x", "count or pages"},
		{"unknown track", run, "nope", 1, "x", "nope"},
		{"unit 0", run, "pages", 0, "x", "at least 1"},
		{"unit at the total", run, "pages", 100, "x", ""},
		{"unit above the total", run, "pages", 101, "x", "above the total"},
		{"unbounded track", urun, "pages", 5000, "x", ""},
	}
	for _, c := range cases {
		err := e.lib.SetUnitNote(bg, c.run, c.key, c.unit, c.body)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if err := e.lib.SetUnitNote(bg, run+1000, "pages", 1, "x"); !errors.Is(err, ErrNoRun) {
		t.Fatalf("unknown run: %v", err)
	}
	// deleting is refused on a track that takes no notes as well
	if err := e.lib.SetUnitNote(bg, run, "percent", 1, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("percent delete: %v", err)
	}
}

func TestEntryNotesAndReviewLimits(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "un-4", fp(100), Entry{Status: Active})
	long := strings.Repeat("😀", MaxNotesRunes)
	if err := e.lib.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Notes: long, Review: long}, nil); err != nil {
		t.Fatal(err)
	}
	for name, en := range map[string]Entry{
		"notes 10001":    {ItemID: id, Status: Active, Notes: long + "x"},
		"review 10001":   {ItemID: id, Status: Active, Review: long + "x"},
		"notes control":  {ItemID: id, Status: Active, Notes: "a\x1bb"},
		"review control": {ItemID: id, Status: Active, Review: "a\u0085b"},
	} {
		if err := e.lib.UpdateEntry(bg, en, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDeletingARunDeletesItsUnitNotes(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "un-5", fp(100), Entry{Status: Completed, Progress: map[string]float64{"pages": 100}})
	first := displayRunOf(t, e, id)
	if err := e.lib.SetUnitNote(bg, first, "pages", 3, "kept?"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.StartRun(bg, id, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.DeleteRun(bg, first); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM unit_notes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("unit notes left: %d, %v", n, err)
	}
}

func TestTimelineMergesUnitNotes(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "un-6", fp(100), Entry{Status: Active, Progress: map[string]float64{"pages": 0}})
	run := displayRunOf(t, e, id)
	insertRunEvent(t, e, id, run, at(2026, 3, 1, 9, 0, 0), eventProgress, "pages", fp(4), fp(7), "", "", OriginManual)
	insertRunEvent(t, e, id, run, at(2026, 3, 2, 9, 0, 0), eventStatus, "", nil, nil, "active", "paused", OriginManual)
	if err := e.lib.SetUnitNote(bg, run, "pages", 6, "on six"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(bg, run, "pages", 50, "no event"); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.Timeline(bg, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("timeline = %+v", got)
	}
	// the note on page 6 follows the event that reached it, at its time
	if got[0].Type != "progress" || got[1].Type != "note" || got[1].Note == nil || got[1].Note.Unit != 6 ||
		got[1].Note.Body != "on six" || got[1].Key != "pages" || !got[1].At.Equal(at(2026, 3, 1, 9, 0, 0)) {
		t.Fatalf("matched note = %+v / %+v", got[0], got[1])
	}
	// a note without an event sits at its creation time (now: after the seeded events)
	if got[2].Type != "status" || got[3].Type != "note" || got[3].Note.Unit != 50 || !got[3].At.Equal(got[3].Note.CreatedAt) {
		t.Fatalf("unmatched note = %+v", got[3])
	}
}
