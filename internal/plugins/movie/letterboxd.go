package movie

import (
	"archive/zip"
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
)

// letterboxdFiles are the only archive members that are read, at the root and
// with exactly these (case-sensitive) names.
var letterboxdFiles = []string{"watched.csv", "diary.csv", "ratings.csv", "watchlist.csv", "reviews.csv"}

// letterboxdImporter reads the ZIP Letterboxd sends for a data export. Rows are
// merged per film. The key is the normalised name and year, not the URI: the
// URI in diary.csv and reviews.csv names the log entry, not the film, so only
// the film URIs (watched, ratings, watchlist) are kept as the external id.
type letterboxdImporter struct{}

type lbRow struct {
	line                                     int
	name, year, uri, rating, rewatch, review string
	watchedDate, date                        string
}

type lbFilm struct {
	title, uri, diaryURI string
	year                 int
	first                int
	watched, planned     bool
	diary, rewatches     int
	rating               *int
	finished             *time.Time
	review               string
	reviewKey            string
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
	for _, name := range letterboxdFiles {
		f := members[name]
		if f == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return plugin.ParseResult{}, err
		}
		rows, err := readLetterboxdCSV(f)
		if err != nil {
			return plugin.ParseResult{}, fmt.Errorf("%w: %s: %w", plugin.ErrBadInput, name, err)
		}
		for _, row := range rows {
			title := plugin.SingleLine(row.name, maxTitleRunes)
			if title == "" {
				res.Skipped = append(res.Skipped, plugin.SkippedRow{Line: row.line, Reason: name + ": row has no film name"})
				continue
			}
			year := parseYear(row.year)
			key := lbKey(title, year)
			film := films[key]
			if film == nil {
				film = &lbFilm{title: title, year: year, first: len(order)}
				films[key] = film
				order = append(order, film)
			}
			film.apply(name, row)
		}
	}
	for _, film := range order {
		res.Entries = append(res.Entries, film.entry())
	}
	return res, nil
}

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
		if f.diaryURI == "" {
			f.diaryURI = uri
		}
		if strings.EqualFold(strings.TrimSpace(row.rewatch), "yes") {
			f.rewatches++
		}
		if d, ok := parseDay(row.watchedDate); ok && (f.finished == nil || d.After(*f.finished)) {
			f.finished = &d
		}
		if f.rating == nil {
			if v, ok := halfStars(row.rating); ok {
				f.rating = &v
			}
		}
	case "reviews.csv":
		f.watched = true
		text := plugin.MultiLine(row.review, maxTextRunes)
		if text == "" {
			return
		}
		// Latest watched date wins; a later row wins a tie.
		k := row.watchedDate
		if k == "" {
			k = row.date
		}
		if f.review == "" || k >= f.reviewKey {
			f.review, f.reviewKey = text, k
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

// readLetterboxdCSV reads one member through a size cap (the header's size is
// not trusted) and returns its rows with the columns found by name.
func readLetterboxdCSV(f *zip.File) ([]lbRow, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, letterboxdMaxMember+1))
	if err != nil {
		return nil, err
	}
	if len(data) > letterboxdMaxMember {
		return nil, errors.New("file is larger than 10 MiB")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	col := map[string]int{}
	for i, h := range records[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		if _, dup := col[h]; !dup {
			col[h] = i
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
	}
	var rows []lbRow
	for i, rec := range records[1:] {
		rows = append(rows, lbRow{
			line: i + 2,
			name: get(rec, "name"), year: get(rec, "year"), uri: get(rec, "letterboxd uri"),
			rating: get(rec, "rating"), rewatch: get(rec, "rewatch"), review: get(rec, "review"),
			watchedDate: strings.TrimSpace(get(rec, "watched date")), date: strings.TrimSpace(get(rec, "date")),
		})
	}
	return rows, nil
}

var _ plugin.Importer = letterboxdImporter{}
