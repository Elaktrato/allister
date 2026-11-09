package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/allister/allister/internal/provider"
)

func TestListPageLimitOffsetKeepsOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var ids []int64
	for i := 0; i < 5; i++ {
		id := mustSave(t, s, sample(provider.AniList, fmt.Sprint(i), nil))
		s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching})
		s.db.Exec(`UPDATE list_entries SET updated_at = ? WHERE anime_id = ?`, fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), id)
		ids = append(ids, id)
	}
	// newest first: ids[4], ids[3], ids[2], ids[1], ids[0]
	p1, err := s.ListPage(ctx, nil, 2, 0)
	if err != nil || len(p1) != 2 || p1[0].AnimeID != ids[4] || p1[1].AnimeID != ids[3] {
		t.Fatalf("%+v %v", p1, err)
	}
	p3, _ := s.ListPage(ctx, nil, 2, 4)
	if len(p3) != 1 || p3[0].AnimeID != ids[0] {
		t.Fatalf("%+v", p3)
	}
	none, _ := s.ListPage(ctx, nil, 2, 10)
	if len(none) != 0 {
		t.Fatal(none)
	}
	w := Watching
	f, _ := s.ListPage(ctx, &w, 1, 1)
	if len(f) != 1 || f[0].AnimeID != ids[3] {
		t.Fatalf("%+v", f)
	}
}

func TestListRowCarriesSourceAndMALID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(provider.Manual, "m1", nil)
	a.MALID = "777"
	id := mustSave(t, s, a)
	b := mustSave(t, s, sample(provider.AniList, "9", nil))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching})
	s.UpsertListEntry(ctx, ListEntry{AnimeID: b, Status: Watching})
	rows, _ := s.List(ctx, nil)
	got := map[int64]ListRow{}
	for _, r := range rows {
		got[r.AnimeID] = r
	}
	if r := got[id]; r.Source != provider.Manual || r.SourceID != "m1" || r.MALID != "777" {
		t.Fatalf("%+v", r)
	}
	if r := got[b]; r.Source != provider.AniList || r.SourceID != "9" || r.MALID != "" {
		t.Fatalf("%+v", r)
	}
}

func TestGetListRow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := mustSave(t, s, sample(provider.AniList, "1", intp(12)))
	if r, err := s.GetListRow(ctx, id); r != nil || err != nil {
		t.Fatalf("%v %v", r, err)
	}
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Paused, Progress: 4})
	r, err := s.GetListRow(ctx, id)
	if err != nil || r == nil || r.Entry.Progress != 4 || *r.Episodes != 12 || len(r.Titles) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDeleteAnimeCascades(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample(provider.AniList, "1", nil)
	a.Relations = []provider.Relation{{Kind: "sequel", Source: provider.AniList, ID: "2", Titles: []provider.Title{{Lang: "en", Text: "S"}}}}
	id := mustSave(t, s, a)
	other := mustSave(t, s, sample(provider.AniList, "3", nil))
	s.UpsertListEntry(ctx, ListEntry{AnimeID: id, Status: Watching})
	s.UpsertListEntry(ctx, ListEntry{AnimeID: other, Status: Watching})
	if _, err := s.db.Exec(`INSERT INTO people (source, source_id, name) VALUES ('anilist','c1','Char')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO anime_cast (anime_id, character_person_id, role) VALUES (?, (SELECT id FROM people LIMIT 1), 'MAIN')`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAnime(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"anime_titles", "anime_relations", "anime_cast", "list_entries"} {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM `+tbl+` WHERE anime_id = ?`, id).Scan(&n)
		if n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM anime WHERE id = ?`, id).Scan(&n)
	if n != 0 {
		t.Error("anime remains")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&n)
	if n != 1 {
		t.Errorf("people rows %d", n)
	}
	if e, _ := s.GetListEntry(ctx, other); e == nil {
		t.Error("other entry removed")
	}
	if err := s.DeleteAnime(ctx, id); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("second delete: %v", err)
	}
}
