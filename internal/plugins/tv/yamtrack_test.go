package tv

import (
	"context"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const yamtrackTVCSV = `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
101,tmdb,tv,The Show,https://img/show.jpg,,,8.0,In progress,,2026-01-01 00:00:00+00:00,2026-02-01 00:00:00+00:00,15
101,tmdb,season,The Show,,1,,,Completed,,,2026-01-01 00:00:00+00:00,12
101,tmdb,season,The Show,,2,,,In progress,,,2026-02-01 00:00:00+00:00,3
101,tmdb,season,The Show,,3,,,Planning,,,,
101,tmdb,season,The Show,,0,,,Completed,,,2026-01-01 00:00:00+00:00,1
101,tmdb,episode,The Show,,1,1,,,,,2026-01-01 00:00:00+00:00,
101,tmdb,episode,The Show,,1,2,,,,,2026-01-01 00:00:00+00:00,
101,tmdb,episode,The Show,,2,1,,,,,2026-02-01 00:00:00+00:00,
102,tmdb,tv,No Episode Rows,,,,,Completed,,2026-01-01 00:00:00+00:00,,0
103,manual,tv,Manual Show,,,,,Planning,,,,
999,tmdb,season,Orphan,,1,,,Completed,,,2026-01-01 00:00:00+00:00,5
2001,igdb,game,Game,,,,,Completed,,,,
`

func TestYamtrackTVAggregation(t *testing.T) {
	res, err := yamtrackTVImporter{}.Parse(context.Background(), strings.NewReader(yamtrackTVCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || res.Ignored != 1 || len(res.Skipped) != 1 {
		t.Fatalf("entries=%+v ignored=%d skipped=%+v", res.Entries, res.Ignored, res.Skipped)
	}
	e := res.Entries[0]
	if e.Kind != KindShow || e.Title != "The Show" || e.Status != plugin.Active ||
		e.Progress["season"] != 2 || e.Progress["episode"] != 15 || e.ExternalIDs["tmdb"] != "101" ||
		e.Rating == nil || *e.Rating != 8 || e.CoverURL != "https://img/show.jpg" {
		t.Fatalf("first = %+v", e)
	}
	if res.Entries[1].Title != "No Episode Rows" || res.Entries[1].Progress["episode"] != 0 {
		t.Fatalf("second = %+v", res.Entries[1])
	}
	if len(res.Entries[2].ExternalIDs) != 0 || res.Entries[2].Status != plugin.Planned {
		t.Fatalf("manual = %+v", res.Entries[2])
	}
}
