package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// SQL name of the unit notes: a short note on one unit (one step of a count
// or pages track) of a run.
const tableUnitNotes = "unit_notes"

// MaxUnitNoteRunes is the longest unit note (in runes) the library accepts.
const MaxUnitNoteRunes = 2000

// UnitNote is the note on unit Unit (1-based) of progress track Key of run
// RunID. Body uses the formatting language of notes (internal/textfmt).
type UnitNote struct {
	RunID                int64
	Key                  string
	Unit                 int
	Body                 string
	CreatedAt, UpdatedAt time.Time
}

// SetUnitNote stores body as the note on unit unit of track key of run
// runID, replacing an earlier one (its creation time stays). An empty or
// blank body deletes the note. It is refused (ErrInvalid) for a track that
// is not a count or pages track of the item's kind, a unit below 1 or above
// the track's total when the item has one, and a body longer than
// MaxUnitNoteRunes or with control characters (tab and newline are fine).
// A stale run id is ErrNoRun.
func (l *Library) SetUnitNote(ctx context.Context, runID int64, key string, unit int, body string) error {
	blank := strings.TrimSpace(body) == ""
	if !blank {
		if utf8.RuneCountInString(body) > MaxUnitNoteRunes {
			return invalid("the note is longer than %d characters", MaxUnitNoteRunes)
		}
		if err := cleanText("note", body, multiLine); err != nil {
			return err
		}
	}
	if unit < 1 {
		return invalid("the unit must be at least 1")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set unit note: %w", err)
	}
	defer tx.Rollback()
	var itemID int64
	err = tx.QueryRowContext(ctx, `SELECT item_id FROM `+tableRuns+` WHERE id = ?`, runID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRun
	}
	if err != nil {
		return fmt.Errorf("set unit note: %w", err)
	}
	pluginID, kindKey, _, _, err := itemMeta(ctx, tx, itemID)
	if err != nil {
		return err
	}
	kind, err := l.kindDef(pluginID, kindKey)
	if err != nil {
		return err
	}
	def, ok := progressDef(kind, key)
	if !ok {
		return invalid("progress %q is not a progress of kind %q", key, kind.Key)
	}
	if def.Type != plugin.Count && def.Type != plugin.Pages {
		return invalid("notes can only be added to count or pages progress, not %q", key)
	}
	if float64(unit) > maxProgressValue {
		return invalid("the unit %d is too large", unit)
	}
	if def.TotalField != "" {
		var total float64
		err := tx.QueryRowContext(ctx, `SELECT num FROM item_fields WHERE item_id = ? AND key = ? AND num IS NOT NULL ORDER BY seq LIMIT 1`,
			itemID, def.TotalField).Scan(&total)
		switch {
		case err == nil:
			if total > 0 && float64(unit) > total {
				return invalid("the unit %d is above the total of %v", unit, total)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("set unit note: %w", err)
		}
	}
	if blank {
		_, err = tx.ExecContext(ctx, `DELETE FROM `+tableUnitNotes+` WHERE run_id = ? AND progress_key = ? AND unit = ?`, runID, key, unit)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO `+tableUnitNotes+`(run_id, progress_key, unit, body, created_at, updated_at)
			VALUES (?,?,?,?,`+nowSQL+`,`+nowSQL+`)
			ON CONFLICT(run_id, progress_key, unit) DO UPDATE SET body = excluded.body, updated_at = excluded.updated_at`,
			runID, key, unit, body)
	}
	if err != nil {
		return fmt.Errorf("set unit note: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set unit note: %w", err)
	}
	return nil
}

// UnitNotes returns the unit notes of run runID ordered by track and unit;
// an unknown run has none.
func (l *Library) UnitNotes(ctx context.Context, runID int64) ([]UnitNote, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT progress_key, unit, body, created_at, updated_at FROM `+tableUnitNotes+`
		WHERE run_id = ? ORDER BY progress_key, unit`, runID)
	if err != nil {
		return nil, fmt.Errorf("load unit notes: %w", err)
	}
	var out []UnitNote
	for rows.Next() {
		n := UnitNote{RunID: runID}
		var created, updated string
		if err := rows.Scan(&n.Key, &n.Unit, &n.Body, &created, &updated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("load unit notes: %w", err)
		}
		if n.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stored unit note time %q: %w", created, err)
		}
		if n.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stored unit note time %q: %w", updated, err)
		}
		out = append(out, n)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("load unit notes: %w", err)
	}
	return out, nil
}
