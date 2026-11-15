// Package imdb parses the CSV exports IMDb offers for ratings and watchlists.
// Both exports share the columns this parser needs; the Title Type column is
// localised by the account language, so it is classified through an alias
// table instead of being compared to one spelling.
package imdb

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// ErrBadFormat means the input is not a usable IMDb export (unreadable CSV or
// a required column is missing).
var ErrBadFormat = errors.New("not an IMDb CSV export")

// Classified Title Type values.
const (
	TypeMovie   = "movie"
	TypeTV      = "tv"
	TypeIgnored = "ignored"
	TypeUnknown = "unknown"
)

const (
	maxRows      = 100000
	maxTitleRune = 300
	maxRawRunes  = 40
)

var constRe = regexp.MustCompile(`^tt[0-9]{7,8}$`)

// Row is one export row. A row whose Const is not valid has Invalid set (and
// an empty Const); the caller reports it as skipped.
type Row struct {
	Line    int
	Const   string
	Title   string
	TypeKey string // normalised Title Type
	TypeRaw string
	Year    int // 0 when absent
	Rating  *int
	RatedOn *time.Time
	Invalid string
	Rated   bool     // the Your Rating cell is not empty
	Notes   []string // row problems that keep the row
}

// NormaliseType lower-cases a Title Type and removes spaces and hyphens.
func NormaliseType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s)
}

var typeTable = map[string]string{
	"movie": TypeMovie, "film": TypeMovie, "tvmovie": TypeMovie, "fernsehfilm": TypeMovie,
	"short": TypeMovie, "kurzfilm": TypeMovie, "video": TypeMovie, "tvspecial": TypeMovie, "fernsehspecial": TypeMovie,
	"tvseries": TypeTV, "fernsehserie": TypeTV, "tvminiseries": TypeTV, "miniserie": TypeTV,
	"tvepisode": TypeIgnored, "fernsehepisode": TypeIgnored, "videogame": TypeIgnored, "videospiel": TypeIgnored,
	"podcastseries": TypeIgnored, "podcastepisode": TypeIgnored, "musicvideo": TypeIgnored, "musikvideo": TypeIgnored,
}

// Classify maps a normalised Title Type to movie, tv, ignored or unknown.
func Classify(typeKey string) string {
	if c, ok := typeTable[typeKey]; ok {
		return c
	}
	return TypeUnknown
}

// Parse reads an IMDb ratings or watchlist export. Columns are found by name,
// case-insensitively. Const, Title and Title Type are required.
func Parse(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
	}
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, string(rune(0xFEFF)))))
		if _, ok := col[h]; !ok {
			col[h] = i
		}
	}
	for _, need := range []string{"const", "title", "title type"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("%w: missing column %q", ErrBadFormat, need)
		}
	}
	var rows []Row
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadFormat, err)
		}
		if len(rows) >= maxRows {
			return nil, fmt.Errorf("%w: too many rows", ErrBadFormat)
		}
		line, _ := cr.FieldPos(0)
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		row := Row{
			Line:    line,
			Title:   plugin.SingleLine(get("title"), maxTitleRune),
			TypeRaw: plugin.SingleLine(get("title type"), 60),
		}
		row.TypeKey = NormaliseType(row.TypeRaw)
		if c := get("const"); constRe.MatchString(c) {
			row.Const = c
		} else {
			row.Invalid = "invalid IMDb id"
		}
		if y, err := strconv.Atoi(get("year")); err == nil && y >= 1900 && y <= 2100 {
			row.Year = y
		}
		if raw := get("your rating"); raw != "" {
			row.Rated = true
			if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 10 {
				row.Rating = &n
			} else {
				row.Notes = append(row.Notes, fmt.Sprintf("IMDb rating %q is not 1–10; not imported", plugin.SingleLine(raw, maxRawRunes)))
			}
		}
		if raw := get("date rated"); raw != "" {
			if d, err := time.Parse("2006-01-02", raw); err == nil {
				row.RatedOn = &d
			} else {
				row.Notes = append(row.Notes, fmt.Sprintf("IMDb date %q not understood", plugin.SingleLine(raw, maxRawRunes)))
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
