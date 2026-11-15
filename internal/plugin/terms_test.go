package plugin

import (
	"reflect"
	"strings"
	"testing"
)

func TestCleanTerms(t *testing.T) {
	got := CleanTerms([]string{" Action ", "action", "", "  ", "RPG\nx", "Indie", "Extra"}, 10, 3)
	if want := []string{"Action", "RPG x", "Indie"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := CleanTerms([]string{strings.Repeat("a", 20)}, 5, 0); len(got) != 0 {
		t.Fatalf("limit 0 keeps nothing: %q", got)
	}
	if got := CleanTerms([]string{strings.Repeat("a", 20)}, 5, 1); len(got) != 1 || len([]rune(got[0])) > 5 {
		t.Fatalf("term not capped: %q", got)
	}
}

func TestCleanTags(t *testing.T) {
	rank := func(n int) *int { return &n }
	got := CleanTags([]Tag{
		{Name: " Open world ", Rank: rank(80), Spoiler: true},
		{Name: "open WORLD"},
		{Name: " "},
		{Name: "Bad rank", Rank: rank(101)},
		{Name: "Negative", Rank: rank(-1)},
		{Name: "Over limit"},
	}, 60, 3)
	if len(got) != 3 || got[0].Name != "Open world" || got[0].Rank == nil || *got[0].Rank != 80 || !got[0].Spoiler ||
		got[1].Name != "Bad rank" || got[1].Rank != nil || got[2].Name != "Negative" || got[2].Rank != nil {
		t.Fatalf("%+v", got)
	}
}
