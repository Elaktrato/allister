package library

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

// DeriveSeriesStatus is the status of a series from its parts' statuses, the
// first rule that applies: all dropped is dropped; every non-dropped part
// completed is completed; all planned is planned; any active is active; any
// paused is paused; otherwise (completed and planned parts) active.
func DeriveSeriesStatus(parts []Status) Status {
	var dropped, completed, planned, active, paused int
	for _, s := range parts {
		switch s {
		case Dropped:
			dropped++
		case Completed:
			completed++
		case Planned:
			planned++
		case Active:
			active++
		case Paused:
			paused++
		}
	}
	switch {
	case len(parts) > 0 && dropped == len(parts):
		return Dropped
	case completed > 0 && completed+dropped == len(parts):
		return Completed
	case len(parts) > 0 && planned == len(parts):
		return Planned
	case active > 0:
		return Active
	case paused > 0:
		return Paused
	}
	return Active
}

// SeriesRow is one row of the series list with its derived values.
type SeriesRow struct {
	Series           Series
	Parts, Completed int
	Status           Status
	Cover            string
	Updated          time.Time
}

// SeriesQuery selects series of one plugin. Text is a case-insensitive
// substring of the series title or of any part's titles; Status filters on
// the derived status; Sort is "" (newest update first) or "title".
type SeriesQuery struct {
	PluginID string
	Text     string
	Status   Status
	Sort     string
	Page     int
	PageSize int
}

// SeriesPage is one page of series rows; Page is the page actually returned.
type SeriesPage struct {
	Rows               []SeriesRow
	Total, Page, Pages int
}

// seriesRows loads every series of the plugin with derived values and applies
// the text filter, in newest-update order. The status filter is not applied.
func (l *Library) seriesRows(ctx context.Context, q SeriesQuery) ([]SeriesRow, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.plugin_id = ?`, q.PluginID)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	var out []SeriesRow
	byID := map[int64]*SeriesRow{}
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("list series: %w", err)
		}
		out = append(out, SeriesRow{Series: *s})
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	for i := range out {
		byID[out[i].Series.ID] = &out[i]
	}
	statuses := map[int64][]Status{}
	pr, err := l.db.QueryContext(ctx, `SELECT sp.series_id, e.status, e.updated_at, i.cover_path
		FROM series_parts sp JOIN series s ON s.id = sp.series_id
		JOIN `+tableEntries+` e ON e.item_id = sp.item_id JOIN items i ON i.id = sp.item_id
		WHERE s.plugin_id = ? ORDER BY sp.series_id, sp.position`, q.PluginID)
	if err != nil {
		return nil, fmt.Errorf("list series parts: %w", err)
	}
	for pr.Next() {
		var id int64
		var status, updated, cover string
		if err := pr.Scan(&id, &status, &updated, &cover); err != nil {
			pr.Close()
			return nil, fmt.Errorf("list series parts: %w", err)
		}
		at, err := time.Parse(time.RFC3339, updated)
		if err != nil {
			pr.Close()
			return nil, fmt.Errorf("list series parts: entry updated_at: %w", err)
		}
		r := byID[id]
		if r.Parts == 0 {
			r.Cover = cover
		}
		r.Parts++
		if Status(status) == Completed {
			r.Completed++
		}
		if at.After(r.Updated) {
			r.Updated = at
		}
		statuses[id] = append(statuses[id], Status(status))
	}
	if err := closeRows(pr); err != nil {
		return nil, fmt.Errorf("list series parts: %w", err)
	}
	for i := range out {
		out[i].Status = DeriveSeriesStatus(statuses[out[i].Series.ID])
	}
	if text := strings.TrimSpace(truncateRunes(q.Text, maxFilterRunes)); text != "" {
		keep, err := l.seriesMatching(ctx, q.PluginID, text)
		if err != nil {
			return nil, err
		}
		out = slices.DeleteFunc(out, func(r SeriesRow) bool { return !keep[r.Series.ID] })
	}
	slices.SortStableFunc(out, func(a, b SeriesRow) int {
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return cmp.Compare(b.Series.ID, a.Series.ID)
	})
	return out, nil
}

// seriesMatching returns the ids of the plugin's series whose title or any
// part's titles contain text (ASCII case-insensitive, like the list's text
// filter).
func (l *Library) seriesMatching(ctx context.Context, pluginID, text string) (map[int64]bool, error) {
	if strings.ContainsRune(text, 0) {
		return nil, nil
	}
	pat := containsPattern(text)
	rows, err := l.db.QueryContext(ctx, `SELECT s.id FROM series s WHERE s.plugin_id = ? AND (lower(s.title) LIKE ? ESCAPE '\'
		OR EXISTS (SELECT 1 FROM series_parts sp JOIN item_titles t ON t.item_id = sp.item_id
			WHERE sp.series_id = s.id AND lower(t.text) LIKE ? ESCAPE '\'))`, pluginID, pat, pat)
	if err != nil {
		return nil, fmt.Errorf("list series text: %w", err)
	}
	keep := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list series text: %w", err)
		}
		keep[id] = true
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("list series text: %w", err)
	}
	return keep, nil
}

// ListSeries returns a page of the plugin's series (every kind). Pages past
// the end return the last page.
func (l *Library) ListSeries(ctx context.Context, q SeriesQuery) (SeriesPage, error) {
	rows, err := l.seriesRows(ctx, q)
	if err != nil {
		return SeriesPage{}, err
	}
	if q.Status != "" {
		rows = slices.DeleteFunc(rows, func(r SeriesRow) bool { return r.Status != q.Status })
	}
	if q.Sort == "title" {
		slices.SortStableFunc(rows, func(a, b SeriesRow) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Series.Title), strings.ToLower(b.Series.Title)), cmp.Compare(a.Series.ID, b.Series.ID))
		})
	}
	size := q.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)
	total := len(rows)
	pages := max(1, (total+size-1)/size)
	pg := SeriesPage{Total: total, Pages: pages, Page: min(max(q.Page, 1), pages)}
	from := min((pg.Page-1)*size, total)
	pg.Rows = rows[from:min(from+size, total)]
	return pg, nil
}

// SeriesCounts returns the number of matching series per derived status (all
// five present); q.Status is ignored.
func (l *Library) SeriesCounts(ctx context.Context, q SeriesQuery) (map[Status]int, error) {
	rows, err := l.seriesRows(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[Status]int{}
	for _, s := range Statuses() {
		out[s] = 0
	}
	for _, r := range rows {
		out[r.Status]++
	}
	return out, nil
}

// SeriesParts returns the series' parts as list rows in position order.
func (l *Library) SeriesParts(ctx context.Context, seriesID int64, lang string) ([]Row, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT item_id FROM series_parts WHERE series_id = ? ORDER BY position`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("series parts: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("series parts: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("series parts: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var pluginID, kind string
	if err := l.db.QueryRowContext(ctx, `SELECT plugin_id, kind FROM series WHERE id = ?`, seriesID).Scan(&pluginID, &kind); err != nil {
		return nil, fmt.Errorf("series parts: %w", err)
	}
	man, _ := l.reg.Manifest(pluginID)
	loaded, err := l.loadRows(ctx, unmatchedClause(man), ` WHERE items.id IN (`+placeholders(len(ids))+`)`, args, "", nil, len(ids), 0, lang)
	if err != nil {
		return nil, err
	}
	pos := map[int64]int{}
	for i, id := range ids {
		pos[id] = i
	}
	slices.SortFunc(loaded, func(a, b Row) int { return cmp.Compare(pos[a.Item.ID], pos[b.Item.ID]) })
	return loaded, nil
}

// HasSeries reports whether the plugin has any series.
func (l *Library) HasSeries(ctx context.Context, pluginID string) (bool, error) {
	var n int
	err := l.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM series WHERE plugin_id = ?)`, pluginID).Scan(&n)
	return n == 1, err
}

// SeriesMemberIDs returns the ids of the items of the plugin and kind that
// are a part of any series.
func (l *Library) SeriesMemberIDs(ctx context.Context, pluginID, kind string) (map[int64]bool, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT p.item_id FROM series_parts p JOIN series s ON s.id = p.series_id WHERE s.plugin_id = ? AND s.kind = ?`, pluginID, kind)
	if err != nil {
		return nil, fmt.Errorf("series members: %w", err)
	}
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("series members: %w", err)
		}
		out[id] = true
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("series members: %w", err)
	}
	return out, nil
}

// ItemSeries is the series an item belongs to and its 1-based position.
type ItemSeries struct {
	UID, Title string
	Position   int
}

// SeriesOfItems returns the series membership of the given items in one
// query; items in no series are absent from the map.
func (l *Library) SeriesOfItems(ctx context.Context, itemIDs []int64) (map[int64]ItemSeries, error) {
	out := map[int64]ItemSeries{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		args[i] = id
	}
	rows, err := l.db.QueryContext(ctx, `SELECT p.item_id, s.uid, s.title, p.position FROM series_parts p JOIN series s ON s.id = p.series_id WHERE p.item_id IN (`+placeholders(len(args))+`)`, args...) //nolint:gosec // G202: constant SQL and placeholders; values are bound
	if err != nil {
		return nil, fmt.Errorf("series of items: %w", err)
	}
	for rows.Next() {
		var id int64
		var is ItemSeries
		if err := rows.Scan(&id, &is.UID, &is.Title, &is.Position); err != nil {
			rows.Close()
			return nil, fmt.Errorf("series of items: %w", err)
		}
		out[id] = is
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("series of items: %w", err)
	}
	return out, nil
}
