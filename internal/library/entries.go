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

// SQL table names of the user's list.
const (
	tableEntries       = "entries"
	tableEntryProgress = "entry_progress"
)

// MaxCommentRunes is the longest entry comment (in runes) the library
// accepts; forms cap the typed comment at it.
const MaxCommentRunes = 2000

// Entry is the user's list entry of an item. Progress holds the values of
// the item's ACTIVE progress keys; values of keys that are no longer active
// stay in the database but are not part of an Entry (see SetActiveProgress).
// StartedOn and FinishedOn are calendar days: written as the calendar day of
// the given time (in its own zone), read back as UTC midnight.
type Entry struct {
	ItemID                int64
	Status                Status
	Rating                *int
	Comment               string
	StartedOn, FinishedOn *time.Time
	Progress              map[string]float64
	UpdatedAt             time.Time
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
	if utf8.RuneCountInString(e.Comment) > MaxCommentRunes {
		return invalid("comment is longer than %d characters", MaxCommentRunes)
	}
	if err := cleanText("comment", e.Comment, multiLine); err != nil {
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

// writeEntry stores the scalar part of the entry and the progress values
// present in e.Progress. The caller validated e.
func writeEntry(ctx context.Context, tx *sql.Tx, itemID int64, e Entry) error {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntries+`(item_id, status, rating, comment, started_on, finished_on, updated_at)
		VALUES (?,?,?,?,?,?,`+nowSQL+`)
		ON CONFLICT(item_id) DO UPDATE SET status = excluded.status, rating = excluded.rating, comment = excluded.comment,
		  started_on = excluded.started_on, finished_on = excluded.finished_on, updated_at = excluded.updated_at`,
		itemID, string(e.Status), rating, e.Comment, st, fin); err != nil {
		return fmt.Errorf("save entry: %w", err)
	}
	keys := make([]string, 0, len(e.Progress))
	for k := range e.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntryProgress+`(item_id, key, value) VALUES (?,?,?)
			ON CONFLICT(item_id, key) DO UPDATE SET value = excluded.value`, itemID, k, e.Progress[k]); err != nil {
			return fmt.Errorf("save entry progress: %w", err)
		}
	}
	return nil
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
// against the stored item. Status, rating, comment and dates are replaced as a
// whole; progress values are replaced only for the keys present in e.Progress,
// other keys are left untouched (see ClearProgress). It returns ErrNoEntry for
// an unknown item.
func (l *Library) UpsertEntry(ctx context.Context, e Entry) error {
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
	if err := l.ValidateEntry(def, it, e); err != nil {
		return err
	}
	if err := writeEntry(ctx, tx, e.ItemID, e); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save entry: %w", err)
	}
	return nil
}

// Entry returns the item's entry with the values of its active progress keys,
// or nil, nil when the item has no entry.
func (l *Library) Entry(ctx context.Context, itemID int64) (*Entry, error) {
	e := Entry{ItemID: itemID, Progress: map[string]float64{}}
	var status, pk, updated string
	var rating sql.NullInt64
	var started, finished sql.NullString
	err := l.db.QueryRowContext(ctx, `SELECT e.status, e.rating, e.comment, e.started_on, e.finished_on, e.updated_at, i.progress_keys
		FROM `+tableEntries+` e JOIN items i ON i.id = e.item_id WHERE e.item_id = ?`, itemID).
		Scan(&status, &rating, &e.Comment, &started, &finished, &updated, &pk)
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
	if err := e.fill(status, rating, started, finished, updated); err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT key, value FROM `+tableEntryProgress+` WHERE item_id = ?`, itemID)
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

// RemoveEntry deletes the item's entry together with its progress values
// (active and retained), so a later entry starts from zero. The item stays.
// It is a no-op (nil) when the item has no entry or does not exist.
func (l *Library) RemoveEntry(ctx context.Context, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM ` + tableEntryProgress + ` WHERE item_id = ?`, `DELETE FROM ` + tableEntries + ` WHERE item_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, itemID); err != nil {
			return fmt.Errorf("remove entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	return nil
}
