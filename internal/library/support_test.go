package library

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

var bookQ0 = Query{PluginID: "book", Kind: "book"}

func TestRemoveEntryDeletesEntryAndProgressOnly(t *testing.T) {
	e := newEnv(t)
	id := addBook(t, e.lib, "1", fp(300), Entry{Status: Active, Progress: map[string]float64{"pages": 12}})
	other := addBook(t, e.lib, "2", fp(300), Entry{Status: Active, Progress: map[string]float64{"pages": 5}})
	if err := e.lib.RemoveEntry(bg, id); err != nil {
		t.Fatal(err)
	}
	if en, err := e.lib.Entry(bg, id); en != nil || err != nil {
		t.Fatalf("entry remains: %+v %v", en, err)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ?`, id); n != 0 {
		t.Fatalf("progress rows remain: %d", n)
	}
	if it := mustItem(t, e.lib, id); it.SourceID != "1" {
		t.Fatal("item removed")
	}
	if en := mustEntry(t, e.lib, other); en.Progress["pages"] != 5 {
		t.Fatalf("other entry touched: %+v", en)
	}
	// idempotent: no entry, unknown item
	if err := e.lib.RemoveEntry(bg, id); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.RemoveEntry(bg, 9999); err != nil {
		t.Fatal(err)
	}
	// a new entry starts from zero progress
	if err := e.lib.UpsertEntry(bg, Entry{ItemID: id, Status: Planned}); err != nil {
		t.Fatal(err)
	}
	if en := mustEntry(t, e.lib, id); len(en.Progress) != 0 {
		t.Fatalf("old progress came back: %v", en.Progress)
	}
}

func TestRowsLimitOffsetUnpaged(t *testing.T) {
	e := newEnv(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		id := bookT(t, e.lib, fmt.Sprint(i), fmt.Sprint("T", i), nil, Entry{Status: Active})
		if _, err := e.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rowIDs := func(rs []Row) []int64 {
		var out []int64
		for _, r := range rs {
			out = append(out, r.Item.ID)
		}
		return out
	}
	all, err := e.lib.Rows(bg, bookQ0, "en", -1, 0)
	if err != nil || !reflect.DeepEqual(rowIDs(all), []int64{ids[4], ids[3], ids[2], ids[1], ids[0]}) {
		t.Fatalf("all = %v, %v", rowIDs(all), err)
	}
	if all[0].Title != "T4" || all[0].Item.Fields["author"].Text != "Frank Herbert" {
		t.Fatalf("row parts missing: %+v", all[0])
	}
	p, _ := e.lib.Rows(bg, bookQ0, "en", 2, 1)
	if !reflect.DeepEqual(rowIDs(p), []int64{ids[3], ids[2]}) {
		t.Fatalf("2@1 = %v", rowIDs(p))
	}
	if p, err := e.lib.Rows(bg, bookQ0, "en", 2, 10); err != nil || len(p) != 0 {
		t.Fatalf("beyond = %v %v", p, err)
	}
	// Page and PageSize are ignored
	q := bookQ0
	q.Page, q.PageSize = 3, 1
	if p, _ := e.lib.Rows(bg, q, "en", -1, 0); len(p) != 5 {
		t.Fatalf("paging fields applied: %d rows", len(p))
	}
	st := Active
	q = bookQ0
	q.Status = &st
	if _, err := e.lib.Rows(bg, Query{PluginID: "nope", Kind: "x"}, "", -1, 0); err == nil {
		t.Fatal("unknown plugin accepted")
	}
	if p, _ := e.lib.Rows(bg, q, "en", 1, 0); len(p) != 1 || p[0].Item.ID != ids[4] {
		t.Fatalf("status filter = %v", rowIDs(p))
	}
}

func TestRowOfOneItem(t *testing.T) {
	e := newEnv(t)
	d := bookWith("1", fp(300))
	d.Source, d.ExternalIDs = "manual", map[string]string{"isbn": "978"}
	id, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Paused, Progress: map[string]float64{"pages": 4}}, WithSyncState("partial"))
	if err != nil {
		t.Fatal(err)
	}
	noEntry := mustSave(t, e.lib, "book", "book", bookData("2"))
	r, err := e.lib.Row(bg, id, "ja")
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	if r.Item.ID != id || r.Entry.Status != Paused || r.Entry.Progress["pages"] != 4 || r.Title != "デューン" ||
		!r.Flags.Unmatched || !r.Flags.PartialSync || r.Item.ExternalIDs["isbn"] != "978" || *r.Item.Fields["pages"].Num != 300 {
		t.Fatalf("row %+v", r)
	}
	for _, missing := range []int64{noEntry, 9999} {
		if r, err := e.lib.Row(bg, missing, "en"); r != nil || err != nil {
			t.Fatalf("Row(%d) = %+v, %v", missing, r, err)
		}
	}
}

func TestListRowsCarryExternalIDs(t *testing.T) {
	e := newEnv(t)
	d := bookData("1")
	d.ExternalIDs = map[string]string{"isbn": "978", "foo": "x"}
	if _, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Planned}); err != nil {
		t.Fatal(err)
	}
	bookT(t, e.lib, "2", "No ids", nil, Entry{})
	pg := mustList(t, e.lib, bookQ0)
	got := map[string]map[string]string{}
	for _, r := range pg.Rows {
		got[r.Item.SourceID] = r.Item.ExternalIDs
	}
	if !reflect.DeepEqual(got, map[string]map[string]string{"1": {"isbn": "978", "foo": "x"}, "2": nil}) {
		t.Fatalf("external ids %v", got)
	}
}

func TestImageReferenced(t *testing.T) {
	e := newEnv(t)
	d := bookData("1")
	d.CoverURL = "cover.png"
	id := mustSave(t, e.lib, "book", "book", d)
	if err := e.lib.SaveCredits(bg, id, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p", Name: "P"}}}); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.lib.Credits(bg, id)
	if err := e.lib.SetPersonImagePath(bg, cs[0].Person.ID, "face.jpg"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"cover.png": true, "face.jpg": true, "other.png": false, "": false} {
		if got, err := e.lib.ImageReferenced(bg, name); err != nil || got != want {
			t.Errorf("ImageReferenced(%q) = %v, %v", name, got, err)
		}
	}
}

func TestItemsToSync(t *testing.T) {
	e := newEnv(t)
	save := func(id, state string) int64 {
		return mustSave(t, e.lib, "book", "book", bookData(id), WithSyncState(state))
	}
	a := save("1", "pending")
	save("2", "done")
	b := save("3", "partial")
	tv := mustSave(t, e.lib, "tv", "show", tvData("4", nil), WithSyncState("partial"))
	got, err := e.lib.ItemsToSync(bg, "book")
	if err != nil || !reflect.DeepEqual(got, []int64{a, b}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, _ := e.lib.ItemsToSync(bg, "tv"); !reflect.DeepEqual(got, []int64{tv}) {
		t.Fatalf("tv got %v", got)
	}
}

func TestCreditDetailRoundTrips(t *testing.T) {
	e := newEnv(t)
	id := tvItem(t, e.lib, "1")
	cs := []plugin.Credit{
		{Role: "actor", Detail: "MAIN", Person: plugin.PersonRef{ID: "p1", Name: "A"}},
		{Role: "actor", Person: plugin.PersonRef{ID: "p2", Name: "B"}},
	}
	if err := e.lib.SaveCredits(bg, id, cs); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.Credits(bg, id)
	if err != nil || len(got) != 2 || got[0].Detail != "MAIN" || got[1].Detail != "" {
		t.Fatalf("credits %+v %v", got, err)
	}
	bad := []plugin.Credit{{Role: "actor", Detail: "a\x00b", Person: plugin.PersonRef{ID: "p1", Name: "A"}}}
	wantInvalid(t, e.lib.SaveCredits(bg, id, bad), "detail")
}

func TestAcceptsURLAndImageRef(t *testing.T) {
	for s, want := range map[string]bool{"": true, "https://a.example/x": true, "http://a.example": true,
		"javascript:alert(1)": false, "//evil.example/x": false, "data:text/html,x": false, "a.png": false, "https://a.example/\x00": false} {
		if got := AcceptsURL(s); got != want {
			t.Errorf("AcceptsURL(%q) = %v, want %v", s, got, want)
		}
	}
	for s, want := range map[string]bool{"": true, "https://a.example/x.jpg": true, "a.png": true,
		"dir/a.png": false, "../a.png": false, "javascript:x": false, "data:image/png;base64,x": false} {
		if got := AcceptsImageRef(s); got != want {
			t.Errorf("AcceptsImageRef(%q) = %v, want %v", s, got, want)
		}
	}
}
