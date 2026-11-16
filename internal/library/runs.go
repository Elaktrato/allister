package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
