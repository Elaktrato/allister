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
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(actor_person_id IS NULL),0) FROM anime_cast WHERE anime_id=?`, id).Scan(&n, &nulls)
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
	s.db.QueryRow(`SELECT COUNT(*) FROM anime_cast`).Scan(&n)
	s.db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&people)
	if n != 2 || people != 4 {
		t.Fatalf("cast=%d people=%d", n, people)
	}
	// a re-sync replaces the cast and keeps a local image path
	va := personID(t, s, "anilist", "v1")
	if err := s.SetPersonImagePath(ctx, va, "people/v1.jpg"); err != nil {
		t.Fatal(err)
	}
	m := cast("1", "1")
	m.Actor.Name = "Renamed"
	if err := s.SaveCast(ctx, id, []provider.CastMember{m}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Person(ctx, va)
	if p.Name != "Renamed" || p.ImagePath != "people/v1.jpg" {
		t.Fatalf("%+v", p)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM anime_cast`).Scan(&n)
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
