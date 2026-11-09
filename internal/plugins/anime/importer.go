package anime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/store"
)

// yamtrackImporter adapts yamtrack.Parse to plugin.Importer. Text taken from
// the file (titles, comments, notes, cover urls) is sanitised so a row can
// never make the entry unsaveable; rows of other media types, which the
// legacy parser marks as ignored, are not reported. Only a file that is not
// in the format (yamtrack.ErrBadFormat) is plugin.ErrBadInput; a read error
// keeps its own cause. The importer reads the WHOLE stream: capping the upload
// size is the web layer's job (Task 12 must enforce it).
type yamtrackImporter struct{}

var statusClass = map[store.Status]plugin.StatusClass{
	store.Watching:    plugin.Active,
	store.PlanToWatch: plugin.Planned,
	store.Paused:      plugin.Paused,
	store.Completed:   plugin.Completed,
	store.Dropped:     plugin.Dropped,
}

func (yamtrackImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return plugin.ParseResult{}, err
	}
	tr := &errTrackingReader{r: r}
	rows, skipped, err := yamtrack.Parse(tr)
	if err != nil {
		// the legacy parser reports a failing read as ErrBadFormat too; a read
		// error is not bad input and keeps its own cause
		if tr.err != nil {
			return plugin.ParseResult{}, fmt.Errorf("anime import: read: %w", tr.err)
		}
		if errors.Is(err, yamtrack.ErrBadFormat) {
			return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
		}
		return plugin.ParseResult{}, fmt.Errorf("anime import: %w", err)
	}
	var res plugin.ParseResult
	for _, row := range rows {
		e := plugin.ParsedEntry{
			Kind:        KindAnime,
			Title:       plugin.SingleLine(row.Title, maxTitleRunes),
			ExternalIDs: map[string]string{"mal": row.MALID},
			Status:      statusClass[row.Status],
			Rating:      row.Rating,
			Progress:    map[string]float64{"episode": float64(row.Progress)},
			StartedOn:   row.StartedOn,
			FinishedOn:  row.FinishedOn,
			Comment:     plugin.MultiLine(row.Comment, maxCommentRunes),
			CoverURL:    plugin.HTTPSURL(row.ImageURL),
		}
		if e.Title == "" {
			e.Title = "MAL #" + row.MALID
		}
		// the legacy parser already cut the comment to 2000 runes and does not
		// say so, so a comment of exactly that length is reported as truncated
		if utf8.RuneCountInString(row.Comment) >= maxCommentRunes {
			e.Notes = append(e.Notes, fmt.Sprintf("comment truncated to %d characters", maxCommentRunes))
		}
		for _, n := range row.Notes {
			if n = plugin.SingleLine(n, 500); n != "" {
				e.Notes = append(e.Notes, n)
			}
		}
		res.Entries = append(res.Entries, e)
	}
	for _, s := range skipped {
		if s.Ignored {
			continue
		}
		res.Skipped = append(res.Skipped, plugin.SkippedRow{
			Line: s.Line, Title: plugin.SingleLine(s.Title, maxTitleRunes), Reason: plugin.SingleLine(s.Reason, 500),
		})
	}
	return res, nil
}

// errTrackingReader remembers the first read error other than io.EOF.
type errTrackingReader struct {
	r   io.Reader
	err error
}

func (t *errTrackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}
