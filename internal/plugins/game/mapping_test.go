package game

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func TestGameData(t *testing.T) {
	day := time.Date(2011, 11, 11, 0, 0, 0, 0, time.UTC)
	rec := gameRecord{
		Source: SourceIGDB, ID: "9606",
		Titles: []plugin.Title{
			{Lang: "en", Text: "The Elder Scrolls V: Skyrim"},
			{Lang: "ja", Text: "スカイリム"},
			{Lang: "", Text: "dropped"},
			{Lang: "en", Text: ""},
		},
		Fields: map[string]plugin.FieldValue{
			"year":          {Num: num(2011)},
			"released_on":   {Date: &day},
			"platforms":     {Text: "PC\nPlayStation 3"},
			"developer":     {Text: "Bethesda Game Studios"},
			"publisher":     {Text: "Bethesda Softworks"},
			"time_main":     {Num: num(2000)},
			"time_extra":    {Num: num(3500)},
			"time_complete": {Num: num(9000)},
			"summary":       {Text: "line one\n\nline two"},
			"":              {Text: "dropped"},
		},
		CoverURL: "https://img.example/skyrim.jpg", ExternalURL: "https://www.igdb.com/games/the-elder-scrolls-v-skyrim",
		ExternalIDs: map[string]string{"igdb": "9606", "steam": "72850", "": "empty", "mobygames": ""},
		Genres:      []string{"Role-playing (RPG)", "role-playing (rpg)", "Adventure"},
		Tags: []plugin.Tag{
			{Name: "Fantasy"},
			{Name: "fantasy"},
			{Name: "Open world", Spoiler: true},
		},
		Recommendations: []plugin.ItemSummary{
			{Source: SourceIGDB, ID: "1029", Titles: []plugin.Title{{Lang: "en", Text: "The Elder Scrolls V: Skyrim"}}},
		},
	}
	d := gameData(rec)
	if d.Source != SourceIGDB || d.ID != "9606" || d.Kind != KindGame {
		t.Fatalf("summary = %+v", d.ItemSummary)
	}
	if len(d.Titles) != 2 || d.Titles[0].Text != "The Elder Scrolls V: Skyrim" || d.Titles[0].Lang != "en" ||
		d.Titles[1].Text != "スカイリム" || d.Titles[1].Lang != "ja" {
		t.Fatalf("titles = %+v", d.Titles)
	}
	if d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2011 {
		t.Fatalf("year = %+v", d.Fields["year"])
	}
	if d.Fields["released_on"].Date == nil || !d.Fields["released_on"].Date.Equal(day) {
		t.Fatalf("released_on = %+v", d.Fields["released_on"])
	}
	if d.Fields["platforms"].Text != "PC PlayStation 3" || d.Fields["developer"].Text != "Bethesda Game Studios" ||
		d.Fields["publisher"].Text != "Bethesda Softworks" {
		t.Fatalf("text fields = %+v", d.Fields)
	}
	for key, want := range map[string]float64{"time_main": 2000, "time_extra": 3500, "time_complete": 9000} {
		if d.Fields[key].Num == nil || *d.Fields[key].Num != want {
			t.Errorf("%s = %+v", key, d.Fields[key])
		}
	}
	if !strings.Contains(d.Fields["summary"].Text, "\n\n") {
		t.Fatalf("summary = %q", d.Fields["summary"].Text)
	}
	if _, ok := d.Fields[""]; ok {
		t.Fatalf("empty field key kept: %+v", d.Fields)
	}
	if len(d.ExternalIDs) != 2 || d.ExternalIDs["igdb"] != "9606" || d.ExternalIDs["steam"] != "72850" {
		t.Fatalf("external ids = %+v", d.ExternalIDs)
	}
	if d.CoverURL != rec.CoverURL || d.ExternalURL != rec.ExternalURL {
		t.Fatalf("urls = %q %q", d.CoverURL, d.ExternalURL)
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Role-playing (RPG)" || d.Genres[1] != "Adventure" {
		t.Fatalf("genres = %+v", d.Genres)
	}
	if len(d.Tags) != 2 || d.Tags[0].Name != "Fantasy" || d.Tags[1].Name != "Open world" || !d.Tags[1].Spoiler {
		t.Fatalf("tags = %+v", d.Tags)
	}

	recs := gameRecommendations(rec.Recommendations)
	if len(recs) != 1 || recs[0].Kind != KindGame || recs[0].ID != "1029" ||
		recs[0].Titles[0].Text != "The Elder Scrolls V: Skyrim" {
		t.Fatalf("recommendations = %+v", recs)
	}
}

func TestGameCaps(t *testing.T) {
	day := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	r := gameRecord{
		Source: strings.Repeat("s", 200), ID: strings.Repeat("i", 200),
		Titles: []plugin.Title{{Lang: "en", Text: strings.Repeat("x", 400)}},
		Fields: map[string]plugin.FieldValue{
			"summary":     {Text: strings.Repeat("y", 30000)},
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
	var rs []plugin.ItemSummary
	for i := 0; i < 30; i++ {
		rs = append(rs, plugin.ItemSummary{
			Source: strings.Repeat("s", 200), ID: fmt.Sprintf("rec-%02d", i),
			Titles: []plugin.Title{{Lang: "en", Text: strings.Repeat("r", 400)}},
		})
	}

	d := gameData(r)
	if len([]rune(d.Source)) != maxIDRunes || len([]rune(d.ID)) != maxIDRunes {
		t.Fatalf("id caps = %q %q", d.Source, d.ID)
	}
	if len([]rune(d.Titles[0].Text)) != maxTitleRunes {
		t.Fatalf("title cap = %d", len([]rune(d.Titles[0].Text)))
	}
	if len([]rune(d.Fields["summary"].Text)) != maxTextRunes {
		t.Fatalf("text cap = %d", len([]rune(d.Fields["summary"].Text)))
	}
	if len(d.Genres) != maxGenres || len([]rune(d.Genres[0])) != maxTermRunes || len([]rune(d.Tags[0].Name)) != maxTermRunes {
		t.Fatalf("term caps = %d %q", len(d.Genres), d.Genres[0])
	}
	if len(d.Tags) != maxTags {
		t.Fatalf("tag cap = %d", len(d.Tags))
	}

	recs := gameRecommendations(rs)
	if len(recs) != maxRecs {
		t.Fatalf("rec cap = %d", len(recs))
	}
	for _, rec := range recs {
		if len([]rune(rec.Source)) != maxIDRunes || len([]rune(rec.Titles[0].Text)) != maxTitleRunes {
			t.Fatalf("rec caps = %+v", rec)
		}
	}
}

func TestGameDropsEmptyValues(t *testing.T) {
	d := gameData(gameRecord{
		Source: " ", ID: "",
		CoverURL: "ftp://example/x.jpg", ExternalURL: "javascript:alert(1)",
		Fields: map[string]plugin.FieldValue{
			"blank": {},
			"year":  {Num: num(0)},
		},
		Genres: []string{" ", ""},
		Tags:   []plugin.Tag{{Name: " "}},
	})
	if d.Source != "" || d.ID != "" || d.Kind != KindGame {
		t.Fatalf("summary = %+v", d.ItemSummary)
	}
	if d.CoverURL != "" || d.ExternalURL != "" {
		t.Fatalf("urls = %q %q", d.CoverURL, d.ExternalURL)
	}
	if len(d.ExternalIDs) != 0 {
		t.Fatalf("external ids = %+v", d.ExternalIDs)
	}
	if len(d.Genres) != 0 || len(d.Tags) != 0 {
		t.Fatalf("terms = %+v %+v", d.Genres, d.Tags)
	}
	if _, ok := d.Fields["blank"]; ok {
		t.Fatalf("empty field kept: %+v", d.Fields)
	}
	if d.Fields["year"].Num == nil || *d.Fields["year"].Num != 0 {
		t.Fatalf("explicit zero num dropped: %+v", d.Fields["year"])
	}
}

func TestGameDropsEmptyRecommendations(t *testing.T) {
	rs := []plugin.ItemSummary{
		{ID: "no-source", Titles: []plugin.Title{{Lang: "en", Text: "A"}}},
		{Source: SourceIGDB, Titles: []plugin.Title{{Lang: "en", Text: "B"}}},
		{Source: SourceIGDB, ID: "no-title"},
		{Source: SourceIGDB, ID: "has-title", Titles: []plugin.Title{{Lang: "", Text: "C"}}},
		{Source: SourceIGDB, ID: "9606", CoverURL: "ftp://example/x.jpg",
			Titles: []plugin.Title{{Lang: "en", Text: "Kept"}}},
	}
	out := gameRecommendations(rs)
	if len(out) != 1 {
		t.Fatalf("recommendations = %+v", out)
	}
	if out[0].ID != "9606" || out[0].Kind != KindGame || out[0].CoverURL != "" || out[0].Titles[0].Text != "Kept" {
		t.Fatalf("kept = %+v", out[0])
	}
}
