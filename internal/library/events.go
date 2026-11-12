package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
)

// SQL table name of the entry change log.
const tableEntryEvents = "entry_events"

// event types of the change log (fixed by the core, like the statuses).
const (
	eventProgress = "progress"
	eventStatus   = "status"
	eventRating   = "rating"
)

// EventOrigin says who made an entry change: the user (manual), an import or
// a backup restore. The activity heatmap counts only manual changes.
type EventOrigin string

const (
	OriginManual  EventOrigin = "manual"
	OriginImport  EventOrigin = "import"
	OriginRestore EventOrigin = "restore"
)

func validOrigin(o EventOrigin) bool {
	return o == OriginManual || o == OriginImport || o == OriginRestore
}

// entryOptions carries the options of one entry write.
type entryOptions struct{ origin EventOrigin }

// EntryOption customises an entry write.
type EntryOption func(*entryOptions)

// WithEventOrigin records the change in the event log with the given origin
// (the default is OriginManual).
func WithEventOrigin(o EventOrigin) EntryOption {
	return func(opt *entryOptions) { opt.origin = o }
}

// KindRef names one plugin kind, the unit of the activity heatmap's scope.
type KindRef struct{ PluginID, Kind string }

// activityStampLayout is the occurred_at format written by nowSQL.
const activityStampLayout = "2006-01-02T15:04:05Z"

// activityPredicate selects the manual events that count as activity: a
// progress increase or a status change to completed.
const activityPredicate = `e.origin = 'manual' AND (
		(e.event_type = 'progress' AND e.new_num > e.old_num) OR
		(e.event_type = 'status' AND e.new_text = 'completed' AND e.old_text <> 'completed'))`

// ActivityDays counts the activity events by UTC day. from is inclusive, to
// exclusive; an empty scope means every kind.
func (l *Library) ActivityDays(ctx context.Context, from, to time.Time, scope []KindRef) (map[string]int, error) {
	q := `SELECT substr(e.occurred_at,1,10), COUNT(*) FROM ` + tableEntryEvents + ` e
		JOIN items i ON i.id = e.item_id
		WHERE ` + activityPredicate + ` AND e.occurred_at >= ? AND e.occurred_at < ?`
	args := []any{from.UTC().Format(activityStampLayout), to.UTC().Format(activityStampLayout)}
	if len(scope) > 0 {
		parts := make([]string, 0, len(scope))
		for _, k := range scope {
			parts = append(parts, "(i.plugin_id = ? AND i.kind = ?)")
			args = append(args, k.PluginID, k.Kind)
		}
		q += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	q += " GROUP BY substr(e.occurred_at,1,10)"
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query activity days: %w", err)
	}
	days := map[string]int{}
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("query activity days: %w", err)
		}
		days[day] = n
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("query activity days: %w", err)
	}
	return days, nil
}

// FirstActivityDay returns the UTC day of the earliest manual event; ok is
// false when there is none.
func (l *Library) FirstActivityDay(ctx context.Context) (string, bool, error) {
	var first sql.NullString
	if err := l.db.QueryRowContext(ctx, `SELECT MIN(substr(occurred_at,1,10)) FROM `+tableEntryEvents+` WHERE origin = 'manual'`).Scan(&first); err != nil {
		return "", false, fmt.Errorf("query first activity day: %w", err)
	}
	return first.String, first.Valid, nil
}

// entryTx returns the item's entry as read inside tx, or nil, nil when the
// item has no entry. It is the baseline for the change log: it is read before
// the write.
func entryTx(ctx context.Context, tx *sql.Tx, itemID int64) (*Entry, error) {
	return loadEntry(ctx, tx, itemID)
}

// insertEvent appends one event to the log, stamped with the database's
// current time so every write in one transaction shares it.
func insertEvent(ctx context.Context, tx *sql.Tx, itemID int64, eventType, progressKey string,
	oldNum, newNum *float64, oldText, newText string, origin EventOrigin) error {
	if !validOrigin(origin) {
		return invalid("unknown event origin %q", origin)
	}
	var oldV, newV any
	if oldNum != nil {
		oldV = *oldNum
	}
	if newNum != nil {
		newV = *newNum
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntryEvents+`(item_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin)
		VALUES (?,`+nowSQL+`,?,?,?,?,?,?,?)`,
		itemID, eventType, progressKey, oldV, newV, oldText, newText, string(origin)); err != nil {
		return fmt.Errorf("record entry event: %w", err)
	}
	return nil
}

// recordEvents appends the changes between old and e: one progress event per
// key present in the save whose value changed, then a status event and a
// rating event when those changed. old nil means the entry is new (a
// baseline) and nothing is recorded. A key absent from old starts at 0.
func recordEvents(ctx context.Context, tx *sql.Tx, itemID int64, old *Entry, e Entry, origin EventOrigin) error {
	if old == nil {
		return nil
	}
	keys := make([]string, 0, len(e.Progress))
	for k := range e.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		from, to := old.Progress[k], e.Progress[k]
		if from == to {
			continue
		}
		if err := insertEvent(ctx, tx, itemID, eventProgress, k, &from, &to, "", "", origin); err != nil {
			return err
		}
	}
	if old.Status != e.Status {
		if err := insertEvent(ctx, tx, itemID, eventStatus, "", nil, nil, string(old.Status), string(e.Status), origin); err != nil {
			return err
		}
	}
	if !sameRating(old.Rating, e.Rating) {
		if err := insertEvent(ctx, tx, itemID, eventRating, "", ratingNum(old.Rating), ratingNum(e.Rating), "", "", origin); err != nil {
			return err
		}
	}
	return nil
}

// ratingNum maps an optional rating to the event column: NULL is unrated and
// 0 stays a real value.
func ratingNum(r *int) *float64 {
	if r == nil {
		return nil
	}
	v := float64(*r)
	return &v
}

func sameRating(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
