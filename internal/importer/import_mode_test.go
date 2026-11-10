package importer

import (
	"context"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

func (e *genEnv) submitAccount(importerID, account, mode string) int64 {
	e.t.Helper()
	id, err := e.im.SubmitAccount(context.Background(), "book", importerID, account, mode)
	if err != nil {
		e.t.Fatalf("submit account: %v", err)
	}
	return id
}

// importOnce runs a fresh account import of one book row.
func importOnce(t *testing.T, e *genEnv, row plugin.ParsedEntry, mode string) {
	t.Helper()
	e.bookImp.AccountRows = []plugin.ParsedEntry{row}
	id := e.submitAccount("books-csv", "someone", mode)
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
}

// TestImportModesUpdateExistingEntries: skip leaves an existing entry alone,
// newer applies only when the row is newer than the entry, always applies
// regardless - and only status, rating and the primary progress change.
func TestImportModesUpdateExistingEntries(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		mode    string
		rowTime time.Time
		want    string // "unchanged", "updated", "skipped-newer"
	}{
		{"skip", ModeSkip, base.Add(48 * time.Hour), "unchanged"},
		{"newer applies", ModeNewer, base.Add(48 * time.Hour), "updated"},
		{"newer keeps the local edit", ModeNewer, base.Add(-48 * time.Hour), "unchanged"},
		{"no timestamp is never newer", ModeNewer, time.Time{}, "unchanged"},
		{"always applies an older row", ModeAlways, base.Add(-48 * time.Hour), "updated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newGenEnv(t)
			// the entry exists as an earlier import left it, changed locally
			importOnce(t, e, bookEntry("1", "One", plugin.Completed, 300), ModeSkip)
			it, err := e.lib.ItemByExternalID(context.Background(), "book", "book", "isbn", "1")
			if err != nil || it == nil {
				t.Fatalf("%v %v", it, err)
			}
			// the local entry is an old one, made at the base time
			if _, err := e.dbh.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`,
				base.Format("2006-01-02T15:04:05Z"), it.ID); err != nil {
				t.Fatal(err)
			}
			row := bookEntry("1", "One", plugin.Active, 42)
			row.Rating = ip(9)
			if !c.rowTime.IsZero() {
				row.UpdatedAt = &c.rowTime
			}
			importOnce(t, e, row, c.mode)
			en, err := e.lib.Entry(context.Background(), it.ID)
			if err != nil || en == nil {
				t.Fatalf("%v %v", en, err)
			}
			wantStatus, wantPages := library.Completed, 300.0
			if c.want == "updated" {
				wantStatus, wantPages = library.Active, 42
			}
			if en.Status != wantStatus || en.Progress["pages"] != wantPages {
				t.Fatalf("%s: %+v (want %s %v)", c.name, en, wantStatus, wantPages)
			}
			if c.want == "updated" && (en.Rating == nil || *en.Rating != 9) {
				t.Fatalf("rating not applied: %+v", en)
			}
			if c.want != "updated" && en.Rating != nil && *en.Rating == 9 {
				t.Fatalf("rating changed: %+v", en)
			}
		})
	}
}

// TestImportModesReportUpdated: the report counts updated rows separately.
func TestImportModesReportUpdated(t *testing.T) {
	e := newGenEnv(t)
	importOnce(t, e, bookEntry("1", "One", plugin.Completed, 300), ModeSkip)
	row := bookEntry("1", "One", plugin.Active, 10)
	importOnce(t, e, row, ModeAlways)
	r := e.report()
	if r.Updated != 1 || r.Skipped != 0 || r.Imported != 0 {
		t.Fatalf("%+v", r)
	}
}
