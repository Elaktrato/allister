package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/ids"
	"github.com/allister/allister/internal/plugin"
)

// maxSeriesTitleRunes is the longest series title (in runes).
const maxSeriesTitleRunes = 300

var (
	// ErrAlreadyInSeries is returned when an item already belongs to a series.
	ErrAlreadyInSeries = errors.New("library: the entry is already in a series")
	// ErrNoSeries is returned for an unknown series.
	ErrNoSeries = errors.New("library: no such series")
)

// Series is a hand-built, ordered group of list entries of one plugin and kind.
type Series struct {
	ID                   int64
	UID, PluginID, Kind  string
	Title                string
	CreatedAt, UpdatedAt time.Time
}

// cleanSeriesTitle returns the cleaned title or ErrInvalid.
func cleanSeriesTitle(title string) (string, error) {
	if err := cleanText("series title", title, singleLine); err != nil {
		return "", err
	}
	t := plugin.SingleLine(title, 1<<20)
	if t == "" {
		return "", invalid("series title is empty")
	}
	if utf8.RuneCountInString(t) > maxSeriesTitleRunes {
		return "", invalid("series title is longer than %d characters", maxSeriesTitleRunes)
	}
	return t, nil
}

const seriesCols = `s.id, s.uid, s.plugin_id, s.kind, s.title, s.created_at, s.updated_at`

type rowScanner interface{ Scan(...any) error }

// scanSeries scans a seriesCols row; extra destinations follow the columns.
func scanSeries(r rowScanner, extra ...any) (*Series, error) {
	var s Series
	var created, updated string
	dest := append([]any{&s.ID, &s.UID, &s.PluginID, &s.Kind, &s.Title, &created, &updated}, extra...)
	if err := r.Scan(dest...); err != nil {
		return nil, err
	}
	var err error
	if s.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return nil, fmt.Errorf("library: series created_at: %w", err)
	}
	if s.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return nil, fmt.Errorf("library: series updated_at: %w", err)
	}
	return &s, nil
}

// CreateSeries starts a series titled title with the item as its first part.
// The item must have an entry (ErrNoEntry) and be in no series
// (ErrAlreadyInSeries); an empty or invalid title is ErrInvalid.
func (l *Library) CreateSeries(ctx context.Context, itemID int64, title string) (Series, error) {
	clean, err := cleanSeriesTitle(title)
	if err != nil {
		return Series{}, err
	}
	uid, err := ids.NewManualID()
	if err != nil {
		return Series{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Series{}, err
	}
	defer tx.Rollback()
	var pluginID, kind string
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id, i.kind FROM items i JOIN `+tableEntries+` e ON e.item_id = i.id WHERE i.id = ?`, itemID).Scan(&pluginID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, ErrNoEntry
	}
	if err != nil {
		return Series{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		return Series{}, err
	}
	if n > 0 {
		return Series{}, ErrAlreadyInSeries
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO series(uid, plugin_id, kind, title, created_at, updated_at) VALUES (?, ?, ?, ?, `+nowSQL+`, `+nowSQL+`)`, uid, pluginID, kind, clean)
	if err != nil {
		return Series{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Series{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO series_parts(series_id, item_id, position) VALUES (?, ?, 1)`, id, itemID); err != nil {
		return Series{}, mapPartInsertErr(err)
	}
	s, err := scanSeries(tx.QueryRowContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.id = ?`, id))
	if err != nil {
		return Series{}, err
	}
	if err := tx.Commit(); err != nil {
		return Series{}, err
	}
	return *s, nil
}

// SeriesByUID returns the series with the uid of the given plugin, or
// ErrNoSeries when there is none (or it belongs to another plugin).
func (l *Library) SeriesByUID(ctx context.Context, pluginID, uid string) (*Series, error) {
	s, err := scanSeries(l.db.QueryRowContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.plugin_id = ? AND s.uid = ?`, pluginID, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSeries
	}
	return s, err
}

// SeriesOfItem returns the item's series and its 1-based position, or
// (nil, 0, nil) when the item is in no series.
func (l *Library) SeriesOfItem(ctx context.Context, itemID int64) (*Series, int, error) {
	var pos int
	s, err := scanSeries(l.db.QueryRowContext(ctx, `SELECT `+seriesCols+`, p.position FROM series_parts p JOIN series s ON s.id = p.series_id WHERE p.item_id = ?`, itemID), &pos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return s, pos, nil
}

// RenameSeries sets the title of a series (ErrInvalid for a bad title,
// ErrNoSeries for an unknown id).
func (l *Library) RenameSeries(ctx context.Context, id int64, title string) error {
	clean, err := cleanSeriesTitle(title)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE series SET title = ?, updated_at = `+nowSQL+` WHERE id = ?`, clean, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoSeries
	}
	return tx.Commit()
}

// mapPartInsertErr turns the UNIQUE failure of a concurrent membership insert
// (the pre-check and the insert are not atomic across connections) into
// ErrAlreadyInSeries; other errors pass through.
func mapPartInsertErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAlreadyInSeries
	}
	return err
}

// execer is the part of *sql.Tx the series helpers need.
type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// renumberSeries rewrites the positions of a series densely (1..n, keeping the
// order) and deletes the series when it has no parts left. It reports whether
// the series still exists.
func renumberSeries(ctx context.Context, tx execer, seriesID int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id FROM series_parts WHERE series_id = ? ORDER BY position, item_id`, seriesID)
	if err != nil {
		return false, err
	}
	var items []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	if len(items) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM series WHERE id = ?`, seriesID)
		return false, err
	}
	for i, id := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = ? WHERE series_id = ? AND item_id = ? AND position <> ?`, i+1, seriesID, id, i+1); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE series SET updated_at = `+nowSQL+` WHERE id = ?`, seriesID)
	return true, err
}

// AddPart adds the item to the series. The item must be of the series' plugin
// and kind (ErrInvalid), have an entry (ErrNoEntry) and be in no series
// (ErrAlreadyInSeries). With a positionField the item goes after the last part
// whose own number is not greater than the item's (parts without a number
// count as before it); without one, or without a number on the item, it is
// appended.
func (l *Library) AddPart(ctx context.Context, seriesID, itemID int64, positionField string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sPlugin, sKind string
	err = tx.QueryRowContext(ctx, `SELECT plugin_id, kind FROM series WHERE id = ?`, seriesID).Scan(&sPlugin, &sKind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSeries
	}
	if err != nil {
		return err
	}
	var iPlugin, iKind string
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id, i.kind FROM items i JOIN `+tableEntries+` e ON e.item_id = i.id WHERE i.id = ?`, itemID).Scan(&iPlugin, &iKind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoEntry
	}
	if err != nil {
		return err
	}
	if iPlugin != sPlugin || iKind != sKind {
		return invalid("the entry is not of the series' kind")
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadyInSeries
	}
	pos, err := insertPosition(ctx, tx, seriesID, itemID, positionField)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = position + 1 WHERE series_id = ? AND position >= ?`, seriesID, pos); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO series_parts(series_id, item_id, position) VALUES (?, ?, ?)`, seriesID, itemID, pos); err != nil {
		return mapPartInsertErr(err)
	}
	if _, err := renumberSeries(ctx, tx, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}

// insertPosition returns the 1-based position for a new part (see AddPart).
func insertPosition(ctx context.Context, tx *sql.Tx, seriesID, itemID int64, field string) (int, error) {
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE series_id = ?`, seriesID).Scan(&total); err != nil {
		return 0, err
	}
	if field == "" {
		return total + 1, nil
	}
	var mine sql.NullFloat64
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT f.num FROM item_fields f WHERE f.item_id = ? AND f.key = ? AND f.num IS NOT NULL ORDER BY f.seq LIMIT 1)`, itemID, field).Scan(&mine); err != nil {
		return 0, err
	}
	if !mine.Valid {
		return total + 1, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.position, (SELECT f.num FROM item_fields f WHERE f.item_id = p.item_id AND f.key = ? AND f.num IS NOT NULL ORDER BY f.seq LIMIT 1) FROM series_parts p WHERE p.series_id = ? ORDER BY p.position`, field, seriesID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	pos := 1
	for rows.Next() {
		var p int
		var num sql.NullFloat64
		if err := rows.Scan(&p, &num); err != nil {
			return 0, err
		}
		if !num.Valid || num.Float64 <= mine.Float64 {
			pos = p + 1
		}
	}
	return pos, rows.Err()
}

// MovePart moves the item one place up (delta -1) or down (+1) within the
// series. Moving past either end does nothing; an item that is not a part of
// the series is ErrNoSeries.
func (l *Library) MovePart(ctx context.Context, seriesID, itemID int64, delta int) error {
	if delta != -1 && delta != 1 {
		return invalid("move by -1 or 1")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pos, total int
	err = tx.QueryRowContext(ctx, `SELECT position FROM series_parts WHERE series_id = ? AND item_id = ?`, seriesID, itemID).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSeries
	}
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE series_id = ?`, seriesID).Scan(&total); err != nil {
		return err
	}
	target := pos + delta
	if target < 1 || target > total {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = ? WHERE series_id = ? AND position = ?`, pos, seriesID, target); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = ? WHERE series_id = ? AND item_id = ?`, target, seriesID, itemID); err != nil {
		return err
	}
	if _, err := renumberSeries(ctx, tx, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemovePart takes the item out of the series and keeps its entry. The series
// is deleted when it was the last part; an item that is not a part is
// ErrNoSeries.
func (l *Library) RemovePart(ctx context.Context, seriesID, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM series_parts WHERE series_id = ? AND item_id = ?`, seriesID, itemID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoSeries
	}
	if _, err := renumberSeries(ctx, tx, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSeries deletes the series. With removeEntries every part item is
// deleted as DeleteItem does; otherwise the entries stay, ungrouped. An
// unknown id is ErrNoSeries.
func (l *Library) DeleteSeries(ctx context.Context, seriesID int64, removeEntries bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series WHERE id = ?`, seriesID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNoSeries
	}
	if removeEntries {
		rows, err := tx.QueryContext(ctx, `SELECT item_id FROM series_parts WHERE series_id = ?`, seriesID)
		if err != nil {
			return err
		}
		var items []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			items = append(items, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range items {
			if err := deleteItemTx(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM series WHERE id = ?`, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}
