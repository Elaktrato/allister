package library

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func mustCustomList(t *testing.T, l *Library, name string) List {
	t.Helper()
	ls, err := l.CreateList(bg, name)
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func addToList(t *testing.T, l *Library, listID int64, items ...int64) {
	t.Helper()
	for _, id := range items {
		if err := l.AddToCustomList(bg, listID, id); err != nil {
			t.Fatal(err)
		}
	}
}

// listTitles are the titles of the list's rows in the order of sort.
func listTitles(t *testing.T, l *Library, listID int64, sort string) []string {
	t.Helper()
	rows, err := l.ListRows(bg, listID, sort, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func listNames(t *testing.T, l *Library) []string {
	t.Helper()
	ls, err := l.Lists(bg)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range ls {
		out = append(out, x.Name)
	}
	return out
}

func titled(d plugin.ItemData, title string) plugin.ItemData {
	d.Titles = []plugin.Title{{Lang: "en", Text: title}}
	return d
}

func addTitledBook(t *testing.T, l *Library, id, title string, e Entry) int64 {
	t.Helper()
	itemID, err := l.AddToList(bg, "book", "book", titled(bookData(id), title), e)
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

func addTitledShow(t *testing.T, l *Library, id, title string, e Entry) int64 {
	t.Helper()
	itemID, err := l.AddToList(bg, "tv", "show", titled(tvData(id, nil), title), e)
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

// Names differing only by case or Unicode folding are one list name, stored
// as first typed (Review Focus 5); renaming onto another list's name is
// refused, onto its own name in another case is allowed.
func TestListNamesFoldAndStayUnique(t *testing.T) {
	e := newEnv(t)
	c := mustCustomList(t, e.lib, "  Comfort ")
	if c.Name != "Comfort" || c.ManualOrder || c.Items != 0 {
		t.Fatalf("created %+v", c)
	}
	for _, dup := range []string{"comfort", "COMFORT"} {
		if _, err := e.lib.CreateList(bg, dup); !errors.Is(err, ErrListExists) {
			t.Fatalf("CreateList(%q) = %v, want ErrListExists", dup, err)
		}
	}
	a := mustCustomList(t, e.lib, "Ä")
	if _, err := e.lib.CreateList(bg, "ä"); !errors.Is(err, ErrListExists) {
		t.Fatalf("CreateList(ä) = %v, want ErrListExists", err)
	}
	if err := e.lib.RenameList(bg, a.ID, "comfort"); !errors.Is(err, ErrListExists) {
		t.Fatalf("RenameList onto another = %v, want ErrListExists", err)
	}
	if err := e.lib.RenameList(bg, a.ID, "ä"); err != nil {
		t.Fatalf("RenameList to own name = %v", err)
	}
	if got, want := listNames(t, e.lib), []string{"Comfort", "ä"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lists = %q, want %q", got, want)
	}
	for _, bad := range []string{"", "   ", "a\x01b", "line\nbreak", strings.Repeat("x", MaxListNameRunes+1)} {
		if _, err := e.lib.CreateList(bg, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateList(%q) = %v, want ErrInvalid", bad, err)
		}
		if err := e.lib.RenameList(bg, a.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("RenameList(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := e.lib.CreateList(bg, strings.Repeat("ü", MaxListNameRunes)); err != nil {
		t.Fatalf("a name of %d runes: %v", MaxListNameRunes, err)
	}
	if err := e.lib.RenameList(bg, 999, "x"); !errors.Is(err, ErrNoList) {
		t.Fatalf("RenameList(unknown) = %v, want ErrNoList", err)
	}
	if err := e.lib.DeleteList(bg, 999); !errors.Is(err, ErrNoList) {
		t.Fatalf("DeleteList(unknown) = %v, want ErrNoList", err)
	}
}

// A list mixes plugins; adding is idempotent; the progress counts are the
// display status completed; the item knows its lists.
func TestListMixedPluginsAndCounts(t *testing.T) {
	e := newEnv(t)
	ls := mustCustomList(t, e.lib, "Franchise")
	other := mustCustomList(t, e.lib, "Other")
	b := addTitledBook(t, e.lib, "l-1", "Book One", Entry{Status: Completed})
	s := addTitledShow(t, e.lib, "l-2", "Show Two", Entry{Status: Active})
	p := addTitledBook(t, e.lib, "l-3", "Book Three", Entry{Status: Planned})
	addToList(t, e.lib, ls.ID, b, s, p, b, s)
	addToList(t, e.lib, other.ID, s)
	if err := e.lib.SetListOrder(bg, ls.ID, true); err != nil {
		t.Fatal(err)
	}
	rows, err := e.lib.ListRows(bg, ls.ID, "position", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Item.PluginID+":"+r.Title+":"+string(r.Entry.Status))
	}
	if want := []string{"book:Book One:completed", "tv:Show Two:active", "book:Book Three:planned"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	all, err := e.lib.Lists(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Items != 3 || all[0].Done != 1 || all[1].Items != 1 || all[1].Done != 0 {
		t.Fatalf("Lists = %+v", all)
	}
	// Done follows the display run's status
	setDisplayStatus(t, e.db, p, Completed)
	got1, err := e.lib.CustomList(bg, ls.ID)
	if err != nil || got1.Done != 2 || got1.Items != 3 {
		t.Fatalf("CustomList = %+v, %v", got1, err)
	}
	of, err := e.lib.ListsOfItem(bg, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(of) != 2 || of[0].Name != "Franchise" || of[1].Name != "Other" {
		t.Fatalf("ListsOfItem = %+v", of)
	}
	if err := e.lib.AddToCustomList(bg, 999, b); !errors.Is(err, ErrNoList) {
		t.Fatalf("AddToCustomList(unknown list) = %v", err)
	}
	if err := e.lib.AddToCustomList(bg, ls.ID, 999); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("AddToCustomList(unknown item) = %v", err)
	}
	if _, err := e.lib.CustomList(bg, 999); !errors.Is(err, ErrNoList) {
		t.Fatalf("CustomList(unknown) = %v", err)
	}
}

// Manual order moves like series parts; positions stay dense after a
// removal; moving is refused while the list is sorted.
func TestListManualMove(t *testing.T) {
	e := newEnv(t)
	ls := mustCustomList(t, e.lib, "Order")
	a := addTitledBook(t, e.lib, "m-1", "A", Entry{Status: Planned})
	b := addTitledShow(t, e.lib, "m-2", "B", Entry{Status: Planned})
	c := addTitledBook(t, e.lib, "m-3", "C", Entry{Status: Planned})
	d := addTitledBook(t, e.lib, "m-4", "D", Entry{Status: Planned})
	addToList(t, e.lib, ls.ID, a, b, c, d)
	if err := e.lib.MoveInList(bg, ls.ID, c, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move in a sorted list = %v, want ErrInvalid", err)
	}
	if err := e.lib.SetListOrder(bg, ls.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MoveInList(bg, ls.ID, c, -1); err != nil {
		t.Fatal(err)
	}
	if got, want := listTitles(t, e.lib, ls.ID, "position"), []string{"A", "C", "B", "D"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after up = %q, want %q", got, want)
	}
	if err := e.lib.RemoveFromCustomList(bg, ls.ID, a); err != nil {
		t.Fatal(err)
	}
	// past the top is a no-op; positions are dense again
	if err := e.lib.MoveInList(bg, ls.ID, c, -1); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MoveInList(bg, ls.ID, b, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.MoveInList(bg, ls.ID, b, 1); err != nil {
		t.Fatal(err)
	}
	if got, want := listTitles(t, e.lib, ls.ID, "position"), []string{"C", "D", "B"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after moves = %q, want %q", got, want)
	}
	if n := count(t, e.db, `SELECT count(*) FROM list_items WHERE list_id = ? AND position BETWEEN 1 AND 3`, ls.ID); n != 3 {
		t.Fatalf("dense positions: %d of 3", n)
	}
	if err := e.lib.MoveInList(bg, ls.ID, a, 1); !errors.Is(err, ErrNoList) {
		t.Fatalf("move a non-member = %v, want ErrNoList", err)
	}
	if err := e.lib.MoveInList(bg, ls.ID, b, 2); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move by 2 = %v, want ErrInvalid", err)
	}
	if err := e.lib.SetListOrder(bg, 999, true); !errors.Is(err, ErrNoList) {
		t.Fatalf("SetListOrder(unknown) = %v", err)
	}
}

func TestListSortModes(t *testing.T) {
	e := newEnv(t)
	ls := mustCustomList(t, e.lib, "Sorted")
	z := addTitledBook(t, e.lib, "s-1", "zebra", Entry{Status: Completed})
	a := addTitledShow(t, e.lib, "s-2", "Apple", Entry{Status: Dropped})
	m := addTitledBook(t, e.lib, "s-3", "mango", Entry{Status: Planned})
	k := addTitledShow(t, e.lib, "s-4", "Kiwi", Entry{Status: Active})
	addToList(t, e.lib, ls.ID, z, a, m, k)
	if _, err := e.lib.db.Exec(`UPDATE list_items SET added_at = CASE item_id WHEN ? THEN '2026-01-03T00:00:00Z' WHEN ? THEN '2026-01-01T00:00:00Z' ELSE '2026-01-02T00:00:00Z' END`, z, a); err != nil {
		t.Fatal(err)
	}
	for sort, want := range map[string][]string{
		"title": {"Apple", "Kiwi", "mango", "zebra"},
		"":      {"Apple", "Kiwi", "mango", "zebra"}, // a sorted list defaults to title
		// newest first; the same time falls back to the later place first
		"added":  {"zebra", "Kiwi", "mango", "Apple"},
		"status": {"mango", "Kiwi", "zebra", "Apple"},
	} {
		if got := listTitles(t, e.lib, ls.ID, sort); !reflect.DeepEqual(got, want) {
			t.Errorf("sort %q = %q, want %q", sort, got, want)
		}
	}
	if _, err := e.lib.ListRows(bg, ls.ID, "position", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("position on a sorted list = %v, want ErrInvalid", err)
	}
	if _, err := e.lib.ListRows(bg, ls.ID, "rating", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown sort = %v, want ErrInvalid", err)
	}
	if err := e.lib.SetListOrder(bg, ls.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, want := listTitles(t, e.lib, ls.ID, ""), []string{"zebra", "Apple", "mango", "Kiwi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manual default = %q, want %q", got, want)
	}
	// title languages per plugin
	rows, err := e.lib.ListRows(bg, ls.ID, "title", map[string]string{"book": "en"})
	if err != nil || len(rows) != 4 {
		t.Fatalf("ListRows with langs = %v, %v", rows, err)
	}
}

// Removing an entry from the library takes it out of every list; deleting a
// list keeps its entries.
func TestListMembershipFollowsEntries(t *testing.T) {
	e := newEnv(t)
	one := mustCustomList(t, e.lib, "One")
	two := mustCustomList(t, e.lib, "Two")
	a := addTitledBook(t, e.lib, "d-1", "A", Entry{Status: Planned})
	b := addTitledShow(t, e.lib, "d-2", "B", Entry{Status: Planned})
	addToList(t, e.lib, one.ID, a, b)
	addToList(t, e.lib, two.ID, a)
	if err := e.lib.RemoveEntry(bg, a); err != nil {
		t.Fatal(err)
	}
	if got := listTitles(t, e.lib, one.ID, "title"); !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("after removal = %q", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM list_items WHERE item_id = ?`, a); n != 0 {
		t.Fatalf("list places of the removed entry: %d", n)
	}
	if err := e.lib.DeleteItem(bg, b); err != nil {
		t.Fatal(err)
	}
	c := addTitledBook(t, e.lib, "d-3", "C", Entry{Status: Planned})
	addToList(t, e.lib, two.ID, c)
	if err := e.lib.DeleteList(bg, two.ID); err != nil {
		t.Fatal(err)
	}
	if mustEntry(t, e.lib, c) == nil {
		t.Fatal("deleting a list removed its entry")
	}
	if got := listNames(t, e.lib); !reflect.DeepEqual(got, []string{"One"}) {
		t.Fatalf("lists = %q", got)
	}
}

// Membership of one item is saved at once, for the shown lists only: a
// shown list ticked is joined (appended), a shown list unticked is left,
// every list not shown keeps its membership, and an unknown shown id is
// ignored.
func TestSetListsOfItem(t *testing.T) {
	e := newEnv(t)
	one := mustCustomList(t, e.lib, "One")
	two := mustCustomList(t, e.lib, "Two")
	three := mustCustomList(t, e.lib, "Three")
	four := mustCustomList(t, e.lib, "Four")
	a := addTitledBook(t, e.lib, "p-1", "A", Entry{Status: Planned})
	b := addTitledBook(t, e.lib, "p-2", "B", Entry{Status: Planned})
	addToList(t, e.lib, two.ID, b)
	addToList(t, e.lib, one.ID, a)
	addToList(t, e.lib, four.ID, a) // joined elsewhere, not on the form
	shown := []int64{one.ID, two.ID, three.ID}
	if err := e.lib.SetListsOfItem(bg, a, shown, []int64{two.ID, three.ID, two.ID}); err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		of, err := e.lib.ListsOfItem(bg, a)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, x := range of {
			names = append(names, x.Name)
		}
		return names
	}
	if got := names(); !reflect.DeepEqual(got, []string{"Four", "Three", "Two"}) {
		t.Fatalf("lists of item = %q", got)
	}
	if got := listTitles(t, e.lib, two.ID, ""); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("two = %q", got)
	}
	if err := e.lib.SetListOrder(bg, two.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := listTitles(t, e.lib, two.ID, "position"); !reflect.DeepEqual(got, []string{"B", "A"}) {
		t.Fatalf("two in manual order = %q (a joined list appends)", got)
	}
	// a shown list deleted meanwhile is ignored, the rest is saved
	if err := e.lib.SetListsOfItem(bg, a, []int64{one.ID, 999}, []int64{one.ID, 999}); err != nil {
		t.Fatalf("unknown shown list = %v, want it ignored", err)
	}
	if got := names(); !reflect.DeepEqual(got, []string{"Four", "One", "Three", "Two"}) {
		t.Fatalf("after an unknown list: %q", got)
	}
	if err := e.lib.SetListsOfItem(bg, 999, nil, nil); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown item = %v, want ErrNoEntry", err)
	}
	if err := e.lib.SetListsOfItem(bg, a, shown, nil); err != nil {
		t.Fatal(err)
	}
	if got := names(); !reflect.DeepEqual(got, []string{"Four"}) {
		t.Fatalf("cleared shown lists: %q", got)
	}
}

// A disabled plugin's entries are hidden from the rows and the counts, but
// stay in the list.
func TestListHidesDisabledPlugins(t *testing.T) {
	e := newEnv(t)
	ls := mustCustomList(t, e.lib, "Mixed")
	b := addTitledBook(t, e.lib, "h-1", "Book", Entry{Status: Completed})
	s := addTitledShow(t, e.lib, "h-2", "Show", Entry{Status: Completed})
	addToList(t, e.lib, ls.ID, b, s)
	if err := e.reg.SetEnabled(bg, "tv", false); err != nil {
		t.Fatal(err)
	}
	if got := listTitles(t, e.lib, ls.ID, "title"); !reflect.DeepEqual(got, []string{"Book"}) {
		t.Fatalf("rows = %q", got)
	}
	if got, _ := e.lib.CustomList(bg, ls.ID); got.Items != 1 || got.Done != 1 {
		t.Fatalf("counts = %+v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM list_items WHERE list_id = ?`, ls.ID); n != 2 {
		t.Fatalf("stored members = %d, want 2", n)
	}
	if err := e.reg.SetEnabled(bg, "tv", true); err != nil {
		t.Fatal(err)
	}
	if got := listTitles(t, e.lib, ls.ID, "title"); !reflect.DeepEqual(got, []string{"Book", "Show"}) {
		t.Fatalf("rows after enabling = %q", got)
	}
}

// SearchLibrary finds entries of every enabled plugin by title.
func TestSearchLibrary(t *testing.T) {
	e := newEnv(t)
	addTitledBook(t, e.lib, "q-1", "Kingdom Book", Entry{Status: Planned})
	addTitledShow(t, e.lib, "q-2", "kingdom show", Entry{Status: Planned})
	addTitledBook(t, e.lib, "q-3", "Other", Entry{Status: Planned})
	mustSave(t, e.lib, "book", "book", titled(bookData("q-4"), "Kingdom unsaved")) // no entry
	got := func(text string, limit int) []string {
		t.Helper()
		rows, err := e.lib.SearchLibrary(bg, text, nil, limit)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	if g := got("KINGDOM", 10); !reflect.DeepEqual(g, []string{"Kingdom Book", "kingdom show"}) {
		t.Fatalf("search = %q", g)
	}
	if g := got("kingdom", 1); len(g) != 1 {
		t.Fatalf("limit 1 = %q", g)
	}
	if g := got("  ", 10); len(g) != 0 {
		t.Fatalf("blank = %q", g)
	}
	if g := got("100%", 10); len(g) != 0 {
		t.Fatalf("literal percent = %q", g)
	}
	if err := e.reg.SetEnabled(bg, "tv", false); err != nil {
		t.Fatal(err)
	}
	if g := got("kingdom", 10); !reflect.DeepEqual(g, []string{"Kingdom Book"}) {
		t.Fatalf("with tv disabled = %q", g)
	}
}

// MergeCandidates searches like SearchLibrary within one plugin and kind,
// without the given item, so other plugins' matches never crowd it out.
func TestMergeCandidates(t *testing.T) {
	e := newEnv(t)
	keep := addTitledBook(t, e.lib, "k", "Kingdom", Entry{Status: Planned})
	other := addTitledBook(t, e.lib, "o", "The Kingdom", Entry{Status: Planned})
	mustSave(t, e.lib, "book", "book", titled(bookData("u"), "Kingdom unsaved")) // no entry
	for i := range 210 {
		addTitledShow(t, e.lib, fmt.Sprintf("s%d", i), fmt.Sprintf("A kingdom show %d", i), Entry{Status: Planned})
	}
	rows, err := e.lib.MergeCandidates(bg, "book", "book", keep, "kingdom", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Item.ID != other {
		t.Fatalf("candidates = %+v, want only %d", rows, other)
	}
	if rows, err = e.lib.MergeCandidates(bg, "book", "show", keep, "kingdom", nil, 20); err != nil || len(rows) != 0 {
		t.Fatalf("other kind: %d rows, %v", len(rows), err)
	}
}
