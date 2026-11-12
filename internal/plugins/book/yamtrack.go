package book

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/plugins/yamtrack"
)

const (
	// maxCommentRunes is the comment cap the shared Yamtrack parser applies to
	// the notes cell and this importer keeps for the entry.
	maxCommentRunes = 2000
	// maxNoteRunes bounds one note copied from the parser.
	maxNoteRunes = 500
)

// yamtrackNovelImporter keeps the light novels of a Yamtrack general export as
// Series entries. The CSV does not carry the AniList format, so one batch
// lookup over the distinct MyAnimeList ids of the manga/mal rows decides:
// only NOVEL rows become entries. Every other row is counted as Ignored and
// never saved as a manual Series row, because a manga or book row is not a
// novel. The Series source then resolves each kept row and adds volumes,
// chapters, author and cover.
type yamtrackNovelImporter struct{ c novelClient }

var _ plugin.Importer = yamtrackNovelImporter{}

// yamtrackNovelRow is one manga/mal row with the MyAnimeList id the batch
// lookup is keyed by.
type yamtrackNovelRow struct {
	row yamtrack.RawRow
	id  int
}

func (im yamtrackNovelImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := yamtrack.ParseRaw(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	var res plugin.ParseResult
	var kept []yamtrackNovelRow
	seen := map[int]bool{}
	for _, row := range rows {
		id, ok := yamtrackNovelMALID(row)
		if !ok {
			res.Ignored++
			continue
		}
		if seen[id] {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: plugin.SingleLine(row.Title, maxTitleRunes),
				Reason: fmt.Sprintf("duplicate media_id %d", id)})
			continue
		}
		seen[id] = true
		if row.Status == "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: plugin.SingleLine(row.Title, maxTitleRunes),
				Reason: "unknown status " + plugin.SingleLine(row.StatusRaw, maxNoteRunes)})
			continue
		}
		kept = append(kept, yamtrackNovelRow{row: row, id: id})
	}
	if len(kept) == 0 {
		return res, nil
	}
	ids := make([]int, 0, len(kept))
	for _, k := range kept {
		ids = append(ids, k.id)
	}
	formats, err := im.c.MediaFormatsByMALIDs(ctx, anilist.TypeManga, ids)
	if err != nil {
		return plugin.ParseResult{}, mapAniListErr(err)
	}
	for _, k := range kept {
		if formats[k.id] != "NOVEL" {
			res.Ignored++
			continue
		}
		res.Entries = append(res.Entries, yamtrackNovelEntry(k.row, k.id))
	}
	return res, nil
}

// yamtrackNovelMALID returns the MyAnimeList id of a manga row from the mal
// source. Another media type, another source and a missing or non-numeric id
// are not candidates: their rows cannot be looked up on AniList.
func yamtrackNovelMALID(row yamtrack.RawRow) (int, bool) {
	if !strings.EqualFold(row.MediaType, "manga") || !strings.EqualFold(row.Source, "mal") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(row.MediaID))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// yamtrackNovelEntry maps one novel row to the contract entry. Text taken from
// the file is sanitised so the entry is always saveable; the parser's notes are
// copied and shortened.
func yamtrackNovelEntry(row yamtrack.RawRow, id int) plugin.ParsedEntry {
	title := plugin.SingleLine(row.Title, maxTitleRunes)
	if title == "" {
		title = "MyAnimeList #" + strconv.Itoa(id)
	}
	e := plugin.ParsedEntry{
		Kind:        KindSeries,
		Title:       title,
		ExternalIDs: map[string]string{"mal": strconv.Itoa(id)},
		Status:      row.Status,
		Rating:      row.Rating,
		Progress:    map[string]float64{"chapter": float64(max(row.Progress, 0))},
		StartedOn:   row.StartedOn,
		FinishedOn:  row.FinishedOn,
		Comment:     plugin.MultiLine(row.Comment, maxCommentRunes),
		CoverURL:    plugin.HTTPSURL(row.ImageURL),
	}
	// The parser already cut the comment to the cap and does not say so, so a
	// comment of exactly that length is reported as truncated.
	if utf8.RuneCountInString(row.Comment) >= maxCommentRunes {
		e.Notes = append(e.Notes, fmt.Sprintf("comment truncated to %d characters", maxCommentRunes))
	}
	for _, n := range row.Notes {
		if n = plugin.SingleLine(n, maxNoteRunes); n != "" {
			e.Notes = append(e.Notes, n)
		}
	}
	return e
}
