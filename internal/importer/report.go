package importer

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
	// ItemID, SourceID and Kind identify the saved item (unmatched rows link
	// to it as /{plugin}/manual/{SourceID}); zero and "" when none was saved.
	ItemID   int64
	SourceID string
	Kind     string
}

// Report is the progress of one import.
type Report struct {
	ID int64
	// PluginID and ImporterID name the importer; ImporterID is "" for an
	// import made by an older version.
	PluginID, ImporterID string
	Filename             string
	State                string
	// Total counts the stored rows; Ignored the rows the importer left out
	// as not meant for it (other media types).
	Total, Ignored int
	// Processed is every row with an outcome; Remaining the rest.
	Processed, Remaining int
	Imported, Skipped    int
	Unmatched, Failed    int
	// Active is true while the import is pending or running.
	Active    bool
	Note      string
	CanResume bool
	// ETA roughly estimates the time to process the remaining rows: the
	// pending rows that may need a source request at the source's rate.
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

// Latest reconciles stalled imports and returns the report of the most
// recent import, or nil, nil.
func (r *Runner) Latest(ctx context.Context) (*Report, error) {
	if err := r.Reconcile(ctx); err != nil {
		return nil, err
	}
	var id int64
	err := r.d.DB.QueryRowContext(ctx, `SELECT id FROM imports ORDER BY id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest import: %w", err)
	}
	return r.Report(ctx, id)
}

// Report returns the report of import id, or nil, nil when there is none.
func (r *Runner) Report(ctx context.Context, id int64) (*Report, error) {
	var rep Report
	err := r.d.DB.QueryRowContext(ctx, `SELECT id, plugin_id, importer_id, filename, state, total, ignored, COALESCE(note, '')
		FROM imports WHERE id = ?`, id).
		Scan(&rep.ID, &rep.PluginID, &rep.ImporterID, &rep.Filename, &rep.State, &rep.Total, &rep.Ignored, &rep.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("import report: %w", err)
	}
	rep.Active = rep.State == statePending || rep.State == stateRunning

	rows, err := r.d.DB.QueryContext(ctx, `SELECT outcome, COUNT(*) FROM import_rows WHERE import_id=? GROUP BY outcome`, rep.ID)
	if err != nil {
		return nil, fmt.Errorf("import report: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var outcome string
		var n int
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, fmt.Errorf("import report: %w", err)
		}
		switch outcome {
		case outcomePending:
			rep.Remaining = n
		case outcomeImported:
			rep.Imported = n
		case outcomeSkipped:
			rep.Skipped = n
		case outcomeUnmatched:
			rep.Unmatched = n
		case outcomeFailed:
			rep.Failed = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("import report: %w", err)
	}
	rows.Close()
	rep.Processed = rep.Imported + rep.Skipped + rep.Unmatched + rep.Failed
	rep.CanResume = (rep.State == stateFailed || rep.State == stateCancelled) && rep.Remaining > 0 && rep.ImporterID != ""
	if rep.ETA, err = r.eta(ctx, &rep); err != nil {
		return nil, err
	}

	if rep.UnmatchedRows, err = r.items(ctx, rep.ID, outcomeUnmatched); err != nil {
		return nil, err
	}
	if rep.FailedRows, err = r.items(ctx, rep.ID, outcomeFailed); err != nil {
		return nil, err
	}
	if rep.SkippedRows, err = r.items(ctx, rep.ID, outcomeSkipped); err != nil {
		return nil, err
	}
	rep.MoreUnmatched = max(0, rep.Unmatched-len(rep.UnmatchedRows))
	rep.MoreFailed = max(0, rep.Failed-len(rep.FailedRows))
	rep.MoreSkipped = max(0, rep.Skipped-len(rep.SkippedRows))
	return &rep, nil
}

// eta estimates the time left: the pending rows that carry an external id
// (each may need one source request; rows already saved will not) at the
// source's requests per minute. Without a source it is 0.
func (r *Runner) eta(ctx context.Context, rep *Report) (time.Duration, error) {
	if rep.Remaining == 0 {
		return 0, nil
	}
	ref, ok := r.resolve(rep.PluginID, rep.ImporterID)
	if !ok || ref.srcInfo.RequestsPerMinute <= 0 {
		return 0, nil
	}
	var n int
	if err := r.d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_rows WHERE import_id=? AND outcome=? AND external_id <> ''`,
		rep.ID, outcomePending).Scan(&n); err != nil {
		return 0, fmt.Errorf("import report: %w", err)
	}
	return time.Duration(n) * time.Minute / time.Duration(ref.srcInfo.RequestsPerMinute), nil
}

func (r *Runner) items(ctx context.Context, importID int64, outcome string) ([]Item, error) {
	rows, err := r.d.DB.QueryContext(ctx, `SELECT r.title, r.message, COALESCE(r.item_id, 0), COALESCE(i.source_id, ''), COALESCE(i.kind, '')
		FROM import_rows r LEFT JOIN items i ON i.id = r.item_id
		WHERE r.import_id=? AND r.outcome=? ORDER BY r.seq LIMIT ?`, importID, outcome, listCap)
	if err != nil {
		return nil, fmt.Errorf("import report: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Title, &it.Message, &it.ItemID, &it.SourceID, &it.Kind); err != nil {
			return nil, fmt.Errorf("import report: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
