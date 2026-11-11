package movie

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

const yamtrackMovieCSV = `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
101,tmdb,movie,The Movie,https://img/movie.jpg,,,8.0,Completed,a comment,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,1
102,tmdb,movie,Planned Movie,,,,7.0,Planning,,,,1
103,manual,movie,Manual Movie,,,,6.0,Completed,,,,1
104,tvdb,movie,Wrong Source,,,,5.0,Completed,,,,1
105,tmdb,movie,Bad Status,,,,,Sometimes,,,,1
101,tmdb,movie,The Movie,,,,8.0,Completed,,,,1
2001,igdb,game,Game,,,,,Completed,,,,
,tmdb,movie,,,,,,Completed,,,,1
106,tmdb,movie,,,,,9.0,Completed,,,,1
107,tmdb,MOVIE,Upper Case,,,,9.0,Completed,,,,1
108,tmdb,movie,Blank Status,,,,4.0,,,,,1
`

func TestYamtrackMovieRows(t *testing.T) {
	res, err := yamtrackMovieImporter{}.Parse(context.Background(), strings.NewReader(yamtrackMovieCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 6 || res.Ignored != 1 || len(res.Skipped) != 4 {
		t.Fatalf("entries=%+v ignored=%d skipped=%+v", res.Entries, res.Ignored, res.Skipped)
	}
	wantTitles := []string{"The Movie", "Planned Movie", "Manual Movie", "Wrong Source", "TMDB #106", "Upper Case"}
	for i, want := range wantTitles {
		if res.Entries[i].Title != want || res.Entries[i].Kind != KindMovie {
			t.Fatalf("entry %d = %+v", i, res.Entries[i])
		}
		if len(res.Entries[i].Progress) != 0 {
			t.Fatalf("entry %d has progress tracks: %+v", i, res.Entries[i].Progress)
		}
	}
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	first := res.Entries[0]
	if first.Status != plugin.Completed || first.Rating == nil || *first.Rating != 8 ||
		first.StartedOn == nil || !first.StartedOn.Equal(started) ||
		first.FinishedOn == nil || !first.FinishedOn.Equal(finished) ||
		first.Comment != "a comment" || first.CoverURL != "https://img/movie.jpg" ||
		first.ExternalIDs["tmdb"] != "101" || len(first.ExternalIDs) != 1 || len(first.Notes) != 0 {
		t.Fatalf("first = %+v", first)
	}
	if planned := res.Entries[1]; planned.Status != plugin.Planned || planned.Rating == nil || *planned.Rating != 7 ||
		planned.StartedOn != nil || planned.CoverURL != "" || planned.ExternalIDs["tmdb"] != "102" {
		t.Fatalf("planned = %+v", planned)
	}
	if manual := res.Entries[2]; manual.Status != plugin.Completed || len(manual.ExternalIDs) != 0 ||
		!hasNote(manual.Notes, "not TMDB") {
		t.Fatalf("manual = %+v", manual)
	}
	if wrong := res.Entries[3]; wrong.Status != plugin.Completed || len(wrong.ExternalIDs) != 0 ||
		!hasNote(wrong.Notes, "not TMDB") {
		t.Fatalf("wrong source = %+v", wrong)
	}
	if synthetic := res.Entries[4]; synthetic.Status != plugin.Completed || synthetic.ExternalIDs["tmdb"] != "106" {
		t.Fatalf("synthetic title = %+v", synthetic)
	}
	// The duplicate is reported while scanning the rows; the bad status and the
	// blank rows are only known after grouping, so compare by line, not by order.
	wantSkipped := map[int]string{6: "status", 7: "duplicate", 9: "no source/id or title", 12: "status"}
	for _, s := range res.Skipped {
		reason, ok := wantSkipped[s.Line]
		if !ok || !strings.Contains(s.Reason, reason) {
			t.Fatalf("unexpected skipped row %+v", s)
		}
		delete(wantSkipped, s.Line)
	}
	if len(wantSkipped) != 0 {
		t.Fatalf("missing skipped rows %v (got %+v)", wantSkipped, res.Skipped)
	}
}

func hasNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}
