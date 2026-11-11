package yamtrack

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// RawRow is one CSV row before a plugin decides what its media type means.
// The anime and TV importers filter/aggregate these rows; the CSV, score,
// date, progress and status-column handling stays in one place.
type RawRow struct {
	Line                  int
	Source, MediaID       string
	MediaType, Title      string
	ImageURL              string
	SeasonNumber          int
	EpisodeNumber         int
	Status                plugin.StatusClass
	StatusRaw             string
	Rating                *int
	Progress              int
	StartedOn, FinishedOn *time.Time
	Comment               string
	Notes                 []string
}

var rawRequiredColumns = []string{"media_type", "status", "title"}

// ParseRaw reads every row of a Yamtrack export without filtering a media
// type. It accepts blank statuses (episode rows use no status), blank ids and
// unknown media types; callers decide what to ignore or skip. The header and
// CSV handling are the same as Parse.
func ParseRaw(r io.Reader) ([]RawRow, error) {
	br := bufio.NewReader(r)
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	hdr, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the file is empty", ErrBadFormat)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
	}
	col := map[string]int{}
	for i, h := range hdr {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\xEF\xBB\xBF")))
		if _, dup := col[h]; !dup {
			col[h] = i
		}
	}
	var missing []string
	for _, c := range rawRequiredColumns {
		if _, ok := col[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing column(s) %s", ErrBadFormat, strings.Join(missing, ", "))
	}
	var out []RawRow
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
		}
		line, _ := cr.FieldPos(0)
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		row := RawRow{
			Line: line, Source: get("source"), MediaID: get("media_id"),
			MediaType: get("media_type"), Title: truncate(get("title"), maxTitleRunes),
			ImageURL: get("image"), StatusRaw: get("status"), Comment: truncate(get("notes"), maxCommentRunes),
		}
		row.Status = statusMap[strings.ToLower(row.StatusRaw)]
		row.SeasonNumber = parseRawInt(get("season_number"), "season_number", &row.Notes)
		row.EpisodeNumber = parseRawInt(get("episode_number"), "episode_number", &row.Notes)
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
		out = append(out, row)
	}
	return out, nil
}

func parseRawInt(s, name string, notes *[]string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		*notes = append(*notes, fmt.Sprintf("invalid %s %q, using 0", name, s))
		return 0
	}
	return n
}
