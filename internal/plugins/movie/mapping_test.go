package movie

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func TestMovieDataAndCredits(t *testing.T) {
	day := time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC)
	rec := movieRecord{
		Source: SourceTMDB, ID: "603",
		Titles: []plugin.Title{
			{Lang: "en", Text: "The Matrix"},
			{Lang: "original", Text: "The Matrix"},
			{Lang: "", Text: "dropped"},
		},
		Fields: map[string]plugin.FieldValue{
			"year": {Num: num(1999)}, "runtime": {Num: num(136)},
			"director": {Text: "Lana Wachowski"}, "released_on": {Date: &day},
			"synopsis": {Text: "line one\n\nline two"},
		},
		CoverURL: "https://img.example/m.jpg", ExternalURL: "https://www.themoviedb.org/movie/603",
		ExternalIDs: map[string]string{"tmdb": "603", "imdb": "tt0133093", "": "empty"},
		Genres:      []string{"Action", "action", "Science Fiction"},
		Tags:        []plugin.Tag{{Name: "Cyberpunk"}, {Name: "cyberpunk"}},
		Credits: []plugin.Credit{
			{Role: "actor", Person: plugin.PersonRef{ID: "character-1", Name: "Neo"},
				With: &plugin.PersonRef{ID: "person-2", Name: "Keanu Reeves"}, WithRole: "actor"},
			{Role: "director", Person: plugin.PersonRef{ID: "person-3", Name: "Lana Wachowski"}},
		},
		Recommendations: []plugin.ItemSummary{
			{Source: SourceTMDB, ID: "604", Titles: []plugin.Title{{Lang: "en", Text: "The Matrix Reloaded"}}},
		},
	}
	d := movieData(rec)
	if d.Source != SourceTMDB || d.ID != "603" || d.Kind != KindMovie {
		t.Fatalf("summary = %+v", d.ItemSummary)
	}
	if len(d.Titles) != 2 || d.Titles[0].Text != "The Matrix" || d.Titles[1].Lang != "original" {
		t.Fatalf("titles = %+v", d.Titles)
	}
	if d.Fields["year"].Num == nil || *d.Fields["year"].Num != 1999 || d.Fields["runtime"].Num == nil ||
		*d.Fields["runtime"].Num != 136 || d.Fields["director"].Text != "Lana Wachowski" ||
		d.Fields["released_on"].Date == nil || !d.Fields["released_on"].Date.Equal(day) ||
		!strings.Contains(d.Fields["synopsis"].Text, "\n\n") {
		t.Fatalf("fields = %+v", d.Fields)
	}
	if len(d.ExternalIDs) != 2 || d.ExternalIDs["tmdb"] != "603" || d.ExternalIDs["imdb"] != "tt0133093" {
		t.Fatalf("external ids = %+v", d.ExternalIDs)
	}
	if d.CoverURL != rec.CoverURL || d.ExternalURL != rec.ExternalURL {
		t.Fatalf("urls = %q %q", d.CoverURL, d.ExternalURL)
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Action" || d.Genres[1] != "Science Fiction" {
		t.Fatalf("genres = %+v", d.Genres)
	}
	if len(d.Tags) != 1 || d.Tags[0].Name != "Cyberpunk" {
		t.Fatalf("tags = %+v", d.Tags)
	}

	c := movieCredits(rec.Credits)
	if len(c) != 2 {
		t.Fatalf("credits = %+v", c)
	}
	if c[0].Role != "actor" || c[0].Person.ID != "character-1" || c[0].Person.Name != "Neo" ||
		c[0].With == nil || c[0].With.ID != "person-2" || c[0].With.Name != "Keanu Reeves" ||
		c[0].WithRole != "actor" {
		t.Fatalf("cast = %+v", c[0])
	}
	if c[1].Role != "director" || c[1].Person.ID != "person-3" || c[1].With != nil {
		t.Fatalf("crew = %+v", c[1])
	}

	recs := movieRecommendations(rec.Recommendations)
	if len(recs) != 1 || recs[0].Kind != KindMovie || recs[0].ID != "604" ||
		recs[0].Titles[0].Text != "The Matrix Reloaded" {
		t.Fatalf("recommendations = %+v", recs)
	}
}

func TestMovieCaps(t *testing.T) {
	day := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	r := movieRecord{
		Source: strings.Repeat("s", 200), ID: strings.Repeat("i", 200),
		Titles: []plugin.Title{{Lang: "en", Text: strings.Repeat("x", 400)}},
		Fields: map[string]plugin.FieldValue{
			"synopsis":    {Text: strings.Repeat("y", 30000)},
			"released_on": {Date: &day},
		},
		Tags: []plugin.Tag{{Name: strings.Repeat("t", 80)}},
	}
	r.Genres = append(r.Genres, strings.Repeat("g", 80))
	for i := 0; i < 60; i++ {
		r.Genres = append(r.Genres, fmt.Sprintf("genre-%02d", i))
	}
	for i := 0; i < 120; i++ {
		r.Tags = append(r.Tags, plugin.Tag{Name: fmt.Sprintf("tag-%03d", i)})
	}
	for i := 0; i < 600; i++ {
		r.Credits = append(r.Credits, plugin.Credit{
			Role: "actor", Person: plugin.PersonRef{ID: strings.Repeat("p", 200), Name: strings.Repeat("n", 400)},
		})
	}
	var rs []plugin.ItemSummary
	for i := 0; i < 30; i++ {
		rs = append(rs, plugin.ItemSummary{
			Source: strings.Repeat("s", 200), ID: fmt.Sprintf("rec-%02d", i),
			Titles: []plugin.Title{{Lang: "en", Text: strings.Repeat("r", 400)}},
		})
	}

	d := movieData(r)
	if len([]rune(d.Source)) != maxIDRunes || len([]rune(d.ID)) != maxIDRunes {
		t.Fatalf("id caps = %q %q", d.Source, d.ID)
	}
	if len([]rune(d.Titles[0].Text)) != maxTitleRunes {
		t.Fatalf("title cap = %d", len([]rune(d.Titles[0].Text)))
	}
	if len([]rune(d.Fields["synopsis"].Text)) != maxTextRunes {
		t.Fatalf("text cap = %d", len([]rune(d.Fields["synopsis"].Text)))
	}
	if len(d.Genres) != maxGenres || len([]rune(d.Genres[0])) != maxTermRunes || len([]rune(d.Tags[0].Name)) != maxTermRunes {
		t.Fatalf("term caps = %d %q", len(d.Genres), d.Genres[0])
	}
	if len(d.Tags) != maxTags {
		t.Fatalf("tag cap = %d", len(d.Tags))
	}

	c := movieCredits(r.Credits)
	if len(c) != maxCredits || len([]rune(c[0].Person.ID)) != maxIDRunes || len([]rune(c[0].Person.Name)) != maxNameRunes {
		t.Fatalf("credit caps = %d", len(c))
	}

	recs := movieRecommendations(rs)
	if len(recs) != maxRecs {
		t.Fatalf("rec cap = %d", len(recs))
	}
	for _, rec := range recs {
		if len([]rune(rec.Source)) != maxIDRunes || len([]rune(rec.Titles[0].Text)) != maxTitleRunes {
			t.Fatalf("rec caps = %+v", rec)
		}
	}
}

func TestMovieDropsEmptyRecommendations(t *testing.T) {
	rs := []plugin.ItemSummary{
		{ID: "no-source", Titles: []plugin.Title{{Lang: "en", Text: "A"}}},
		{Source: SourceTMDB, Titles: []plugin.Title{{Lang: "en", Text: "B"}}},
		{Source: SourceTMDB, ID: "no-title"},
		{Source: SourceTMDB, ID: "has-title", Titles: []plugin.Title{{Lang: "", Text: "C"}}},
		{Source: SourceTMDB, ID: "604", CoverURL: "ftp://example/x.jpg",
			Titles: []plugin.Title{{Lang: "en", Text: "Kept"}}},
	}
	out := movieRecommendations(rs)
	if len(out) != 1 {
		t.Fatalf("recommendations = %+v", out)
	}
	if out[0].ID != "604" || out[0].Kind != KindMovie || out[0].CoverURL != "" || out[0].Titles[0].Text != "Kept" {
		t.Fatalf("kept = %+v", out[0])
	}
}
