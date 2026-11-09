package store

import (
	"context"
	"testing"

	"github.com/allister/allister/internal/provider"
)

func cast(char, actor string) provider.CastMember {
	return provider.CastMember{
		Character: provider.Person{Source: provider.AniList, ID: "c" + char, Name: "Char " + char, ImageURL: "https://x/c" + char},
		Actor:     &provider.Person{Source: provider.AniList, ID: "v" + actor, Name: "VA " + actor, ImageURL: "https://x/v" + actor, URL: "https://u/" + actor},
		Role:      "MAIN",
	}
}

func personID(t *testing.T, s *Store, src, sid string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM people WHERE source=? AND source_id=?`, src, sid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSaveCastLinksVoiceActors(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	noActor := provider.CastMember{Character: provider.Person{Source: provider.AniList, ID: "cz", Name: "Z"}}
	if err := s.SaveCast(ctx, id, []provider.CastMember{cast("1", "1"), noActor}); err != nil {
		t.Fatal(err)
	}
	var n, nulls int
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(counterpart_person_id IS NULL),0) FROM credits WHERE item_id=?`, id).Scan(&n, &nulls)
	if n != 2 || nulls != 1 {
		t.Fatalf("n=%d nulls=%d", n, nulls)
	}
	va := personID(t, s, "anilist", "v1")
	p, err := s.Person(ctx, va)
	if err != nil || p == nil || p.Name != "VA 1" || p.ExternalURL != "https://u/1" || p.Source != provider.AniList || p.SourceID != "v1" || p.ImagePath != "https://x/v1" {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := s.Person(ctx, 9999); p != nil || err != nil {
		t.Fatal("expected nil")
	}
	apps, err := s.PersonAnime(ctx, va)
	if err != nil || len(apps) != 1 || apps[0].AnimeID != id || apps[0].Character != "Char 1" || len(apps[0].Titles) != 2 {
		t.Fatalf("%+v %v", apps, err)
	}
	// character's own page: Character is the character's own name
	apps, _ = s.PersonAnime(ctx, personID(t, s, "anilist", "c1"))
	if len(apps) != 1 || apps[0].Character != "Char 1" {
		t.Fatalf("%+v", apps)
	}
}

func TestSaveCastIsIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", nil))
	for i := 0; i < 2; i++ {
		if err := s.SaveCast(ctx, id, []provider.CastMember{cast("1", "1"), cast("2", "2")}); err != nil {
			t.Fatal(err)
		}
	}
	var n, people int
	s.db.QueryRow(`SELECT COUNT(*) FROM credits`).Scan(&n)
	s.db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&people)
	if n != 2 || people != 4 {
		t.Fatalf("cast=%d people=%d", n, people)
	}
	// a re-sync replaces the cast and keeps a local image path
	va := personID(t, s, "anilist", "v1")
	if err := s.SetPersonImagePath(ctx, va, "v1.jpg"); err != nil {
		t.Fatal(err)
	}
	m := cast("1", "1")
	m.Actor.Name = "Renamed"
	if err := s.SaveCast(ctx, id, []provider.CastMember{m}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Person(ctx, va)
	if p.Name != "Renamed" || p.ImagePath != "v1.jpg" {
		t.Fatalf("%+v", p)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM credits`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestPersonAnimeOnlyReturnsSavedAnime(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(provider.AniList, "1", nil))
	b := mustSave(t, s, sample(provider.AniList, "2", nil))
	s.SaveCast(ctx, a, []provider.CastMember{cast("1", "1")})
	s.SaveCast(ctx, b, []provider.CastMember{cast("2", "2")})
	apps, err := s.PersonAnime(ctx, personID(t, s, "anilist", "v1"))
	if err != nil || len(apps) != 1 || apps[0].AnimeID != a {
		t.Fatalf("%+v %v", apps, err)
	}
	// same actor in a second saved anime appears for both
	s.SaveCast(ctx, b, []provider.CastMember{cast("2", "1")})
	apps, _ = s.PersonAnime(ctx, personID(t, s, "anilist", "v1"))
	if len(apps) != 2 {
		t.Fatalf("%+v", apps)
	}
}

func TestAnimePeople(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(provider.AniList, "1", nil))
	b := mustSave(t, s, sample(provider.AniList, "2", nil))
	if err := s.SaveCast(ctx, a, []provider.CastMember{cast("1", "1"), cast("2", "1")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCast(ctx, b, []provider.CastMember{cast("9", "9")}); err != nil {
		t.Fatal(err)
	}
	ppl, err := s.AnimePeople(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	// characters c1, c2 and one distinct actor v1
	if len(ppl) != 3 {
		t.Fatalf("got %+v", ppl)
	}
	for _, p := range ppl {
		if p.ID == 0 || p.ImagePath == "" || p.Name == "" {
			t.Fatalf("incomplete %+v", p)
		}
	}
	if ppl, err := s.AnimePeople(ctx, 999); err != nil || len(ppl) != 0 {
		t.Fatalf("empty: %v %v", ppl, err)
	}
}

func TestPersonAnimeSourceAndRole(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(provider.AniList, "10", nil))
	b := mustSave(t, s, sample(provider.AniList, "20", nil))
	// actor v1 voices two characters in a; character c1 appears in a and b
	s.SaveCast(ctx, a, []provider.CastMember{cast("1", "1"), cast("2", "1"), cast("2", "1")})
	s.SaveCast(ctx, b, []provider.CastMember{cast("1", "3")})

	apps, err := s.PersonAnime(ctx, personID(t, s, "anilist", "v1"))
	if err != nil || len(apps) != 2 {
		t.Fatalf("%+v %v", apps, err)
	}
	for i, ch := range []string{"Char 1", "Char 2"} {
		x := apps[i]
		if x.AnimeID != a || x.Source != provider.AniList || x.SourceID != "10" || !x.AsActor || x.Character != ch {
			t.Fatalf("row %d: %+v", i, x)
		}
	}
	apps, _ = s.PersonAnime(ctx, personID(t, s, "anilist", "c1"))
	if len(apps) != 2 || apps[0].AsActor || apps[1].AsActor || apps[0].Character != "Char 1" ||
		apps[0].SourceID != "10" || apps[1].SourceID != "20" || apps[1].Source != provider.AniList {
		t.Fatalf("%+v", apps)
	}
}

func TestPersonAnimeActorAndCharacterBothKinds(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustSave(t, s, sample(provider.AniList, "10", nil))
	self := provider.Person{Source: provider.AniList, ID: "dual", Name: "Dual"}
	other := provider.Person{Source: provider.AniList, ID: "o", Name: "Other"}
	s.SaveCast(ctx, a, []provider.CastMember{
		{Character: self, Actor: &other},
		{Character: other, Actor: &self},
	})
	apps, err := s.PersonAnime(ctx, personID(t, s, "anilist", "dual"))
	if err != nil || len(apps) != 2 {
		t.Fatalf("%+v %v", apps, err)
	}
	// ordered by character name: "Dual" (as character) then "Other" (as actor)
	if apps[0].AsActor || apps[0].Character != "Dual" || !apps[1].AsActor || apps[1].Character != "Other" {
		t.Fatalf("%+v", apps)
	}
}
