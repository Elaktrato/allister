package importer

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata" // the zone test must not depend on the host's zone database

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// idleEnv is the generic env whose book importer is an account importer with
// IdlePause. Its clock stands at 2026-01-01 00:00 UTC.
func idleEnv(t *testing.T) *genEnv {
	t.Helper()
	return newGenEnvWith(t, false, false, func(bk *plugintest.Plugin) {
		bk.M.Importers[0].AccountLabel = "User"
		bk.M.Importers[0].IdlePause = true
	})
}

// daysAgo is noon UTC n days before the env's clock day.
func daysAgo(n int) *time.Time {
	t := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
	return &t
}

// activeRow is an Active book row last active (and modified) at last.
func activeRow(isbn string, pages float64, last *time.Time) plugin.ParsedEntry {
	row := bookEntry(isbn, "Book "+isbn, plugin.Active, pages)
	row.LastActive, row.UpdatedAt = last, last
	return row
}

// importRows runs one account import of rows in mode.
func importRows(t *testing.T, e *genEnv, mode string, rows ...plugin.ParsedEntry) {
	t.Helper()
	e.bookImp.AccountRows = rows
	id := e.submitAccount("books-csv", "someone", mode)
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
}

// setStatus gives the entry of isbn a manual status, made long ago (so a
// row modified later is "newer").
func setStatus(t *testing.T, e *genEnv, isbn string, st library.Status) int64 {
	t.Helper()
	it, en := e.item("book", "book", "isbn", isbn)
	en.Status = st
	if err := e.lib.UpsertEntry(context.Background(), *en); err != nil {
		t.Fatal(err)
	}
	backdate(t, e, it.ID)
	return it.ID
}

func backdate(t *testing.T, e *genEnv, itemID int64) {
	t.Helper()
	backdateTo(t, e, itemID, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
}

// backdateTo makes the entry's (and its run's) last change at.
func backdateTo(t *testing.T, e *genEnv, itemID int64, at time.Time) {
	t.Helper()
	for _, q := range []string{`UPDATE entries SET updated_at = ? WHERE item_id = ?`, `UPDATE entry_runs SET updated_at = ? WHERE item_id = ?`} {
		if _, err := e.dbh.Exec(q, at.UTC().Format("2006-01-02T15:04:05Z"), itemID); err != nil {
			t.Fatal(err)
		}
	}
}

func statusOf(t *testing.T, e *genEnv, isbn string) library.Status {
	t.Helper()
	_, en := e.item("book", "book", "isbn", isbn)
	return en.Status
}

// lastStatusEvent is the origin and new value of the item's latest status event.
func lastStatusEvent(t *testing.T, e *genEnv, itemID int64) (origin, status string) {
	t.Helper()
	if err := e.dbh.QueryRow(`SELECT origin, new_text FROM entry_events WHERE item_id = ? AND event_type = 'status' ORDER BY id DESC LIMIT 1`, itemID).Scan(&origin, &status); err != nil {
		t.Fatal(err)
	}
	return origin, status
}

// TestIdleNewEntries: a new row last active longer ago than the default
// cutoff of 30 days is saved as Paused, one active within it as Active; a row
// without a last activity, or one that is not Active, is left as it is.
func TestIdleNewEntries(t *testing.T) {
	e := idleEnv(t)
	planned := bookEntry("4", "Four", plugin.Planned, 0)
	planned.LastActive = daysAgo(400)
	importRows(t, e, ModeSkip,
		activeRow("1", 10, daysAgo(31)), // idle
		activeRow("2", 10, daysAgo(30)), // exactly the cutoff: still active
		activeRow("3", 10, nil),         // unknown
		planned,
	)
	for isbn, want := range map[string]library.Status{"1": library.Paused, "2": library.Active, "3": library.Active, "4": library.Planned} {
		if got := statusOf(t, e, isbn); got != want {
			t.Errorf("isbn %s: %q, want %q", isbn, got, want)
		}
	}
}

// TestIdleDaysSetting: the cutoff is the plugin's idle_days setting; 0
// turns the rule off.
func TestIdleDaysSetting(t *testing.T) {
	e := idleEnv(t)
	if err := e.settings.SetIdleDays(context.Background(), "book", 5); err != nil {
		t.Fatal(err)
	}
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(6)), activeRow("2", 10, daysAgo(5)))
	if a, b := statusOf(t, e, "1"), statusOf(t, e, "2"); a != library.Paused || b != library.Active {
		t.Fatalf("5 days: %q %q", a, b)
	}

	off := idleEnv(t)
	if err := off.settings.SetIdleDays(context.Background(), "book", 0); err != nil {
		t.Fatal(err)
	}
	importRows(t, off, ModeSkip, activeRow("1", 10, daysAgo(4000)))
	if got := statusOf(t, off, "1"); got != library.Active {
		t.Fatalf("off: %q", got)
	}
}

// TestIdleOnlyForIdlePauseImporters: an importer that does not opt in keeps
// today's behaviour even when its rows carry a last activity.
func TestIdleOnlyForIdlePauseImporters(t *testing.T) {
	e := newGenEnv(t)
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(400)))
	if got := statusOf(t, e, "1"); got != library.Active {
		t.Fatalf("%q", got)
	}
}

// TestIdleCalendarDayInConfiguredZone: "today" is the configured zone's
// calendar day. At 2026-01-01 00:00 UTC it is still 31 December in Los
// Angeles, so a last activity on 1 December (local) is 30 days ago there
// (active) but 31 days ago in UTC.
func TestIdleCalendarDayInConfiguredZone(t *testing.T) {
	last := time.Date(2025, 12, 1, 20, 0, 0, 0, time.UTC) // 1 December in both zones
	utc := idleEnv(t)
	importRows(t, utc, ModeSkip, activeRow("1", 10, &last))
	if got := statusOf(t, utc, "1"); got != library.Paused {
		t.Fatalf("UTC: %q", got)
	}
	la := idleEnv(t)
	if err := la.settings.SetTimeZone(context.Background(), "America/Los_Angeles"); err != nil {
		t.Fatal(err)
	}
	importRows(t, la, ModeSkip, activeRow("1", 10, &last))
	if got := statusOf(t, la, "1"); got != library.Active {
		t.Fatalf("Los Angeles: %q", got)
	}
}

// TestIdlePausesExistingActiveEntryWithoutNewerRow: under "newer" an Active
// entry whose row is idle is paused even though the row is not newer than
// the entry (the change comes from the cutoff, not the source); only the
// status changes, and the event has the import origin.
func TestIdlePausesExistingActiveEntryWithoutNewerRow(t *testing.T) {
	e := idleEnv(t)
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(2)))
	it, en := e.item("book", "book", "isbn", "1")
	en.Rating = ip(7)
	if err := e.lib.UpsertEntry(context.Background(), *en); err != nil { // a manual change after the last activity
		t.Fatal(err)
	}
	// ... but itself longer ago than the cutoff, like the last activity
	backdateTo(t, e, it.ID, *daysAgo(45))
	row := activeRow("1", 99, daysAgo(60))
	importRows(t, e, ModeNewer, row)
	_, got := e.item("book", "book", "isbn", "1")
	if got.Status != library.Paused {
		t.Fatalf("status %q, want paused", got.Status)
	}
	if got.Progress["pages"] != 10 || got.Rating == nil || *got.Rating != 7 {
		t.Fatalf("only the status may change: %+v", got)
	}
	if origin, st := lastStatusEvent(t, e, it.ID); origin != string(library.OriginImport) || st != string(library.Paused) {
		t.Fatalf("event %q %q", origin, st)
	}
	if r := e.report(); r.Updated != 1 {
		t.Fatalf("report %+v", r)
	}

	// a second run has nothing to change
	importRows(t, e, ModeNewer, row)
	if r := e.report(); r.Updated != 0 || r.Skipped != 1 {
		t.Fatalf("second run %+v", r)
	}
}

// TestIdleNewerRowStillApplies: a newer idle row updates the entry as usual,
// with the status Paused; a newer active row brings a Paused or Planned entry
// back to Active.
func TestIdleNewerRowStillApplies(t *testing.T) {
	e := idleEnv(t)
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(2)), activeRow("2", 10, daysAgo(2)), activeRow("3", 10, daysAgo(2)))
	it1, _ := e.item("book", "book", "isbn", "1")
	backdate(t, e, it1.ID)
	setStatus(t, e, "2", library.Paused)
	setStatus(t, e, "3", library.Planned)
	importRows(t, e, ModeNewer, activeRow("1", 50, daysAgo(60)), activeRow("2", 60, daysAgo(1)), activeRow("3", 70, daysAgo(1)))
	if _, en := e.item("book", "book", "isbn", "1"); en.Status != library.Paused || en.Progress["pages"] != 50 {
		t.Errorf("idle newer row: %+v", en)
	}
	if _, en := e.item("book", "book", "isbn", "2"); en.Status != library.Active || en.Progress["pages"] != 60 {
		t.Errorf("paused, played again: %+v", en)
	}
	if _, en := e.item("book", "book", "isbn", "3"); en.Status != library.Active || en.Progress["pages"] != 70 {
		t.Errorf("planned, played again: %+v", en)
	}
}

// TestIdleKeepsCompletedAndDropped: an import never changes the status of an
// entry shown as Completed or Dropped (progress still updates), whatever the
// row's activity.
func TestIdleKeepsCompletedAndDropped(t *testing.T) {
	for _, st := range []library.Status{library.Completed, library.Dropped} {
		for _, last := range []*time.Time{daysAgo(1), daysAgo(90)} {
			e := idleEnv(t)
			importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(2)))
			setStatus(t, e, "1", st)
			importRows(t, e, ModeAlways, activeRow("1", 80, last))
			if _, en := e.item("book", "book", "isbn", "1"); en.Status != st || en.Progress["pages"] != 80 {
				t.Errorf("%s, last %v: %+v", st, last, en)
			}
		}
	}
}

// TestIdleSkipModeChangesNothing: skip means skip, also for an idle row.
func TestIdleSkipModeChangesNothing(t *testing.T) {
	e := idleEnv(t)
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(2)))
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(90)))
	if got := statusOf(t, e, "1"); got != library.Active {
		t.Fatalf("%q", got)
	}
}

// TestIdlePausedStaysWithoutKnownActivity: Paused goes back to Active only
// when the row's last activity is within the cutoff; a row without one keeps
// the Paused status (other fields still apply).
func TestIdlePausedStaysWithoutKnownActivity(t *testing.T) {
	e := idleEnv(t)
	importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(2)))
	setStatus(t, e, "1", library.Paused)
	importRows(t, e, ModeAlways, activeRow("1", 20, nil))
	if _, en := e.item("book", "book", "isbn", "1"); en.Status != library.Paused || en.Progress["pages"] != 20 {
		t.Fatalf("%+v", en)
	}
}

// TestIdleRepauseRespectsManualEdit: a game paused by the rule and set back
// to Active by hand is not paused again by a row that is not newer while the
// manual change is within the cutoff; once that change is older than the
// cutoff too, the next import pauses it.
func TestIdleRepauseRespectsManualEdit(t *testing.T) {
	e := idleEnv(t)
	row := activeRow("1", 10, daysAgo(60))
	importRows(t, e, ModeNewer, row)
	if got := statusOf(t, e, "1"); got != library.Paused {
		t.Fatalf("first import: %q", got)
	}
	it, en := e.item("book", "book", "isbn", "1")
	en.Status = library.Active
	if err := e.lib.UpsertEntry(context.Background(), *en); err != nil {
		t.Fatal(err)
	}
	backdateTo(t, e, it.ID, *daysAgo(0)) // changed by hand today
	importRows(t, e, ModeNewer, row)
	if got := statusOf(t, e, "1"); got != library.Active {
		t.Fatalf("manual edit undone: %q", got)
	}
	backdateTo(t, e, it.ID, *daysAgo(31)) // the edit is now past the cutoff
	importRows(t, e, ModeNewer, row)
	if got := statusOf(t, e, "1"); got != library.Paused {
		t.Fatalf("idle past the cutoff: %q", got)
	}
}

// TestIdleKeepsCompletedAndDroppedInNewerMode: under "newer" an idle row that
// is not newer leaves a Completed or Dropped entry alone.
func TestIdleKeepsCompletedAndDroppedInNewerMode(t *testing.T) {
	for _, st := range []library.Status{library.Completed, library.Dropped} {
		e := idleEnv(t)
		importRows(t, e, ModeSkip, activeRow("1", 10, daysAgo(200)))
		id := setStatus(t, e, "1", st)
		backdateTo(t, e, id, *daysAgo(100)) // after the row's activity, past the cutoff
		importRows(t, e, ModeNewer, activeRow("1", 80, daysAgo(200)))
		if _, en := e.item("book", "book", "isbn", "1"); en.Status != st || en.Progress["pages"] != 10 {
			t.Errorf("%s: %+v", st, en)
		}
		if r := e.report(); r.Updated != 0 {
			t.Errorf("%s: report %+v", st, r)
		}
	}
}
