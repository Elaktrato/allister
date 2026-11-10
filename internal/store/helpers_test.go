package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func intp(i int) *int { return &i }

func sample(src anilist.Source, id string, episodes *int) anilist.Anime {
	return anilist.Anime{SearchResult: anilist.SearchResult{
		Source: src, ID: id, Episodes: episodes, Type: "TV",
		Titles: []anilist.Title{{Lang: "ja", Text: "ABC"}, {Lang: "en", Text: "ABC en"}, {Lang: "en", Text: "ABC en"}},
	}}
}

func mustSave(t *testing.T, s *Store, a anilist.Anime, opts ...SaveOption) int64 {
	t.Helper()
	id, err := s.SaveAnime(context.Background(), a, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
