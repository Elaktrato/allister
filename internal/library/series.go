package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		return Series{}, err
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
