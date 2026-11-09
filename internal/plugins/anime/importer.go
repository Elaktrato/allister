package anime

import (
	"context"
	"fmt"
	"io"

	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/store"
)

// yamtrackImporter adapts yamtrack.Parse to plugin.Importer. Text taken from
// the file (titles, comments, notes, cover urls) is sanitised so a row can
// never make the entry unsaveable; rows of other media types, which the
// legacy parser marks as ignored, are not reported.
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
	rows, skipped, err := yamtrack.Parse(r)
	if err != nil {
		return plugin.ParseResult{}, fmt.Errorf("%w: %w", plugin.ErrBadInput, err)
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
			Comment:     plugin.MultiLine(row.Comment, 2000),
			CoverURL:    plugin.HTTPSURL(row.ImageURL),
		}
		if e.Title == "" {
			e.Title = "MAL #" + row.MALID
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
