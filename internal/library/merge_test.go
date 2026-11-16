package library

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// mergeBook is a book of the "book" plugin with the given source identity,
// title and external ids, added to the library with notes when inLib.
func mergeBook(t *testing.T, e *env, src, id, title string, ext map[string]string, inLib bool, notes string) int64 {
	t.Helper()
	d := dupBook(src, id, title, ext)
	if !inLib {
		return mustSave(t, e.lib, "book", "book", d)
	}
	itemID, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Planned, Notes: notes})
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

// dbDump renders every row of every table, for "nothing changed" checks.
func dbDump(t *testing.T, d *sql.DB) string {
	t.Helper()
	rows, err := d.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if err := closeRows(rows); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, tb := range tables {
		rows, err := d.Query(`SELECT * FROM "` + tb + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", tb, vals)
		}
		if err := closeRows(rows); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

func mergeIDs(t *testing.T, d *sql.DB, q string, args ...any) []int64 {
	t.Helper()
	rows, err := d.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := closeRows(rows); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestMergeCombinesRunsNotesEventsAndKeepsOneOpenRun(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "mine")
	other := mergeBook(t, e, "books", "o", "Dune!", nil, true, "theirs")
	mustExec(t, e.db, `INSERT INTO item_fields(item_id, key, vtype, num, seq) VALUES (?, 'pages', 'number', 100, 0)`, keep)
	k1 := displayRunOf(t, e, keep)
	setRun(t, e, k1, Completed, 8, "2024-01-10", "2024-01-20", map[string]float64{"pages": 100})
	k2 := seedRun(t, e, keep, 2, Active, nil, "2024-06-01", nil, map[string]float64{"pages": 10})
	o1 := displayRunOf(t, e, other)
	setRun(t, e, o1, Paused, nil, "2024-03-01", nil, map[string]float64{"pages": 300, "percent": 40})
	o2 := seedRun(t, e, other, 2, Planned, nil, nil, nil, nil) // created 2024-01-01, no start
	if err := e.lib.SetUnitNote(bg, o1, "pages", 5, "a good page"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, `INSERT INTO entry_events(item_id, occurred_at, event_type, progress_key, old_num, new_num, origin, run_id)
		VALUES (?, '2024-03-05T10:00:00Z', 'progress', 'pages', 0, 300, 'manual', ?)`, other, o1)
	mustExec(t, e.db, `INSERT INTO entry_events(item_id, occurred_at, event_type, old_text, new_text, origin, run_id)
		VALUES (?, '2023-05-05T10:00:00Z', 'status', 'active', 'completed', 'manual', NULL)`, other)
	from, to := day(2000, 1, 1), day(2100, 1, 1)
	before, err := e.lib.ActivityDays(bg, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := count(t, e.db, `SELECT count(*) FROM entry_events`)

	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}

	if it, err := e.lib.Item(bg, other); err != nil || it != nil {
		t.Fatalf("other item = %v, %v; want deleted", it, err)
	}
	runs := runsOf(t, e, keep)
	var gotIDs []int64
	var gotStatus []string
	for i, r := range runs {
		if r.Number != i+1 {
			t.Fatalf("run %d has number %d, want dense numbers: %+v", r.ID, r.Number, runs)
		}
		gotIDs = append(gotIDs, r.ID)
		gotStatus = append(gotStatus, r.Status)
	}
	if want := []int64{o2, k1, o1, k2}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("run order = %v, want %v", gotIDs, want)
	}
	if want := []string{"dropped", "completed", "dropped", "active"}; !reflect.DeepEqual(gotStatus, want) {
		t.Fatalf("run statuses = %v, want %v (one open run at most)", gotStatus, want)
	}
	if got := runs[2].Progress; !reflect.DeepEqual(got, map[string]float64{"pages": 300, "percent": 40}) {
		t.Fatalf("moved progress = %v, want pages 300 above keep's total and the inactive percent", got)
	}
	if got := displayRunOf(t, e, keep); got != k2 {
		t.Fatalf("display run = %d, want %d", got, k2)
	}
	notes, err := e.lib.UnitNotes(bg, o1)
	if err != nil || len(notes) != 1 || notes[0].Body != "a good page" {
		t.Fatalf("unit notes = %+v, %v", notes, err)
	}
	if got := count(t, e.db, `SELECT count(*) FROM entry_events`); got != events {
		t.Fatalf("events = %d, want %d (no new events, none lost)", got, events)
	}
	if got := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ? AND run_id = ?`, keep, o1); got != 1 {
		t.Fatalf("events of the moved run = %d, want 1", got)
	}
	if got := count(t, e.db, `SELECT count(*) FROM entry_events WHERE item_id = ? AND run_id IS NULL`, keep); got != 1 {
		t.Fatalf("orphan events on keep = %d, want 1", got)
	}
	after, err := e.lib.ActivityDays(bg, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("activity days changed: %v -> %v", before, after)
	}
	ent, err := e.lib.Entry(bg, keep)
	if err != nil {
		t.Fatal(err)
	}
	if ent.Notes != "mine\n\n---\n\ntheirs" {
		t.Fatalf("notes = %q", ent.Notes)
	}
}

func TestMergeNotesWithoutDividerWhenOneSideIsEmpty(t *testing.T) {
	for _, c := range []struct{ keep, other, want string }{{"", "theirs", "theirs"}, {"mine", "", "mine"}} {
		e := newEnv(t)
		keep := mergeBook(t, e, "books", "k", "Dune", nil, true, c.keep)
		other := mergeBook(t, e, "books", "o", "Dune", nil, true, c.other)
		if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
			t.Fatal(err)
		}
		ent, err := e.lib.Entry(bg, keep)
		if err != nil {
			t.Fatal(err)
		}
		if ent.Notes != c.want {
			t.Fatalf("notes %q + %q = %q, want %q", c.keep, c.other, ent.Notes, c.want)
		}
	}
}

func TestMergeGivesKeepTheOtherEntryWhenOnlyOtherIsInTheLibrary(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, false, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "theirs")
	o1 := displayRunOf(t, e, other)
	setRun(t, e, o1, Active, 7, "2024-02-02", nil, map[string]float64{"pages": 42})
	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}
	ent, err := e.lib.Entry(bg, keep)
	if err != nil || ent == nil {
		t.Fatalf("entry = %v, %v", ent, err)
	}
	if ent.Notes != "theirs" || ent.Status != Active || ent.RunID != o1 || ent.Progress["pages"] != 42 {
		t.Fatalf("entry = %+v", ent)
	}
}

func TestMergeUnitesTagsListsAndKeepsListPlaces(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
	x := mergeBook(t, e, "books", "x", "X", nil, true, "")
	y := mergeBook(t, e, "books", "y", "Y", nil, true, "")
	if err := e.lib.SetEntryTags(bg, keep, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetEntryTags(bg, other, []string{"alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	both, err := e.lib.CreateList(bg, "Both")
	if err != nil {
		t.Fatal(err)
	}
	only, err := e.lib.CreateList(bg, "Only other")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int64{{both.ID, x}, {both.ID, other}, {both.ID, keep}, {only.ID, other}, {only.ID, y}} {
		if err := e.lib.AddToCustomList(bg, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}
	tags, err := e.lib.EntryTags(bg, []int64{keep})
	if err != nil {
		t.Fatal(err)
	}
	if got := tags[keep]; !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("tags = %v", got)
	}
	order := func(listID int64) []int64 {
		return mergeIDs(t, e.db, `SELECT item_id FROM list_items WHERE list_id = ? ORDER BY position`, listID)
	}
	if got := order(both.ID); !reflect.DeepEqual(got, []int64{x, keep}) {
		t.Fatalf("list of both = %v, want keep's own place %v", got, []int64{x, keep})
	}
	if got := order(only.ID); !reflect.DeepEqual(got, []int64{keep, y}) {
		t.Fatalf("list of only other = %v, want the other's place %v", got, []int64{keep, y})
	}
	if got := mergeIDs(t, e.db, `SELECT position FROM list_items WHERE list_id = ? ORDER BY position`, both.ID); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("positions = %v, want dense", got)
	}
}

func TestMergeSeriesRule(t *testing.T) {
	t.Run("keep takes the other's place", func(t *testing.T) {
		e := newEnv(t)
		keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
		other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
		y := mergeBook(t, e, "books", "y", "Y", nil, true, "")
		s, err := e.lib.CreateSeries(bg, other, "Saga")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.lib.AddPart(bg, s.ID, y, ""); err != nil {
			t.Fatal(err)
		}
		if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
			t.Fatal(err)
		}
		if got := mergeIDs(t, e.db, `SELECT item_id FROM series_parts WHERE series_id = ? ORDER BY position`, s.ID); !reflect.DeepEqual(got, []int64{keep, y}) {
			t.Fatalf("parts = %v, want %v", got, []int64{keep, y})
		}
	})
	t.Run("keep stays in its own series", func(t *testing.T) {
		e := newEnv(t)
		keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
		other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
		z := mergeBook(t, e, "books", "z", "Z", nil, true, "")
		mine, err := e.lib.CreateSeries(bg, keep, "Mine")
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := e.lib.CreateSeries(bg, other, "Theirs")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.lib.AddPart(bg, theirs.ID, z, ""); err != nil {
			t.Fatal(err)
		}
		if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
			t.Fatal(err)
		}
		if got := mergeIDs(t, e.db, `SELECT item_id FROM series_parts WHERE series_id = ?`, mine.ID); !reflect.DeepEqual(got, []int64{keep}) {
			t.Fatalf("own series = %v", got)
		}
		if got := mergeIDs(t, e.db, `SELECT position FROM series_parts WHERE series_id = ? AND item_id = ?`, theirs.ID, z); !reflect.DeepEqual(got, []int64{1}) {
			t.Fatalf("other series renumbered = %v, want [1]", got)
		}
	})
}

func TestMergeAppliesSelectedChangesAndAddsExternalIDs(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", map[string]string{"isbn": "111"}, true, "")
	other := mergeBook(t, e, "books", "o", "Dune Messiah", map[string]string{"isbn": "222", "goodreads": "g1"}, true, "")
	credits := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Frank Herbert"}}}
	if err := e.lib.SaveCredits(bg, other, credits); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MergeItems(bg, keep, other, []string{"titles", "credits"}); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, e.lib, keep)
	if len(it.Titles) != 1 || it.Titles[0].Text != "Dune Messiah" {
		t.Fatalf("titles = %+v", it.Titles)
	}
	if want := map[string]string{"isbn": "111", "goodreads": "g1"}; !reflect.DeepEqual(it.ExternalIDs, want) {
		t.Fatalf("external ids = %v, want %v", it.ExternalIDs, want)
	}
	cs, err := e.lib.Credits(bg, keep)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Person.SourceID != "p1" {
		t.Fatalf("credits = %+v", cs)
	}
	if n := count(t, e.db, `SELECT count(*) FROM people`); n != 1 {
		t.Fatalf("people = %d, want the same person reused", n)
	}
}

func TestMergeAliasFindsTheSurvivor(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}
	// the importer dedupes through ItemBySource
	it, err := e.lib.ItemBySource(bg, "book", "book", "books", "o")
	if err != nil || it == nil || it.ID != keep {
		t.Fatalf("ItemBySource(other) = %v, %v; want keep %d", it, err, keep)
	}
	items, err := e.lib.ItemByPluginSource(bg, "book", "books", "o")
	if err != nil || len(items) != 1 || items[0].ID != keep {
		t.Fatalf("ItemByPluginSource(other) = %v, %v", items, err)
	}
	_, err = e.lib.AddToList(bg, "book", "book", dupBook("books", "o", "Dune", nil), Entry{Status: Planned})
	var ase *AlreadySavedError
	if !errors.As(err, &ase) || ase.ExistingID != keep {
		t.Fatalf("AddToList(other) = %v, want AlreadySavedError{%d}", err, keep)
	}
	_, err = e.lib.SaveItem(bg, "book", "book", dupBook("books", "o", "Dune", nil))
	if !errors.As(err, &ase) || ase.ExistingID != keep {
		t.Fatalf("SaveItem(other) = %v, want AlreadySavedError{%d}", err, keep)
	}
	manual := mergeBook(t, e, manualSource, "m", "Dune", nil, false, "")
	if err := e.lib.ConvertToSource(bg, manual, dupBook("books", "o", "Dune", nil)); !errors.As(err, &ase) || ase.ExistingID != keep {
		t.Fatalf("ConvertToSource(other) = %v, want AlreadySavedError{%d}", err, keep)
	}

	// a later merge carries the aliases on
	third := mergeBook(t, e, "books", "t", "Dune", nil, true, "")
	if err := e.lib.MergeItems(bg, third, keep, nil); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"o", "k"} {
		it, err := e.lib.ItemBySource(bg, "book", "book", "books", sid)
		if err != nil || it == nil || it.ID != third {
			t.Fatalf("ItemBySource(%s) = %v, %v; want %d", sid, it, err, third)
		}
	}
}

func TestMergeManualKeepTakesTheSourceLink(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, manualSource, "m1", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
	credits := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Frank Herbert"}}}
	if err := e.lib.SaveCredits(bg, other, credits); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MergeItems(bg, keep, other, []string{"credits"}); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, e.lib, keep)
	if it.Source != "books" || it.SourceID != "o" {
		t.Fatalf("keep identity = %s/%s, want books/o", it.Source, it.SourceID)
	}
	if n := count(t, e.db, `SELECT count(*) FROM item_aliases`); n != 0 {
		t.Fatalf("aliases = %d, want none", n)
	}
	if got, err := e.lib.ItemBySource(bg, "book", "book", "books", "o"); err != nil || got == nil || got.ID != keep {
		t.Fatalf("ItemBySource = %v, %v", got, err)
	}
	cs, err := e.lib.Credits(bg, keep)
	if err != nil || len(cs) != 1 || cs[0].Person.Source != "books" {
		t.Fatalf("credits = %+v, %v; want the person under the taken source", cs, err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM people`); n != 1 {
		t.Fatalf("people = %d, want 1", n)
	}
}

func TestMergeManualOtherLeavesNoAliasAndNoForeignCredits(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, manualSource, "m1", "Dune", nil, true, "")
	credits := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Frank Herbert"}}}
	if err := e.lib.SaveCredits(bg, other, credits); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MergeItems(bg, keep, other, []string{"credits"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM item_aliases`); n != 0 {
		t.Fatalf("aliases = %d, want none for a manual item", n)
	}
	it := mustItem(t, e.lib, keep)
	if it.Source != "books" || it.SourceID != "k" {
		t.Fatalf("keep identity = %s/%s", it.Source, it.SourceID)
	}
	// a manual person id means nothing to the survivor's source
	if n := count(t, e.db, `SELECT count(*) FROM credits WHERE item_id = ?`, keep); n != 0 {
		t.Fatalf("credits = %d, want none across sources", n)
	}
}

func TestMergeRefusals(t *testing.T) {
	e := newEnv(t)
	a := mergeBook(t, e, "books", "a", "Dune", nil, true, "")
	b2 := mustSave(t, e.lib, "book2", "book", dupBook("books", "b", "Dune", nil))
	show := mustSave(t, e.lib, "tv", "show", dupShow("s", "Dune", nil))
	before := dbDump(t, e.db)
	for name, pair := range map[string][2]int64{
		"same": {a, a}, "unknown keep": {999, a}, "unknown other": {a, 999},
		"other plugin": {a, b2}, "other plugin and kind": {a, show},
	} {
		if err := e.lib.MergeItems(bg, pair[0], pair[1], nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if after := dbDump(t, e.db); after != before {
		t.Fatal("a refused merge changed the database")
	}
}

func TestMergeFailureLeavesBothItemsUntouched(t *testing.T) {
	setup := func(t *testing.T) (*env, int64, int64) {
		e := newEnv(t)
		keep := mergeBook(t, e, manualSource, "m1", "Dune", map[string]string{"isbn": "1"}, true, "mine")
		other := mergeBook(t, e, "books", "o", "Dune Messiah", map[string]string{"goodreads": "g"}, true, "theirs")
		seedRun(t, e, keep, 2, Active, nil, "2024-06-01", nil, map[string]float64{"pages": 3})
		seedRun(t, e, other, 2, Active, nil, "2024-05-01", nil, map[string]float64{"pages": 4})
		if err := e.lib.SetEntryTags(bg, other, []string{"beta"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.lib.CreateSeries(bg, other, "Saga"); err != nil {
			t.Fatal(err)
		}
		mustExec(t, e.db, `INSERT INTO entry_events(item_id, occurred_at, event_type, old_text, new_text, origin)
			VALUES (?, '2023-05-05T10:00:00Z', 'status', 'active', 'completed', 'manual')`, other)
		return e, keep, other
	}
	t.Run("forced failure at the delete", func(t *testing.T) {
		e, keep, other := setup(t)
		mustExec(t, e.db, fmt.Sprintf(`CREATE TRIGGER force_fail BEFORE DELETE ON items WHEN OLD.id = %d
			BEGIN SELECT RAISE(ABORT, 'forced failure'); END`, other))
		before := dbDump(t, e.db)
		err := e.lib.MergeItems(bg, keep, other, []string{"titles"})
		if err == nil || !strings.Contains(err.Error(), "forced failure") {
			t.Fatalf("err = %v, want the forced failure", err)
		}
		if after := dbDump(t, e.db); after != before {
			t.Fatalf("a failed merge changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
	t.Run("an unknown change key, checked last", func(t *testing.T) {
		e, keep, other := setup(t)
		before := dbDump(t, e.db)
		wantInvalid(t, e.lib.MergeItems(bg, keep, other, []string{"titles", "nope"}), "nope")
		if after := dbDump(t, e.db); after != before {
			t.Fatal("a failed merge changed the database")
		}
	})
}

func TestMergeRunTieGoesToTheSurvivorFirst(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
	k1, o1 := displayRunOf(t, e, keep), displayRunOf(t, e, other)
	setRun(t, e, o1, Completed, nil, "2024-01-01", "2024-01-02", nil)
	setRun(t, e, k1, Completed, nil, "2024-01-01", "2024-01-03", nil)
	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, e, keep)
	if len(runs) != 2 || runs[0].ID != k1 || runs[1].ID != o1 {
		t.Fatalf("runs = %+v, want keep's run first on the same start day", runs)
	}
}

func TestMergeRefusesNotesAboveTheLimit(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, strings.Repeat("a", 6000))
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, strings.Repeat("b", 6000))
	before := dbDump(t, e.db)
	wantInvalid(t, e.lib.MergeItems(bg, keep, other, nil), "the merged notes would be longer than 10000 characters; shorten one of them first")
	if after := dbDump(t, e.db); after != before {
		t.Fatal("a refused merge changed the database")
	}
}

func TestMergePutsTheOpenRunLast(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune", nil, true, "")
	k1, o1 := displayRunOf(t, e, keep), displayRunOf(t, e, other)
	setRun(t, e, k1, Active, 6, "2020-03-01", nil, map[string]float64{"pages": 12})
	setRun(t, e, o1, Completed, nil, "2024-01-01", "2024-02-01", nil)
	if err := e.lib.MergeItems(bg, keep, other, nil); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, e, keep)
	if len(runs) != 2 || runs[0].ID != o1 || runs[1].ID != k1 {
		t.Fatalf("runs = %+v, want the open run last", runs)
	}
	open := runs[1]
	if open.Number != 2 || open.Status != "active" || open.Started.String != "2020-03-01" || open.Finished.Valid ||
		!reflect.DeepEqual(open.Progress, map[string]float64{"pages": 12}) || open.Rating.Int64 != 6 {
		t.Fatalf("open run = %+v", open)
	}
	if runs[0].Status != "completed" {
		t.Fatalf("closed run = %+v", runs[0])
	}
	if got := displayRunOf(t, e, keep); got != k1 {
		t.Fatalf("display run = %d, want %d", got, k1)
	}
}

func TestMergeChangesOffersWhatMergeItemsApplies(t *testing.T) {
	e := newEnv(t)
	keep := mergeBook(t, e, "books", "k", "Dune", nil, true, "")
	other := mergeBook(t, e, "books", "o", "Dune Messiah", nil, true, "")
	manual := mergeBook(t, e, manualSource, "m1", "Dune Messiah", nil, true, "")
	credits := []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Frank Herbert"}}}
	if err := e.lib.SaveCredits(bg, other, credits); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SaveCredits(bg, manual, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p2", Name: "Someone Else"}}}); err != nil {
		t.Fatal(err)
	}
	keys := func(cs []Change) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Key)
		}
		return out
	}
	before := dbDump(t, e.db)
	got, err := e.lib.MergeChanges(bg, keep, other)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"titles", "cover", "credits"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("same source: keys = %v, want %v", keys(got), want)
	}
	// a manual item's people mean nothing to the survivor's source
	if got, err = e.lib.MergeChanges(bg, keep, manual); err != nil || !reflect.DeepEqual(keys(got), []string{"titles", "cover"}) {
		t.Fatalf("manual other: keys = %v, %v", keys(got), err)
	}
	// a manual survivor takes the other's source: its credits count
	if got, err = e.lib.MergeChanges(bg, manual, other); err != nil || !slices.Contains(keys(got), "credits") {
		t.Fatalf("manual keep: keys = %v, %v", keys(got), err)
	}
	if dbDump(t, e.db) != before {
		t.Fatal("MergeChanges changed the database")
	}
	for name, pair := range map[string][2]int64{"same": {keep, keep}, "unknown": {keep, 999}} {
		if _, err := e.lib.MergeChanges(bg, pair[0], pair[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
