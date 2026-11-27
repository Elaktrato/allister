package book

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

type fakeNovelClient struct {
	search  []anilist.SearchResult
	media   anilist.Media
	byMAL   anilist.Media
	account []anilist.AccountEntry
	err     error
	format  string
	queries []string

	// the batch format lookup
	formats     map[int]string
	formatErr   error
	formatType  string
	formatIDs   []int
	formatCalls int
}

func (f *fakeNovelClient) SearchFormat(_ context.Context, _, format, q string) ([]anilist.SearchResult, error) {
	f.format = format
	f.queries = append(f.queries, q)
	return f.search, f.err
}
func (f *fakeNovelClient) Media(context.Context, string, string) (anilist.Media, error) {
	return f.media, f.err
}
func (f *fakeNovelClient) MediaByMALID(context.Context, string, string) (anilist.Media, error) {
	return f.byMAL, f.err
}
func (f *fakeNovelClient) MediaList(context.Context, string, string) ([]anilist.AccountEntry, error) {
	return f.account, f.err
}
func (f *fakeNovelClient) MediaFormatsByMALIDs(_ context.Context, typ string, malIDs []int) (map[int]string, error) {
	f.formatCalls++
	f.formatType = typ
	f.formatIDs = append(f.formatIDs, malIDs...)
	if f.formatErr != nil {
		return nil, f.formatErr
	}
	return f.formats, nil
}

func novelMedia() anilist.Media {
	date := time.Date(2009, 4, 10, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 11, 2, 0, 0, 0, 0, time.UTC)
	return anilist.Media{
		SearchResult: anilist.SearchResult{
			Source: anilist.AniList, ID: "51479", Type: "NOVEL",
			Titles: []anilist.Title{{Lang: "ja", Text: "ソードアート・オンライン"}, {Lang: "ja-latn", Text: "Sword Art Online"}, {Lang: "en", Text: "Sword Art Online"}},
			Year:   ip(2009), Volumes: ip(12), Chapters: ip(120), CoverURL: "https://img.example/sao.jpg",
			Synopsis: "A death game.",
		},
		StartDate: &date, EndDate: &endDate, Format: "NOVEL", MALID: "21479", ExternalURL: "https://anilist.co/manga/51479",
		Genres: []string{"Fantasy", "Romance"},
		Tags:   []anilist.Tag{{Name: "Isekai", Rank: ip(90), Spoiler: true}},
		Staff: []anilist.StaffMember{
			{Role: "Story", Name: "Reki Kawahara", URL: "https://anilist.co/staff/1", ImageURL: "https://img.example/rk.jpg"},
			{Role: "Art", Name: "abec", URL: "https://anilist.co/staff/2"},
		},
	}
}

func TestNovelSearchFiltersNonNovels(t *testing.T) {
	f := &fakeNovelClient{search: []anilist.SearchResult{
		{Source: anilist.AniList, ID: "1", Type: "MANGA", Titles: []anilist.Title{{Lang: "en", Text: "Manga"}}},
		{Source: anilist.AniList, ID: "2", Type: "NOVEL", Titles: []anilist.Title{{Lang: "en", Text: "Novel"}}},
	}}
	s := &anilistSource{c: f}
	res, err := s.Search(context.Background(), KindSeries, "sao")
	if err != nil || len(res) != 1 || res[0].ID != "2" || res[0].Kind != KindSeries {
		t.Fatalf("%+v %v", res, err)
	}
	if f.format != "NOVEL" || len(f.queries) != 1 || f.queries[0] != "sao" {
		t.Fatalf("format=%q queries=%v", f.format, f.queries)
	}
}

func TestNovelDetailsMapsFieldsAndStaff(t *testing.T) {
	s := &anilistSource{c: &fakeNovelClient{media: novelMedia()}}
	d, err := s.Details(context.Background(), KindSeries, "51479")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != KindSeries || d.ID != "51479" || d.ExternalIDs["anilist"] != "51479" || d.ExternalIDs["mal"] != "21479" {
		t.Fatalf("data = %+v", d)
	}
	if d.Fields["author"].Text != "Reki Kawahara" || d.Fields["volumes"].Num == nil || *d.Fields["volumes"].Num != 12 ||
		d.Fields["chapters"].Num == nil || *d.Fields["chapters"].Num != 120 ||
		d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2009 ||
		d.Fields["published_on"].Date == nil || d.Fields["published_on"].Date.Format("2006-01-02") != "2009-04-10" ||
		d.Fields["ended_on"].Date == nil || d.Fields["ended_on"].Date.Format("2006-01-02") != "2024-11-02" {
		t.Fatalf("fields = %+v", d.Fields)
	}
	if len(d.Titles) != 3 || d.Titles[1].Text != "Sword Art Online" || len(d.Genres) != 2 ||
		len(d.Tags) != 1 || d.Tags[0].Name != "Isekai" || !d.Tags[0].Spoiler {
		t.Fatalf("titles/genres/tags = %+v %v %+v", d.Titles, d.Genres, d.Tags)
	}
	people, err := s.People(context.Background(), KindSeries, "51479")
	if err != nil || len(people) != 2 || people[0].Role != "author" || people[0].Person.ID != "anilist-1" ||
		people[1].Role != "illustrator" || people[1].Person.ID != "anilist-2" {
		t.Fatalf("people = %+v %v", people, err)
	}
}

func TestNovelDetailsRejectsManga(t *testing.T) {
	m := novelMedia()
	m.Format = "MANGA"
	s := &anilistSource{c: &fakeNovelClient{media: m}}
	if _, err := s.Details(context.Background(), KindSeries, "51479"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestNovelLookupByMALID(t *testing.T) {
	s := &anilistSource{c: &fakeNovelClient{byMAL: novelMedia()}}
	d, err := s.Lookup(context.Background(), KindSeries, "mal", "21479")
	if err != nil || d.ID != "51479" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestNovelErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"not found", anilist.ErrNotFound, plugin.ErrNotFound},
		{"rate limited", anilist.ErrRateLimited, plugin.ErrRateLimited},
		{"unavailable", anilist.ErrUnavailable, plugin.ErrUnavailable},
		{"bad response", anilist.ErrBadResponse, plugin.ErrBadResponse},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &anilistSource{c: &fakeNovelClient{err: c.err}}
			_, err := s.Search(context.Background(), KindSeries, "x")
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestNovelDetailsWithoutEndDate(t *testing.T) {
	m := novelMedia()
	m.EndDate = nil
	s := &anilistSource{c: &fakeNovelClient{media: m}}
	d, err := s.Details(context.Background(), KindSeries, "51479")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Fields["ended_on"]; ok {
		t.Fatalf("ended_on set without an end date: %+v", d.Fields["ended_on"])
	}
}

// TestNovelDetailsExtras: a light novel gets the AniList extras except
// season and studios.
func TestNovelDetailsExtras(t *testing.T) {
	m := novelMedia()
	m.Status, m.Season, m.Country, m.Adult = "NOT_YET_RELEASED", "SPRING", "JP", true
	m.AverageScore, m.Popularity = ip(71), ip(9000)
	m.Synonyms = []string{"SAO", "Sword Art Online", "SAO"}
	m.Studios = []string{"Ignored"}
	m.StreamingLinks = []anilist.LinkRef{
		{Site: "BOOK WALKER", URL: "https://bookwalker.jp/sao", Type: "STREAMING"},
		{Site: "Official Site", URL: "https://sao.example", Type: "INFO"},
	}
	m.BannerURL = "https://s4.anilist.co/banner/51479.jpg"
	m.Trailer = &anilist.TrailerRef{ID: "abc_DEF-1", Site: "youtube", Thumbnail: "https://i.ytimg.com/vi/abc_DEF-1/hqdefault.jpg"}
	d := novelData(m)
	f := d.Fields
	for key, want := range map[string]string{"release_status": "upcoming", "country": "JP", "adult": "yes"} {
		if f[key].Text != want {
			t.Errorf("%s = %q, want %q", key, f[key].Text, want)
		}
	}
	if v := f["community_score"].Num; v == nil || *v != 71 {
		t.Errorf("community score = %v", v)
	}
	if v := f["popularity"].Num; v == nil || *v != 9000 {
		t.Errorf("popularity = %v", v)
	}
	for _, key := range []string{"season", "studio"} {
		if v, ok := f[key]; ok {
			t.Errorf("%s set on a light novel: %+v", key, v)
		}
	}
	alts := []plugin.Title{}
	for _, ti := range d.Titles {
		if ti.Lang == plugin.AltTitleLang {
			alts = append(alts, ti)
		}
	}
	if len(alts) != 1 || alts[0].Text != "SAO" || len(d.Titles) != 4 {
		t.Errorf("titles = %+v", d.Titles)
	}
	if len(d.Links) != 1 || d.Links[0] != (plugin.Link{Label: "BOOK WALKER", URL: "https://bookwalker.jp/sao"}) {
		t.Errorf("links = %+v", d.Links)
	}
	if d.BannerURL != "https://s4.anilist.co/banner/51479.jpg" {
		t.Errorf("banner = %q", d.BannerURL)
	}
	if want := (plugin.Trailer{URL: "https://www.youtube.com/watch?v=abc_DEF-1", ThumbnailURL: "https://i.ytimg.com/vi/abc_DEF-1/hqdefault.jpg"}); d.Trailer == nil || *d.Trailer != want {
		t.Errorf("trailer = %+v", d.Trailer)
	}
}

// TestNovelSummaryCarriesTheAdultFlag: a light novel search result carries
// the adult value, so the core can hide it.
func TestNovelSummaryCarriesTheAdultFlag(t *testing.T) {
	if got := novelSummary(anilist.SearchResult{Source: anilist.AniList, ID: "1", Adult: true}).Fields["adult"].Text; got != "yes" {
		t.Errorf("adult = %q", got)
	}
	if got := novelSummary(anilist.SearchResult{Source: anilist.AniList, ID: "1"}).Fields["adult"].Text; got != "no" {
		t.Errorf("not adult = %q", got)
	}
}
