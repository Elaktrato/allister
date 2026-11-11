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
// Parse uses it for the anime filter and the TV importer groups it; the CSV,
// score, date, progress and status-column handling stays in one place.
type RawRow struct {
	Line             int
	Source, MediaID  string
	MediaType, Title string
	ImageURL         string
	SeasonNumber     int
	EpisodeNumber    int
	Status           plugin.StatusClass
	StatusRaw        string
	Rating           *int
	// Progress is the integer progress; ProgressRaw is the cell as written. An
	// integer fills both, a duration (such as "1h 16min") only ProgressRaw.
	Progress              int
	ProgressRaw           string
	StartedOn, FinishedOn *time.Time
	Comment               string
	Notes                 []string
}

var rawRequiredColumns = []string{"media_id", "media_type", "status", "title"}

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
			row.ProgressRaw = p
			n, err := strconv.Atoi(p)
			switch {
			case err == nil && n >= 0 && n <= maxProgress:
				row.Progress = n
			default:
				if _, ok := ParsePlaytime(p); !ok {
					row.Notes = append(row.Notes, fmt.Sprintf("invalid progress %q, using 0", p))
				}
			}
		}
		row.Rating = parseScore(get("score"), &row.Notes)
		row.StartedOn = parseDay(get("start_date"), "start date", &row.Notes)
		row.FinishedOn = parseDay(get("end_date"), "end date", &row.Notes)
		out = append(out, row)
	}
	return out, nil
}

// ParsePlaytime reads a playtime cell as seconds. It accepts hours and minutes
// with or without a space between the parts ("1h 16min", "1h16min", "2h",
// "16min") and plain minutes ("45"). An empty, negative or over-large cell is
// not a playtime.
func ParsePlaytime(s string) (seconds int, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n > maxProgress {
			return 0, false
		}
		return n * 60, true
	}
	minutes := 0
	rest := s
	hoursSeen, minutesSeen := false, false
	for rest != "" {
		if hoursSeen || minutesSeen {
			rest = strings.TrimLeft(rest, " ")
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.Atoi(rest[:i])
		if err != nil || n > maxProgress {
			return 0, false
		}
		rest = rest[i:]
		switch {
		case strings.HasPrefix(rest, "h"):
			if hoursSeen || minutesSeen {
				return 0, false
			}
			hoursSeen = true
			minutes += n * 60
			rest = rest[1:]
		case strings.HasPrefix(rest, "min"):
			if minutesSeen {
				return 0, false
			}
			minutesSeen = true
			minutes += n
			rest = rest[3:]
		default:
			return 0, false
		}
	}
	if !hoursSeen && !minutesSeen || minutes > maxProgress {
		return 0, false
	}
	return minutes * 60, true
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
