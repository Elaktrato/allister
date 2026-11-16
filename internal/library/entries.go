package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// Status is the class of a list entry; the five values are fixed by the core
// (plugins only supply labels).
type Status string

const (
	Planned   Status = "planned"
	Active    Status = "active"
	Paused    Status = "paused"
	Completed Status = "completed"
	Dropped   Status = "dropped"
)

// Statuses returns the five statuses in a stable order.
func Statuses() []Status { return []Status{Planned, Active, Paused, Completed, Dropped} }

func validStatus(s Status) bool { return slices.Contains(Statuses(), s) }

// SQL table name of the user's list: one row per entry (library
// membership and the entry's notes); the state lives in its runs (runs.go).
const tableEntries = "entries"

// MaxNotesRunes is the longest entry notes text (in runes) the library
// accepts; forms cap the typed notes at it. MaxReviewRunes is the same for a
// run's review.
const (
	MaxNotesRunes  = 10000
	MaxReviewRunes = 10000
)

// Entry is the user's list entry of an item as it presents itself: the
// entry's notes plus the state of its display run (status, rating, review,
// dates, progress; see refreshDisplayRun). Writing an Entry writes the
// notes to the entry and the rest to the display run. Progress holds the
// values of the item's ACTIVE progress keys; values of keys that are no
// longer active stay in the database but are not part of an Entry (see
// SetActiveProgress). StartedOn and FinishedOn are calendar days: written as
// the calendar day of the given time (in its own zone), read back as UTC
// midnight. UpdatedAt is the newer of the entry's and the run's change
// times. RunID, RunNumber and Repeats (the number of completed runs) are
// read only.
type Entry struct {
	ItemID                int64
	Status                Status
	Rating                *int
	Notes                 string
	Review                string
	StartedOn, FinishedOn *time.Time
	Progress              map[string]float64
	UpdatedAt             time.Time
	RunID                 int64
	RunNumber             int
	Repeats               int
}

// stateCols are the entry_state columns (aliased e) that stateScan reads.
const stateCols = `e.status, e.rating, e.notes, e.review, e.started_on, e.finished_on, e.updated_at, e.run_id, e.run_number, e.repeats`

// stateScan holds the raw entry_state columns until fill converts them.
type stateScan struct {
	status, updated   string
	rating            sql.NullInt64
	started, finished sql.NullString
}

// dest returns the scan destinations of stateCols, in order.
func (s *stateScan) dest(e *Entry) []any {
	return []any{&s.status, &s.rating, &e.Notes, &e.Review, &s.started, &s.finished, &s.updated, &e.RunID, &e.RunNumber, &e.Repeats}
}

// fill converts the scanned columns into e.
func (s *stateScan) fill(e *Entry) error {
	return e.fill(s.status, s.rating, s.started, s.finished, s.updated)
}

// writtenDay returns the calendar day of t as written (YYYY-MM-DD), or ok=false
// when the year is outside 1..9999.
func writtenDay(t *time.Time) (day string, ok bool) {
	if t == nil {
		return "", true
	}
	if t.Year() < 1 || t.Year() > 9999 {
		return "", false
	}
	return plugin.DateOnly(*t).Format(dateLayout), true
}

// ValidateEntry checks e against the item it belongs to: item supplies the
// active progress keys and, through the kind's total fields, the totals. It
// returns ErrInvalid with a readable reason. kind must be the item's kind.
func (l *Library) ValidateEntry(kind plugin.KindDef, item *Item, e Entry) error {
	if !validStatus(e.Status) {
		return invalid("status %q must be one of planned, active, paused, completed, dropped", e.Status)
	}
	if e.Rating != nil && (*e.Rating < 0 || *e.Rating > 10) {
		return invalid("rating %d must be between 0 and 10", *e.Rating)
	}
	if utf8.RuneCountInString(e.Notes) > MaxNotesRunes {
		return invalid("notes are longer than %d characters", MaxNotesRunes)
	}
	if err := cleanText("notes", e.Notes, multiLine); err != nil {
		return err
	}
	if utf8.RuneCountInString(e.Review) > MaxReviewRunes {
		return invalid("review is longer than %d characters", MaxReviewRunes)
	}
	if err := cleanText("review", e.Review, multiLine); err != nil {
		return err
	}
	started, ok1 := writtenDay(e.StartedOn)
	finished, ok2 := writtenDay(e.FinishedOn)
	if !ok1 || !ok2 {
		return invalid("dates must be in the years 1 to 9999")
	}
	// ISO calendar days compare correctly as text
	if started != "" && finished != "" && finished < started {
		return invalid("finish date %s is before the start date %s", finished, started)
	}
	keys := make([]string, 0, len(e.Progress))
	for k := range e.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !slices.Contains(item.ProgressKeys, k) {
			return invalid("progress %q is not active for this item", k)
		}
		def, ok := progressDef(kind, k)
		if !ok {
			return invalid("progress %q is not a progress of kind %q", k, kind.Key)
		}
		total, bounded := progressBound(def, item)
		if err := checkProgressValue(def, e.Progress[k], total, bounded); err != nil {
			return err
		}
	}
	return nil
}

// writeEntry stores the entry's notes and, on its display run, the scalar
// state and the progress values present in e.Progress; an item without an
// entry gets one with run 1. The caller validated e.
func writeEntry(ctx context.Context, tx *sql.Tx, itemID int64, e Entry) error {
	_, err := writeEntryMode(ctx, tx, itemID, e, false)
	return err
}

// errPastDisplayRun refuses reopening a past display run: the completed run
// shown behind a dropped repeat. Only the latest run may be planned, active
// or paused (as UpdateRun keeps it); starting again makes a new run.
var errPastDisplayRun = invalid("the shown run is an earlier run behind a dropped repeat: it can only be completed or dropped; start it again instead")

// pastDisplayRun reports whether the item's display run is not its latest
// run (a dropped repeat fell back to the latest completed run).
func pastDisplayRun(ctx context.Context, q queryer, itemID int64) (bool, error) {
	var past bool
	err := q.QueryRowContext(ctx, `SELECT r.number < (SELECT MAX(l.number) FROM `+tableRuns+` l WHERE l.item_id = e.item_id)
		FROM `+tableEntries+` e JOIN `+tableRuns+` r ON r.id = e.display_run_id WHERE e.item_id = ?`, itemID).Scan(&past)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("display run: %w", err)
	}
	return past, nil
}

// writeEntryMode is writeEntry; with createOnly an existing entry is left
// untouched and created is false (nothing written). Setting a past display
// run (see pastDisplayRun) to planned, active or paused is ErrInvalid.
func writeEntryMode(ctx context.Context, tx *sql.Tx, itemID int64, e Entry, createOnly bool) (created bool, err error) {
	started, _ := writtenDay(e.StartedOn)
	finished, _ := writtenDay(e.FinishedOn)
	var rating, st, fin any
	if e.Rating != nil {
		rating = *e.Rating
	}
	if started != "" {
		st = started
	}
	if finished != "" {
		fin = finished
	}
	runID, err := displayRunTx(ctx, tx, itemID)
	switch {
	case errors.Is(err, ErrNoEntry):
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntries+`(item_id, notes, added_at, updated_at)
			VALUES (?,?,`+nowSQL+`,`+nowSQL+`)`, itemID, e.Notes); err != nil {
			return false, fmt.Errorf("save entry: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO `+tableRuns+`(item_id, number, status, rating, review, started_on, finished_on, created_at, updated_at)
			VALUES (?,1,?,?,?,?,?,`+nowSQL+`,`+nowSQL+`)`, itemID, string(e.Status), rating, e.Review, st, fin)
		if err != nil {
			return false, fmt.Errorf("save entry: %w", err)
		}
		if runID, err = res.LastInsertId(); err != nil {
			return false, fmt.Errorf("save entry: %w", err)
		}
		created = true
	case err != nil:
		return false, err
	case createOnly:
		return false, nil
	default:
		if e.Status != Completed && e.Status != Dropped {
			past, err := pastDisplayRun(ctx, tx, itemID)
			if err != nil {
				return false, err
			}
			if past {
				return false, errPastDisplayRun
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET notes = ?, updated_at = `+nowSQL+` WHERE item_id = ?`,
			e.Notes, itemID); err != nil {
			return false, fmt.Errorf("save entry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableRuns+` SET status = ?, rating = ?, review = ?, started_on = ?, finished_on = ?,
			updated_at = `+nowSQL+` WHERE id = ?`, string(e.Status), rating, e.Review, st, fin, runID); err != nil {
			return false, fmt.Errorf("save entry: %w", err)
		}
	}
	keys := make([]string, 0, len(e.Progress))
	for k := range e.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableRunProgress+`(run_id, key, value) VALUES (?,?,?)
			ON CONFLICT(run_id, key) DO UPDATE SET value = excluded.value`, runID, k, e.Progress[k]); err != nil {
			return false, fmt.Errorf("save entry progress: %w", err)
		}
	}
	if err := refreshDisplayRun(ctx, tx, itemID); err != nil {
		return false, err
	}
	return created, nil
}

// AddToList saves the item and its list entry in ONE transaction and returns
// the item id. The entry is validated against the not yet saved item (totals
// from d.Fields, active keys from the options or the kind's defaults) before
// anything is written. An existing item yields an *AlreadySavedError
// (matching ErrAlreadySaved) and nothing is written. e.ItemID is ignored.
func (l *Library) AddToList(ctx context.Context, pluginID, kind string, d plugin.ItemData, e Entry, opts ...SaveOption) (int64, error) {
	o := saveOpts{syncState: "pending"}
	for _, f := range opts {
		f(&o)
	}
	if err := checkSyncState(o.syncState); err != nil {
		return 0, err
	}
	p, err := l.prepare(pluginID, kind, d, o.progressKeys)
	if err != nil {
		return 0, err
	}
	if err := l.ValidateEntry(p.def, &Item{ProgressKeys: p.progress, Fields: d.Fields}, e); err != nil {
		return 0, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("add to list: %w", err)
	}
	defer tx.Rollback()
	id, err := insertItem(ctx, tx, pluginID, kind, d, o.syncState, p)
	if err != nil {
		return 0, err
	}
	if err := writeEntry(ctx, tx, id, e); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("add to list: %w", err)
	}
	return id, nil
}

// UpsertEntry creates or replaces the entry of e.ItemID after validating it
// against the stored item. Notes, and the display run's status, rating,
// review and dates, are replaced as a whole (a new entry gets run 1); the
// display run's progress values are replaced only for the keys present in
// e.Progress, other keys are left untouched (see ClearProgress). It returns ErrNoEntry for
// an unknown item, and ErrInvalid when it would set a past display run (see
// pastDisplayRun) to planned, active or paused. The change is recorded in the event log; the option
// WithEventOrigin sets its origin (manual by default), and creating the entry
// records nothing.
func (l *Library) UpsertEntry(ctx context.Context, e Entry, opts ...EntryOption) error {
	o := entryOptions{origin: OriginManual}
	for _, f := range opts {
		f(&o)
	}
	if !validOrigin(o.origin) {
		return invalid("unknown event origin %q", o.origin)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save entry: %w", err)
	}
	defer tx.Rollback()
	// the active keys and totals are read inside the (write-locking)
	// transaction, so a concurrent change cannot slip in before the write
	it, def, err := l.txItem(ctx, tx, e.ItemID)
	if err != nil {
		return err
	}
	old, err := entryTx(ctx, tx, e.ItemID)
	if err != nil {
		return err
	}
	if err := l.ValidateEntry(def, it, e); err != nil {
		return err
	}
	if err := writeEntry(ctx, tx, e.ItemID, e); err != nil {
		return err
	}
	if err := recordEvents(ctx, tx, e.ItemID, old, e, o.origin); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save entry: %w", err)
	}
	return nil
}

// CreateEntry creates the entry of e.ItemID only when the item has none yet
// (insert-if-absent): in ONE write-locking transaction it validates e against
// the stored item (like UpsertEntry) and inserts it. An existing entry is
// left untouched and ErrEntryExists is returned; an unknown item is
// ErrNoEntry. Progress values are written for the keys in e.Progress.
func (l *Library) CreateEntry(ctx context.Context, e Entry) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	defer tx.Rollback()
	it, def, err := l.txItem(ctx, tx, e.ItemID)
	if err != nil {
		return err
	}
	if err := l.ValidateEntry(def, it, e); err != nil {
		return err
	}
	created, err := writeEntryMode(ctx, tx, e.ItemID, e, true)
	if err != nil {
		return err
	}
	if !created {
		return ErrEntryExists
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	return nil
}

// Entry returns the item's entry with the values of its active progress keys,
// or nil, nil when the item has no entry.
func (l *Library) Entry(ctx context.Context, itemID int64) (*Entry, error) {
	return loadEntry(ctx, l.db, itemID)
}

// loadEntry reads the item's entry through q (a database or a transaction) and
// returns nil, nil when the item has no entry.
func loadEntry(ctx context.Context, q queryer, itemID int64) (*Entry, error) {
	e := Entry{ItemID: itemID, Progress: map[string]float64{}}
	var pk string
	var st stateScan
	err := q.QueryRowContext(ctx, `SELECT `+stateCols+`, i.progress_keys
		FROM `+viewEntryState+` e JOIN items i ON i.id = e.item_id WHERE e.item_id = ?`, itemID).
		Scan(append(st.dest(&e), &pk)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load entry: %w", err)
	}
	var active []string
	if err := decodeKeys(pk, &active); err != nil {
		return nil, err
	}
	if err := st.fill(&e); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM `+tableRunProgress+` WHERE run_id = ?`, e.RunID)
	if err != nil {
		return nil, fmt.Errorf("load entry progress: %w", err)
	}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("load entry progress: %w", err)
		}
		if slices.Contains(active, k) {
			e.Progress[k] = v
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("load entry progress: %w", err)
	}
	return &e, nil
}

// fill sets the scalar fields from stored column values.
func (e *Entry) fill(status string, rating sql.NullInt64, started, finished sql.NullString, updated string) error {
	e.Status = Status(status)
	if rating.Valid {
		r := int(rating.Int64)
		e.Rating = &r
	}
	parse := func(ns sql.NullString) (*time.Time, error) {
		if !ns.Valid {
			return nil, nil
		}
		t, err := time.Parse(dateLayout, ns.String)
		if err != nil {
			return nil, fmt.Errorf("stored entry date %q: %w", ns.String, err)
		}
		return &t, nil
	}
	var err error
	if e.StartedOn, err = parse(started); err != nil {
		return err
	}
	if e.FinishedOn, err = parse(finished); err != nil {
		return err
	}
	if e.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return fmt.Errorf("stored entry time %q: %w", updated, err)
	}
	return nil
}

// RemoveEntry deletes the item's entry together with its runs and their
// progress values (active and retained), so a later entry starts from zero;
// its events stay with the item, without a run. The item stays,
// but leaves its series (a part always has an entry); the series is
// renumbered, and removed with its last part. It is a no-op (nil) when the item has no entry or does not exist.
func (l *Library) RemoveEntry(ctx context.Context, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	defer tx.Rollback()
	if err := removeEntryTx(ctx, tx, itemID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	return nil
}

// removeEntryTx is RemoveEntry inside tx.
func removeEntryTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	var seriesID int64
	switch err := tx.QueryRowContext(ctx, `SELECT series_id FROM series_parts WHERE item_id = ?`, itemID).Scan(&seriesID); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("remove entry: %w", err)
	default:
		if _, err := tx.ExecContext(ctx, `DELETE FROM series_parts WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("remove entry: %w", err)
		}
		if _, err := renumberSeries(ctx, tx, seriesID); err != nil {
			return fmt.Errorf("remove entry: %w", err)
		}
	}
	// runs, their progress, tags and list places cascade from the entry
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+tableEntries+` WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	return nil
}

// ApplyImport applies the three list fields of an import row - status,
// rating and the primary progress value - to an existing entry's display run
// (an import never starts a run; when the display run is a past run, see
// pastDisplayRun, its status is kept) and bumps its updated_at. With a non-nil
// newerThan it does nothing when the entry is not older than that source
// modification time: the user's own later changes win. Everything else
// (notes, review, dates, the other progress tracks) stays as it is. The
// change is recorded in the event log with the import origin, which the
// activity heatmap leaves out. It reports whether anything was written.
func (l *Library) ApplyImport(ctx context.Context, itemID int64, in Entry, newerThan *time.Time) (bool, error) {
	cur, err := l.Entry(ctx, itemID)
	if err != nil {
		return false, err
	}
	if cur == nil {
		return false, nil // no entry to update
	}
	if newerThan != nil && !cur.UpdatedAt.Before(*newerThan) {
		return false, nil
	}
	it, err := l.Item(ctx, itemID)
	if err != nil {
		return false, err
	}
	if it == nil {
		return false, nil
	}
	upd := Entry{ItemID: itemID, Status: cur.Status, Rating: cur.Rating,
		Notes: cur.Notes, Review: cur.Review, StartedOn: cur.StartedOn, FinishedOn: cur.FinishedOn,
		Progress: map[string]float64{}}
	for k, v := range cur.Progress {
		upd.Progress[k] = v
	}
	// an import never reopens a past display run: its status stays
	past, err := pastDisplayRun(ctx, l.db, itemID)
	if err != nil {
		return false, err
	}
	if in.Status != "" && !past {
		upd.Status = in.Status
	}
	if in.Rating != nil {
		upd.Rating = in.Rating
	}
	for key, value := range in.Progress {
		if slices.Contains(it.ProgressKeys, key) {
			upd.Progress[key] = value
		}
	}
	if err := l.UpsertEntry(ctx, upd, WithEventOrigin(OriginImport)); err != nil {
		return false, err
	}
	return true, nil
}
