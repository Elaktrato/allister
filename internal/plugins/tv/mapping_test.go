package tv

import (
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func TestShowDataAndCredits(t *testing.T) {
	day := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	rec := showRecord{
		Source: "tvmaze", ID: "1",
		Titles: []plugin.Title{{Lang: "en", Text: "Dark"}, {Lang: "original", Text: "Dark"}},
		Fields: map[string]plugin.FieldValue{
			"type": {Text: "series"}, "year": {Num: num(2017)}, "started_on": {Date: &day},
			"synopsis": {Text: "line one\n\nline two"},
		},
		CoverURL: "https://img.example/d.jpg", ExternalURL: "https://tvmaze.com/shows/1",
		ExternalIDs: map[string]string{"tvmaze": "1", "tvdb": "2"},
		Genres:      []string{"Mystery", "mystery", "Drama"}, Tags: []plugin.Tag{{Name: "Time Travel"}},
		Credits: []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "character-1", Name: "Jonas"},
			With: &plugin.PersonRef{ID: "person-2", Name: "Louis Actor"}, WithRole: "actor"}},
	}
	d := showData(rec)
	if d.Kind != KindShow || d.ID != "1" || len(d.Titles) != 2 || d.Fields["type"].Text != "series" ||
		d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2017 || d.Fields["started_on"].Date == nil ||
		len(d.Genres) != 2 || len(d.Tags) != 1 || d.ExternalIDs["tvdb"] != "2" || d.CoverURL != rec.CoverURL ||
		!strings.Contains(d.Fields["synopsis"].Text, "\n\n") {
		t.Fatalf("data = %+v", d)
	}
	c := showCredits(rec.Credits)
	if len(c) != 1 || c[0].Person.ID != "character-1" || c[0].With == nil || c[0].With.ID != "person-2" {
		t.Fatalf("credits = %+v", c)
	}
}

func TestShowDataCaps(t *testing.T) {
	r := showRecord{Source: "tvmaze", ID: "1", Titles: []plugin.Title{{Lang: "en", Text: strings.Repeat("x", 400)}},
		Genres: []string{strings.Repeat("g", 80)}, Tags: []plugin.Tag{{Name: strings.Repeat("t", 80)}}}
	d := showData(r)
	if len([]rune(d.Titles[0].Text)) != maxTitleRunes || len([]rune(d.Genres[0])) != maxTermRunes || len([]rune(d.Tags[0].Name)) != maxTermRunes {
		t.Fatalf("caps = %+v %+v", d.Titles, d.Genres)
	}
}
