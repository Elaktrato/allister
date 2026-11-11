package book

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

type goodreadsImporter struct{}

func (goodreadsImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: not a Goodreads CSV export: %v", plugin.ErrBadInput, err)
	}
	cols := map[string]int{}
	for i, name := range header {
		name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
		cols[name] = i
	}
	if _, ok := cols["title"]; !ok {
		return plugin.ParseResult{}, fmt.Errorf("%w: the Goodreads export has no Title column", plugin.ErrBadInput)
	}
	if _, ok := cols["exclusive shelf"]; !ok {
		return plugin.ParseResult{}, fmt.Errorf("%w: the Goodreads export has no Exclusive Shelf column", plugin.ErrBadInput)
	}
	var out plugin.ParseResult
	for line := 2; ; line++ {
		if err := ctx.Err(); err != nil {
			return plugin.ParseResult{}, err
		}
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return plugin.ParseResult{}, fmt.Errorf("%w: row %d: %v", plugin.ErrBadInput, line, err)
		}
		title := plugin.SingleLine(field(row, cols, "title"), maxTitleRunes)
		if title == "" {
			out.Skipped = append(out.Skipped, plugin.SkippedRow{Line: line, Reason: "no title"})
			continue
		}
		status, ok := goodreadsStatus(field(row, cols, "exclusive shelf"))
		if !ok {
			out.Skipped = append(out.Skipped, plugin.SkippedRow{Line: line, Title: title,
				Reason: "unknown shelf " + plugin.SingleLine(field(row, cols, "exclusive shelf"), 40)})
			continue
		}
		entry := plugin.ParsedEntry{Kind: KindBook, Title: title, Status: status}
		notes := []string{}
		isbn := canonicalISBN(field(row, cols, "isbn13"))
		if isbn == "" {
			isbn = canonicalISBN(field(row, cols, "isbn"))
		}
		if isbn != "" {
			entry.ExternalIDs = map[string]string{"isbn": isbn}
		} else if raw := strings.TrimSpace(field(row, cols, "isbn")); raw != "" || strings.TrimSpace(field(row, cols, "isbn13")) != "" {
			notes = append(notes, "ISBN is not valid; saved without one")
		}
		if id := strings.TrimSpace(field(row, cols, "book id")); id != "" {
			if entry.ExternalIDs == nil {
				entry.ExternalIDs = map[string]string{}
			}
			entry.ExternalIDs["goodreads"] = id
		}
		fields := map[string]plugin.FieldValue{}
		if v := plugin.SingleLine(field(row, cols, "author"), maxNameRunes); v != "" {
			fields["author"] = plugin.FieldValue{Text: v}
		}
		pages := parseInt(field(row, cols, "number of pages"))
		if pages > 0 {
			p := float64(pages)
			fields["pages"] = plugin.FieldValue{Num: &p}
			if status == plugin.Completed {
				entry.Progress = map[string]float64{"pages": p}
			}
		}
		year := parseInt(field(row, cols, "original publication year"))
		if year <= 0 {
			year = parseInt(field(row, cols, "year published"))
		}
		if year > 0 {
			y := float64(year)
			fields["year"] = plugin.FieldValue{Num: &y}
		}
		if v := plugin.SingleLine(field(row, cols, "publisher"), maxNameRunes); v != "" {
			fields["publisher"] = plugin.FieldValue{Text: v}
		}
		if isbn != "" {
			fields["isbn"] = plugin.FieldValue{Text: isbn}
		}
		if len(fields) > 0 {
			entry.Fields = fields
		}
		if rating := parseInt(field(row, cols, "my rating")); rating > 0 {
			if rating > 5 {
				notes = append(notes, "rating is above 5, left unrated")
			} else {
				r := rating * 2
				entry.Rating = &r
			}
		}
		if day := parseGoodreadsDate(field(row, cols, "date read")); day != nil {
			entry.FinishedOn = day
		}
		if review := plugin.MultiLine(field(row, cols, "my review"), maxTextRunes); review != "" {
			entry.Comment = review
		}
		entry.Notes = notes
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func field(row []string, cols map[string]int, name string) string {
	i, ok := cols[name]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

func goodreadsStatus(s string) (plugin.StatusClass, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read":
		return plugin.Completed, true
	case "currently-reading", "currently reading":
		return plugin.Active, true
	case "to-read", "to read":
		return plugin.Planned, true
	}
	return "", false
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func parseGoodreadsDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"2006/01/02", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			day := plugin.DateOnly(t)
			return &day
		}
	}
	return nil
}

// canonicalISBN returns the ISBN-13 form of a Goodreads, Google Books or
// Open Library identifier. ISBN-10s are converted only when their check digit
// is valid; invalid identifiers return "".
func canonicalISBN(raw string) string {
	s := normaliseISBN(strings.Trim(raw, `="' `))
	switch len(s) {
	case 13:
		if validISBN13(s) {
			return s
		}
	case 10:
		if !validISBN10(s) {
			return ""
		}
		base := "978" + s[:9]
		sum := 0
		for i := 0; i < 12; i++ {
			n := int(base[i] - '0')
			if i%2 == 1 {
				n *= 3
			}
			sum += n
		}
		check := (10 - sum%10) % 10
		return base + strconv.Itoa(check)
	}
	return ""
}

func validISBN10(s string) bool {
	sum := 0
	for i := 0; i < 10; i++ {
		n := 0
		switch {
		case s[i] >= '0' && s[i] <= '9':
			n = int(s[i] - '0')
		case i == 9 && (s[i] == 'X' || s[i] == 'x'):
			n = 10
		default:
			return false
		}
		sum += (10 - i) * n
	}
	return sum%11 == 0
}

func validISBN13(s string) bool {
	if len(s) != 13 {
		return false
	}
	sum := 0
	for i := 0; i < 12; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		n := int(s[i] - '0')
		if i%2 == 1 {
			n *= 3
		}
		sum += n
	}
	return s[12] >= '0' && s[12] <= '9' && (10-sum%10)%10 == int(s[12]-'0')
}
