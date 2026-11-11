package game

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

const yamtrackGameCSV = `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
100,igdb,game,Completed Game,https://img/game.jpg,,,8.0,Completed,Imported from Steam,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,1h 16min
101,igdb,game,Paused Game,,,,,Paused,,,,16min
102,igdb,game,Planning Game,,,,,Planning,,,,0min
100,igdb,game,Completed Game,,,,,Completed,,,,1h 16min
,manual,game,Manual Game,,,,,Completed,,,,45
104,steam,game,Other Source Game,,,,,Completed,,,,1min
105,igdb,game,Bad Status Game,,,,,Sometimes,,,,1min
106,igdb,game,Blank Status Game,,,,,,,,,1min
2001,tmdb,tv,TV Row,,,,,Completed,,,,36
,manual,game,,,,,,Completed,,,,1min
107,igdb,game,,,,,,Completed,,,,1min
108,igdb,GAME,Upper Case Game,,,,,Completed,,,,2min
109,igdb,game,Noted Game,,,,,Completed,Great game,,,1min
abc,igdb,game,Slug Game,,,,,Completed,,,,1min
`

func TestYamtrackGameRows(t *testing.T) {
	res, err := yamtrackGameImporter{}.Parse(context.Background(), strings.NewReader(yamtrackGameCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 9 || res.Ignored != 1 || len(res.Skipped) != 4 {
		t.Fatalf("entries=%+v ignored=%d skipped=%+v", res.Entries, res.Ignored, res.Skipped)
	}
	for i, e := range res.Entries {
		if e.Kind != KindGame {
			t.Fatalf("entry %d kind = %q", i, e.Kind)
		}
	}

	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	completed := res.Entries[0]
	if completed.Title != "Completed Game" || completed.Status != plugin.Completed ||
		completed.Rating == nil || *completed.Rating != 8 ||
		completed.StartedOn == nil || !completed.StartedOn.Equal(started) ||
		completed.FinishedOn == nil || !completed.FinishedOn.Equal(finished) ||
		completed.CoverURL != "https://img/game.jpg" ||
		completed.ExternalIDs["igdb"] != "100" || len(completed.ExternalIDs) != 1 ||
		completed.Progress["playtime"] != 4560 {
		t.Fatalf("completed = %+v", completed)
	}
	// The exact Steam note describes Yamtrack's own import and is dropped.
	if completed.Comment != "" || len(completed.Notes) != 0 {
		t.Fatalf("completed comment = %q notes = %v", completed.Comment, completed.Notes)
	}

	if paused := res.Entries[1]; paused.Status != plugin.Paused || paused.Progress["playtime"] != 960 {
		t.Fatalf("paused = %+v", paused)
	}
	if planned := res.Entries[2]; planned.Status != plugin.Planned ||
		planned.Progress["playtime"] != 0 || planned.Rating != nil {
		t.Fatalf("planned = %+v", planned)
	}
	if manual := res.Entries[3]; manual.Status != plugin.Completed || len(manual.ExternalIDs) != 0 ||
		len(manual.Notes) != 0 || manual.Progress["playtime"] != 2700 {
		t.Fatalf("manual = %+v", manual)
	}
	if other := res.Entries[4]; len(other.ExternalIDs) != 0 || !hasNote(other.Notes, "not IGDB") ||
		other.Progress["playtime"] != 60 {
		t.Fatalf("other source = %+v", other)
	}
	if synthetic := res.Entries[5]; synthetic.Title != "IGDB #107" ||
		synthetic.ExternalIDs["igdb"] != "107" || len(synthetic.ExternalIDs) != 1 {
		t.Fatalf("synthetic title = %+v", synthetic)
	}
	if upper := res.Entries[6]; upper.Title != "Upper Case Game" || upper.ExternalIDs["igdb"] != "108" ||
		upper.Progress["playtime"] != 120 {
		t.Fatalf("upper-case media type = %+v", upper)
	}
	if noted := res.Entries[7]; noted.Comment != "Great game" || len(noted.Notes) != 0 {
		t.Fatalf("kept note = %+v", noted)
	}
	if slug := res.Entries[8]; len(slug.ExternalIDs) != 0 || !hasNote(slug.Notes, "not valid") ||
		slug.Title != "Slug Game" {
		t.Fatalf("invalid IGDB id = %+v", slug)
	}

	// The duplicate is reported while scanning the rows; the bad status, the
	// blank row and the title-less row are only known after grouping, so
	// compare by line, not by order.
	wantSkipped := map[int]string{5: "duplicate", 8: "status", 9: "status", 11: "no source/id or title"}
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
