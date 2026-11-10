package anime

import (
	"testing"

	"github.com/allister/allister/internal/plugins/anilist"
)

func TestItemDataRelationsAndSource(t *testing.T) {
	a := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "m1", Titles: []anilist.Title{{Lang: "en", Text: "T"}}}}
	a.Relations = []anilist.Relation{
		{Kind: "source", Source: anilist.AniList, ID: "1"},
		{Kind: "", Source: anilist.AniList, ID: "2"},       // no kind: dropped
		{Kind: "sequel", Source: anilist.AniList, ID: " "}, // no id: dropped
		{Kind: "sequel", ID: "3", Type: "TV"},              // no source: AniList
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
	a := anilist.Media{SearchResult: anilist.SearchResult{ID: "1", Type: "tv", Episodes: &zero, Year: &old}}
	if f := ItemData(a).Fields; len(f) != 0 {
		t.Fatalf("out of range values kept: %+v", f)
	}
	a.Episodes, a.Type = &big, "TV"
	if f := ItemData(a).Fields; f["type"].Text != "TV" || f["episodes"].Num != nil {
		t.Fatalf("fields %+v", f)
	}
}

func TestCreditsDropUnusablePeople(t *testing.T) {
	ok := anilist.Person{ID: "c1", Name: "C"}
	cs := Credits([]anilist.CastMember{
		{Character: anilist.Person{ID: "c0", Name: " \n "}, Role: "MAIN"},
		{Character: ok, Actor: &anilist.Person{ID: "", Name: "V"}, Role: "MAIN"},
		{Character: anilist.Person{ID: "c2", Name: "D", URL: "javascript:x", ImageURL: "data:x"}, Role: "SUPPORTING"},
	})
	if len(cs) != 2 || cs[0].With != nil || cs[0].WithRole != "" || cs[0].Detail != "MAIN" || cs[0].Role != roleCharacter {
		t.Fatalf("credits %+v", cs)
	}
	if cs[1].Person.URL != "" || cs[1].Person.ImageURL != "" || cs[1].Detail != "SUPPORTING" {
		t.Fatalf("hostile urls kept %+v", cs[1])
	}
}

func TestItemDataCleansSourceText(t *testing.T) {
	zero := 0
	a := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "m", Type: "tv", Episodes: &zero,
		Titles: []anilist.Title{{Lang: "en", Text: "My  Show"}}, Synopsis: "a\r\n\r\nb", CoverURL: "upload.png"}}
	s := ItemData(a)
	if s.Titles[0].Text != "My Show" || s.Fields["synopsis"].Text != "a\n\nb" || len(s.Fields) != 1 || s.CoverURL != "" {
		t.Fatalf("source mode: %+v", s)
	}
}

func TestItemDataRelationURLs(t *testing.T) {
	a := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: "1"}}
	a.Relations = []anilist.Relation{
		{Kind: "source", Source: anilist.AniList, ID: "30021"},
		{Kind: "sequel", Source: anilist.AniList, ID: "2000"},
		{Kind: "other", Source: "elsewhere", ID: "9"},
		{Kind: "sequel", Source: anilist.AniList, ID: "1/../x?y"},
	}
	d := ItemData(a)
	want := []string{"https://anilist.co/manga/30021", "https://anilist.co/anime/2000", "", "https://anilist.co/anime/1%2F..%2Fx%3Fy"}
	if len(d.Relations) != len(want) {
		t.Fatalf("relations %+v", d.Relations)
	}
	for i, w := range want {
		if got := d.Relations[i].Item.URL; got != w {
			t.Errorf("relation %d url = %q, want %q", i, got, w)
		}
	}
}
