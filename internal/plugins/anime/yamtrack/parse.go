// Package yamtrack parses a Yamtrack CSV export (Parse). The anime plugin's
// Yamtrack importer wraps it; the generic runner (package importer) stores
// and resolves the rows.
package yamtrack

import (
	"bufio"
	"bytes"
	"encoding/csv"
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
	br := bufio.NewReader(r)
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	hdr, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: the file is empty", ErrBadFormat)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
	}
	col := map[string]int{}
	for i, h := range hdr {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\xEF\xBB\xBF")))
		if _, dup := col[h]; !dup {
			col[h] = i
		}
	}
	var missing []string
	for _, c := range requiredColumns {
		if _, ok := col[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("%w: missing column(s) %s", ErrBadFormat, strings.Join(missing, ", "))
	}

	seen := map[string]bool{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
		}
		line, _ := cr.FieldPos(0)
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		title := truncate(get("title"), maxTitleRunes)
		skip := func(reason string, ignored bool) {
			skipped = append(skipped, SkippedRow{Line: line, Reason: reason, Title: title, Ignored: ignored})
		}

		if mt := get("media_type"); !strings.EqualFold(mt, "anime") {
			skip(fmt.Sprintf("not an anime row (media_type %q)", mt), true)
			continue
		}
		if src := get("source"); src != "" && !strings.EqualFold(src, "mal") {
			skip(fmt.Sprintf("source is %q, only mal ids can be matched", src), false)
			continue
		}
		malID := get("media_id")
		if n, err := strconv.ParseUint(malID, 10, 31); err != nil || n == 0 {
			skip(fmt.Sprintf("invalid media_id %q", malID), false)
			continue
		}
		status, ok := statusMap[strings.ToLower(get("status"))]
		if !ok {
			skip(fmt.Sprintf("unknown status %q", get("status")), false)
			continue
		}
		if seen[malID] {
			skip("duplicate media_id "+malID, false)
			continue
		}
		row := Row{MALID: malID, Title: title, ImageURL: get("image"), Status: status,
			Comment: truncate(get("notes"), maxCommentRunes)}
		if row.Title == "" {
			row.Title = "MAL #" + malID
			row.Notes = append(row.Notes, "no title in the export, using "+row.Title)
		}
		// Bad optional fields never drop the row: they are neutralised and noted.
		if p := get("progress"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > maxProgress {
				row.Notes = append(row.Notes, fmt.Sprintf("invalid progress %q, using 0", p))
			} else {
				row.Progress = n
			}
		}
		row.Rating = parseScore(get("score"), &row.Notes)
		row.StartedOn = parseDay(get("start_date"), "start date", &row.Notes)
		row.FinishedOn = parseDay(get("end_date"), "end date", &row.Notes)
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
