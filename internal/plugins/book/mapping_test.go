package book

import (
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func TestBookDataMapsTheBook(t *testing.T) {
	date := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	r := bookRecord{
		Source: "openlibrary", ID: "OL1", Title: "Der Wüstenplanet", Subtitle: "Roman",
		Author: "Frank Herbert", ISBN: "9780441013593", Publisher: "Heyne", Language: "de",
		Synopsis: "Line one.\nLine two.", CoverURL: "https://covers.example/1.jpg",
		ExternalURL: "https://openlibrary.org/works/OL1", Series: "Dune",
		Pages: 300, Year: 2001, PublishedOn: &date,
		Genres:      []string{"Fantasy", "fantasy", "Science Fiction"},
		Tags:        []plugin.Tag{{Name: "Desert", Rank: ip(90), Spoiler: true}, {Name: "desert"}},
		ExternalIDs: map[string]string{"isbn": "9780441013593"},
	}
	d := bookData(r)
	if d.Kind != KindBook || d.Source != "openlibrary" || d.ID != "OL1" {
		t.Fatalf("summary = %+v", d)
	}
	if len(d.Titles) != 1 || d.Titles[0] != (plugin.Title{Lang: "de", Text: "Der Wüstenplanet: Roman"}) {
		t.Fatalf("titles = %+v", d.Titles)
	}
	for key, want := range map[string]string{
		"author": "Frank Herbert", "publisher": "Heyne", "isbn": "9780441013593",
		"series": "Dune", "synopsis": "Line one.\nLine two.",
	} {
		if got := d.Fields[key].Text; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if d.Fields["pages"].Num == nil || *d.Fields["pages"].Num != 300 ||
		d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2001 {
		t.Fatalf("numbers = %+v", d.Fields)
	}
	if d.Fields["published_on"].Date == nil || d.Fields["published_on"].Date.Format("2006-01-02") != "2001-02-03" {
		t.Fatalf("published_on = %+v", d.Fields["published_on"])
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Fantasy" || d.Genres[1] != "Science Fiction" {
		t.Fatalf("genres = %v", d.Genres)
	}
	if len(d.Tags) != 1 || d.Tags[0].Name != "Desert" || d.Tags[0].Rank == nil || *d.Tags[0].Rank != 90 || !d.Tags[0].Spoiler {
		t.Fatalf("tags = %+v", d.Tags)
	}
	if d.CoverURL != r.CoverURL || d.ExternalURL != r.ExternalURL || d.ExternalIDs["isbn"] != r.ISBN {
		t.Fatalf("links = %+v", d)
	}
}

func TestBookDataCapsAndEmptyValues(t *testing.T) {
	r := bookRecord{
		Title: "T", Language: "xx", Genres: []string{strings.Repeat("g", 80)},
		Tags: []plugin.Tag{{}, {Name: strings.Repeat("t", 80)}}, ExternalIDs: map[string]string{"isbn": ""},
	}
	d := bookData(r)
	if d.Titles[0].Lang != "xx" {
		t.Fatalf("language = %+v", d.Titles)
	}
	if len(d.Genres) != 1 || len([]rune(d.Genres[0])) != maxTermRunes {
		t.Fatalf("genres = %v", d.Genres)
	}
	if len(d.Tags) != 1 || len([]rune(d.Tags[0].Name)) != maxTermRunes {
		t.Fatalf("tags = %+v", d.Tags)
	}
	if d.ExternalIDs["isbn"] != "" {
		t.Fatalf("empty external id kept: %v", d.ExternalIDs)
	}
}

func TestBookCredits(t *testing.T) {
	r := bookRecord{Credits: []plugin.Credit{
		{Role: " author ", Person: plugin.PersonRef{ID: "a1", Name: " Ursula ", URL: "https://authors.example/a1", ImageURL: "https://img.example/a1.jpg"}},
		{Role: "author", Person: plugin.PersonRef{}}, // no name/id: dropped
		{Role: "illustrator", Person: plugin.PersonRef{ID: "a2", Name: "Artist"}},
	}}
	c := bookCredits(r)
	if len(c) != 2 || c[0].Role != "author" || c[0].Person.Name != "Ursula" ||
		c[1].Person.ID != "a2" || c[1].Role != "illustrator" {
		t.Fatalf("credits = %+v", c)
	}
}

func ip(i int) *int { return &i }
