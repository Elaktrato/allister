package movie

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

const (
	letterboxdMaxUpload = 20 << 20 // the upload cap, enforced again here
	letterboxdMaxMember = 10 << 20 // uncompressed bytes per CSV
	letterboxdMaxRows   = 100000   // data rows across all files
	letterboxdMaxFields = 64       // fields per record
	letterboxdMaxListed = 1000     // skipped rows listed before one summary row
)

// letterboxdFiles are the only archive members that are read, at the root and
// with exactly these (case-sensitive) names.
var letterboxdFiles = []string{"watched.csv", "diary.csv", "ratings.csv", "watchlist.csv", "reviews.csv"}

// letterboxdImporter reads the ZIP Letterboxd sends for a data export. Rows are
// merged per film. The key is the lower-cased name and year, not the URI: the
// URI in diary.csv and reviews.csv names the log entry, not the film, so only
// the film URIs (watched, ratings, watchlist) are kept as the external id.
type letterboxdImporter struct{}

type lbRow struct {
	line                                     int
	name, year, uri, rating, rewatch, review string
	watchedDate, date                        string
}

type lbFilm struct {
	title, uri       string
	year             int
	watched, planned bool
	diary, rewatches int
	rating           *int
	diaryRating      *int
	ratingFrom       time.Time // watch date of the diary rating
	finished         *time.Time
	review           string
	reviewDay        *time.Time
}

func (letterboxdImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	body, err := io.ReadAll(io.LimitReader(r, letterboxdMaxUpload+1))
	if err != nil {
		return plugin.ParseResult{}, err
	}
	if len(body) > letterboxdMaxUpload {
		return plugin.ParseResult{}, fmt.Errorf("%w: the file is larger than 20 MiB", plugin.ErrBadInput)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: not a ZIP archive", plugin.ErrBadInput)
	}
	members := map[string]*zip.File{}
	for _, f := range zr.File {
		if _, dup := members[f.Name]; !dup {
			members[f.Name] = f
		}
	}
	if members["watched.csv"] == nil && members["diary.csv"] == nil && members["watchlist.csv"] == nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: the archive has no watched.csv, diary.csv or watchlist.csv", plugin.ErrBadInput)
	}

	var res plugin.ParseResult
	films := map[string]*lbFilm{}
	var order []*lbFilm
	total, unnamed := 0, 0
	for _, name := range letterboxdFiles {
		f := members[name]
		if f == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return plugin.ParseResult{}, err
		}
		err := readLetterboxdCSV(f, func(row lbRow) error {
			total++
			if total > letterboxdMaxRows {
				return errTooManyRows
			}
			title := plugin.SingleLine(row.name, maxTitleRunes)
			if title == "" {
				if unnamed++; unnamed <= letterboxdMaxListed {
					res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.line, Reason: name + ": row has no film name"})
				}
				return nil
			}
			year := parseYear(row.year)
			key := lbKey(title, year)
			film := films[key]
			if film == nil {
				film = &lbFilm{title: title, year: year}
				films[key] = film
				order = append(order, film)
			}
			film.apply(name, row)
			return nil
		})
		if errors.Is(err, errTooManyRows) {
			return plugin.ParseResult{}, fmt.Errorf("%w: the export has more than 100,000 rows", plugin.ErrBadInput)
		}
		if err != nil {
			return plugin.ParseResult{}, fmt.Errorf("%w: %s: %w", plugin.ErrBadInput, name, err)
		}
	}
	if unnamed > letterboxdMaxListed {
		res.Skipped = append(res.Skipped, plugin.SkippedRow{Reason: fmt.Sprintf("…and %d more rows without a name", unnamed-letterboxdMaxListed)})
	}
	for _, film := range order {
		res.Entries = append(res.Entries, film.entry())
	}
	return res, nil
}

var errTooManyRows = errors.New("too many rows")

func (f *lbFilm) apply(file string, row lbRow) {
	uri := letterboxdPath(row.uri)
	switch file {
	case "watched.csv":
		f.watched = true
		f.setURI(uri)
	case "ratings.csv":
		f.setURI(uri)
		if v, ok := halfStars(row.rating); ok {
			f.rating = &v
		}
	case "watchlist.csv":
		f.planned = true
		f.setURI(uri)
	case "diary.csv":
		f.watched = true
		f.diary++
		if strings.EqualFold(strings.TrimSpace(row.rewatch), "yes") {
			f.rewatches++
		}
		d, hasDay := parseDay(row.watchedDate)
		if hasDay && (f.finished == nil || d.After(*f.finished)) {
			f.finished = &d
		}
		// The rating of the latest watch (a later row wins a tie or a missing
		// date); ratings.csv overrides it below.
		if v, ok := halfStars(row.rating); ok && (f.diaryRating == nil || !d.Before(f.ratingFrom)) {
			f.diaryRating, f.ratingFrom = &v, d
		}
	case "reviews.csv":
		f.watched = true
		text := plugin.MultiLine(row.review, maxTextRunes)
		if text == "" {
			return
		}
		// Latest watched date (fallback: log date) wins; a later row wins a tie.
		day, ok := parseDay(row.watchedDate)
		if !ok {
			day, ok = parseDay(row.date)
		}
		if !ok {
			day = time.Time{}
		}
		if f.review == "" || f.reviewDay == nil || !day.Before(*f.reviewDay) {
			f.review, f.reviewDay = text, &day
		}
	}
}

func (f *lbFilm) setURI(uri string) {
	if f.uri == "" {
		f.uri = uri
	}
}

func (f *lbFilm) entry() plugin.ParsedEntry {
	e := plugin.ParsedEntry{Kind: KindMovie, Title: f.title, Status: plugin.Planned}
	if f.watched {
		e.Status = plugin.Completed
		e.Rating = f.rating
		if e.Rating == nil {
			e.Rating = f.diaryRating
		}
		e.FinishedOn = f.finished
		e.Comment = f.review
	}
	if f.rating != nil && !f.watched {
		// A rating alone means the film was seen.
		e.Status, e.Rating = plugin.Completed, f.rating
	}
	if f.rewatches > 0 {
		n := f.diary
		if n < 2 {
			n = 2
		}
		e.Notes = append(e.Notes, fmt.Sprintf("watched %d times", n))
	}
	if f.year > 0 {
		y := float64(f.year)
		e.Fields = map[string]plugin.FieldValue{"year": {Num: &y}}
	}
	if uri := f.uri; uri != "" {
		e.ExternalIDs = map[string]string{"letterboxd": uri}
	}
	return e
}

func lbKey(title string, year int) string {
	return strings.ToLower(title) + "|" + strconv.Itoa(year)
}

func parseYear(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1850 || n > 2200 {
		return 0
	}
	return n
}

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// halfStars maps 0.5-5 stars to a 1-10 rating.
func halfStars(s string) (int, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v < 0.5 || v > 5 {
		return 0, false
	}
	return int(math.Round(v * 2)), true
}

// letterboxdPath returns the path of a Letterboxd URI without slashes at the
// ends (https://boxd.it/abc becomes abc), sanitised; "" when it has none.
func letterboxdPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return plugin.SingleLine(strings.Trim(u.Path, "/"), maxIDRunes)
}

// readLetterboxdCSV streams one member through a size cap (the header's size
// is not trusted), finds the columns by name and hands each data row to
// emit. Records are limited to letterboxdMaxFields fields.
func readLetterboxdCSV(f *zip.File, emit func(lbRow) error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	lr := &io.LimitedReader{R: rc, N: letterboxdMaxMember + 1}
	br := bufio.NewReader(&lineCapReader{r: lr})
	if b, err := br.Peek(3); err == nil && string(b) == "\xef\xbb\xbf" {
		_, _ = br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	tooBig := func() bool { return lr.N <= 0 }
	var col map[string]int
	line := 0
	for {
		rec, err := cr.Read()
		if tooBig() {
			return errors.New("file is larger than 10 MiB")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		line++
		if len(rec) > letterboxdMaxFields {
			return fmt.Errorf("a record has more than %d fields", letterboxdMaxFields)
		}
		if col == nil {
			col = map[string]int{}
			for i, h := range rec {
				h = strings.ToLower(strings.TrimSpace(h))
				if _, dup := col[h]; !dup {
					col[h] = i
				}
			}
			continue
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		row := lbRow{
			line: line,
			name: get("name"), year: get("year"), uri: get("letterboxd uri"),
			rating: get("rating"), rewatch: get("rewatch"), review: get("review"),
			watchedDate: strings.TrimSpace(get("watched date")), date: strings.TrimSpace(get("date")),
		}
		if err := emit(row); err != nil {
			return err
		}
	}
}

// lineCapReader fails a read that carries on for more than letterboxdMaxLine
// bytes without a newline, so one giant line cannot become millions of fields
// before the field limit is checked.
type lineCapReader struct {
	r   io.Reader
	run int
}

const letterboxdMaxLine = 1 << 20

func (l *lineCapReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			l.run = 0
		} else if l.run++; l.run > letterboxdMaxLine {
			return n, errors.New("a line is longer than 1 MiB")
		}
	}
	return n, err
}

var _ plugin.Importer = letterboxdImporter{}
