package yamtrack

import (
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestParseRawKeepsAllMediaTypesAndBlankStatuses(t *testing.T) {
	csv := `media_id,source,media_type,title,image,season_number,episode_number,score,status,notes,start_date,end_date,progress
119362,tmdb,tv,Word of Honor,https://img/x.jpg,,,,Completed,,2026-06-21 13:37:00+00:00,2026-06-21 13:37:00+00:00,36
119362,tmdb,season,Word of Honor,,1,,,Completed,,,2026-06-21 13:37:00+00:00,36
119362,tmdb,episode,Word of Honor,,1,1,,,watched,,2026-06-21 13:37:00+00:00,
1001,mal,anime,Anime,,,1,8.0,In progress,,,,
`
	rows, err := ParseRaw(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	if rows[0].MediaType != "tv" || rows[0].Source != "tmdb" || rows[0].Status != plugin.Completed || rows[0].Progress != 36 {
		t.Fatalf("tv = %+v", rows[0])
	}
	if rows[1].MediaType != "season" || rows[1].SeasonNumber != 1 || rows[1].Progress != 36 {
		t.Fatalf("season = %+v", rows[1])
	}
	if rows[2].MediaType != "episode" || rows[2].Status != "" || rows[2].SeasonNumber != 1 || rows[2].EpisodeNumber != 1 || rows[2].FinishedOn == nil {
		t.Fatalf("episode = %+v", rows[2])
	}
	if rows[3].MediaType != "anime" || rows[3].Status != plugin.Active || rows[3].Rating == nil || *rows[3].Rating != 8 {
		t.Fatalf("anime = %+v", rows[3])
	}
}

func TestParsePlaytime(t *testing.T) {
	for _, c := range []struct {
		in  string
		sec int
		ok  bool
	}{
		{"0min", 0, true}, {"1min", 60, true}, {"16min", 960, true},
		{"1h 16min", 4560, true}, {"2h", 7200, true}, {"45", 2700, true},
		{"1h16min", 4560, true}, {"", 0, false}, {"abc", 0, false},
	} {
		sec, ok := ParsePlaytime(c.in)
		if sec != c.sec || ok != c.ok {
			t.Errorf("ParsePlaytime(%q) = %d, %v; want %d, %v", c.in, sec, ok, c.sec, c.ok)
		}
	}
}

func TestParseRawProgressKeepsRawCell(t *testing.T) {
	csv := `media_id,source,media_type,title,status,progress
1,igdb,game,Game Sample,Completed,1h 16min
2,mal,anime,Anime Sample,Completed,12
3,mal,anime,Bad Sample,Completed,abc
`
	rows, err := ParseRaw(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	if r := rows[0]; r.ProgressRaw != "1h 16min" || r.Progress != 0 || len(r.Notes) != 0 {
		t.Errorf("duration row = %+v", r)
	}
	if r := rows[1]; r.ProgressRaw != "12" || r.Progress != 12 || len(r.Notes) != 0 {
		t.Errorf("integer row = %+v", r)
	}
	if r := rows[2]; r.ProgressRaw != "abc" || r.Progress != 0 || !hasRawNote(r, "invalid progress") {
		t.Errorf("invalid row = %+v", r)
	}
}

func hasRawNote(r RawRow, sub string) bool {
	for _, n := range r.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
