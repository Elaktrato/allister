package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// A bulk "Update from source" run of one plugin: every sourced item of the
// plugin that has a list entry is asked once (one job per item, kind
// RefreshJobKind, payload {"run_id", "item_id", "plugin_id"}), and each job
// records its outcome here. The run lives in source_refresh_runs and the
// jobs table, so it survives a restart.

// RefreshJobKind is the job kind of one item of a refresh run (package
// jobs registers its handler under this kind). The jobs table belongs to
// package jobs and is only read here.
const RefreshJobKind = "refresh_item"

// Outcomes of one item of a refresh run (RecordRefresh).
const (
	RefreshUpdated   = "updated"
	RefreshUnchanged = "unchanged"
	RefreshFailed    = "failed"
)

// RefreshStoppedCredential is the stop reason of a run whose source has no
// usable credential.
const RefreshStoppedCredential = "credential"

// maxFailedTitles bounds the failed titles a run keeps.
const maxFailedTitles = 50

// maxFailedTitleRunes bounds one kept failed title.
const maxFailedTitleRunes = 200

// ErrRefreshRunning: the plugin already has an unfinished refresh run.
var ErrRefreshRunning = errors.New("library: an update from source is already running")

// ErrNoRefreshRun: the refresh run does not exist.
var ErrNoRefreshRun = errors.New("library: no such update from source")

// RefreshRun is one bulk "Update from source" run of a plugin. FinishedAt
// is nil while it runs; Stopped names why it stopped asking the source
// (RefreshStoppedCredential), "" otherwise. FailedTitles holds at most 50
// titles of the failed items, in the order they failed.
type RefreshRun struct {
	ID                                int64
	PluginID                          string
	StartedAt                         time.Time
	FinishedAt                        *time.Time
	Total, Updated, Unchanged, Failed int
	FailedTitles                      []string
	Stopped                           string
}

// Done is the number of items the run has recorded.
func (r RefreshRun) Done() int { return r.Updated + r.Unchanged + r.Failed }

// refreshCandidatesSQL selects the items a run of plugin ? asks: sourced
// (not manual) items with a list entry, in id order.
const refreshCandidatesSQL = `FROM items i JOIN ` + tableEntries + ` e ON e.item_id = i.id
	WHERE i.plugin_id = ? AND i.source <> 'manual' AND i.source_id <> ''`

// RefreshCandidates is the number of items a refresh run of the plugin
// would ask now.
func (l *Library) RefreshCandidates(ctx context.Context, pluginID string) (int, error) {
	var n int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) `+refreshCandidatesSQL, pluginID).Scan(&n); err != nil {
		return 0, fmt.Errorf("refresh candidates: %w", err)
	}
	return n, nil
}

// StartRefreshRun starts a refresh run of the plugin over its sourced,
// non-manual items with a list entry and returns the run and their ids (in
// id order); the caller enqueues one job per id. A run without items is
// finished at once. ErrRefreshRunning while the plugin has an unfinished
// run (after reconciling runs whose jobs are gone).
func (l *Library) StartRefreshRun(ctx context.Context, pluginID string) (RefreshRun, []int64, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
	}
	defer tx.Rollback()
	if err := reconcileRefreshRuns(ctx, tx); err != nil {
		return RefreshRun{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id `+refreshCandidatesSQL+` ORDER BY i.id`, pluginID)
	if err != nil {
		return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
	}
	// one statement: the check for an unfinished run and the insert cannot
	// interleave with another start
	finished := "NULL"
	if len(ids) == 0 {
		finished = nowSQL
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO source_refresh_runs(plugin_id, started_at, finished_at, total)
		SELECT ?, `+nowSQL+`, `+finished+`, ?
		WHERE NOT EXISTS (SELECT 1 FROM source_refresh_runs WHERE plugin_id = ? AND finished_at IS NULL)
		RETURNING id`, pluginID, len(ids), pluginID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return RefreshRun{}, nil, ErrRefreshRunning
	}
	if err != nil {
		return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
	}
	run, err := refreshRun(ctx, tx, `id = ?`, id)
	if err != nil {
		return RefreshRun{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return RefreshRun{}, nil, fmt.Errorf("start refresh: %w", err)
	}
	return *run, ids, nil
}

// RecordRefresh counts one item of the run with outcome RefreshUpdated,
// RefreshUnchanged or RefreshFailed (title is kept for a failed item, up to
// 50 titles), and finishes the run when every item is counted. The count
// and the finish check are one transaction of increments, so concurrent
// workers never lose a count. ErrInvalid for another outcome,
// ErrNoRefreshRun for an unknown run.
func (l *Library) RecordRefresh(ctx context.Context, runID int64, outcome, title string) error {
	var (
		stmt string
		args []any
	)
	switch outcome {
	case RefreshUpdated:
		stmt = `UPDATE source_refresh_runs SET updated = updated + 1 WHERE id = ?`
	case RefreshUnchanged:
		stmt = `UPDATE source_refresh_runs SET unchanged = unchanged + 1 WHERE id = ?`
	case RefreshFailed:
		stmt = `UPDATE source_refresh_runs SET failed = failed + 1,
			failed_titles = CASE WHEN ? <> '' AND json_array_length(failed_titles) < ?
				THEN json_insert(failed_titles, '$[#]', ?) ELSE failed_titles END
			WHERE id = ?`
		t := plugin.SingleLine(title, maxFailedTitleRunes)
		args = []any{t, maxFailedTitles, t}
	default:
		return fmt.Errorf("%w: unknown refresh outcome %q", ErrInvalid, outcome)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record refresh: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, stmt, append(args, runID)...)
	if err != nil {
		return fmt.Errorf("record refresh: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("record refresh: %w", err)
	} else if n == 0 {
		return ErrNoRefreshRun
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_refresh_runs SET finished_at = `+nowSQL+`
		WHERE id = ? AND finished_at IS NULL AND updated + unchanged + failed >= total`, runID); err != nil {
		return fmt.Errorf("record refresh: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record refresh: %w", err)
	}
	return nil
}

// StopRefreshRun records why the run stopped asking the source (the first
// reason stays). Its remaining jobs still record their items, as failed
// without asking the source, so the run finishes.
func (l *Library) StopRefreshRun(ctx context.Context, runID int64, reason string) error {
	if _, err := l.db.ExecContext(ctx, `UPDATE source_refresh_runs SET stopped = ? WHERE id = ? AND stopped = ''`,
		plugin.SingleLine(reason, 40), runID); err != nil {
		return fmt.Errorf("stop refresh: %w", err)
	}
	return nil
}

// RefreshRunByID returns the run, nil when there is none.
func (l *Library) RefreshRunByID(ctx context.Context, runID int64) (*RefreshRun, error) {
	return refreshRun(ctx, l.db, `id = ?`, runID)
}

// LatestRefreshRun returns the plugin's newest refresh run, nil when it has
// none. It first finishes runs whose jobs are gone (see
// reconcileRefreshRuns).
func (l *Library) LatestRefreshRun(ctx context.Context, pluginID string) (*RefreshRun, error) {
	if err := reconcileRefreshRuns(ctx, l.db); err != nil {
		return nil, err
	}
	return refreshRun(ctx, l.db, `plugin_id = ? ORDER BY id DESC LIMIT 1`, pluginID)
}

// refreshJobRunSQL is a job row's run_id (NULL for a payload that is not
// JSON).
const refreshJobRunSQL = `CASE WHEN json_valid(j.payload_json) THEN json_extract(j.payload_json, '$.run_id') END`

// reconcileRefreshRuns finishes unfinished runs that no job will ever
// count again: no job of the run is pending or running, and either every
// item's job was enqueued (the queue gave up on some, or they are gone) or
// the run was stopped. Their uncounted items count as failed.
func reconcileRefreshRuns(ctx context.Context, x execer) error {
	_, err := x.ExecContext(ctx, `UPDATE source_refresh_runs SET finished_at = `+nowSQL+`,
		failed = MAX(failed, total - updated - unchanged)
		WHERE finished_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = ? AND j.state IN ('pending','running')
			AND `+refreshJobRunSQL+` = source_refresh_runs.id)
		AND (stopped <> '' OR (SELECT COUNT(*) FROM jobs j WHERE j.kind = ?
			AND `+refreshJobRunSQL+` = source_refresh_runs.id) >= total)`, RefreshJobKind, RefreshJobKind)
	if err != nil {
		return fmt.Errorf("reconcile refresh runs: %w", err)
	}
	return nil
}

func refreshRun(ctx context.Context, q queryer, where string, args ...any) (*RefreshRun, error) {
	var (
		r               RefreshRun
		started, titles string
		finished        sql.NullString
	)
	err := q.QueryRowContext(ctx, `SELECT id, plugin_id, started_at, finished_at, total, updated, unchanged, failed,
		failed_titles, stopped FROM source_refresh_runs WHERE `+where, args...).
		Scan(&r.ID, &r.PluginID, &started, &finished, &r.Total, &r.Updated, &r.Unchanged, &r.Failed, &titles, &r.Stopped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("refresh run: %w", err)
	}
	if r.StartedAt, err = time.Parse(time.RFC3339, started); err != nil {
		return nil, fmt.Errorf("refresh run %d: %w", r.ID, err)
	}
	if finished.Valid {
		t, err := time.Parse(time.RFC3339, finished.String)
		if err != nil {
			return nil, fmt.Errorf("refresh run %d: %w", r.ID, err)
		}
		r.FinishedAt = &t
	}
	if err := json.Unmarshal([]byte(titles), &r.FailedTitles); err != nil {
		return nil, fmt.Errorf("refresh run %d: failed titles: %w", r.ID, err)
	}
	return &r, nil
}
