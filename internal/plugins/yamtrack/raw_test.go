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
