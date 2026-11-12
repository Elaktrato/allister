package library

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func progressOf(t *testing.T, l *Library, id int64) map[string]float64 {
	t.Helper()
	return mustEntry(t, l, id).Progress
}

// TestIncrementStartsAPlannedEntry: a +1 on a planned entry moves it to
// active — the user started consuming it — while every other status stays.
func TestIncrementStartsAPlannedEntry(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	planned := addBook(t, l, "p1", fp(10), Entry{Status: Planned})
	if err := l.Increment(bg, planned); err != nil {
		t.Fatal(err)
	}
	if en := mustEntry(t, l, planned); en.Status != Active || en.Progress["pages"] != 1 {
		t.Fatalf("planned +1: %+v", en)
	}
	for _, st := range []Status{Active, Paused, Dropped, Completed} {
		id := addBook(t, l, "s-"+string(st), fp(10), Entry{Status: st})
		if err := l.Increment(bg, id); err != nil {
			t.Fatal(err)
		}
		if en := mustEntry(t, l, id); en.Status != st {
			t.Fatalf("%s +1 changed the status to %s", st, en.Status)
		}
	}
}

func TestIncrementCapsAtTotalAndNoPrimary(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	// bounded: creates at 1, counts up, stops at the total
	id := addBook(t, l, "i1", fp(3), Entry{Status: Active})
	for i := 1; i <= 5; i++ {
		if err := l.Increment(bg, id); err != nil {
			t.Fatal(err)
		}
		want := float64(min(i, 3))
		if got := progressOf(t, l, id)["pages"]; got != want {
			t.Fatalf("after %d increments: %v, want %v", i, got, want)
		}
	}
	// unbounded: no total value, keeps counting
	id = addBook(t, l, "i2", nil, Entry{Status: Active, Progress: map[string]float64{"pages": 99}})
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := progressOf(t, l, id)["pages"]; got != 100 {
		t.Fatalf("pages = %v", got)
	}
	// a total of 1 or more caps even when the row is created by the increment
	id = addBook(t, l, "i3", fp(1), Entry{Status: Active})
	for range 3 {
		if err := l.Increment(bg, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := progressOf(t, l, id)["pages"]; got != 1 {
		t.Fatalf("pages = %v", got)
	}
	// the tv primary key is "episode", not the first active key
	id = addShow(t, l, "i4", fp(12), Entry{Status: Active}, WithProgressKeys([]string{"season", "episode"}))
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := progressOf(t, l, id); !slices.Equal([]float64{got["episode"]}, []float64{1}) || len(got) != 1 {
		t.Fatalf("progress = %v", got)
	}
	// no entry
	noEntry := mustSave(t, l, "book", "book", bookWith("i5", nil))
	if err := l.Increment(bg, noEntry); err != ErrNoEntry {
		t.Fatalf("err = %v", err)
	}
	if err := l.Increment(bg, 12345); err != ErrNoEntry {
		t.Fatalf("err = %v", err)
	}
	// primary key inactive for the item: documented no-op
	id = addBook(t, l, "i6", nil, Entry{Status: Active}, WithProgressKeys([]string{"percent"}))
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, id); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	// kind without a primary key: no-op
	nid, err := l.AddToList(bg, "nop", "book", bookWith("i7", nil), Entry{Status: Active})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Increment(bg, nid); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, nid); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	// the entry is touched when the progress moves
	id = addBook(t, l, "i8", nil, Entry{Status: Active})
	if _, err := e.db.Exec(`UPDATE entries SET updated_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := mustEntry(t, l, id); got.UpdatedAt.Year() < 2020 {
		t.Fatalf("updated_at = %v", got.UpdatedAt)
	}
}

func TestIncrementConcurrent(t *testing.T) {
	for _, c := range []struct {
		name  string
		total *float64
		want  float64
	}{{"unbounded", nil, 30}, {"capped", fp(20), 20}} {
		t.Run(c.name, func(t *testing.T) {
			l := newLib(t)
			id := addBook(t, l, "cc", c.total, Entry{Status: Active})
			var wg sync.WaitGroup
			errs := make(chan error, 30)
			for range 30 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- l.Increment(bg, id)
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := progressOf(t, l, id)["pages"]; got != c.want {
				t.Fatalf("pages = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSetActiveProgressRetainsInactiveValues(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "sa", fp(300), Entry{Status: Active, Progress: map[string]float64{"pages": 120}})
	if err := l.SetActiveProgress(bg, id, []string{"percent"}); err != nil {
		t.Fatal(err)
	}
	if it := mustItem(t, l, id); !slices.Equal(it.ProgressKeys, []string{"percent"}) {
		t.Fatalf("keys = %v", it.ProgressKeys)
	}
	// the value stays in the table but is hidden from the entry
	var v float64
	if err := e.db.QueryRow(`SELECT value FROM entry_progress WHERE item_id = ? AND key = 'pages'`, id).Scan(&v); err != nil || v != 120 {
		t.Fatalf("retained = %v, %v", v, err)
	}
	if got := progressOf(t, l, id); len(got) != 0 {
		t.Fatalf("progress = %v", got)
	}
	// inactive keys cannot be written, and +1 is a no-op while pages is inactive
	wantInvalid(t, l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 5}}), "not active")
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"percent": 40}}); err != nil {
		t.Fatal(err)
	}
	// re-activating shows the old value again
	if err := l.SetActiveProgress(bg, id, []string{"pages"}); err != nil {
		t.Fatal(err)
	}
	if got := progressOf(t, l, id); got["pages"] != 120 || len(got) != 1 {
		t.Fatalf("progress = %v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, id); n != 2 {
		t.Fatalf("rows = %d, want pages and percent retained", n)
	}
	// a set equals a combination regardless of order
	tid := addShow(t, l, "sb", nil, Entry{Status: Active})
	if err := l.SetActiveProgress(bg, tid, []string{"position", "episode"}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetActiveProgress(bg, 9999, []string{"pages"}); err != ErrNoEntry {
		t.Fatalf("err = %v", err)
	}
}

func TestSetActiveProgressRejectsIneligibleCombination(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "sr", nil, Entry{Status: Active})
	for _, keys := range [][]string{{"pages", "percent"}, {"nope"}, {}, nil, {"pages", "pages"}} {
		wantInvalid(t, e.lib.SetActiveProgress(bg, id, keys), "combination")
	}
	var pk string
	if err := e.db.QueryRow(`SELECT progress_keys FROM items WHERE id = ?`, id).Scan(&pk); err != nil || pk != `["pages"]` {
		t.Fatalf("progress_keys = %q, %v", pk, err)
	}
}

func TestFinishedAndNotStartedRules(t *testing.T) {
	l := newLib(t)
	book := kindOf(t, l, "book", "book")
	tv := kindOf(t, l, "tv", "show")
	pages := func(total *float64, keys ...string) *Item {
		it := &Item{ProgressKeys: keys}
		if total != nil {
			it.Fields = map[string]plugin.FieldValue{"pages": {Num: total}, "episodes": {Num: total}}
		}
		return it
	}
	type p = map[string]float64
	cases := []struct {
		name                 string
		kind                 plugin.KindDef
		item                 *Item
		prog                 p
		finished, notStarted bool
	}{
		{"pages at total", book, pages(fp(300), "pages"), p{"pages": 300}, true, false},
		{"pages below", book, pages(fp(300), "pages"), p{"pages": 299}, false, false},
		{"pages zero", book, pages(fp(300), "pages"), p{"pages": 0}, false, true},
		{"pages absent", book, pages(fp(300), "pages"), nil, false, true},
		{"pages no total never finished", book, pages(nil, "pages"), p{"pages": 99999}, false, false},
		{"pages no total not started", book, pages(nil, "pages"), p{"pages": 0}, false, true},
		{"percent 100", book, pages(nil, "percent"), p{"percent": 100}, true, false},
		{"percent 99", book, pages(nil, "percent"), p{"percent": 99}, false, false},
		{"percent absent", book, pages(nil, "percent"), nil, false, true},
		{"percent ignores pages total", book, pages(fp(300), "percent"), p{"percent": 100, "pages": 1}, true, false},
		{"inactive pages ignored", book, pages(fp(300), "percent"), p{"pages": 300}, false, true},
		{"tv episode at total", tv, pages(fp(12), "episode"), p{"episode": 12}, true, false},
		{"tv unbounded position does not block", tv, pages(fp(12), "episode", "position"), p{"episode": 12, "position": 0}, true, false},
		{"tv position alone is progress", tv, pages(fp(12), "episode", "position"), p{"episode": 0, "position": 30}, false, false},
		{"tv not started", tv, pages(fp(12), "episode", "position"), p{"episode": 0}, false, true},
		{"tv episode below", tv, pages(fp(12), "episode", "position"), p{"episode": 5, "position": 10}, false, false},
		{"tv no total never finished", tv, pages(nil, "season", "episode"), p{"season": 9, "episode": 99}, false, false},
		{"tv season alone unbounded", tv, pages(fp(12), "season", "episode"), p{"season": 3}, false, false},
		{"tv inactive position ignored", tv, pages(fp(12), "episode"), p{"episode": 0, "position": 100}, false, true},
		{"no active keys", tv, &Item{}, nil, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := Entry{Status: Active, Progress: c.prog}
			if got := Finished(c.kind, c.item, e); got != c.finished {
				t.Errorf("Finished = %v, want %v", got, c.finished)
			}
			if got := NotStarted(c.kind, c.item, e); got != c.notStarted {
				t.Errorf("NotStarted = %v, want %v", got, c.notStarted)
			}
		})
	}
}

// TestProgressStateFiltersAgreeWithHelpers builds a matrix of items (active
// key combinations, totals, values including retained inactive ones and values
// above the total) and checks that the SQL progress filters select exactly the
// items the Go helpers classify the same way.
func TestProgressStateFiltersAgreeWithHelpers(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	type spec struct {
		pluginID, kind string
		keys           []string
		total          *float64
		vals           map[string]*float64
	}
	var specs []spec
	choices := []*float64{nil, fp(0), fp(2), fp(3), fp(100)}
	for _, keys := range [][]string{{"pages"}, {"percent"}} {
		for _, total := range []*float64{nil, fp(3), fp(0), fp(-1)} {
			for _, a := range choices {
				for _, b := range choices {
					specs = append(specs, spec{"book", "book", keys, total, map[string]*float64{"pages": a, "percent": b}})
				}
			}
		}
	}
	vals3 := choices[:4]
	for _, keys := range [][]string{{"episode"}, {"season", "episode"}, {"episode", "position"}} {
		for _, total := range []*float64{nil, fp(3), fp(0), fp(-1)} {
			for _, a := range vals3 {
				for _, b := range vals3 {
					for _, c := range vals3 {
						specs = append(specs, spec{"tv", "show", keys, total, map[string]*float64{"season": a, "episode": b, "position": c}})
					}
				}
			}
		}
	}
	type fixture struct {
		id           int64
		fin, not, in bool
	}
	fixtures := map[string]map[int64]fixture{"book": {}, "tv": {}}
	for i, s := range specs {
		d := bookData(fmt.Sprint(i))
		totalKey := "pages"
		if s.pluginID == "tv" {
			d, totalKey = tvData(fmt.Sprint(i), nil), "episodes"
		}
		id := mustSave(t, l, s.pluginID, s.kind, d, WithProgressKeys(s.keys))
		if s.total != nil {
			// direct insert: zero and negative totals bypass validation
			if _, err := e.db.Exec(`INSERT INTO item_fields(item_id,key,vtype,num) VALUES (?,?,'number',?)`, id, totalKey, *s.total); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active}); err != nil {
			t.Fatal(err)
		}
		all := map[string]float64{}
		for k, v := range s.vals {
			if v == nil {
				continue
			}
			all[k] = *v
			// direct insert: bypasses validation so inactive and out-of-range values exist
			if _, err := e.db.Exec(`INSERT INTO entry_progress(item_id,key,value) VALUES (?,?,?)`, id, k, *v); err != nil {
				t.Fatal(err)
			}
		}
		it := mustItem(t, l, id)
		def := kindOf(t, l, s.pluginID, s.kind)
		en := Entry{ItemID: id, Status: Active, Progress: all}
		f := fixture{id: id, fin: Finished(def, it, en), not: NotStarted(def, it, en)}
		f.in = !f.fin && !f.not
		if f.fin && f.not {
			t.Errorf("item %d (%s %v total %v vals %v) is both finished and not started", id, s.pluginID, s.keys, s.total, all)
		}
		fixtures[s.pluginID][id] = f
	}
	for pluginID, kind := range map[string]string{"book": "book", "tv": "show"} {
		member := map[int64]int{}
		defer func(pluginID string) {
			for id := range fixtures[pluginID] {
				if member[id] != 1 {
					t.Errorf("%s item %d is in %d progress states, want exactly 1", pluginID, id, member[id])
				}
			}
		}(pluginID)
		for _, st := range []ProgressState{ProgressNotStarted, ProgressInProgress, ProgressFinished} {
			got := map[int64]bool{}
			for page := 1; ; page++ {
				pg, err := l.List(bg, Query{PluginID: pluginID, Kind: kind, Progress: st, Page: page, PageSize: 200}, "en")
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range pg.Rows {
					got[r.Item.ID] = true
					member[r.Item.ID]++
				}
				if page >= pg.Pages {
					break
				}
			}
			n := 0
			for id, f := range fixtures[pluginID] {
				want := map[ProgressState]bool{ProgressNotStarted: f.not, ProgressInProgress: f.in, ProgressFinished: f.fin}[st]
				if want {
					n++
				}
				if got[id] != want {
					t.Errorf("%s %s item %d: sql=%v helper=%v", pluginID, st, id, got[id], want)
				}
			}
			if n == 0 || n == len(fixtures[pluginID]) {
				t.Errorf("%s %s: matrix is degenerate (%d of %d)", pluginID, st, n, len(fixtures[pluginID]))
			}
		}
		pg, err := l.List(bg, Query{PluginID: pluginID, Kind: kind, Progress: ProgressAny, PageSize: 1}, "en")
		if err != nil || pg.Total != len(fixtures[pluginID]) {
			t.Fatalf("any: %d, %v", pg.Total, err)
		}
	}
}

func TestZeroTotalIsUnbounded(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	tv := kindOf(t, l, "tv", "show")
	id := addShow(t, l, "z0", fp(0), Entry{Status: Active})
	it := mustItem(t, l, id)
	if !NotStarted(tv, it, Entry{}) || Finished(tv, it, Entry{}) {
		t.Fatal("total 0 without progress must be not started and not finished")
	}
	states := func() (n, p, f int) {
		for st, c := range map[ProgressState]*int{ProgressNotStarted: &n, ProgressInProgress: &p, ProgressFinished: &f} {
			pg := mustList(t, l, Query{PluginID: "tv", Kind: "show", Progress: st})
			*c = pg.Total
		}
		return
	}
	if n, p, f := states(); n != 1 || p != 0 || f != 0 {
		t.Fatalf("states = %d/%d/%d", n, p, f)
	}
	if err := l.UpsertEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"episode": 5}}); err != nil {
		t.Fatalf("progress above a total of 0: %v", err)
	}
	if n, p, f := states(); n != 0 || p != 1 || f != 0 {
		t.Fatalf("states = %d/%d/%d", n, p, f)
	}
	for range 3 {
		if err := l.Increment(bg, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := progressOf(t, l, id)["episode"]; got != 8 {
		t.Fatalf("episode = %v, want 8 (no cap)", got)
	}
	// a negative stored total is unbounded too
	if _, err := e.db.Exec(`UPDATE item_fields SET num = -4 WHERE item_id = ? AND key = 'episodes'`, id); err != nil {
		t.Fatal(err)
	}
	if err := l.Increment(bg, id); err != nil || progressOf(t, l, id)["episode"] != 9 {
		t.Fatalf("increment with negative total: %v", err)
	}
	it = mustItem(t, l, id)
	if err := l.ValidateEntry(tv, it, Entry{Status: Active, Progress: map[string]float64{"episode": 1000}}); err != nil {
		t.Fatal(err)
	}
}

func TestHugeTermListFailsFast(t *testing.T) {
	l := newLib(t)
	q := Query{PluginID: "tv", Kind: "show"}
	for i := 0; i < 100000; i++ {
		q.Genres = append(q.Genres, fmt.Sprint("genre-", i))
	}
	start := time.Now()
	_, err := l.List(bg, q, "en")
	var fe *FilterError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestUpdateEntrySwitchesCombinationAndWritesAtomically(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "ue", fp(10), Entry{Status: Active, Progress: map[string]float64{"episode": 2}})
	itemRow := func() string {
		var pk, upd string
		if err := e.db.QueryRow(`SELECT progress_keys, updated_at FROM items WHERE id = ?`, id).Scan(&pk, &upd); err != nil {
			t.Fatal(err)
		}
		return pk + " " + upd
	}
	// nil keys: the active combination stays and the item row is not touched
	if _, err := e.db.Exec(`UPDATE items SET updated_at = '2000-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	before := itemRow()
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Paused, Rating: ip(0), Progress: map[string]float64{"episode": 3}}, nil); err != nil {
		t.Fatal(err)
	}
	if itemRow() != before {
		t.Fatalf("item touched: %s -> %s", before, itemRow())
	}
	// the same combination in another order is no change either
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Paused, Progress: map[string]float64{"episode": 3}}, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	if itemRow() != before {
		t.Fatal("item touched by an unchanged combination")
	}
	if en := mustEntry(t, l, id); en.Status != Paused || en.Progress["episode"] != 3 {
		t.Fatalf("%+v", en)
	}
	// switch: the new keys are active and the values for them written in one go
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"episode": 4, "position": 600}}, []string{"position", "episode"}); err != nil {
		t.Fatal(err)
	}
	if it := mustItem(t, l, id); !slices.Equal(it.ProgressKeys, []string{"position", "episode"}) {
		t.Fatalf("keys %v", it.ProgressKeys)
	}
	if got := progressOf(t, l, id); got["episode"] != 4 || got["position"] != 600 {
		t.Fatalf("progress %v", got)
	}
	// back: the inactive value is retained
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"episode": 5}}, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ? AND key = 'position' AND value = 600`, id); n != 1 {
		t.Fatal("position not retained")
	}
}

// TestUpdateEntryRecordsChanges: the old entry is the baseline, so a track
// that only becomes active counts from 0, and a status change alone is one
// event.
func TestUpdateEntryRecordsChanges(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "ev-ue", fp(12), Entry{Status: Active, Progress: map[string]float64{"episode": 2}})
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"episode": 4, "position": 600}},
		[]string{"position", "episode"}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id), []event{
		{Type: "progress", Key: "episode", OldNum: fp(2), NewNum: fp(4), Origin: OriginManual},
		{Type: "progress", Key: "position", OldNum: fp(0), NewNum: fp(600), Origin: OriginManual},
	})
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Completed, Progress: map[string]float64{"episode": 4, "position": 600}}, nil); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id)[2:], []event{
		{Type: "status", OldText: "active", NewText: "completed", Origin: OriginManual},
	})
}

// TestUpdateEntryRetainedValueIsTheEventBaseline: the baseline is read after
// the combination switch, so re-activating a key whose stored value is saved
// unchanged logs nothing, and a real change logs the stored old value.
func TestUpdateEntryRetainedValueIsTheEventBaseline(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-rt", fp(10), Entry{Status: Active, Progress: map[string]float64{"pages": 5}})
	// pages=5 is retained while percent is the active track
	if err := l.SetActiveProgress(bg, id, []string{"percent"}); err != nil {
		t.Fatal(err)
	}
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 5}}, []string{"pages"}); err != nil {
		t.Fatal(err)
	}
	if got := readEvents(t, l, id); len(got) != 0 {
		t.Fatalf("re-saving the retained value wrote events: %s", showEvents(got))
	}
	if got := progressOf(t, l, id); got["pages"] != 5 {
		t.Fatalf("progress = %v", got)
	}
	// the same baseline makes a real change start at the stored value
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"pages": 6}}, nil); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id), []event{
		{Type: "progress", Key: "pages", OldNum: fp(5), NewNum: fp(6), Origin: OriginManual},
	})
}

func TestUpdateEntryRollsBackOnError(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "ur", fp(10), Entry{Status: Active, Progress: map[string]float64{"episode": 2}, Comment: "old"})
	snapshot := func() string {
		var pk string
		if err := e.db.QueryRow(`SELECT progress_keys || updated_at FROM items WHERE id = ?`, id).Scan(&pk); err != nil {
			t.Fatal(err)
		}
		en := mustEntry(t, l, id)
		return fmt.Sprintf("%s %v %s %v %d", pk, en.Status, en.Comment, en.Progress,
			count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, id))
	}
	before := snapshot()
	for name, c := range map[string]struct {
		e    Entry
		keys []string
		want string
	}{
		"above the new total":             {Entry{Status: Completed, Comment: "new", Progress: map[string]float64{"season": 1, "episode": 11}}, []string{"season", "episode"}, "above the total"},
		"key outside the new combination": {Entry{Status: Completed, Comment: "new", Progress: map[string]float64{"position": 5}}, []string{"season", "episode"}, "not active"},
		"ineligible combination":          {Entry{Status: Completed, Comment: "new"}, []string{"season", "position"}, "combination"},
		"empty combination":               {Entry{Status: Completed, Comment: "new"}, []string{}, "combination"},
		"bad status":                      {Entry{Status: "watching", Comment: "new"}, []string{"season", "episode"}, "status"},
		"finish before start": {Entry{Status: Completed, StartedOn: tp(day(2024, 2, 1)), FinishedOn: tp(day(2024, 1, 1))},
			[]string{"season", "episode"}, "before the start"},
	} {
		c.e.ItemID = id
		err := l.UpdateEntry(bg, c.e, c.keys)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", name, err)
		}
		if got := snapshot(); got != before {
			t.Fatalf("%s: changed\n%s\n%s", name, before, got)
		}
	}
	// no item, no entry
	if err := l.UpdateEntry(bg, Entry{ItemID: 9999, Status: Active}, nil); err != ErrNoEntry {
		t.Fatalf("unknown item: %v", err)
	}
	bare := mustSave(t, l, "tv", "show", tvData("bare", nil))
	if err := l.UpdateEntry(bg, Entry{ItemID: bare, Status: Active}, []string{"episode"}); err != ErrNoEntry {
		t.Fatalf("no entry: %v", err)
	}
	if en, _ := l.Entry(bg, bare); en != nil {
		t.Fatal("entry created")
	}
}

func TestItemPluginID(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "pi", nil, Entry{Status: Active})
	if p, err := e.lib.ItemPluginID(bg, id); err != nil || p != "tv" {
		t.Fatalf("%q %v", p, err)
	}
	bare := mustSave(t, e.lib, "book", "book", bookWith("pb", nil))
	if p, err := e.lib.ItemPluginID(bg, bare); err != nil || p != "book" {
		t.Fatalf("no entry: %q %v", p, err)
	}
	if _, err := e.lib.ItemPluginID(bg, 9999); err != ErrNoEntry {
		t.Fatalf("unknown: %v", err)
	}
}

func tp(t time.Time) *time.Time { return &t }

func TestUpdateEntryZeroClearsOnlyGivenKeys(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "uz", nil, Entry{Status: Active, Progress: map[string]float64{"episode": 7, "position": 600}}, WithProgressKeys([]string{"episode", "position"}))
	if err := l.SetActiveProgress(bg, id, []string{"season", "episode"}); err != nil {
		t.Fatal(err)
	}
	if err := l.UpdateEntry(bg, Entry{ItemID: id, Status: Active, Progress: map[string]float64{"season": 3, "episode": 0}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := progressOf(t, l, id); got["episode"] != 0 || got["season"] != 3 || len(got) != 2 {
		t.Fatalf("progress %v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ? AND key = 'position' AND value = 600`, id); n != 1 {
		t.Fatal("retained position touched")
	}
}

func TestStoredProgressIncludesRetained(t *testing.T) {
	e := newEnv(t)
	id := addShow(t, e.lib, "sp", nil, Entry{Status: Active, Progress: map[string]float64{"episode": 7, "position": 600}}, WithProgressKeys([]string{"episode", "position"}))
	if err := e.lib.SetActiveProgress(bg, id, []string{"episode"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.StoredProgress(bg, id)
	if err != nil || got["episode"] != 7 || got["position"] != 600 || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := e.lib.StoredProgress(bg, 9999); err != nil || len(got) != 0 {
		t.Fatalf("unknown: %v %v", got, err)
	}
}

func TestCombinationIndex(t *testing.T) {
	e := newEnv(t)
	k := kindOf(t, e.lib, "tv", "show")
	for _, c := range []struct {
		keys []string
		want int
	}{
		{[]string{"episode"}, 0}, {[]string{"episode", "season"}, 1}, {[]string{"position", "episode"}, 2},
		{[]string{"episode", "episode"}, -1}, {[]string{"season"}, -1}, {nil, -1}, {[]string{}, -1}, {[]string{"season", "episode", "position"}, -1},
	} {
		if got := CombinationIndex(k, c.keys); got != c.want {
			t.Errorf("%v: %d, want %d", c.keys, got, c.want)
		}
	}
}

func TestIncrementRecordsProgressAndStatusEvents(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-p1", nil, Entry{Status: Planned})
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id), []event{
		{Type: "progress", Key: "pages", OldNum: fp(0), NewNum: fp(1), Origin: OriginManual},
		{Type: "status", OldText: "planned", NewText: "active", Origin: OriginManual},
	})
	// the second +1 moves only the progress
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, readEvents(t, l, id)[2:], []event{
		{Type: "progress", Key: "pages", OldNum: fp(1), NewNum: fp(2), Origin: OriginManual},
	})
}

func TestIncrementAtTotalWritesNoEvent(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addBook(t, l, "ev-p2", fp(2), Entry{Status: Active, Progress: map[string]float64{"pages": 2}})
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := readEvents(t, l, id); len(got) != 0 {
		t.Fatalf("a capped +1 wrote events: %s", showEvents(got))
	}
	// a no-op because the primary track is inactive writes none either
	inactive := addBook(t, l, "ev-p3", fp(2), Entry{Status: Active}, WithProgressKeys([]string{"percent"}))
	if err := l.Increment(bg, inactive); err != nil {
		t.Fatal(err)
	}
	if got := readEvents(t, l, inactive); len(got) != 0 {
		t.Fatalf("an inactive +1 wrote events: %s", showEvents(got))
	}
	// RemoveEntry and ClearProgress are not activity
	if err := l.ClearProgress(bg, id, "pages"); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveEntry(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := readEvents(t, l, id); len(got) != 0 {
		t.Fatalf("clear/remove wrote events: %s", showEvents(got))
	}
}

func TestIncrementResetsResetOnPrimaryTrack(t *testing.T) {
	e := newEnv(t)
	l := e.lib
	id := addShow(t, l, "r1", fp(10), Entry{Status: Active, Progress: map[string]float64{"episode": 5, "position": 1200}},
		WithProgressKeys([]string{"episode", "position"}))
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	got := progressOf(t, l, id)
	if got["episode"] != 6 {
		t.Fatalf("episode = %v", got)
	}
	if _, ok := got["position"]; ok {
		t.Fatalf("position was not reset: %v", got)
	}
	// a capped +1 does not reset the position
	id = addShow(t, l, "r2", fp(2), Entry{Status: Active, Progress: map[string]float64{"episode": 2, "position": 300}},
		WithProgressKeys([]string{"episode", "position"}))
	if err := l.Increment(bg, id); err != nil {
		t.Fatal(err)
	}
	got = progressOf(t, l, id)
	if got["episode"] != 2 || got["position"] != 300 {
		t.Fatalf("capped increment = %v", got)
	}
}
