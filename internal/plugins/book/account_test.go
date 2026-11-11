package book

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

func accountEntry(id int, format, status string, progress, volumes int, score float64) anilist.AccountEntry {
	var e anilist.AccountEntry
	e.Status, e.Score, e.Progress, e.ProgressVolumes, e.UpdatedAt = status, score, progress, volumes, 1700000000
	e.Media.ID, e.Media.Format = id, format
	e.Media.Title.Romaji = "Novel " + string(rune('0'+id))
	e.Media.Cover.Large = "https://img.example/" + string(rune('0'+id)) + ".jpg"
	return e
}

func TestAniListAccountFiltersNovels(t *testing.T) {
	f := &fakeNovelClient{account: []anilist.AccountEntry{
		accountEntry(1, "NOVEL", "CURRENT", 42, 5, 8),
		accountEntry(2, "MANGA", "CURRENT", 10, 0, 7),
	}}
	res, err := anilistAccountImporter{c: f}.ImportAccount(context.Background(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Ignored != 1 {
		t.Fatalf("entries=%+v ignored=%d", res.Entries, res.Ignored)
	}
	e := res.Entries[0]
	if e.Kind != KindSeries || e.ExternalIDs["anilist"] != "1" || e.Status != plugin.Active ||
		e.Progress["chapter"] != 42 || e.Progress["volume"] != 5 ||
		e.Rating == nil || *e.Rating != 8 || e.CoverURL != "https://img.example/1.jpg" ||
		e.UpdatedAt == nil || !e.UpdatedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("row = %+v", e)
	}
}

func TestAniListAccountRejectsFileAndEmptyAccount(t *testing.T) {
	ai := anilistAccountImporter{}
	if _, err := ai.Parse(context.Background(), strings.NewReader("x")); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("parse err = %v", err)
	}
	if _, err := ai.ImportAccount(context.Background(), " two words "); !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("account err = %v", err)
	}
}
