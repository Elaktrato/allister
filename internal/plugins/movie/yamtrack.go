package movie

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/yamtrack"
)

// yamtrackMovieImporter filters the movie rows out of a Yamtrack export. Rows
// of other media types are only counted (Ignored); a movie row is grouped by
// its source and id (or title when it has no id) so a duplicated row is
// reported once. Only a TMDB source with a numeric id keeps an external id;
// every other movie row is saved as a manual entry with a note.
type yamtrackMovieImporter struct{}

func (yamtrackMovieImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	rows, err := yamtrack.ParseRaw(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
	}
	var res plugin.ParseResult
	groups := map[string]*yamtrack.RawRow{}
	order := []string{}
	for _, row := range rows {
		if !strings.EqualFold(row.MediaType, KindMovie) {
			res.Ignored++
			continue
		}
		key := yamtrackMovieKey(row)
		if key == "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "movie row has no source/id or title"})
			continue
		}
		if _, ok := groups[key]; ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "duplicate movie row"})
			continue
		}
		kept := row
		groups[key] = &kept
		order = append(order, key)
	}
	for _, key := range order {
		row := groups[key]
		entry, reason := movieEntry(*row)
		if reason != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: reason})
			continue
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

// yamtrackMovieKey groups a row by its id, or by its title when there is no id.
// Yamtrack's progress is a watch count, not a position, so it never takes part.
func yamtrackMovieKey(row yamtrack.RawRow) string {
	if id := strings.TrimSpace(row.MediaID); id != "" {
		return row.Source + "|" + id
	}
	if row.Title != "" {
		return row.Source + "|title|" + row.Title
	}
	return ""
}

// movieEntry maps one grouped row to the contract entry. The second return is a
// skip reason; the entry is valid when it is empty.
func movieEntry(row yamtrack.RawRow) (plugin.ParsedEntry, string) {
	tmdbID := ""
	if row.Source == SourceTMDB {
		tmdbID = numericID(row.MediaID)
	}
	title := plugin.SingleLine(row.Title, maxTitleRunes)
	if title == "" {
		if tmdbID == "" {
			return plugin.ParsedEntry{}, "movie row has no title"
		}
		title = "TMDB #" + tmdbID
	}
	if row.Status == "" {
		return plugin.ParsedEntry{}, "movie row has an unknown status"
	}
	entry := plugin.ParsedEntry{
		Kind: KindMovie, Title: title, Status: row.Status,
		Rating: row.Rating, StartedOn: row.StartedOn, FinishedOn: row.FinishedOn,
		Comment:  plugin.MultiLine(row.Comment, maxTextRunes),
		CoverURL: plugin.HTTPSURL(row.ImageURL),
		Notes:    append([]string(nil), row.Notes...),
	}
	switch {
	case tmdbID != "":
		entry.ExternalIDs = map[string]string{"tmdb": tmdbID}
	case row.MediaID != "" && row.Source == SourceTMDB:
		entry.Notes = append(entry.Notes, fmt.Sprintf("TMDB id %q is not valid; saved as a manual entry", plugin.SingleLine(row.MediaID, maxIDRunes)))
	case row.MediaID != "":
		entry.Notes = append(entry.Notes, fmt.Sprintf("source %q is not TMDB; saved as a manual entry", plugin.SingleLine(row.Source, maxIDRunes)))
	}
	return entry, ""
}

func numericID(s string) string {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

var _ plugin.Importer = yamtrackMovieImporter{}
