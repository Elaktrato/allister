package library

// Bulk operations: one change applied to many entries at once, each call in
// ONE transaction. A row the library refuses (an invalid result, a past
// display run, a series rule, an item without an entry) is skipped with a
// readable reason and never fails the batch; a database error rolls back
// everything. Entry changes are logged with the bulk event origin, which the
// activity heatmap does not count.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// BulkResult is the outcome of a bulk operation: how many entries it changed
// and which it skipped, with why. Every distinct id given is either changed
// or skipped.
type BulkResult struct {
	Changed int
	Skipped []BulkSkip
}

// BulkSkip is one entry a bulk operation left as it was.
type BulkSkip struct {
	ItemID int64
	Reason string
}

// reasonNotInLibrary is the skip reason of an id without an entry.
const reasonNotInLibrary = "not in your library"

// MatchingIDs returns the id of every entry the list query q matches, in the
// list's order for the title language lang (as List and Rows), without
// paging: exactly the union of all pages. q.Page and q.PageSize are ignored.
func (l *Library) MatchingIDs(ctx context.Context, q Query, lang string) ([]int64, error) {
	c, err := l.compile(q)
	if err != nil {
		return nil, err
	}
	whereSQL, whereArgs := c.where()
	order, orderArgs, err := c.orderBy(q.Sort, lang)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // G202: the query builder joins constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT items.id`+fromSQL+whereSQL+order, append(whereArgs, orderArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("matching ids: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("matching ids: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("matching ids: %w", err)
	}
	return ids, nil
}

// bulkRow changes one entry inside the bulk transaction. A non-empty skip
// (or an ErrInvalid, ErrNoEntry or ErrAlreadyInSeries error) skips the row:
// whatever it wrote is undone; any other error fails the whole batch.
type bulkRow func(tx *sql.Tx, itemID int64) (skip string, err error)

// skipReason maps a refusal of the library to a readable reason; ok is false
// for an error that must fail the batch.
func skipReason(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNoEntry):
		return reasonNotInLibrary, true
	case errors.Is(err, ErrAlreadyInSeries):
		return "already in another series", true
	case errors.Is(err, ErrInvalid):
		return strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "), true
	}
	return "", false
}

// bulk runs row for every distinct id (in the given order) in one
// transaction, after check (run first in the same transaction; its error
// fails the call). Each row runs under a savepoint, so a skipped row leaves
// nothing behind.
func (l *Library) bulk(ctx context.Context, what string, ids []int64, check func(tx *sql.Tx) error, row bulkRow) (BulkResult, error) {
	var res BulkResult
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("%s: %w", what, err)
	}
	defer tx.Rollback()
	if check != nil {
		if err := check(tx); err != nil {
			return BulkResult{}, err
		}
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.ExecContext(ctx, `SAVEPOINT bulk_row`); err != nil {
			return BulkResult{}, fmt.Errorf("%s: %w", what, err)
		}
		skip, err := "", hasEntry(ctx, tx, id)
		if err == nil {
			skip, err = row(tx, id)
		}
		if err != nil {
			reason, ok := skipReason(err)
			if !ok {
				return BulkResult{}, fmt.Errorf("%s: %w", what, err)
			}
			skip = reason
		}
		if skip != "" {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO bulk_row`); err != nil {
				return BulkResult{}, fmt.Errorf("%s: %w", what, err)
			}
			res.Skipped = append(res.Skipped, BulkSkip{ItemID: id, Reason: skip})
		} else {
			res.Changed++
		}
		if _, err := tx.ExecContext(ctx, `RELEASE bulk_row`); err != nil {
			return BulkResult{}, fmt.Errorf("%s: %w", what, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return BulkResult{}, fmt.Errorf("%s: %w", what, err)
	}
	return res, nil
}

// BulkSetStatus sets the status of each entry's display run as a correction
// (it never starts a run). Completed also sets the finish date to the
// calendar day of today where it is empty and every active progress track
// with a known total to that total (unbounded tracks stay); Active sets the
// start date to today where it is empty; other statuses change the status
// only. Rows the library refuses (a past display run behind a dropped
// repeat, dates out of order) are skipped. The changes are logged with the
// bulk origin. An unknown status is ErrInvalid.
func (l *Library) BulkSetStatus(ctx context.Context, ids []int64, st Status, today time.Time) (BulkResult, error) {
	if !validStatus(st) {
		return BulkResult{}, invalid("status %q must be one of planned, active, paused, completed, dropped", st)
	}
	if _, ok := writtenDay(&today); !ok {
		return BulkResult{}, invalid("dates must be in the years 1 to 9999")
	}
	return l.bulk(ctx, "set status", ids, nil, func(tx *sql.Tx, id int64) (string, error) {
		old, err := entryTx(ctx, tx, id)
		if err != nil {
			return "", err
		}
		if old == nil {
			return reasonNotInLibrary, nil
		}
		it, def, err := l.txItem(ctx, tx, id)
		if err != nil {
			return "", err
		}
		e := *old
		e.Progress = map[string]float64{}
		for k, v := range old.Progress {
			e.Progress[k] = v
		}
		e.Status = st
		switch st {
		case Completed:
			if e.FinishedOn == nil {
				e.FinishedOn = &today
			}
			for _, tr := range activeTracks(def, it) {
				if tr.bounded {
					e.Progress[tr.key] = tr.total
				}
			}
		case Active:
			if e.StartedOn == nil {
				e.StartedOn = &today
			}
		}
		if err := l.ValidateEntry(def, it, e); err != nil {
			return "", err
		}
		if err := writeEntry(ctx, tx, id, e); err != nil {
			return "", err
		}
		return "", recordEvents(ctx, tx, id, old, e, OriginBulk)
	})
}

// BulkAddToList appends the entries to the custom list in ids order; an
// entry in it already stays where it is and is skipped. ErrNoList for an
// unknown list.
func (l *Library) BulkAddToList(ctx context.Context, ids []int64, listID int64) (BulkResult, error) {
	check := func(tx *sql.Tx) error {
		_, err := listOrder(ctx, tx, listID)
		return err
	}
	return l.bulk(ctx, "add to list", ids, check, func(tx *sql.Tx, id int64) (string, error) {
		var in bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM list_items WHERE list_id = ? AND item_id = ?)`, listID, id).Scan(&in); err != nil {
			return "", err
		}
		if in {
			return "already in this list", nil
		}
		return "", joinList(ctx, tx, listID, id)
	})
}

// BulkTags adds the own tags add to each entry (missing tags are created as
// first typed, an existing tag keeps its spelling) and removes the tags
// remove (matched by their folded name); the entries' other tags stay.
// Removing happens first, so a name in both lists ends up on the entries.
// A refused name to add is ErrInvalid and nothing is written; an entry that
// would carry more than the most tags is skipped.
func (l *Library) BulkTags(ctx context.Context, ids []int64, add, remove []string) (BulkResult, error) {
	type tagName struct{ name, key string }
	var adds []tagName
	for _, raw := range add {
		if strings.TrimSpace(raw) == "" && utf8.ValidString(raw) {
			continue
		}
		name, key, err := userTagName(raw)
		if err != nil {
			return BulkResult{}, err
		}
		if !slices.ContainsFunc(adds, func(t tagName) bool { return t.key == key }) {
			adds = append(adds, tagName{name, key})
		}
	}
	var removeKeys []any
	for _, raw := range remove {
		if name := strings.TrimSpace(raw); name != "" {
			removeKeys = append(removeKeys, termKey(name))
		}
	}
	return l.bulk(ctx, "tags", ids, nil, func(tx *sql.Tx, id int64) (string, error) {
		if len(removeKeys) > 0 {
			//nolint:gosec // G202: constant SQL and placeholders; values are bound
			if _, err := tx.ExecContext(ctx, `DELETE FROM entry_user_tags WHERE item_id = ? AND tag_id IN
				(SELECT id FROM user_tags WHERE name_key IN (`+placeholders(len(removeKeys))+`))`, append([]any{id}, removeKeys...)...); err != nil {
				return "", err
			}
		}
		for _, t := range adds {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_tags(name, name_key, created_at) VALUES (?, ?, `+nowSQL+`)
				ON CONFLICT(name_key) DO NOTHING`, t.name, t.key); err != nil {
				return "", err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO entry_user_tags(item_id, tag_id)
				SELECT ?, id FROM user_tags WHERE name_key = ? ON CONFLICT DO NOTHING`, id, t.key); err != nil {
				return "", err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM entry_user_tags WHERE item_id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n > maxEntryTags {
			return fmt.Sprintf("more than %d tags", maxEntryTags), nil
		}
		return "", nil
	})
}

// BulkAddToSeries appends the entries to the series in ids order, after its
// existing parts. The series rules apply: an entry of another plugin or
// kind, or in another series, is skipped; one in this series already stays
// where it is and is skipped. ErrNoSeries for an unknown series.
func (l *Library) BulkAddToSeries(ctx context.Context, ids []int64, seriesID int64) (BulkResult, error) {
	check := func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM series WHERE id = ?`, seriesID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSeries
		}
		if err != nil {
			return fmt.Errorf("add to series: %w", err)
		}
		return nil
	}
	return l.bulk(ctx, "add to series", ids, check, func(tx *sql.Tx, id int64) (string, error) {
		var in bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM series_parts WHERE series_id = ? AND item_id = ?)`, seriesID, id).Scan(&in); err != nil {
			return "", err
		}
		if in {
			return "already in this series", nil
		}
		return "", addPartTx(ctx, tx, seriesID, id, "")
	})
}

// BulkRemove removes the entries from the library like RemoveEntry: their
// runs, tags and list places go, the items stay, and they leave their
// series.
func (l *Library) BulkRemove(ctx context.Context, ids []int64) (BulkResult, error) {
	return l.bulk(ctx, "remove", ids, nil, func(tx *sql.Tx, id int64) (string, error) {
		return "", removeEntryTx(ctx, tx, id)
	})
}
