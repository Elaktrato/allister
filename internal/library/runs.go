package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"
)

// SQL names of the runs: every viewing / reading / playthrough of an entry
// is a run with its own state and progress. entry_state and display_progress
// are views over the display run (see refreshDisplayRun).
const (
	tableRuns           = "entry_runs"
	tableRunProgress    = "run_progress"
	viewEntryState      = "entry_state"
	viewDisplayProgress = "display_progress"
)

// ErrNoRun: the run does not exist (any more).
var ErrNoRun = errors.New("library: no such run")

// Run is one viewing / reading / playthrough of an entry: its own status,
// rating, review, calendar-day dates and progress. Number is dense (1..n)
// per entry. Progress holds the values of the item's ACTIVE progress keys
// (like Entry). CreatedAt and UpdatedAt are the run's own change times.
type Run struct {
	ID                    int64
	Number                int
	Status                Status
	Rating                *int
	Review                string
	StartedOn, FinishedOn *time.Time
	Progress              map[string]float64
	CreatedAt, UpdatedAt  time.Time
}

// RunUpdate is an edit of one run: every field replaces the stored value
// (nil rating is unrated, nil dates are unset). The run's progress is not
// part of it.
type RunUpdate struct {
	Status                Status
	Rating                *int
	Review                string
	StartedOn, FinishedOn *time.Time
}

// runCols are the entry_runs columns (aliased r) that loadRuns reads.
const runCols = `r.id, r.number, r.status, r.rating, r.review, r.started_on, r.finished_on, r.created_at, r.updated_at`

// Runs returns the item's runs, newest (highest number) first; an item
// without an entry has none.
func (l *Library) Runs(ctx context.Context, itemID int64) ([]Run, error) {
	return loadRuns(ctx, l.db, itemID)
}

// loadRuns reads the item's runs through q (a database or a transaction),
// newest first, with the values of the item's active progress keys.
func loadRuns(ctx context.Context, q queryer, itemID int64) ([]Run, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+runCols+` FROM `+tableRuns+` r WHERE r.item_id = ? ORDER BY r.number DESC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("load runs: %w", err)
	}
	var runs []Run
	index := map[int64]int{}
	for rows.Next() {
		var r Run
		var st stateScan
		var created string
		if err := rows.Scan(&r.ID, &r.Number, &st.status, &st.rating, &r.Review, &st.started, &st.finished, &created, &st.updated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("load runs: %w", err)
		}
		var e Entry
		if err := st.fill(&e); err != nil {
			rows.Close()
			return nil, err
		}
		if r.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stored run time %q: %w", created, err)
		}
		r.Status, r.Rating, r.StartedOn, r.FinishedOn, r.UpdatedAt = e.Status, e.Rating, e.StartedOn, e.FinishedOn, e.UpdatedAt
		r.Progress = map[string]float64{}
		index[r.ID] = len(runs)
		runs = append(runs, r)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("load runs: %w", err)
	}
	if len(runs) == 0 {
		return nil, nil
	}
	var pk string
	if err := q.QueryRowContext(ctx, `SELECT progress_keys FROM items WHERE id = ?`, itemID).Scan(&pk); err != nil {
		return nil, fmt.Errorf("load runs: %w", err)
	}
	var active []string
	if err := decodeKeys(pk, &active); err != nil {
		return nil, err
	}
	prows, err := q.QueryContext(ctx, `SELECT p.run_id, p.key, p.value FROM `+tableRunProgress+` p
		JOIN `+tableRuns+` r ON r.id = p.run_id WHERE r.item_id = ?`, itemID)
	if err != nil {
		return nil, fmt.Errorf("load run progress: %w", err)
	}
	for prows.Next() {
		var id int64
		var k string
		var v float64
		if err := prows.Scan(&id, &k, &v); err != nil {
			prows.Close()
			return nil, fmt.Errorf("load run progress: %w", err)
		}
		if i, ok := index[id]; ok && slices.Contains(active, k) {
			runs[i].Progress[k] = v
		}
	}
	if err := closeRows(prows); err != nil {
		return nil, fmt.Errorf("load run progress: %w", err)
	}
	return runs, nil
}

// StartRun starts the entry again: a new run n+1, active, started on the
// calendar day of today (in today's own zone: the caller passes the
// configured time zone's now), with the display run's rating, no progress
// and no review. It is refused (ErrInvalid) unless the display run is
// completed, and ErrNoEntry for an item without an entry. The start is
// logged as a manual status event completed -> active on the new run, which
// becomes the display run. It returns the new run.
func (l *Library) StartRun(ctx context.Context, itemID int64, today time.Time) (Run, error) {
	started, ok := writtenDay(&today)
	if !ok {
		return Run{}, invalid("dates must be in the years 1 to 9999")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("start run: %w", err)
	}
	defer tx.Rollback()
	displayID, err := displayRunTx(ctx, tx, itemID)
	if err != nil {
		return Run{}, err
	}
	var status string
	var rating sql.NullInt64
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT status, rating, (SELECT MAX(number) FROM `+tableRuns+` WHERE item_id = ?)
		FROM `+tableRuns+` WHERE id = ?`, itemID, displayID).Scan(&status, &rating, &last); err != nil {
		return Run{}, fmt.Errorf("start run: %w", err)
	}
	if Status(status) != Completed {
		return Run{}, invalid("only a completed entry can be started again (it is %s)", status)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO `+tableRuns+`(item_id, number, status, rating, review, started_on, created_at, updated_at)
		VALUES (?,?,?,?,'',?,`+nowSQL+`,`+nowSQL+`)`, itemID, last+1, string(Active), rating, started)
	if err != nil {
		return Run{}, fmt.Errorf("start run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return Run{}, fmt.Errorf("start run: %w", err)
	}
	if err := insertEvent(ctx, tx, itemID, runID, eventStatus, "", nil, nil, string(Completed), string(Active), OriginManual); err != nil {
		return Run{}, err
	}
	if err := refreshDisplayRun(ctx, tx, itemID); err != nil {
		return Run{}, err
	}
	runs, err := loadRuns(ctx, tx, itemID)
	if err != nil {
		return Run{}, err
	}
	if len(runs) == 0 || runs[0].ID != runID {
		return Run{}, fmt.Errorf("start run: run %d not found after insert", runID)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("start run: %w", err)
	}
	return runs[0], nil
}

// validateRunUpdate checks u like ValidateEntry checks an entry's state.
func validateRunUpdate(u RunUpdate) error {
	if !validStatus(u.Status) {
		return invalid("status %q must be one of planned, active, paused, completed, dropped", u.Status)
	}
	if u.Rating != nil && (*u.Rating < 0 || *u.Rating > 10) {
		return invalid("rating %d must be between 0 and 10", *u.Rating)
	}
	if utf8.RuneCountInString(u.Review) > MaxReviewRunes {
		return invalid("review is longer than %d characters", MaxReviewRunes)
	}
	if err := cleanText("review", u.Review, multiLine); err != nil {
		return err
	}
	started, ok1 := writtenDay(u.StartedOn)
	finished, ok2 := writtenDay(u.FinishedOn)
	if !ok1 || !ok2 {
		return invalid("dates must be in the years 1 to 9999")
	}
	if started != "" && finished != "" && finished < started {
		return invalid("finish date %s is before the start date %s", finished, started)
	}
	return nil
}

// nullDay is the stored value of a calendar day: NULL when unset.
func nullDay(t *time.Time) any {
	if d, _ := writtenDay(t); d != "" {
		return d
	}
	return nil
}

// UpdateRun replaces a run's status, rating, review and dates. A past run
// (any run but the latest, even when it is the display run because the
// latest is a dropped repeat) can only be completed or dropped; the latest
// run takes any status. Status and rating
// changes are logged as manual events on that run, and the display run is
// recomputed. A stale id is ErrNoRun.
func (l *Library) UpdateRun(ctx context.Context, runID int64, u RunUpdate) error {
	if err := validateRunUpdate(u); err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	defer tx.Rollback()
	var itemID int64
	var number, latest int
	var status string
	var rating sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT item_id, number, status, rating,
		(SELECT MAX(number) FROM `+tableRuns+` l WHERE l.item_id = r.item_id)
		FROM `+tableRuns+` r WHERE id = ?`, runID).Scan(&itemID, &number, &status, &rating, &latest)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRun
	}
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	if number != latest && u.Status != Completed && u.Status != Dropped {
		return invalid("a past run can only be completed or dropped, not %s", u.Status)
	}
	var newRating any
	if u.Rating != nil {
		newRating = *u.Rating
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+tableRuns+` SET status = ?, rating = ?, review = ?, started_on = ?, finished_on = ?,
		updated_at = `+nowSQL+` WHERE id = ?`, string(u.Status), newRating, u.Review, nullDay(u.StartedOn), nullDay(u.FinishedOn), runID); err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	if Status(status) != u.Status {
		if err := insertEvent(ctx, tx, itemID, runID, eventStatus, "", nil, nil, status, string(u.Status), OriginManual); err != nil {
			return err
		}
	}
	var oldRating *int
	if rating.Valid {
		r := int(rating.Int64)
		oldRating = &r
	}
	if !sameRating(oldRating, u.Rating) {
		if err := insertEvent(ctx, tx, itemID, runID, eventRating, "", ratingNum(oldRating), ratingNum(u.Rating), "", "", OriginManual); err != nil {
			return err
		}
	}
	if err := refreshDisplayRun(ctx, tx, itemID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

// DeleteRun deletes a run with its progress and unit notes; the later runs
// renumber densely and the display run is recomputed. Its events stay (past
// activity never changes) without a run. Deleting the entry's only run is
// refused (ErrInvalid: that is removing the entry); a stale id is ErrNoRun.
func (l *Library) DeleteRun(ctx context.Context, runID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	defer tx.Rollback()
	var itemID int64
	var number, total int
	err = tx.QueryRowContext(ctx, `SELECT item_id, number, (SELECT COUNT(*) FROM `+tableRuns+` c WHERE c.item_id = r.item_id)
		FROM `+tableRuns+` r WHERE id = ?`, runID).Scan(&itemID, &number, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRun
	}
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	if total <= 1 {
		return invalid("the only run of an entry cannot be deleted; remove the entry instead")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+tableRuns+` WHERE id = ?`, runID); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	// one row at a time, lowest first: each number below is free by then,
	// so the unique (item_id, number) index never sees a duplicate
	rows, err := tx.QueryContext(ctx, `SELECT id FROM `+tableRuns+` WHERE item_id = ? AND number > ? ORDER BY number`, itemID, number)
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	var later []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("delete run: %w", err)
		}
		later = append(later, id)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	for _, id := range later {
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableRuns+` SET number = number - 1 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete run: %w", err)
		}
	}
	if err := refreshDisplayRun(ctx, tx, itemID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	return nil
}

// displayRunTx returns the id of the item's display run as read inside tx,
// or ErrNoEntry when the item has no entry.
func displayRunTx(ctx context.Context, tx *sql.Tx, itemID int64) (int64, error) {
	var id sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT display_run_id FROM `+tableEntries+` WHERE item_id = ?`, itemID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoEntry
	}
	if err != nil {
		return 0, fmt.Errorf("display run: %w", err)
	}
	if !id.Valid {
		return 0, fmt.Errorf("display run: entry %d has no display run", itemID)
	}
	return id.Int64, nil
}

// refreshDisplayRun applies the display run rule to the item's runs and
// stores the result in entries.display_run_id: the latest run, except that a
// dropped repeat run (number > 1) gives way to the latest completed run, so
// an abandoned repeat does not make the entry look dropped. With no
// completed run the latest run stays. Every run write calls it in the
// write's transaction.
func refreshDisplayRun(ctx context.Context, tx *sql.Tx, itemID int64) error {
	var id int64
	var number int
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id, number, status FROM `+tableRuns+` WHERE item_id = ? ORDER BY number DESC LIMIT 1`, itemID).
		Scan(&id, &number, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("display run: entry %d has no run", itemID)
	}
	if err != nil {
		return fmt.Errorf("display run: %w", err)
	}
	if number > 1 && Status(status) == Dropped {
		var done int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM `+tableRuns+` WHERE item_id = ? AND status = ? ORDER BY number DESC LIMIT 1`,
			itemID, string(Completed)).Scan(&done)
		switch {
		case err == nil:
			id = done
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("display run: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET display_run_id = ? WHERE item_id = ?`, id, itemID); err != nil {
		return fmt.Errorf("display run: %w", err)
	}
	return nil
}
