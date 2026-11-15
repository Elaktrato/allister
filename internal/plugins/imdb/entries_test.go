package imdb

import (
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestEntriesDirect(t *testing.T) {
	r7 := 7
	rows := []Row{
		{Line: 2, Const: "tt0000001", Title: "A", TypeKey: "film", Year: 2001, Rating: &r7, Rated: true},
		{Line: 3, Const: "tt0000001", Title: "A again", TypeKey: "film"},
		{Line: 4, Const: "tt0000002", Title: "B", TypeKey: "fernsehserie"},
		{Line: 5, Const: "tt0000003", Title: "C", TypeKey: "zzz", TypeRaw: "Zzz"},
		{Line: 6, Title: "D", TypeKey: "film", Invalid: "invalid IMDb id"},
		{Line: 7, Const: "tt0000004", Title: "E", TypeKey: "film", Rated: true, Notes: []string{"n"}},
		{Line: 8, Const: "tt0000005", Title: "", TypeKey: "film"},
	}
	res := Entries(rows, "movie", TypeMovie)
	if len(res.Entries) != 3 || res.Ignored != 1 || len(res.Skipped) != 3 {
		t.Fatalf("%+v", res)
	}
	if e := res.Entries[0]; e.Title != "A" || e.Status != plugin.Completed || *e.Rating != 7 {
		t.Fatalf("first wins: %+v", e)
	}
	if e := res.Entries[1]; e.Status != plugin.Completed || e.Rating != nil || len(e.Notes) != 1 {
		t.Fatalf("rated invalid: %+v", e)
	}
	if e := res.Entries[2]; e.Title != "IMDb tt0000005" || e.Status != plugin.Planned || e.Fields != nil {
		t.Fatalf("no title: %+v", e)
	}
	want := []plugin.SkippedRow{
		{Line: 3, Title: "A again", Reason: "duplicate IMDb id tt0000001"},
		{Line: 5, Title: "C", Reason: `unknown Title Type "Zzz"`},
		{Line: 6, Title: "D", Reason: "invalid IMDb id"},
	}
	for i, w := range want {
		if res.Skipped[i] != w {
			t.Errorf("skipped[%d] = %+v, want %+v", i, res.Skipped[i], w)
		}
	}
}
