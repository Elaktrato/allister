package library

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// collectLists returns every BackupList of the library.
func collectLists(t *testing.T, l *Library) []BackupList {
	t.Helper()
	var out []BackupList
	if err := l.BackupLists(bg, func(bl BackupList) error {
		out = append(out, bl)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// threeRunShow is richShow watched twice and started a third time: run 1
// keeps the inactive position value, run 2 a review, runs 1 and 3 a unit
// note; the entry has two own tags and sits in two custom lists.
func threeRunShow(t *testing.T, e *env, id string) int64 {
	t.Helper()
	itemID := richShow(t, e, id)
	upsert := func(st Status, ep float64) {
		t.Helper()
		if err := e.lib.UpsertEntry(bg, Entry{ItemID: itemID, Status: st, Rating: ip(8), Notes: "line one\nline two",
			StartedOn: tp(day(2024, 3, 5)), Progress: map[string]float64{"episode": ep}}); err != nil {
			t.Fatal(err)
		}
	}
	upsert(Completed, 12)
	if err := e.lib.SetUnitNote(bg, mustEntry(t, e.lib, itemID).RunID, "episode", 3, "the **cliffhanger**"); err != nil {
		t.Fatal(err)
	}
	run2, err := e.lib.StartRun(bg, itemID, day(2025, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpdateRun(bg, run2.ID, RunUpdate{Status: Completed, Rating: ip(9), Review: "even better\nthe second time",
		StartedOn: tp(day(2025, 1, 10)), FinishedOn: tp(day(2025, 2, 1))}); err != nil {
		t.Fatal(err)
	}
	run3, err := e.lib.StartRun(bg, itemID, day(2026, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: itemID, Status: Active, Rating: ip(9), Notes: "line one\nline two",
		StartedOn: tp(day(2026, 6, 1)), Progress: map[string]float64{"episode": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetUnitNote(bg, run3.ID, "episode", 1, "rewatch notes"); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetEntryTags(bg, itemID, []string{"with-partner", "Comfort"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Watch again", "Favourites"} {
		lst, err := e.lib.CreateList(bg, name)
		if err != nil && !errors.Is(err, ErrListExists) {
			t.Fatal(err)
		}
		if err != nil {
			all, _ := e.lib.Lists(bg)
			for _, x := range all {
				if x.Name == name {
					lst = x
				}
			}
		}
		if err := e.lib.AddToCustomList(bg, lst.ID, itemID); err != nil {
			t.Fatal(err)
		}
		if name == "Favourites" {
			if err := e.lib.SetListOrder(bg, lst.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	return itemID
}

func TestBackupV2CarriesRunsNotesTagsAndLists(t *testing.T) {
	e := newEnv(t)
	threeRunShow(t, e, "1")
	items := collect(t, e.lib, 200)
	if len(items) != 1 || items[0].Entry == nil {
		t.Fatalf("items = %+v", items)
	}
	en := items[0].Entry
	if en.Notes != "line one\nline two" || en.AddedAt == "" || en.UpdatedAt == "" {
		t.Fatalf("entry = %+v", en)
	}
	if !reflect.DeepEqual(en.UserTags, []string{"Comfort", "with-partner"}) {
		t.Fatalf("user tags = %v", en.UserTags)
	}
	if len(en.Runs) != 3 {
		t.Fatalf("runs = %+v", en.Runs)
	}
	for i, r := range en.Runs {
		if r.Number != i+1 || r.CreatedAt == "" || r.UpdatedAt == "" || len(r.Events) == 0 {
			t.Fatalf("run %d = %+v", i, r)
		}
	}
	r1, r2, r3 := en.Runs[0], en.Runs[1], en.Runs[2]
	// every stored value, also the inactive position of run 1
	if r1.Status != Completed || !reflect.DeepEqual(r1.Progress, map[string]float64{"episode": 12, "position": 90}) {
		t.Fatalf("run 1 = %+v", r1)
	}
	if len(r1.UnitNotes) != 1 || r1.UnitNotes[0].ProgressKey != "episode" || r1.UnitNotes[0].Unit != 3 ||
		r1.UnitNotes[0].Body != "the **cliffhanger**" || r1.UnitNotes[0].CreatedAt == "" {
		t.Fatalf("run 1 notes = %+v", r1.UnitNotes)
	}
	if r2.Status != Completed || r2.Rating == nil || *r2.Rating != 9 || r2.Review != "even better\nthe second time" ||
		r2.StartedOn == nil || *r2.StartedOn != "2025-01-10" || r2.FinishedOn == nil || *r2.FinishedOn != "2025-02-01" {
		t.Fatalf("run 2 = %+v", r2)
	}
	if r3.Status != Active || r3.Progress["episode"] != 2 || len(r3.UnitNotes) != 1 || r3.UnitNotes[0].Unit != 1 {
		t.Fatalf("run 3 = %+v", r3)
	}
	var logged int
	for _, r := range en.Runs {
		logged += len(r.Events)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_events`); logged != n || len(en.OrphanEvents) != 0 {
		t.Fatalf("events in runs = %d, stored = %d, orphans = %d", logged, n, len(en.OrphanEvents))
	}
	raw, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"notes":"line one\nline two"`, `"runs":[{"number":1,`, `"unit_notes":[{"progress_key":"episode","unit":3,`,
		`"user_tags":["Comfort","with-partner"]`, `"review":"even better\nthe second time"`, `"added_at":`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json lacks %s: %s", want, raw)
		}
	}
	for _, not := range []string{`"comment"`, `display_run`, `"orphan_events"`} {
		if strings.Contains(string(raw), not) {
			t.Fatalf("json has %s: %s", not, raw)
		}
	}
	lists := collectLists(t, e.lib)
	if len(lists) != 2 || lists[0].Name != "Favourites" || !lists[0].ManualOrder || lists[1].Name != "Watch again" || lists[1].ManualOrder {
		t.Fatalf("lists = %+v", lists)
	}
	ref := BackupPartRef{PluginID: "tv", Kind: "show", Source: "tvdata", SourceID: "1"}
	for _, bl := range lists {
		if bl.CreatedAt == "" || len(bl.Items) != 1 || bl.Items[0].BackupPartRef != ref || bl.Items[0].Position != 1 || bl.Items[0].AddedAt == "" {
			t.Fatalf("list = %+v", bl)
		}
	}
	raw, _ = json.Marshal(lists[0])
	for _, want := range []string{`"name":"Favourites"`, `"manual_order":true`, `"created_at":`, `"items":[{"plugin_id":"tv","kind":"show","source":"tvdata","source_id":"1","position":1,"added_at":`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("list json lacks %s: %s", want, raw)
		}
	}
}

func TestBackupV2RoundTripAndSecondRestoreAddsNothing(t *testing.T) {
	a := newEnv(t)
	threeRunShow(t, a, "1")
	// a second entry in one of the lists, with a tag spelled differently
	other := addBook(t, a.lib, "b1", fp(100), Entry{Status: Planned})
	if err := a.lib.SetEntryTags(bg, other, []string{"COMFORT"}); err != nil {
		t.Fatal(err)
	}
	for _, x := range must(a.lib.Lists(bg)) {
		if x.Name == "Watch again" {
			if err := a.lib.AddToCustomList(bg, x.ID, other); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, lists := collect(t, a.lib, 200), collectLists(t, a.lib)
	// through JSON, as the archive carries it
	raw, err := json.Marshal(struct {
		Items []BackupItem
		Lists []BackupList
	}{before, lists})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []BackupItem
		Lists []BackupList
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	b := newEnv(t)
	restore := func() (created, added, leftOut int) {
		t.Helper()
		for _, r := range restoreAll(t, b.lib, doc.Items, keepCovers) {
			if r.Outcome == RestoreCreated {
				created++
			}
		}
		for _, bl := range doc.Lists {
			n, left, err := b.lib.RestoreList(bg, bl)
			if err != nil {
				t.Fatal(err)
			}
			added += n
			leftOut += left
		}
		return created, added, leftOut
	}
	if c, n, left := restore(); c != 2 || n != 3 || left != 0 {
		t.Fatalf("first restore: created %d, list items added %d, left out %d", c, n, left)
	}
	if after := collect(t, b.lib, 200); !reflect.DeepEqual(before, after) {
		ja, _ := json.MarshalIndent(before, "", " ")
		jb, _ := json.MarshalIndent(after, "", " ")
		t.Fatalf("round trip differs:\nA: %s\nB: %s", ja, jb)
	}
	if after := collectLists(t, b.lib); !reflect.DeepEqual(lists, after) {
		t.Fatalf("lists differ:\nA: %+v\nB: %+v", lists, after)
	}
	// the display run is recomputed: the active third run
	var number int
	if err := b.db.QueryRow(`SELECT r.number FROM entries e JOIN entry_runs r ON r.id = e.display_run_id
		JOIN items i ON i.id = e.item_id WHERE i.source_id = '1'`).Scan(&number); err != nil || number != 3 {
		t.Fatalf("display run number = %d, %v", number, err)
	}
	snapshot := func() [5]int {
		return [5]int{count(t, b.db, `SELECT count(*) FROM items`), count(t, b.db, `SELECT count(*) FROM entry_runs`),
			count(t, b.db, `SELECT count(*) FROM list_items`), count(t, b.db, `SELECT count(*) FROM entry_user_tags`),
			count(t, b.db, `SELECT count(*) FROM user_tags`)}
	}
	first := snapshot()
	if first != [5]int{2, 4, 3, 3, 2} {
		t.Fatalf("rows after the first restore = %v", first)
	}
	if c, n, left := restore(); c != 0 || n != 0 || left != 0 {
		t.Fatalf("second restore: created %d, list items added %d, left out %d", c, n, left)
	}
	if again := snapshot(); again != first {
		t.Fatalf("second restore changed rows: %v -> %v", first, again)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestBackupV2KeepsOrphanEvents(t *testing.T) {
	a := newEnv(t)
	id := addBook(t, a.lib, "o1", fp(100), Entry{Status: Completed, Progress: map[string]float64{"pages": 100}})
	run2, err := a.lib.StartRun(bg, id, day(2026, 3, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 40}}); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.DeleteRun(bg, run2.ID); err != nil {
		t.Fatal(err)
	}
	orphans := count(t, a.db, `SELECT count(*) FROM entry_events WHERE run_id IS NULL`)
	if orphans < 2 {
		t.Fatalf("orphan events = %d", orphans)
	}
	before := collect(t, a.lib, 200)
	en := before[0].Entry
	if len(en.Runs) != 1 || len(en.OrphanEvents) != orphans {
		t.Fatalf("runs %d, orphan events %d (want %d)", len(en.Runs), len(en.OrphanEvents), orphans)
	}
	b := newEnv(t)
	if r := restoreAll(t, b.lib, before, keepCovers); r[0].Outcome != RestoreCreated {
		t.Fatalf("result = %+v", r[0])
	}
	if n := count(t, b.db, `SELECT count(*) FROM entry_events WHERE run_id IS NULL`); n != orphans {
		t.Fatalf("restored orphan events = %d, want %d", n, orphans)
	}
	if after := collect(t, b.lib, 200); !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip differs:\nA %+v\nB %+v", before[0].Entry, after[0].Entry)
	}
}

func TestBackupV2EventsTruncatedAcrossRuns(t *testing.T) {
	a := newEnv(t)
	id := addBook(t, a.lib, "t1", fp(100), Entry{Status: Completed, Progress: map[string]float64{"pages": 100}})
	insertEventsBulk(t, a.lib, id, 5001)
	if _, err := a.lib.StartRun(bg, id, day(2026, 3, 1)); err != nil {
		t.Fatal(err)
	}
	en := collect(t, a.lib, 200)[0].Entry
	// newest 5,000 of the entry: the start of run 2 and 4,999 of run 1
	if len(en.Runs[1].Events) != 1 || len(en.Runs[0].Events) != 4999 || !en.Runs[0].EventsTruncated || en.Runs[1].EventsTruncated {
		t.Fatalf("events %d/%d, truncated %v/%v", len(en.Runs[0].Events), len(en.Runs[1].Events), en.Runs[0].EventsTruncated, en.Runs[1].EventsTruncated)
	}
	b := newEnv(t)
	res := restoreAll(t, b.lib, collect(t, a.lib, 200), keepCovers)
	if res[0].Outcome != RestoreCreated || len(res[0].Notes) != 1 || !strings.Contains(res[0].Notes[0], "newest 5,000 events") {
		t.Fatalf("result = %+v", res[0])
	}
}

func TestRestoreListMergesByName(t *testing.T) {
	e := newEnv(t)
	x := addBook(t, e.lib, "x", fp(100), Entry{Status: Planned})
	y := addBook(t, e.lib, "y", fp(100), Entry{Status: Planned})
	mustSave(t, e.lib, "book", "book", bookData("noentry"))
	fav, err := e.lib.CreateList(bg, "favourites")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddToCustomList(bg, fav.ID, y); err != nil {
		t.Fatal(err)
	}
	ref := func(id string) BackupListItem {
		return BackupListItem{BackupPartRef: BackupPartRef{PluginID: "book", Kind: "book", Source: "books", SourceID: id}, Position: 1, AddedAt: "2024-01-02T03:04:05Z"}
	}
	bl := BackupList{Name: "Favourites", ManualOrder: true, CreatedAt: "2023-01-01T00:00:00Z",
		Items: []BackupListItem{ref("y"), ref("x"), ref("missing"), ref("noentry"), ref("x")}}
	added, left, err := e.lib.RestoreList(bg, bl)
	if err != nil || added != 1 || left != 2 {
		t.Fatalf("merge = %d added, %d left out, %v", added, left, err)
	}
	got := must(e.lib.Lists(bg))
	if len(got) != 1 || got[0].Name != "favourites" || got[0].ManualOrder || got[0].Items != 2 {
		t.Fatalf("lists = %+v", got)
	}
	var pos int
	if err := e.db.QueryRow(`SELECT position FROM list_items WHERE list_id = ? AND item_id = ?`, fav.ID, x).Scan(&pos); err != nil || pos != 2 {
		t.Fatalf("x position = %d, %v", pos, err)
	}
	if added, left, err := e.lib.RestoreList(bg, bl); err != nil || added != 0 || left != 2 {
		t.Fatalf("again = %d added, %d left out, %v", added, left, err)
	}
	// a new list keeps the backup's name, order, stamps and positions
	bl.Name, bl.Items = "Reread", []BackupListItem{ref("x"), ref("y")}
	bl.Items[0].Position, bl.Items[1].Position = 7, 3
	if added, left, err := e.lib.RestoreList(bg, bl); err != nil || added != 2 || left != 0 {
		t.Fatalf("new = %d added, %d left out, %v", added, left, err)
	}
	lists := collectLists(t, e.lib)
	if len(lists) != 2 || lists[1].Name != "Reread" || !lists[1].ManualOrder || lists[1].CreatedAt != "2023-01-01T00:00:00Z" ||
		len(lists[1].Items) != 2 || lists[1].Items[0].SourceID != "y" || lists[1].Items[0].Position != 1 || lists[1].Items[1].SourceID != "x" ||
		lists[1].Items[1].AddedAt != "2024-01-02T03:04:05Z" {
		t.Fatalf("restored list = %+v", lists)
	}
	// an empty list is restored as such
	if added, left, err := e.lib.RestoreList(bg, BackupList{Name: "Empty"}); err != nil || added != 0 || left != 0 {
		t.Fatalf("empty = %d, %d, %v", added, left, err)
	}
	if n := len(must(e.lib.Lists(bg))); n != 3 {
		t.Fatalf("lists = %d", n)
	}
	for _, bad := range []BackupList{{Name: " "}, {Name: "a\x07b"}, {Name: strings.Repeat("n", 81)}, {Name: "Stamp", CreatedAt: "soon"},
		{Name: "Item stamp", Items: []BackupListItem{{BackupPartRef: ref("x").BackupPartRef, AddedAt: "later"}}}} {
		if _, _, err := e.lib.RestoreList(bg, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: err = %v", bad, err)
		}
	}
	if n := len(must(e.lib.Lists(bg))); n != 3 {
		t.Fatalf("invalid lists written: %d", n)
	}
}

func TestRestoreV2RejectsBadEntries(t *testing.T) {
	a := newEnv(t)
	threeRunShow(t, a, "1")
	good := collect(t, a.lib, 200)[0]
	good.SourceID = "fresh"
	cases := []struct {
		name, want string
		mod        func(*BackupEntry)
	}{
		{"no runs", "run", func(en *BackupEntry) { en.Runs = nil }},
		{"gap", "run numbers", func(en *BackupEntry) { en.Runs[2].Number = 5 }},
		{"duplicate number", "run numbers", func(en *BackupEntry) { en.Runs[1].Number = 1 }},
		{"past run active", "past run", func(en *BackupEntry) { en.Runs[0].Status = Active }},
		{"bad run status", "status", func(en *BackupEntry) { en.Runs[1].Status = "watching" }},
		{"review NUL", "NUL", func(en *BackupEntry) { en.Runs[1].Review = "a\x00b" }},
		{"notes NUL", "NUL", func(en *BackupEntry) { en.Notes = "a\x00b" }},
		{"run progress above total", "above the total", func(en *BackupEntry) { en.Runs[1].Progress["episode"] = 99 }},
		{"run inactive negative", "negative", func(en *BackupEntry) { en.Runs[2].Progress["position"] = -1 }},
		{"run finish before start", "before the start", func(en *BackupEntry) { s := "2020-01-01"; en.Runs[1].FinishedOn = &s }},
		{"run stamp", "time", func(en *BackupEntry) { en.Runs[0].CreatedAt = "soon" }},
		{"added stamp", "time", func(en *BackupEntry) { en.AddedAt = "soon" }},
		{"note on duration", "count or pages", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].ProgressKey = "position" }},
		{"note unknown key", "not a progress", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].ProgressKey = "chapter" }},
		{"note unit 0", "unit", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].Unit = 0 }},
		{"note above total", "above the total", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].Unit = 13 }},
		{"note blank", "note", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].Body = "  " }},
		{"note control", "control character", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].Body = "a\x07" }},
		{"note twice", "twice", func(en *BackupEntry) { en.Runs[0].UnitNotes = append(en.Runs[0].UnitNotes, en.Runs[0].UnitNotes[0]) }},
		{"note stamp", "time", func(en *BackupEntry) { en.Runs[0].UnitNotes[0].UpdatedAt = "soon" }},
		{"tag control", "control character", func(en *BackupEntry) { en.UserTags = []string{"a\x07"} }},
		{"tag long", "longer than", func(en *BackupEntry) { en.UserTags = []string{strings.Repeat("t", 41)} }},
		{"bad orphan event", "type", func(en *BackupEntry) {
			en.OrphanEvents = []BackupEvent{{OccurredAt: "2026-01-01T00:00:00Z", Type: "x", Origin: "manual"}}
		}},
		{"too many events", "events", func(en *BackupEntry) {
			ev := en.Runs[0].Events[0]
			for len(en.Runs[0].Events)+len(en.Runs[1].Events)+len(en.Runs[2].Events) <= 5000 {
				en.Runs[0].Events = append(en.Runs[0].Events, ev)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newEnv(t)
			raw, _ := json.Marshal(good)
			var bi BackupItem
			if err := json.Unmarshal(raw, &bi); err != nil {
				t.Fatal(err)
			}
			tc.mod(bi.Entry)
			res, err := b.lib.RestoreItem(bg, bi, keepCovers)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != RestoreFailed || !strings.Contains(res.Reason, tc.want) {
				t.Fatalf("result = %+v, want failed mentioning %q", res, tc.want)
			}
			if n := count(t, b.db, `SELECT count(*) FROM items`) + count(t, b.db, `SELECT count(*) FROM user_tags`); n != 0 {
				t.Fatalf("rows written: %d", n)
			}
		})
	}
	// many tags beyond the cap
	bi := good
	en := *good.Entry
	en.UserTags = nil
	for i := 0; i < 51; i++ {
		en.UserTags = append(en.UserTags, "tag"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	bi.Entry = &en
	b := newEnv(t)
	if res, err := b.lib.RestoreItem(bg, bi, keepCovers); err != nil || res.Outcome != RestoreFailed || !strings.Contains(res.Reason, "tags") {
		t.Fatalf("51 tags = %+v, %v", res, err)
	}
}

func TestBackupEntryV1UpgradesToRunOne(t *testing.T) {
	started, finished := "2023-05-01", "2023-06-02"
	old := BackupEntryV1{Status: Completed, Rating: ip(9), Comment: "c", StartedOn: &started, FinishedOn: &finished,
		Progress: map[string]float64{"pages": 300, "percent": 40}, UpdatedAt: "2024-01-02T03:04:05Z",
		Events: []BackupEvent{{OccurredAt: "2024-01-02T03:04:05Z", Type: "status", NewText: "completed", Origin: "manual"}}, EventsTruncated: true}
	got := old.Upgrade()
	want := BackupEntry{Notes: "c", AddedAt: "2024-01-02T03:04:05Z", UpdatedAt: "2024-01-02T03:04:05Z", Runs: []BackupRun{{
		Number: 1, Status: Completed, Rating: ip(9), StartedOn: &started, FinishedOn: &finished, Progress: old.Progress,
		Events: old.Events, EventsTruncated: true, CreatedAt: "2024-01-02T03:04:05Z", UpdatedAt: "2024-01-02T03:04:05Z"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upgrade = %+v\nwant %+v", got, want)
	}
	// the v1 JSON shape
	var v1 BackupEntryV1
	if err := json.Unmarshal([]byte(`{"status":"active","rating":null,"comment":"hi","started_on":null,"finished_on":null,"progress":{"pages":3},"updated_at":"2024-01-02T03:04:05Z","events_truncated":true}`), &v1); err != nil {
		t.Fatal(err)
	}
	if v1.Status != Active || v1.Comment != "hi" || v1.Progress["pages"] != 3 || !v1.EventsTruncated {
		t.Fatalf("v1 = %+v", v1)
	}
}
