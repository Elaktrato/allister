package game

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/yamtrack"
)

// steamImportNote is the note Yamtrack writes into the notes column when a game
// itself was imported from Steam. It describes Yamtrack's import, not the game,
// so the game import drops exactly this value and keeps every other note.
const steamImportNote = "Imported from Steam"

// yamtrackGameImporter filters the game rows out of a Yamtrack export. Rows of
// other media types are only counted (Ignored); a game row is grouped by its
// source and id (or title when it has no id) so a duplicated row is reported
// once. Only an IGDB source with a numeric id keeps an external id; every
// other game row is saved as a manual entry with a note. The playtime cell is
// a duration ("1h 16min"), not a count.
type yamtrackGameImporter struct{}

var _ plugin.Importer = yamtrackGameImporter{}

func (yamtrackGameImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
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
		if !strings.EqualFold(row.MediaType, KindGame) {
			res.Ignored++
			continue
		}
		key := yamtrackGameKey(row)
		if key == "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "game row has no source/id or title"})
			continue
		}
		if _, ok := groups[key]; ok {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: "duplicate game row"})
			continue
		}
		kept := row
		groups[key] = &kept
		order = append(order, key)
	}
	for _, key := range order {
		row := groups[key]
		entry, reason := yamtrackGameEntry(*row)
		if reason != "" {
			res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.Line, Title: row.Title, Reason: reason})
			continue
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

// yamtrackGameKey groups a row by its id, or by its title when there is no id.
// The playtime is not part of the identity.
func yamtrackGameKey(row yamtrack.RawRow) string {
	if id := strings.TrimSpace(row.MediaID); id != "" {
		return row.Source + "|" + id
	}
	if row.Title != "" {
		return row.Source + "|title|" + row.Title
	}
	return ""
}

// yamtrackGameEntry maps one grouped row to the contract entry. The second
// return is a skip reason; the entry is valid when it is empty.
func yamtrackGameEntry(row yamtrack.RawRow) (plugin.ParsedEntry, string) {
	igdbID := ""
	if row.Source == SourceIGDB {
		igdbID = numericID(row.MediaID)
	}
	title := plugin.SingleLine(row.Title, maxTitleRunes)
	if title == "" {
		if igdbID == "" {
			return plugin.ParsedEntry{}, "game row has no title"
		}
		title = "IGDB #" + igdbID
	}
	if row.Status == "" {
		return plugin.ParsedEntry{}, "game row has an unknown status"
	}
	comment := plugin.MultiLine(row.Comment, maxTextRunes)
	if comment == steamImportNote {
		comment = ""
	}
	entry := plugin.ParsedEntry{
		Kind: KindGame, Title: title, Status: row.Status,
		Rating: row.Rating, StartedOn: row.StartedOn, FinishedOn: row.FinishedOn,
		Comment:  comment,
		CoverURL: plugin.HTTPSURL(row.ImageURL),
		Notes:    append([]string(nil), row.Notes...),
	}
	if seconds, ok := yamtrack.ParsePlaytime(row.ProgressRaw); ok {
		entry.Progress = map[string]float64{"playtime": float64(seconds)}
	}
	switch {
	case igdbID != "":
		entry.ExternalIDs = map[string]string{"igdb": igdbID}
	case row.MediaID != "" && row.Source == SourceIGDB:
		entry.Notes = append(entry.Notes, fmt.Sprintf("IGDB id %q is not valid; saved as a manual entry", plugin.SingleLine(row.MediaID, maxIDRunes)))
	case row.MediaID != "":
		entry.Notes = append(entry.Notes, fmt.Sprintf("source %q is not IGDB; saved as a manual entry", plugin.SingleLine(row.Source, maxIDRunes)))
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
