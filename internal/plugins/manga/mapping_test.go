package manga

import (
	"reflect"
	"slices"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

func intp(n int) *int { return &n }

// TestItemDataExtras: a manga gets the AniList extras except season and
// studios.
func TestItemDataExtras(t *testing.T) {
	m := anilist.Media{
		SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: "30013", Type: "MANGA",
			Titles: []anilist.Title{{Lang: "ja-latn", Text: "ONE PIECE"}}, Studio: "Ignored"},
		Status: "HIATUS", Season: "FALL", Country: "JP", Adult: false,
		AverageScore: intp(92), Popularity: intp(250000),
		Synonyms: []string{"OP", "ONE PIECE"},
		Studios:  []string{"Ignored"},
		StreamingLinks: []anilist.LinkRef{
			{Site: "MANGA Plus", URL: "https://mangaplus.shueisha.co.jp/titles/100020", Type: "STREAMING"},
			{Site: "Twitter", URL: "https://twitter.com/x", Type: "SOCIAL"},
		},
		BannerURL: "https://s4.anilist.co/banner/30013.jpg",
		Trailer:   &anilist.TrailerRef{ID: "x8abc12", Site: "dailymotion"},
	}
	d := ItemData(m)
	f := d.Fields
	for key, want := range map[string]string{"release_status": "hiatus", "country": "JP", "adult": "no"} {
		if f[key].Text != want {
			t.Errorf("%s = %q, want %q", key, f[key].Text, want)
		}
	}
	if v := f["community_score"].Num; v == nil || *v != 92 {
		t.Errorf("community score = %v", v)
	}
	if v := f["popularity"].Num; v == nil || *v != 250000 {
		t.Errorf("popularity = %v", v)
	}
	for _, key := range []string{"season", "studio"} {
		if v, ok := f[key]; ok {
			t.Errorf("%s set on a manga: %+v", key, v)
		}
	}
	if want := []plugin.Title{{Lang: "ja-latn", Text: "ONE PIECE"}, {Lang: plugin.AltTitleLang, Text: "OP"}}; !slices.Equal(d.Titles, want) {
		t.Errorf("titles = %+v", d.Titles)
	}
	if want := []plugin.Link{{Label: "MANGA Plus", URL: "https://mangaplus.shueisha.co.jp/titles/100020"}}; !slices.Equal(d.Links, want) {
		t.Errorf("links = %+v", d.Links)
	}
	if d.BannerURL != "https://s4.anilist.co/banner/30013.jpg" {
		t.Errorf("banner = %q", d.BannerURL)
	}
	if d.Trailer == nil || *d.Trailer != (plugin.Trailer{URL: "https://www.dailymotion.com/video/x8abc12"}) {
		t.Errorf("trailer = %+v", d.Trailer)
	}
	if err := plugin.Validate(manifest()); err != nil {
		t.Fatal(err)
	}
}

func TestManifestExtras(t *testing.T) {
	k := manifest().Kinds[0]
	var keys, cols, defaults []string
	f := map[string]plugin.FieldDef{}
	for _, d := range k.Fields {
		keys = append(keys, d.Key)
		f[d.Key] = d
	}
	if want := []string{"type", "release_status", "chapters", "volumes", "year", "author", "magazine", "synopsis", "started_on", "ended_on",
		"country", "adult", "community_score", "popularity"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("fields = %v", keys)
	}
	for _, c := range k.Columns {
		cols = append(cols, c.Key)
		if c.Default {
			defaults = append(defaults, c.Key)
		}
	}
	if want := []string{"type", "year", "progress", "rating", "status", "updated", "chapters", "volumes", "author", "magazine", "started_on", "ended_on", "release_status"}; !reflect.DeepEqual(cols, want) {
		t.Errorf("columns = %v", cols)
	}
	if want := []string{"type", "year", "progress", "rating", "status", "updated"}; !reflect.DeepEqual(defaults, want) {
		t.Errorf("default columns = %v", defaults)
	}
	wantFields := []plugin.FieldDef{
		{Key: "release_status", Label: "Release status", Type: plugin.FieldEnum, EnumValues: []string{"finished", "releasing", "upcoming", "hiatus", "cancelled"}, Show: true, Editable: true, Filterable: true},
		{Key: "country", Label: "Country", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true},
		{Key: "adult", Label: "Adult", Type: plugin.FieldEnum, EnumValues: []string{"yes", "no"}, Show: true, Editable: true, Filterable: true},
		{Key: "community_score", Label: "Community score", Type: plugin.FieldNumber, Unit: "%", Min: num(0), Max: num(100), Show: true, Sortable: true},
		{Key: "popularity", Label: "Popularity", Type: plugin.FieldNumber, Min: num(0), Max: num(100000000), Show: true, Sortable: true},
	}
	for _, w := range wantFields {
		if !reflect.DeepEqual(f[w.Key], w) {
			t.Errorf("%s = %+v, want %+v", w.Key, f[w.Key], w)
		}
	}
	if !k.AltTitles || !k.Banner || !k.Trailer || k.AdultField != "adult" {
		t.Errorf("alt titles/banner/trailer/adult = %v/%v/%v/%q", k.AltTitles, k.Banner, k.Trailer, k.AdultField)
	}
	if !slices.ContainsFunc(k.Sections, func(s plugin.SectionDef) bool { return s.Type == plugin.SectionLinks }) {
		t.Error("no links section")
	}
}
