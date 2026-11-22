package backup

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
)

// listOf creates a custom list holding the items.
func (e *env) listOf(name string, ids ...int64) {
	e.t.Helper()
	l, err := e.lib.CreateList(bg, name)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, id := range ids {
		if err := e.lib.AddToCustomList(bg, l.ID, id); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) count(q string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// TestRestoreV1FixtureIntoRuns restores a format-1 archive written by the
// format-1 writer (testdata/backup-v1.zip): each entry becomes run 1, the
// comment the entry's notes, the change log run 1's events.
func TestRestoreV1FixtureIntoRuns(t *testing.T) {
	data, err := os.ReadFile("testdata/backup-v1.zip")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := open(t, data); d.FormatVersion != 1 {
		t.Fatalf("fixture format = %d", d.FormatVersion)
	}
	e := newEnv(t)
	rep := mustRestore(t, e, data)
	if rep.Created != 4 || rep.Failed+rep.Skipped+rep.UnknownPlugin != 0 || rep.SeriesCreated != 1 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	book, err := e.lib.ItemBySource(bg, "book", "book", "books", "v1")
	if err != nil || book == nil || book.CoverPath == "" {
		t.Fatalf("book = %+v, %v", book, err)
	}
	runs, err := e.lib.Runs(bg, book.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	r := runs[0]
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	if r.Number != 1 || r.Status != library.Completed || r.Rating == nil || *r.Rating != 9 || r.Review != "" ||
		r.StartedOn == nil || !r.StartedOn.Equal(day(2023, 5, 1)) || r.FinishedOn == nil || !r.FinishedOn.Equal(day(2023, 6, 2)) ||
		r.Progress["pages"] != 300 || r.CreatedAt.Format(time.RFC3339) != "2026-11-16T09:48:22Z" {
		t.Fatalf("run 1 = %+v", r)
	}
	en, err := e.lib.Entry(bg, book.ID)
	if err != nil || en.Notes != "first read\nloved it" || en.RunNumber != 1 || en.RunID != r.ID {
		t.Fatalf("entry = %+v, %v", en, err)
	}
	if n := e.count(`SELECT count(*) FROM entry_events WHERE run_id IS NULL`); n != 0 {
		t.Fatalf("events without a run = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM entry_events`); n != 5 {
		t.Fatalf("events = %d", n)
	}
	// the show keeps its inactive season value on run 1
	var items []library.BackupItem
	if err := e.lib.BackupItems(bg, 200, func(bi library.BackupItem) error { items = append(items, bi); return nil }); err != nil {
		t.Fatal(err)
	}
	show := items[1]
	if show.SourceID != "v1" || show.Entry == nil || len(show.Entry.Runs) != 1 ||
		!reflect.DeepEqual(show.Entry.Runs[0].Progress, map[string]float64{"episode": 5, "season": 2}) || len(show.Entry.Runs[0].Events) != 2 {
		t.Fatalf("show = %+v", show.Entry)
	}
	if items[2].Entry != nil || items[3].Entry == nil || items[3].Entry.Runs[0].Status != library.Planned {
		t.Fatalf("other items = %+v / %+v", items[2].Entry, items[3].Entry)
	}
	// a second restore of the fixture adds nothing
	rep = mustRestore(t, e, data)
	if rep.Created != 0 || rep.Skipped != 4 || rep.SeriesSkipped != 1 {
		t.Fatalf("second report = %+v", rep)
	}
}

func TestBackupFormat2RestoreTwiceAddsNothing(t *testing.T) {
	a := newEnv(t)
	one := a.book("1", "", &library.Entry{Status: library.Completed, Progress: map[string]float64{"pages": 300}, Notes: "n"})
	if _, err := a.lib.StartRun(bg, one, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	two := a.book("2", "", &library.Entry{Status: library.Planned})
	if err := a.lib.SetEntryTags(bg, one, []string{"Comfort", "reread"}); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.SetEntryTags(bg, two, []string{"comfort"}); err != nil {
		t.Fatal(err)
	}
	a.listOf("Shelf", two, one)
	a.listOf("Empty")
	data := a.archive()
	d, files := open(t, data)
	if d.FormatVersion != FormatVersion {
		t.Fatalf("format = %d", d.FormatVersion)
	}
	body := string(files["backup.json"])
	if i, j, k := strings.Index(body, `"items":`), strings.Index(body, `"series":`), strings.Index(body, `"lists":[`); !(i < j && j < k) {
		t.Fatalf("member order items %d, series %d, lists %d:\n%s", i, j, k, body)
	}

	b := newEnv(t)
	rep := mustRestore(t, b, data)
	if rep.Created != 2 || rep.Lists != 2 || rep.ListItemsAdded != 2 || rep.ListItemsLeftOut != 0 || len(rep.Notes) != 0 {
		t.Fatalf("first report = %+v", rep)
	}
	snapshot := func() [6]int {
		return [6]int{b.count(`SELECT count(*) FROM items`), b.count(`SELECT count(*) FROM entry_runs`), b.count(`SELECT count(*) FROM lists`),
			b.count(`SELECT count(*) FROM list_items`), b.count(`SELECT count(*) FROM entry_user_tags`), b.count(`SELECT count(*) FROM user_tags`)}
	}
	first := snapshot()
	if first != [6]int{2, 3, 2, 2, 3, 2} {
		t.Fatalf("rows = %v", first)
	}
	rep = mustRestore(t, b, data)
	if rep.Created != 0 || rep.Skipped != 2 || rep.ListItemsAdded != 0 || rep.ListItemsLeftOut != 0 {
		t.Fatalf("second report = %+v", rep)
	}
	if again := snapshot(); again != first {
		t.Fatalf("second restore changed rows: %v -> %v", first, again)
	}
	// the restored library backs up the same items and lists
	d2, files2 := open(t, b.archive())
	if !reflect.DeepEqual(d.Items, d2.Items) {
		t.Fatalf("items differ:\nA %+v\nB %+v", d.Items, d2.Items)
	}
	lists := func(body []byte) string {
		s := string(body)
		return s[strings.Index(s, `"lists":`):]
	}
	if lists(files["backup.json"]) != lists(files2["backup.json"]) {
		t.Fatalf("lists differ:\n%s\n%s", lists(files["backup.json"]), lists(files2["backup.json"]))
	}
}

func TestRestoreListsMember(t *testing.T) {
	item := `{"plugin_id":"book","kind":"book","source":"books","source_id":"1","titles":[{"lang":"en","text":"Book 1"}],"progress_keys":["pages"],` +
		`"entry":{"notes":"","runs":[{"number":1,"status":"planned","rating":null,"started_on":null,"finished_on":null,"progress":{}}]}}`
	list := func(name string, ids ...string) string {
		var parts []string
		for _, id := range ids {
			parts = append(parts, `{"plugin_id":"book","kind":"book","source":"books","source_id":"`+id+`","position":1,"added_at":"2024-01-01T00:00:00Z"}`)
		}
		return `{"name":"` + name + `","manual_order":false,"created_at":"2024-01-01T00:00:00Z","items":[` + strings.Join(parts, ",") + `]}`
	}
	t.Run("left out and invalid lists are noted", func(t *testing.T) {
		e := newEnv(t)
		body := header("2") + item + `],"lists":[` + list("Shelf", "1", "gone", "gone2") + `,` + list("bad\\u0007name", "1") + `,42]}`
		rep := mustRestore(t, e, zipOf(t, entry{name: "backup.json", body: []byte(body)}))
		if rep.Created != 1 || rep.Lists != 1 || rep.ListItemsAdded != 1 || rep.ListItemsLeftOut != 2 || len(rep.Notes) != 3 {
			t.Fatalf("report = %+v", rep)
		}
		if !strings.Contains(rep.Notes[0].Reason, "2 entries left out") || rep.Notes[0].Title != "Shelf" ||
			!strings.Contains(rep.Notes[1].Reason, "not valid") || !strings.Contains(rep.Notes[2].Reason, "cannot be read") {
			t.Fatalf("notes = %+v", rep.Notes)
		}
	})
	t.Run("lists before items are refused", func(t *testing.T) {
		e := newEnv(t)
		body := `{"format":"allister.backup","format_version":2,"lists":[],"items":[]}`
		if _, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil); err == nil || !strings.Contains(err.Error(), `"lists" must come after "items"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a second lists member stops the restore", func(t *testing.T) {
		e := newEnv(t)
		body := header("2") + item + `],"lists":[],"lists":[]}`
		rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
		if err != nil || !strings.Contains(rep.Aborted, `more than one "lists"`) {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
	t.Run("a format-1 entry in a format-2 backup fails", func(t *testing.T) {
		e := newEnv(t)
		v1 := `{"plugin_id":"book","kind":"book","source":"books","source_id":"1","titles":[{"lang":"en","text":"Book 1"}],"progress_keys":["pages"],` +
			`"entry":{"status":"planned","rating":null,"comment":"","started_on":null,"finished_on":null,"progress":{}}}`
		rep := mustRestore(t, e, zipOf(t, entry{name: "backup.json", body: []byte(header("2") + v1 + "]}")}))
		if rep.Failed != 1 || !strings.Contains(rep.FailedRows[0].Reason, "run") {
			t.Fatalf("report = %+v", rep)
		}
	})
}

// TestBackupFormat4KeepsBulkEvents: a bulk change's events round-trip; the
// archive says the current format (4, which includes format 3's origin
// "bulk"), so an Allister that does not know that origin refuses the file
// instead of dropping those items.
func TestBackupFormat4KeepsBulkEvents(t *testing.T) {
	a := newEnv(t)
	one := a.book("1", "", &library.Entry{Status: library.Planned})
	if _, err := a.lib.BulkSetStatus(bg, []int64{one}, library.Active, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	q := `SELECT count(*) FROM entry_events WHERE origin = 'bulk'`
	n := a.count(q)
	if n == 0 {
		t.Fatal("no bulk event to back up")
	}
	data := a.archive()
	if d, _ := open(t, data); d.FormatVersion != 4 {
		t.Fatalf("format = %d, want 4", d.FormatVersion)
	}
	b := newEnv(t)
	if rep := mustRestore(t, b, data); rep.Created != 1 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := b.count(q); got != n {
		t.Fatalf("restored bulk events = %d, want %d", got, n)
	}
}

func TestRestoreReadsFormats1To4(t *testing.T) {
	for _, v := range []string{"1", "2", "3", "4"} {
		e := newEnv(t)
		body := `{"format":"allister.backup","format_version":` + v + `,"items":[]}`
		if _, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil); err != nil {
			t.Fatalf("format %s: %v", v, err)
		}
	}
}

// TestRestoreReadsFormat3Archive: a format-3 file (as Allister v0.5.0
// writes it) with an item still restores.
func TestRestoreReadsFormat3Archive(t *testing.T) {
	item, _ := json.Marshal(bookItem("1"))
	body := `{"format":"allister.backup","format_version":3,"items":[` + string(item) + `]}`
	e := newEnv(t)
	rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(body)}), Limits{}, nil)
	if err != nil || rep.Created != 1 {
		t.Fatalf("created = %d, err = %v", rep.Created, err)
	}
}
