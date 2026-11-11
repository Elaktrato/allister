package book

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

type anilistAccountImporter struct{ c novelClient }

func (anilistAccountImporter) Parse(context.Context, io.Reader) (plugin.ParseResult, error) {
	return plugin.ParseResult{}, fmt.Errorf("%w: this importer takes a username, not a file", plugin.ErrBadInput)
}

func (ai anilistAccountImporter) ImportAccount(ctx context.Context, account string) (plugin.ParseResult, error) {
	account = strings.TrimSpace(account)
	if account == "" || len(account) > 64 || strings.IndexFunc(account, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return plugin.ParseResult{}, fmt.Errorf("%w: enter the AniList username (one word)", plugin.ErrBadInput)
	}
	entries, err := ai.c.MediaList(ctx, anilist.TypeManga, account)
	if err != nil {
		return plugin.ParseResult{}, mapAniListErr(err)
	}
	var res plugin.ParseResult
	for i, e := range entries {
		if e.Media.Format != "NOVEL" {
			res.Ignored++
			continue
		}
		st, ok := accountNovelStatus[e.Status]
		if !ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: i + 1, Reason: "unknown list status " + plugin.SingleLine(e.Status, 40)})
			continue
		}
		title := firstNonEmpty(e.Media.Title.Romaji, e.Media.Title.English, e.Media.Title.Native)
		if title == "" {
			title = "AniList #" + fmt.Sprint(e.Media.ID)
		}
		row := plugin.ParsedEntry{
			Kind: KindSeries, Title: plugin.SingleLine(title, maxTitleRunes), Status: st,
			ExternalIDs: map[string]string{"anilist": fmt.Sprint(e.Media.ID)},
			Progress:    map[string]float64{"chapter": float64(max(e.Progress, 0)), "volume": float64(max(e.ProgressVolumes, 0))},
		}
		if e.Score > 0 {
			s := min(10, max(1, int(math.Round(e.Score))))
			row.Rating = &s
		}
		if u := plugin.HTTPSURL(e.Media.Cover.Large); u != "" {
			row.CoverURL = u
		}
		if e.UpdatedAt > 0 {
			t := time.Unix(e.UpdatedAt, 0).UTC()
			row.UpdatedAt = &t
		}
		res.Entries = append(res.Entries, row)
	}
	return res, nil
}

var accountNovelStatus = map[string]plugin.StatusClass{
	"CURRENT":   plugin.Active,
	"REPEATING": plugin.Active,
	"PLANNING":  plugin.Planned,
	"COMPLETED": plugin.Completed,
	"PAUSED":    plugin.Paused,
	"DROPPED":   plugin.Dropped,
}
