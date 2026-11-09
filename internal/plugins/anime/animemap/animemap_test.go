package animemap

import (
	"testing"

	"github.com/allister/allister/internal/provider"
)

func TestItemDataRelationsAndSource(t *testing.T) {
	a := provider.Anime{SearchResult: provider.SearchResult{Source: provider.Manual, ID: "m1", Titles: []provider.Title{{Lang: "en", Text: "T"}}}}
	a.Relations = []provider.Relation{
		{Kind: "source", Source: provider.AniList, ID: "1"},
		{Kind: "", Source: provider.AniList, ID: "2"},       // no kind: dropped
		{Kind: "sequel", Source: provider.AniList, ID: " "}, // no id: dropped
		{Kind: "sequel", ID: "3", Type: "TV"},               // no source: AniList
	}
	d := ItemData(a)
	if d.Source != "manual" || d.ID != "m1" || d.Kind != "anime" {
		t.Fatalf("identity %+v", d.ItemSummary)
	}
	if len(d.Relations) != 2 || d.Relations[0].Item.Kind != "manga" || d.Relations[1].Item.Source != "anilist" ||
		d.Relations[1].Item.Kind != "anime" || d.Relations[1].Item.Fields["type"].Text != "TV" {
		t.Fatalf("relations %+v", d.Relations)
	}
}

func TestItemDataGuardsFields(t *testing.T) {
	zero, big, old := 0, 20000, 1800
	a := provider.Anime{SearchResult: provider.SearchResult{ID: "1", Type: "tv", Episodes: &zero, Year: &old}}
	if f := ItemData(a).Fields; len(f) != 0 {
		t.Fatalf("out of range values kept: %+v", f)
	}
	a.Episodes, a.Type = &big, "TV"
	if f := ItemData(a).Fields; f["type"].Text != "TV" || f["episodes"].Num != nil {
		t.Fatalf("fields %+v", f)
	}
}

func TestCreditsDropUnusablePeople(t *testing.T) {
	ok := provider.Person{ID: "c1", Name: "C"}
	cs := Credits([]provider.CastMember{
		{Character: provider.Person{ID: "c0", Name: " \n "}, Role: "MAIN"},
		{Character: ok, Actor: &provider.Person{ID: "", Name: "V"}, Role: "MAIN"},
		{Character: provider.Person{ID: "c2", Name: "D", URL: "javascript:x", ImageURL: "data:x"}, Role: "SUPPORTING"},
	})
	if len(cs) != 2 || cs[0].With != nil || cs[0].WithRole != "" || cs[0].Detail != "MAIN" || cs[0].Role != RoleCharacter {
		t.Fatalf("credits %+v", cs)
	}
	if cs[1].Person.URL != "" || cs[1].Person.ImageURL != "" || cs[1].Detail != "SUPPORTING" {
		t.Fatalf("hostile urls kept %+v", cs[1])
	}
}

func TestUserItemDataRewritesNothing(t *testing.T) {
	zero := 0
	a := provider.Anime{SearchResult: provider.SearchResult{Source: provider.Manual, ID: "m", Type: "tv", Episodes: &zero,
		Titles: []provider.Title{{Lang: "en", Text: "My  Show"}}, Synopsis: "a\r\n\r\nb", CoverURL: "upload.png"}}
	d := UserItemData(a)
	if d.Titles[0].Text != "My  Show" || d.Fields["synopsis"].Text != "a\r\n\r\nb" || d.Fields["type"].Text != "tv" ||
		d.Fields["episodes"].Num == nil || d.CoverURL != "upload.png" {
		t.Fatalf("user input rewritten: %+v", d)
	}
	s := ItemData(a) // the source mode cleans the same input
	if s.Titles[0].Text != "My Show" || s.Fields["synopsis"].Text != "a\n\nb" || len(s.Fields) != 1 || s.CoverURL != "" {
		t.Fatalf("source mode: %+v", s)
	}
}
