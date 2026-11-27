package anime

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

func TestItemDataRelationsAndSource(t *testing.T) {
	a := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "m1", Titles: []anilist.Title{{Lang: "en", Text: "T"}}}}
	a.Relations = []anilist.Relation{
		{Kind: "source", Source: anilist.AniList, ID: "1", MediaType: "MANGA"},
		{Kind: "", Source: anilist.AniList, ID: "2"},              // no kind: dropped
		{Kind: "sequel", Source: anilist.AniList, ID: " "},        // no id: dropped
		{Kind: "sequel", ID: "3", Type: "TV", MediaType: "ANIME"}, // no source: AniList
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

func TestDurationMapsAndGuardsRange(t *testing.T) {
	ok := 24
	r := anilist.SearchResult{ID: "1", Type: "TV", Duration: &ok}
	if v := toSummary(r).Fields["duration"].Num; v == nil || *v != 24 {
		t.Fatalf("summary duration = %v", v)
	}
	if v := ItemData(anilist.Media{SearchResult: r}).Fields["duration"].Num; v == nil || *v != 24 {
		t.Fatalf("item duration = %v", v)
	}
	for _, bad := range []int{0, 10001} {
		b := bad
		if _, ok := toSummary(anilist.SearchResult{ID: "1", Duration: &b}).Fields["duration"]; ok {
			t.Errorf("duration %d must be dropped", bad)
		}
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
		{Kind: "source", Source: anilist.AniList, ID: "30021", MediaType: "MANGA"},
		{Kind: "sequel", Source: anilist.AniList, ID: "2000", MediaType: "ANIME"},
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

func intp(n int) *int { return &n }

// extrasMedia is an AniList media with every extra set.
func extrasMedia() anilist.Media {
	return anilist.Media{
		SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: "21", Type: "TV",
			Titles: []anilist.Title{{Lang: "ja-latn", Text: "One Piece"}, {Lang: "en", Text: "ONE PIECE"}},
			Studio: "Toei Animation"},
		Status: "RELEASING", Season: "FALL", Country: "JP", Adult: true,
		AverageScore: intp(87), Popularity: intp(612345),
		Synonyms: []string{" OP ", "One Piece", "", "ワン\tピース", "OP"},
		Studios:  []string{"Toei Animation", "Second Studio"},
		StreamingLinks: []anilist.LinkRef{
			{Site: "Crunchyroll", URL: "https://www.crunchyroll.com/one-piece", Type: "STREAMING"},
			{Site: "Official Site", URL: "https://one-piece.com", Type: "INFO"},
			{Site: "Netflix", URL: "https://www.netflix.com/title/80107103", Type: "STREAMING"},
		},
		BannerURL: "https://s4.anilist.co/banner/21.jpg",
		Trailer:   &anilist.TrailerRef{ID: "l_98K4_6UQ0", Site: "youtube", Thumbnail: "https://i.ytimg.com/vi/l_98K4_6UQ0/hqdefault.jpg"},
	}
}

func TestItemDataExtras(t *testing.T) {
	d := ItemData(extrasMedia())
	f := d.Fields
	for key, want := range map[string]string{"release_status": "releasing", "season": "fall", "country": "JP", "adult": "yes"} {
		if f[key].Text != want {
			t.Errorf("%s = %q, want %q", key, f[key].Text, want)
		}
	}
	if v := f["community_score"].Num; v == nil || *v != 87 {
		t.Errorf("community score = %v", v)
	}
	if v := f["popularity"].Num; v == nil || *v != 612345 {
		t.Errorf("popularity = %v", v)
	}
	if want := []string{"Toei Animation", "Second Studio"}; !slices.Equal(f["studio"].List, want) || f["studio"].Text != "" {
		t.Errorf("studio = %+v", f["studio"])
	}
	wantTitles := []plugin.Title{{Lang: "ja-latn", Text: "One Piece"}, {Lang: "en", Text: "ONE PIECE"},
		{Lang: plugin.AltTitleLang, Text: "OP"}, {Lang: plugin.AltTitleLang, Text: "ワン ピース"}}
	if !slices.Equal(d.Titles, wantTitles) {
		t.Errorf("titles = %+v", d.Titles)
	}
	wantLinks := []plugin.Link{{Label: "Crunchyroll", URL: "https://www.crunchyroll.com/one-piece"},
		{Label: "Netflix", URL: "https://www.netflix.com/title/80107103"}}
	if !slices.Equal(d.Links, wantLinks) {
		t.Errorf("links = %+v", d.Links)
	}
	if d.BannerURL != "https://s4.anilist.co/banner/21.jpg" {
		t.Errorf("banner = %q", d.BannerURL)
	}
	if want := (plugin.Trailer{URL: "https://www.youtube.com/watch?v=l_98K4_6UQ0", ThumbnailURL: "https://i.ytimg.com/vi/l_98K4_6UQ0/hqdefault.jpg"}); d.Trailer == nil || *d.Trailer != want {
		t.Errorf("trailer = %+v", d.Trailer)
	}
}

// TestItemDataExtrasGuards: unknown enum values, out-of-range numbers,
// non-https links and images are dropped; lists are capped at 20.
func TestItemDataExtrasGuards(t *testing.T) {
	m := extrasMedia()
	m.Status, m.Season, m.Country, m.Adult = "PAUSED", "MONSOON", " J\tPXXXXXXXX ", false
	m.AverageScore, m.Popularity = intp(101), intp(-1)
	m.BannerURL = "http://s4.anilist.co/banner/21.jpg"
	m.Synonyms, m.StreamingLinks, m.Studios = nil, nil, nil
	for i := range 30 {
		m.Synonyms = append(m.Synonyms, fmt.Sprintf("Alt %d", i))
		m.StreamingLinks = append(m.StreamingLinks, anilist.LinkRef{Site: fmt.Sprintf("S%d", i), URL: fmt.Sprintf("https://s%d.example/x", i), Type: "STREAMING"})
	}
	m.StreamingLinks = append([]anilist.LinkRef{
		{Site: "Plain", URL: "http://plain.example/x", Type: "STREAMING"},
		{Site: " ", URL: "https://nolabel.example/x", Type: "STREAMING"},
		{Site: "Bad", URL: "javascript:alert(1)", Type: "STREAMING"},
	}, m.StreamingLinks...)
	d := ItemData(m)
	for _, key := range []string{"release_status", "season", "community_score", "popularity"} {
		if v, ok := d.Fields[key]; ok {
			t.Errorf("%s kept: %+v", key, v)
		}
	}
	if d.Fields["adult"].Text != "no" || d.Fields["country"].Text != "J PXXXXX" {
		t.Errorf("adult/country = %+v/%+v", d.Fields["adult"], d.Fields["country"])
	}
	if want := []string{"Toei Animation"}; !slices.Equal(d.Fields["studio"].List, want) {
		t.Errorf("studio without a studio list = %+v", d.Fields["studio"])
	}
	alts := 0
	for _, ti := range d.Titles {
		if ti.Lang == plugin.AltTitleLang {
			alts++
		}
	}
	if alts != 20 || len(d.Links) != 20 || d.Links[0].Label != "S0" {
		t.Errorf("alt titles %d, links %d (%+v)", alts, len(d.Links), d.Links)
	}
	if d.BannerURL != "" {
		t.Errorf("http banner kept: %q", d.BannerURL)
	}
	// no adult flag without AniList's answer
	m.Source = anilist.Manual
	if v, ok := ItemData(m).Fields["adult"]; ok {
		t.Errorf("manual adult = %+v", v)
	}
}

func TestTrailerSitesAndURLs(t *testing.T) {
	thumb := "https://i.ytimg.com/vi/x/hqdefault.jpg"
	for _, c := range []struct {
		name string
		in   *anilist.TrailerRef
		want *plugin.Trailer
	}{
		{"youtube", &anilist.TrailerRef{ID: "l_98K4-6UQ0", Site: "youtube", Thumbnail: thumb},
			&plugin.Trailer{URL: "https://www.youtube.com/watch?v=l_98K4-6UQ0", ThumbnailURL: thumb}},
		{"dailymotion", &anilist.TrailerRef{ID: "x8abc12", Site: "dailymotion"},
			&plugin.Trailer{URL: "https://www.dailymotion.com/video/x8abc12"}},
		{"vimeo", &anilist.TrailerRef{ID: "12345", Site: "vimeo", Thumbnail: thumb}, nil},
		{"spaced id", &anilist.TrailerRef{ID: "x y", Site: "youtube", Thumbnail: thumb}, nil},
		{"long id", &anilist.TrailerRef{ID: strings.Repeat("a", 33), Site: "youtube"}, nil},
		{"empty id", &anilist.TrailerRef{Site: "youtube"}, nil},
		{"http thumbnail", &anilist.TrailerRef{ID: "abc", Site: "youtube", Thumbnail: "http://i.ytimg.com/vi/abc/hqdefault.jpg"},
			&plugin.Trailer{URL: "https://www.youtube.com/watch?v=abc"}},
		{"none", nil, nil},
	} {
		m := extrasMedia()
		m.Trailer = c.in
		got := ItemData(m).Trailer
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%s: trailer = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestSummaryStudioIsAList: the search result's main studio is a one-element
// studio list.
func TestSummaryStudioIsAList(t *testing.T) {
	s := toSummary(anilist.SearchResult{ID: "1", Studio: " MADHOUSE "})
	if want := []string{"MADHOUSE"}; !slices.Equal(s.Fields["studio"].List, want) || s.Fields["studio"].Text != "" {
		t.Fatalf("studio = %+v", s.Fields["studio"])
	}
}
