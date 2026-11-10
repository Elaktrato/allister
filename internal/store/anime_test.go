package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugins/anime/anilist"
)

func TestSaveAnimeBlocksDuplicate(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, sample(anilist.AniList, "1", nil))
	_, err := s.SaveAnime(context.Background(), sample(anilist.AniList, "1", nil))
	if !errors.Is(err, ErrAlreadySaved) {
		t.Fatalf("err=%v", err)
	}
}

func TestSameIDDifferentSourceAllowed(t *testing.T) {
	s := newStore(t)
	a := mustSave(t, s, sample(anilist.AniList, "1", nil))
	b := mustSave(t, s, sample(anilist.Manual, "1", nil))
	if a == b {
		t.Fatal("same id")
	}
}

func TestSaveAnimeRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	start := time.Date(2020, 4, 5, 0, 0, 0, 0, time.UTC)
	a := sample(anilist.AniList, "7", intp(12))
	a.Year = intp(2020)
	a.Studio, a.Synopsis, a.CoverURL = "St", "Syn", "https://x/c.jpg"
	a.StartDate = &start
	a.SourceMaterial, a.ExternalURL, a.MALID = "Manga", "https://anilist.co/anime/7", "123"
	a.Relations = []anilist.Relation{{Kind: "sequel", Source: anilist.AniList, ID: "8", Type: "TV",
		Titles: []anilist.Title{{Lang: "ja", Text: "Next"}}}}
	a.Recommendations = []anilist.Recommendation{{ID: "9"}}
	id := mustSave(t, s, a)
	got, err := s.FindBySource(ctx, anilist.AniList, "7")
	if err != nil || got == nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if got.ID != id || got.SyncState != "pending" || got.CoverPath != "https://x/c.jpg" {
		t.Fatalf("%+v", got)
	}
	if got.Source != anilist.AniList || got.Studio != "St" || *got.Episodes != 12 || *got.Year != 2020 || got.MALID != "123" {
		t.Fatalf("%+v", got)
	}
	if !got.StartDate.Equal(start) || got.EndDate != nil {
		t.Fatalf("dates %v %v", got.StartDate, got.EndDate)
	}
	wantTitles := []anilist.Title{{Lang: "ja", Text: "ABC"}, {Lang: "en", Text: "ABC en"}}
	if !reflect.DeepEqual(got.Titles, wantTitles) {
		t.Fatalf("titles %v", got.Titles)
	}
	if !reflect.DeepEqual(got.Relations, a.Relations) {
		t.Fatalf("relations %v", got.Relations)
	}
	if len(got.Recommendations) != 0 {
		t.Fatal("recommendations must be empty")
	}
}

func TestFindBySourceAbsent(t *testing.T) {
	s := newStore(t)
	got, err := s.FindBySource(context.Background(), anilist.AniList, "nope")
	if got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
}

func TestFindByMALID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(anilist.AniList, "1", nil)
	a.MALID = "555"
	id := mustSave(t, s, a)
	mustSave(t, s, sample(anilist.AniList, "2", nil)) // no MAL id
	got, err := s.FindByMALID(ctx, "555")
	if err != nil || got == nil || got.ID != id || got.MALID != "555" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got, err := s.FindByMALID(ctx, "999"); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := s.FindByMALID(ctx, ""); got != nil || err != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestSaveAnimeWithSyncState(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, sample(anilist.Manual, "u-1", nil), WithSyncState("done"))
	got, _ := s.FindBySource(context.Background(), anilist.Manual, "u-1")
	if got.SyncState != "done" {
		t.Fatal(got.SyncState)
	}
}

func TestSetters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(anilist.AniList, "1", nil))
	if err := s.SetAnimeCoverPath(ctx, id, "1.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncState(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.FindBySource(ctx, anilist.AniList, "1")
	if got.CoverPath != "1.jpg" || got.SyncState != "done" {
		t.Fatalf("%+v", got)
	}
}

func TestCascadeOnRemovingAnime(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(anilist.AniList, "1", nil))
	if err := s.SaveCast(ctx, id, []anilist.CastMember{{Character: anilist.Person{Source: anilist.AniList, ID: "c", Name: "C"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"item_titles", "item_fields", "credits", "entries", "entry_progress"} {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n)
		if n != 0 {
			t.Fatalf("%s has %d rows", tbl, n)
		}
	}
}

func TestGetByID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(anilist.AniList, "7", intp(12)))
	got, err := s.GetByID(ctx, id)
	if err != nil || got == nil || got.ID != id || got.Anime.ID != "7" || got.Source != anilist.AniList {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := s.GetByID(ctx, id+100); got != nil || err != nil {
		t.Fatalf("missing: %+v, %v", got, err)
	}
}

func TestCoverReferenced(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(anilist.Manual, "m1", nil)
	a.CoverURL = "abc.png"
	if _, err := s.SaveAnime(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CoverReferenced(ctx, "abc.png"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, err := s.CoverReferenced(ctx, "other.png"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestNeedsSyncIgnoresLiveJobsAndDone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(anilist.AniList, "1", nil), WithSyncState("pending"))
	b := mustSave(t, s, sample(anilist.AniList, "2", nil), WithSyncState("partial"))
	mustSave(t, s, sample(anilist.AniList, "3", nil), WithSyncState("done"))
	c := mustSave(t, s, sample(anilist.AniList, "4", nil), WithSyncState("partial"))
	ins := func(id int64, state string) {
		if _, err := s.db.Exec(`INSERT INTO jobs(kind, payload_json, state) VALUES ('sync_anime', json_object('anime_id', ?), ?)`, id, state); err != nil {
			t.Fatal(err)
		}
	}
	ins(b, "running")
	ins(c, "failed")
	got, err := s.NeedsSync(ctx, "sync_anime")
	if err != nil || !reflect.DeepEqual(got, []int64{a, c}) {
		t.Fatalf("got %v err %v", got, err)
	}
}
