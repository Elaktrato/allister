package manga

// The Yamtrack general CSV importer keeps the manga rows of an export that
// also holds anime, books and games. The CSV does not carry the AniList
// format, so one batch lookup over the distinct MyAnimeList ids of the manga
// rows decides which rows are light novels: those stay in the books Series
// kind (yamtrack-novel) and are counted as Ignored here. Every other row
// becomes a manga entry; a row AniList does not know keeps its MAL id and is
// saved as an unmatched manual manga by the runner. Manga rows from another
// source (such as mangaupdates) cannot be resolved and become manual entries.

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

// malFormatClient is the batch format lookup of the shared AniList client.
type malFormatClient interface {
	MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
}

// yamtrackMangaImporter imports the manga rows of a Yamtrack general export.
type yamtrackMangaImporter struct{ c anilist.Provider }

var _ plugin.Importer = yamtrackMangaImporter{}

// yamtrackMangaCandidate is one row that can become a manga entry: a row with
// a MyAnimeList id to look up (malID > 0), or a non-MAL row (malID == 0) that
// is saved manually.
type yamtrackMangaCandidate struct {
	row   yamtrack.RawRow
	malID int
}

func (im yamtrackMangaImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := yamtrack.ParseRaw(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	var res plugin.ParseResult
	var kept []yamtrackMangaCandidate
	seen := map[int]bool{}
	for _, row := range rows {
		if !strings.EqualFold(row.MediaType, "manga") {
			res.Ignored++
			continue
		}
		title := plugin.SingleLine(row.Title, maxTitleRunes)
		if strings.EqualFold(row.Source, "mal") {
			id, ok := yamtrackMangaMALID(row)
			if !ok {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: title,
					Reason: "no usable MyAnimeList id"})
				continue
			}
			if seen[id] {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: title,
					Reason: fmt.Sprintf("duplicate media_id %d", id)})
				continue
			}
			if row.Status == "" {
				// Skipped before claiming the id, so a later valid duplicate is kept.
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: title,
					Reason: "unknown status " + plugin.SingleLine(row.StatusRaw, maxNoteRunes)})
				continue
			}
			seen[id] = true
			kept = append(kept, yamtrackMangaCandidate{row: row, malID: id})
			continue
		}
		// Another source such as mangaupdates: no id AniList can resolve, so
		// the row is kept as a manual manga entry.
		if title == "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Reason: "no title"})
			continue
		}
		if row.Status == "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: title,
				Reason: "unknown status " + plugin.SingleLine(row.StatusRaw, maxNoteRunes)})
			continue
		}
		kept = append(kept, yamtrackMangaCandidate{row: row})
	}
	var formats map[int]string
	if ids := yamtrackMangaIDs(kept); len(ids) > 0 {
		fc, ok := im.c.(malFormatClient)
		if !ok {
			return plugin.ParseResult{}, fmt.Errorf("manga: the provider cannot look up MyAnimeList formats: %w", plugin.ErrUnavailable)
		}
		formats, err = fc.MediaFormatsByMALIDs(ctx, anilist.TypeManga, ids)
		if err != nil {
			return plugin.ParseResult{}, mapErr(err)
		}
	}
	for _, k := range kept {
		if k.malID > 0 && formats[k.malID] == "NOVEL" {
			// The light novels live in the books Series kind, not here.
			res.Ignored++
			continue
		}
		res.Entries = append(res.Entries, yamtrackMangaEntry(k))
	}
	return res, nil
}

// yamtrackMangaMALID returns the MyAnimeList id of a manga/mal row. A missing,
// non-numeric or non-positive id cannot be looked up on AniList.
func yamtrackMangaMALID(row yamtrack.RawRow) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(row.MediaID))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// yamtrackMangaIDs returns the distinct MAL ids of the candidates that need a
// format lookup, in input order.
func yamtrackMangaIDs(cands []yamtrackMangaCandidate) []int {
	ids := make([]int, 0, len(cands))
	for _, c := range cands {
		if c.malID > 0 {
			ids = append(ids, c.malID)
		}
	}
	return ids
}

// yamtrackMangaEntry maps one candidate row to the contract entry. Text taken
// from the file is sanitised so the entry is always saveable; the parser's
// notes are copied and shortened.
func yamtrackMangaEntry(k yamtrackMangaCandidate) plugin.ParsedEntry {
	row := k.row
	title := plugin.SingleLine(row.Title, maxTitleRunes)
	var external map[string]string
	if k.malID > 0 {
		external = map[string]string{"mal": strconv.Itoa(k.malID)}
		if title == "" {
			title = "MyAnimeList #" + strconv.Itoa(k.malID)
		}
	}
	e := plugin.ParsedEntry{
		Kind:        KindManga,
		Title:       title,
		ExternalIDs: external,
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
