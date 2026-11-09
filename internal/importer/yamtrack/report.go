package yamtrack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Item is one listed row of a Report.
type Item struct {
	Title, Message string
	// AnimeID and SourceID identify the saved entry (unmatched rows link to it
	// as /anime/manual/{SourceID}); zero and "" when none was saved.
	AnimeID  int64
	SourceID string
}

// Report is the progress of one import.
type Report struct {
	ID       int64
	Filename string
	State    string
	// Total counts the stored rows; Ignored the rows of other media types that
	// were left out at upload time.
	Total, Ignored int
	// Processed is every row with an outcome; Remaining the rest.
	Processed, Remaining int
	Imported, Skipped    int
	Unmatched, Failed    int
	// Active is true while the import is pending or running.
	Active    bool
	Note      string
	CanResume bool
	// ETA roughly estimates the time to process the remaining rows.
	ETA time.Duration
	// Row lists are capped at 200; More* count the rows not listed.
	UnmatchedRows, FailedRows, SkippedRows []Item
	MoreUnmatched, MoreFailed, MoreSkipped int
}

// Percent is the processed share of the rows, 0 to 100.
func (r *Report) Percent() int {
	if r.Total <= 0 {
		return 0
	}
	return r.Processed * 100 / r.Total
}

// Latest returns the report of the most recent import, or nil, nil.
func (im *Importer) Latest(ctx context.Context) (*Report, error) {
	im.opMu.Lock()
	err := reconcile(ctx, im.db)
	im.opMu.Unlock()
	if err != nil {
		return nil, err
	}
	var r Report
	err = im.db.QueryRowContext(ctx, `SELECT id, filename, state, total, ignored, COALESCE(note, '') FROM imports ORDER BY id DESC LIMIT 1`).
		Scan(&r.ID, &r.Filename, &r.State, &r.Total, &r.Ignored, &r.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest import: %w", err)
	}
	r.Active = r.State == statePending || r.State == stateRunning

	rows, err := im.db.QueryContext(ctx, `SELECT outcome, COUNT(*) FROM import_rows WHERE import_id=? GROUP BY outcome`, r.ID)
	if err != nil {
		return nil, fmt.Errorf("latest import: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var outcome string
		var n int
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, fmt.Errorf("latest import: %w", err)
		}
		switch outcome {
		case outcomePending:
			r.Remaining = n
		case outcomeImported:
			r.Imported = n
		case outcomeSkipped:
			r.Skipped = n
		case outcomeUnmatched:
			r.Unmatched = n
		case outcomeFailed:
			r.Failed = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latest import: %w", err)
	}
	r.Processed = r.Imported + r.Skipped + r.Unmatched + r.Failed
	r.CanResume = (r.State == stateFailed || r.State == stateCancelled) && r.Remaining > 0
	r.ETA = time.Duration(r.Remaining) * time.Minute / anilistRequestsPerMinute

	if r.UnmatchedRows, err = im.items(ctx, r.ID, outcomeUnmatched); err != nil {
		return nil, err
	}
	if r.FailedRows, err = im.items(ctx, r.ID, outcomeFailed); err != nil {
		return nil, err
	}
	if r.SkippedRows, err = im.items(ctx, r.ID, outcomeSkipped); err != nil {
		return nil, err
	}
	r.MoreUnmatched = max(0, r.Unmatched-len(r.UnmatchedRows))
	r.MoreFailed = max(0, r.Failed-len(r.FailedRows))
	r.MoreSkipped = max(0, r.Skipped-len(r.SkippedRows))
	return &r, nil
}

func (im *Importer) items(ctx context.Context, importID int64, outcome string) ([]Item, error) {
	rows, err := im.db.QueryContext(ctx, `SELECT r.title, r.message, COALESCE(r.anime_id, 0), COALESCE(a.source_id, '')
		FROM import_rows r LEFT JOIN items a ON a.id = r.anime_id
		WHERE r.import_id=? AND r.outcome=? ORDER BY r.seq LIMIT ?`, importID, outcome, listCap)
	if err != nil {
		return nil, fmt.Errorf("latest import: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Title, &it.Message, &it.AnimeID, &it.SourceID); err != nil {
			return nil, fmt.Errorf("latest import: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
