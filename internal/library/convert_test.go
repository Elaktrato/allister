package library

import (
	"errors"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// manualBook saves a manual book with a page total of 300, an isbn and a foo
// external id, a genre, a relation and an entry at page 250.
func manualBook(t *testing.T, e *env) int64 {
	t.Helper()
	d := bookWith("man1", fp(300))
	d.Source = "manual"
	d.CoverURL = "upload.png"
	d.ExternalIDs = map[string]string{"isbn": "111", "foo": "keep"}
	d.Genres = []string{"Old"}
	d.Relations = []plugin.Relation{{Kind: "other", Item: plugin.ItemSummary{Source: "books", ID: "r0"}}}
	id, err := e.lib.AddToList(bg, "book", "book", d, Entry{Status: Completed, Rating: ip(0), Comment: "c", Progress: map[string]float64{"pages": 250}}, WithSyncState("done"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sourced(id string, pages *float64) plugin.ItemData {
	d := bookWith(id, pages)
	d.Titles = []plugin.Title{{Lang: "en", Text: "New Title"}}
	d.Fields["isbn"] = plugin.FieldValue{Text: "978-new"}
	d.Genres = []string{"New"}
	d.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "books", ID: "r1", Titles: []plugin.Title{{Lang: "en", Text: "Next"}}}}}
	return d
}

const oldStamp = "2001-02-03T04:05:06Z"

func entryStamp(t *testing.T, e *env, id int64) string {
	t.Helper()
	var s string
	if err := e.db.QueryRow(`SELECT updated_at FROM entries WHERE item_id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConvertToSourceReplacesItemInPlace(t *testing.T) {
	e := newEnv(t)
	id := manualBook(t, e)
	before := mustEntry(t, e.lib, id)
	if _, err := e.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, oldStamp, id); err != nil {
		t.Fatal(err)
	}
	d := sourced("b700", fp(400))
	d.ExternalIDs = map[string]string{"isbn": "222"}
	if err := e.lib.ConvertToSource(bg, id, d); err != nil {
		t.Fatal(err)
	}
	it := mustItem(t, e.lib, id)
	if it.Source != "books" || it.SourceID != "b700" || it.SyncState != "pending" || it.CoverPath != d.CoverURL || it.ExternalURL != d.ExternalURL {
		t.Fatalf("item %+v", it)
	}
	if !reflect.DeepEqual(it.Titles, d.Titles) || it.Fields["isbn"].Text != "978-new" || *it.Fields["pages"].Num != 400 {
		t.Fatalf("titles/fields %+v %+v", it.Titles, it.Fields)
	}
	if !reflect.DeepEqual(it.Genres, []string{"New"}) || len(it.Relations) != 1 || it.Relations[0].Item.ID != "r1" {
		t.Fatalf("terms/relations %+v %+v", it.Genres, it.Relations)
	}
	// isbn replaced, foo (not in d.ExternalIDs) kept
	if !reflect.DeepEqual(it.ExternalIDs, map[string]string{"isbn": "222", "foo": "keep"}) {
		t.Fatalf("external ids %v", it.ExternalIDs)
	}
	if old, _ := e.lib.ItemBySource(bg, "book", "book", "manual", "man1"); old != nil {
		t.Fatal("old identity still addressable")
	}
	after := mustEntry(t, e.lib, id)
	after.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("entry changed: %+v -> %+v", before, after)
	}
	if s := entryStamp(t, e, id); s != oldStamp {
		t.Fatalf("updated_at touched without a clamp: %s", s)
	}
}

func TestConvertToSourceKeepsExternalIDsWhenNoneGiven(t *testing.T) {
	e := newEnv(t)
	id := manualBook(t, e)
	if err := e.lib.ConvertToSource(bg, id, sourced("b1", nil)); err != nil {
		t.Fatal(err)
	}
	if it := mustItem(t, e.lib, id); !reflect.DeepEqual(it.ExternalIDs, map[string]string{"isbn": "111", "foo": "keep"}) {
		t.Fatalf("external ids %v", it.ExternalIDs)
	}
}

func TestConvertToSourceClampsProgressOnlyWhenExceeded(t *testing.T) {
	for _, c := range []struct {
		name    string
		pages   *float64
		want    float64
		clamped bool
	}{
		{"above new total", fp(200), 200, true},
		{"unknown total", nil, 250, false},
		{"equal total", fp(250), 250, false},
		{"larger total", fp(900), 250, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			id := manualBook(t, e)
			if _, err := e.db.Exec(`UPDATE entries SET updated_at = ? WHERE item_id = ?`, oldStamp, id); err != nil {
				t.Fatal(err)
			}
			if err := e.lib.ConvertToSource(bg, id, sourced("b1", c.pages)); err != nil {
				t.Fatal(err)
			}
			en := mustEntry(t, e.lib, id)
			if en.Progress["pages"] != c.want || en.Status != Completed || en.Comment != "c" || en.Rating == nil || *en.Rating != 0 {
				t.Fatalf("entry %+v", en)
			}
			if touched := entryStamp(t, e, id) != oldStamp; touched != c.clamped {
				t.Fatalf("updated_at touched=%v, want %v", touched, c.clamped)
			}
		})
	}
}

func TestConvertToSourceWithoutEntryCreatesNone(t *testing.T) {
	e := newEnv(t)
	d := bookData("m")
	d.Source = "manual"
	id := mustSave(t, e.lib, "book", "book", d, WithSyncState("done"))
	if err := e.lib.ConvertToSource(bg, id, sourced("b1", fp(10))); err != nil {
		t.Fatal(err)
	}
	if en, err := e.lib.Entry(bg, id); en != nil || err != nil {
		t.Fatalf("entry %+v %v", en, err)
	}
}

func TestConvertToSourceErrors(t *testing.T) {
	e := newEnv(t)
	id := manualBook(t, e)
	other := mustSave(t, e.lib, "book", "book", bookData("b700"))
	before := mustItem(t, e.lib, id)

	err := e.lib.ConvertToSource(bg, id, sourced("b700", nil))
	var ase *AlreadySavedError
	if !errors.Is(err, ErrAlreadySaved) || !errors.As(err, &ase) || ase.ExistingID != other {
		t.Fatalf("duplicate: %v", err)
	}
	if err := e.lib.ConvertToSource(bg, other, sourced("b9", nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-manual: %v", err)
	}
	if err := e.lib.ConvertToSource(bg, 9999, sourced("b9", nil)); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("missing: %v", err)
	}
	m := sourced("x", nil)
	m.Source = "manual"
	if err := e.lib.ConvertToSource(bg, id, m); !errors.Is(err, ErrInvalid) {
		t.Fatalf("manual target: %v", err)
	}
	bad := sourced("b9", nil)
	bad.Fields["nope"] = plugin.FieldValue{Text: "x"}
	if err := e.lib.ConvertToSource(bg, id, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid data: %v", err)
	}
	if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("item changed:\n%+v\n%+v", before, after)
	}
}

func TestConvertToSourceIsAtomic(t *testing.T) {
	e := newEnv(t)
	id := manualBook(t, e)
	before := mustItem(t, e.lib, id)
	beforeEntry := mustEntry(t, e.lib, id)
	if _, err := e.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON relations BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	err := e.lib.ConvertToSource(bg, id, sourced("b1", fp(10)))
	if err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("want a database error, got %v", err)
	}
	if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("not rolled back:\n%+v\n%+v", before, after)
	}
	if after := mustEntry(t, e.lib, id); after.Progress["pages"] != beforeEntry.Progress["pages"] {
		t.Fatalf("progress clamp not rolled back: %v", after.Progress)
	}
}

// The trigger aborts the LAST write of the conversion (the entry's updated_at
// bump after a clamp), so the clamp and every earlier change must roll back.
func TestConvertToSourceRollsBackClampOnLateFailure(t *testing.T) {
	e := newEnv(t)
	id := manualBook(t, e)
	before := mustItem(t, e.lib, id)
	beforeEntry := mustEntry(t, e.lib, id)
	if _, err := e.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON entries BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.ConvertToSource(bg, id, sourced("b1", fp(200))); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("want a database error, got %v", err)
	}
	if after := mustItem(t, e.lib, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("item not rolled back:\n%+v\n%+v", before, after)
	}
	if after := mustEntry(t, e.lib, id); !reflect.DeepEqual(after, beforeEntry) {
		t.Fatalf("entry not rolled back: %+v -> %+v", beforeEntry, after)
	}
	if n := count(t, e.db, `SELECT count(*) FROM entry_progress WHERE item_id = ? AND value = 250`, id); n != 1 {
		t.Fatal("clamp not rolled back")
	}
}
