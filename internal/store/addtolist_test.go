package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/provider"
)

func TestValidateListEntry(t *testing.T) {
	bad := func(name string, e ListEntry, eps *int) {
		t.Helper()
		if err := ValidateListEntry(e, eps); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	bad("status", ListEntry{Status: "nope"}, nil)
	bad("rating high", ListEntry{Status: Watching, Rating: intp(11)}, nil)
	bad("rating low", ListEntry{Status: Watching, Rating: intp(-1)}, nil)
	bad("progress neg", ListEntry{Status: Watching, Progress: -1}, nil)
	bad("progress over", ListEntry{Status: Watching, Progress: 13}, intp(12))
	d1, d2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bad("dates", ListEntry{Status: Watching, StartedOn: &d1, FinishedOn: &d2}, nil)
	if err := ValidateListEntry(ListEntry{Status: Watching, Rating: intp(0), Progress: 12}, intp(12)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateListEntry(ListEntry{Status: PlanToWatch, Progress: 500}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAddToListSavesBoth(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, err := s.AddToList(ctx, sample(provider.AniList, "1", intp(12)), ListEntry{Status: Watching, Rating: intp(0), Progress: 3, Comment: "hi"})
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	e, _ := s.GetListEntry(ctx, id)
	if e == nil || e.Status != Watching || e.Rating == nil || *e.Rating != 0 || e.Progress != 3 || e.Comment != "hi" {
		t.Fatalf("%+v", e)
	}
	sv, _ := s.FindBySource(ctx, provider.AniList, "1")
	if sv == nil || sv.ID != id || sv.SyncState != "pending" {
		t.Fatalf("%+v", sv)
	}
}

func TestAddToListRejectsInvalidEntryBeforeSaving(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, err := s.AddToList(ctx, sample(provider.AniList, "1", intp(12)), ListEntry{Status: Watching, Progress: 99})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if sv, _ := s.FindBySource(ctx, provider.AniList, "1"); sv != nil {
		t.Fatal("anime was saved")
	}
}

func TestAddToListAlreadySaved(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustSave(t, s, sample(provider.AniList, "1", nil))
	_, err := s.AddToList(ctx, sample(provider.AniList, "1", nil), ListEntry{Status: Watching})
	if !errors.Is(err, ErrAlreadySaved) {
		t.Fatalf("got %v", err)
	}
}

func TestCastWithLocalIDs(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	err := s.SaveCast(ctx, id, []provider.CastMember{
		{Character: provider.Person{Source: provider.AniList, ID: "c1", Name: "L", URL: "https://x/c1"}, Actor: &provider.Person{Source: provider.AniList, ID: "a1", Name: "Seiyuu"}, Role: "MAIN"},
		{Character: provider.Person{Source: provider.AniList, ID: "c2", Name: "Light"}, Role: "SUPPORTING"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Cast(ctx, id)
	if err != nil || len(c) != 2 {
		t.Fatal(c, err)
	}
	if c[0].Character.ID == 0 || c[0].Character.Name != "L" || c[0].Actor == nil || c[0].Actor.ID == 0 || c[0].Role != "MAIN" {
		t.Fatalf("%+v", c[0])
	}
	if c[1].Actor != nil || c[1].Character.Name != "Light" {
		t.Fatalf("%+v", c[1])
	}
	if c, err := s.Cast(ctx, 9999); err != nil || len(c) != 0 {
		t.Fatal(c, err)
	}
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The list entry write fails inside the transaction, after the anime, titles
// and relations were inserted; everything must be rolled back.
func TestAddToListRollsBackWhenListInsertFails(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(provider.AniList, "1", intp(12))
	a.Relations = []provider.Relation{{Kind: "sequel", Source: provider.AniList, ID: "2", Titles: []provider.Title{{Lang: "en", Text: "S2"}}}}
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON list_entries BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddToList(ctx, a, ListEntry{Status: Watching, Progress: 1})
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrAlreadySaved) {
		t.Fatalf("want a database error, got %v", err)
	}
	for _, tbl := range []string{"anime", "anime_titles", "anime_relations", "list_entries"} {
		if n := countRows(t, s, tbl); n != 0 {
			t.Fatalf("%s has %d rows after rollback", tbl, n)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddToList(ctx, a, ListEntry{Status: Watching, Progress: 1})
	if err != nil || id == 0 {
		t.Fatalf("retry: %d %v", id, err)
	}
}
