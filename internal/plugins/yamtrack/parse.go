// Package yamtrack parses a Yamtrack CSV export (Parse). The anime plugin's
// Yamtrack importer wraps it; the generic runner (package importer) stores
// and resolves the rows.
package yamtrack

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxTitleRunes   = 300
	maxCommentRunes = 2000
	maxProgress     = 100000
	dateLayout      = "2006-01-02 15:04:05-07:00"
)

// ErrBadFormat means the input is not a usable Yamtrack export (unreadable
// CSV or a required column is missing).
var ErrBadFormat = errors.New("not a Yamtrack CSV export")

// Row is one anime row of the export, already mapped.
type Row struct {
	// MALID is the Yamtrack media_id, the MyAnimeList id.
	MALID, Title, ImageURL string
	Status                 plugin.StatusClass
	// Rating is nil for unrated rows; otherwise 0 to 10.
	Rating     *int
	Progress   int
	StartedOn  *time.Time // calendar days, UTC midnight
	FinishedOn *time.Time
	Comment    string
	Notes      []string
}

// SkippedRow is a CSV row that is not imported. Ignored rows are of another
// media type (games, manga, episodes, ...) and not worth reporting; the other
// skipped rows are anime rows the user should know about.
type SkippedRow struct {
	Line          int
	Reason, Title string
	Ignored       bool
}

var requiredColumns = []string{"media_id", "media_type", "status", "title"}

var statusMap = map[string]plugin.StatusClass{
	"completed":   plugin.Completed,
	"planning":    plugin.Planned,
	"dropped":     plugin.Dropped,
	"paused":      plugin.Paused,
	"in progress": plugin.Active,
}

// Parse reads a Yamtrack export. Only rows with media_type anime and source mal
// become Rows; every other row is reported in skipped. Only non-anime rows,
// other sources, unknown statuses and bad or duplicate media_ids are skipped:
// a bad score, progress, date or an empty title keeps the row and adds to Notes. It tolerates a UTF-8
// BOM, CRLF line ends, quoted newlines and extra columns. Of rows sharing a
// media_id the first is kept. A missing required column is ErrBadFormat.
func Parse(r io.Reader) (rows []Row, skipped []SkippedRow, err error) {
	raw, err := ParseRaw(r)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, rr := range raw {
		title := rr.Title
		skip := func(reason string, ignored bool) {
			skipped = append(skipped, SkippedRow{Line: rr.Line, Reason: reason, Title: title, Ignored: ignored})
		}
		if !strings.EqualFold(rr.MediaType, "anime") {
			skip(fmt.Sprintf("not an anime row (media_type %q)", rr.MediaType), true)
			continue
		}
		if src := strings.TrimSpace(rr.Source); src != "" && !strings.EqualFold(src, "mal") {
			skip(fmt.Sprintf("source is %q, only mal ids can be matched", src), false)
			continue
		}
		malID := strings.TrimSpace(rr.MediaID)
		n, err := strconv.ParseUint(malID, 10, 31)
		if err != nil || n == 0 {
			skip(fmt.Sprintf("invalid media_id %q", malID), false)
			continue
		}
		malID = strconv.FormatUint(n, 10)
		if rr.Status == "" {
			skip(fmt.Sprintf("unknown status %q", rr.StatusRaw), false)
			continue
		}
		if seen[malID] {
			skip("duplicate media_id "+malID, false)
			continue
		}
		row := Row{MALID: malID, Title: title, ImageURL: rr.ImageURL, Status: rr.Status,
			Rating: rr.Rating, Progress: rr.Progress, StartedOn: rr.StartedOn, FinishedOn: rr.FinishedOn,
			Comment: rr.Comment, Notes: append([]string(nil), rr.Notes...)}
		if row.Title == "" {
			row.Title = "MAL #" + malID
			row.Notes = append(row.Notes, "no title in the export, using "+row.Title)
		}
		seen[malID] = true
		rows = append(rows, row)
	}
	return rows, skipped, nil
}

// parseScore maps a Yamtrack score. Empty or exactly zero is unrated; a
// negative or non-numeric score is unrated with a note; anything else is
// rounded half up (with a note when it was not an integer) and clamped to
// 0..10 (so 0.4 is an explicit 0).
func parseScore(sc string, notes *[]string) *int {
	if sc == "" {
		return nil
	}
	f, err := strconv.ParseFloat(sc, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		*notes = append(*notes, fmt.Sprintf("invalid score %q, left unrated", sc))
		return nil
	}
	if f == 0 {
		return nil
	}
	f = min(f, 10) // clamp before converting so huge values cannot overflow int
	n := int(math.Floor(f + 0.5))
	if f != math.Trunc(f) {
		*notes = append(*notes, fmt.Sprintf("score %s rounded to %d", strconv.FormatFloat(f, 'f', -1, 64), n))
	}
	return &n
}

// parseDay returns the calendar day written in a Yamtrack timestamp (or a
// plain 2006-01-02 date) as UTC midnight. Empty gives nil; unparseable gives
// nil and a note. The day is read from the timestamp's own zone (never
// converted to UTC first).
func parseDay(s, label string, notes *[]string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		t, err = time.Parse("2006-01-02", s)
	}
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("invalid %s %q, left empty", label, s))
		return nil
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return &d
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
