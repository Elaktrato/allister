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

const (
	// maxSeriesTitleRunes is the longest series title (in runes).
	maxSeriesTitleRunes = 300
	// maxSeriesDescriptionRunes is the longest series description (in runes).
	maxSeriesDescriptionRunes = 20000
	// maxSeriesPartsTotal is the largest expected number of parts.
	maxSeriesPartsTotal = 100000
)

var (
	// ErrAlreadyInSeries is returned when an item already belongs to a series.
	ErrAlreadyInSeries = errors.New("library: the entry is already in a series")
	// ErrNoSeries is returned for an unknown series.
	ErrNoSeries = errors.New("library: no such series")
)

// Series is a hand-built, ordered group of list entries of one plugin and
// kind, with the owner's own data. Its parts form two blocks with separate
// dense positions: main parts and spin-off parts. Dates are calendar days;
// PartsTotal 0 means not set; LinkedItemID and ParentID 0 mean none. Fields
// holds the kind's series fields (loaded by the single-series reads and
// SpinOffsOf; the list rows leave it nil).
type Series struct {
	ID                   int64
	UID, PluginID, Kind  string
	Title                string
	CreatedAt, UpdatedAt time.Time
	CoverPath            string
	Description          string
	StartedOn, EndedOn   *time.Time
	Finished             bool
	PartsTotal           int
	LinkedItemID         int64
	ParentID             int64
	Fields               map[string]plugin.FieldValue
}

// SeriesDetails is the owner's own data of a series, replaced as a whole by
// UpdateSeriesDetails: an empty value clears it.
type SeriesDetails struct {
	Title, CoverPath, Description string
	StartedOn, EndedOn            *time.Time
	Finished                      bool
	PartsTotal                    int
	LinkedItemID, ParentID        int64
	Fields                        map[string]plugin.FieldValue
}

// holdsOwn reports whether d holds any own value besides the title and the
// parent (see deleteEmptySeriesSQL).
func (d SeriesDetails) holdsOwn() bool {
	return d.CoverPath != "" || d.Description != "" || d.StartedOn != nil || d.EndedOn != nil || d.Finished ||
		d.PartsTotal > 0 || len(d.Fields) > 0 || d.LinkedItemID != 0
}

// SeriesPart is a part of a series and its 1-based position within its
// block (main parts or spin-off parts).
type SeriesPart struct {
	ItemID   int64
	Position int
	SpinOff  bool
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

const seriesCols = `s.id, s.uid, s.plugin_id, s.kind, s.title, s.created_at, s.updated_at,
	s.cover_path, s.description, s.started_on, s.ended_on, s.finished, s.parts_total, s.linked_item_id, s.parent_id`

type rowScanner interface{ Scan(...any) error }

// scanSeries scans a seriesCols row; extra destinations follow the columns.
func scanSeries(r rowScanner, extra ...any) (*Series, error) {
	var s Series
	var created, updated string
	var cover, desc, started, ended sql.NullString
	var total, linked, parent sql.NullInt64
	dest := append([]any{&s.ID, &s.UID, &s.PluginID, &s.Kind, &s.Title, &created, &updated,
		&cover, &desc, &started, &ended, &s.Finished, &total, &linked, &parent}, extra...)
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
	if s.StartedOn, err = parseSeriesDate("started_on", started); err != nil {
		return nil, err
	}
	if s.EndedOn, err = parseSeriesDate("ended_on", ended); err != nil {
		return nil, err
	}
	s.CoverPath, s.Description = cover.String, desc.String
	s.PartsTotal, s.LinkedItemID, s.ParentID = int(total.Int64), linked.Int64, parent.Int64
	return &s, nil
}

// parseSeriesDate decodes a stored calendar day (NULL is nil).
func parseSeriesDate(col string, v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, v.String)
	if err != nil {
		return nil, fmt.Errorf("library: series %s: %w", col, err)
	}
	return &t, nil
}

// attachSeriesFields loads the series fields of the given series in one query.
func attachSeriesFields(ctx context.Context, q queryer, list ...*Series) error {
	if len(list) == 0 {
		return nil
	}
	byID := make(map[int64]*Series, len(list))
	args := make([]any, len(list))
	for i, s := range list {
		byID[s.ID] = s
		args[i] = s.ID
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := q.QueryContext(ctx, `SELECT series_id, key, vtype, text, num, date FROM series_fields
		WHERE series_id IN (`+placeholders(len(args))+`) ORDER BY series_id, key, seq`, args...)
	if err != nil {
		return fmt.Errorf("series fields: %w", err)
	}
	accs := map[int64]*fieldAcc{}
	for rows.Next() {
		var id int64
		var key, vtype string
		var text, date sql.NullString
		var num sql.NullFloat64
		if err := rows.Scan(&id, &key, &vtype, &text, &num, &date); err != nil {
			rows.Close()
			return fmt.Errorf("series fields: %w", err)
		}
		acc := accs[id]
		if acc == nil {
			acc = newFieldAcc()
			accs[id] = acc
		}
		if err := acc.add(key, vtype, nullStr(text), nullNum(num), nullStr(date)); err != nil {
			rows.Close()
			return fmt.Errorf("series fields: %w", err)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("series fields: %w", err)
	}
	for id, acc := range accs {
		byID[id].Fields = acc.vals
	}
	return nil
}

func nullStr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullNum(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
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
	if err != nil {
		return nil, err
	}
	if err := attachSeriesFields(ctx, l.db, s); err != nil {
		return nil, err
	}
	return s, nil
}

// SeriesByID returns the series with the id (with its fields), or ErrNoSeries.
func (l *Library) SeriesByID(ctx context.Context, id int64) (*Series, error) {
	s, err := scanSeries(l.db.QueryRowContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSeries
	}
	if err != nil {
		return nil, err
	}
	if err := attachSeriesFields(ctx, l.db, s); err != nil {
		return nil, err
	}
	return s, nil
}

// SeriesTitles returns the series with the given ids (id, uid, plugin, title)
// in one query; unknown ids are absent from the map.
func (l *Library) SeriesTitles(ctx context.Context, ids []int64) (map[int64]Series, error) {
	out := map[int64]Series{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := l.db.QueryContext(ctx, `SELECT s.id, s.uid, s.plugin_id, s.title FROM series s WHERE s.id IN (`+placeholders(len(ids))+`)`, args...) //nolint:gosec // G202: constant SQL and placeholders; values are bound
	if err != nil {
		return nil, fmt.Errorf("series titles: %w", err)
	}
	for rows.Next() {
		var s Series
		if err := rows.Scan(&s.ID, &s.UID, &s.PluginID, &s.Title); err != nil {
			rows.Close()
			return nil, fmt.Errorf("series titles: %w", err)
		}
		out[s.ID] = s
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("series titles: %w", err)
	}
	return out, nil
}

// SeriesOfItem returns the item's series, its 1-based position within its
// block and whether it is a spin-off part, or (nil, 0, false, nil) when the
// item is in no series.
func (l *Library) SeriesOfItem(ctx context.Context, itemID int64) (*Series, int, bool, error) {
	var pos int
	var spinOff bool
	s, err := scanSeries(l.db.QueryRowContext(ctx, `SELECT `+seriesCols+`, p.position, p.spin_off FROM series_parts p JOIN series s ON s.id = p.series_id WHERE p.item_id = ?`, itemID), &pos, &spinOff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if err := attachSeriesFields(ctx, l.db, s); err != nil {
		return nil, 0, false, err
	}
	return s, pos, spinOff, nil
}

// prepareSeriesDetails checks d without the database and returns the field
// rows to store. ErrInvalid names the offending field.
func prepareSeriesDetails(kind plugin.KindDef, d *SeriesDetails) ([]fieldRow, error) {
	title, err := cleanSeriesTitle(d.Title)
	if err != nil {
		return nil, err
	}
	d.Title = title
	if err := checkImageRef("cover", d.CoverPath); err != nil {
		return nil, err
	}
	if err := cleanText("description", d.Description, multiLine); err != nil {
		return nil, err
	}
	// the cap counts the text as stored: line breaks normalised, trimmed
	desc := plugin.MultiLine(d.Description, utf8.RuneCountInString(d.Description))
	if utf8.RuneCountInString(desc) > maxSeriesDescriptionRunes {
		return nil, invalid("description is longer than %d characters", maxSeriesDescriptionRunes)
	}
	d.Description = desc
	for name, t := range map[string]*time.Time{"started_on": d.StartedOn, "ended_on": d.EndedOn} {
		if t != nil && (t.IsZero() || t.Year() < 1 || t.Year() > 9999) {
			return nil, invalid("%s is not a valid date", name)
		}
	}
	if d.PartsTotal < 0 || d.PartsTotal > maxSeriesPartsTotal {
		return nil, invalid("parts_total must be between 0 and %d", maxSeriesPartsTotal)
	}
	if d.LinkedItemID < 0 || d.ParentID < 0 {
		return nil, invalid("the linked entry and the parent series must be ids")
	}
	return normalizeFields(plugin.KindDef{Key: kind.Key, Fields: kind.SeriesFields}, d.Fields)
}

// UpdateSeriesDetails replaces the owner's data of the series (see
// SeriesDetails) in one transaction and bumps its updated_at. kind is the
// series' kind (another kind is ErrInvalid): Fields are validated against
// its SeriesFields like entry details; a linked entry must be an item of the
// series' plugin and of kind.SeriesLink.Kind; a parent series must be of the
// same plugin, not the series itself and no spin-off itself, and a series
// that has spin-offs cannot get a parent. ErrNoSeries for an unknown id,
// ErrInvalid (naming the field) for invalid data; nothing is stored then.
func (l *Library) UpdateSeriesDetails(ctx context.Context, id int64, kind plugin.KindDef, d SeriesDetails) error {
	rows, err := prepareSeriesDetails(kind, &d)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	defer tx.Rollback()
	var sPlugin, sKind string
	var curLink sql.NullInt64
	switch err := tx.QueryRowContext(ctx, `SELECT plugin_id, kind, linked_item_id FROM series WHERE id = ?`, id).Scan(&sPlugin, &sKind, &curLink); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoSeries
	case err != nil:
		return fmt.Errorf("update series: %w", err)
	}
	if kind.Key != sKind {
		return invalid("kind %q is not the series' kind %q", kind.Key, sKind)
	}
	// an unchanged link stays even when its entry has left the list since
	if err := checkSeriesLink(ctx, tx, sPlugin, kind, d.LinkedItemID, d.LinkedItemID != curLink.Int64); err != nil {
		return err
	}
	if err := checkSeriesParent(ctx, tx, id, sPlugin, d.ParentID); err != nil {
		return err
	}
	if err := writeSeriesValues(ctx, tx, id, d, rows); err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series SET updated_at = `+nowSQL+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	return nil
}

// writeSeriesValues stores d (prepared by prepareSeriesDetails, with its
// field rows) as the series' values and fields; updated_at is left alone.
func writeSeriesValues(ctx context.Context, tx *sql.Tx, id int64, d SeriesDetails, rows []fieldRow) error {
	var started, ended *string
	if d.StartedOn != nil {
		v := plugin.DateOnly(*d.StartedOn).Format(dateLayout)
		started = &v
	}
	if d.EndedOn != nil {
		v := plugin.DateOnly(*d.EndedOn).Format(dateLayout)
		ended = &v
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series SET title = ?, cover_path = ?, description = ?, started_on = ?, ended_on = ?,
		finished = ?, parts_total = ?, linked_item_id = ?, parent_id = ? WHERE id = ?`,
		d.Title, nullIfEmpty(d.CoverPath), nullIfEmpty(d.Description), started, ended,
		d.Finished, nullIfZero(int64(d.PartsTotal)), nullIfZero(d.LinkedItemID), nullIfZero(d.ParentID), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM series_fields WHERE series_id = ?`, id); err != nil {
		return err
	}
	for _, f := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_fields(series_id, key, vtype, text, num, date, seq) VALUES (?,?,?,?,?,?,?)`,
			id, f.key, string(f.vtype), f.text, f.num, f.date, f.seq); err != nil {
			return err
		}
	}
	return nil
}

// checkSeriesLink checks a linked entry (0 = none) against the kind's link;
// with listed it must also have a list entry (a newly chosen link).
func checkSeriesLink(ctx context.Context, tx *sql.Tx, pluginID string, kind plugin.KindDef, itemID int64, listed bool) error {
	if itemID == 0 {
		return nil
	}
	if kind.SeriesLink == nil {
		return invalid("the series of kind %q cannot be linked to an entry", kind.Key)
	}
	var iPlugin, iKind string
	switch err := tx.QueryRowContext(ctx, `SELECT plugin_id, kind FROM items WHERE id = ?`, itemID).Scan(&iPlugin, &iKind); {
	case errors.Is(err, sql.ErrNoRows):
		return invalid("the linked entry does not exist")
	case err != nil:
		return fmt.Errorf("update series: %w", err)
	}
	if iPlugin != pluginID || iKind != kind.SeriesLink.Kind {
		return invalid("the linked entry must be of kind %q of this plugin", kind.SeriesLink.Kind)
	}
	if !listed {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	if n == 0 {
		return invalid("the linked entry is not in your list")
	}
	return nil
}

// checkSeriesParent checks a parent series (0 = none): same plugin, not the
// series itself, no parent of its own, and the series has no spin-offs.
func checkSeriesParent(ctx context.Context, tx *sql.Tx, id int64, pluginID string, parentID int64) error {
	if parentID == 0 {
		return nil
	}
	if parentID == id {
		return invalid("a series cannot be its own parent")
	}
	var pPlugin string
	var grand sql.NullInt64
	switch err := tx.QueryRowContext(ctx, `SELECT plugin_id, parent_id FROM series WHERE id = ?`, parentID).Scan(&pPlugin, &grand); {
	case errors.Is(err, sql.ErrNoRows):
		return invalid("the parent series does not exist")
	case err != nil:
		return fmt.Errorf("update series: %w", err)
	}
	if pPlugin != pluginID {
		return invalid("the parent series belongs to another plugin")
	}
	if grand.Valid {
		return invalid("the parent series is itself a spin-off")
	}
	var children int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series WHERE parent_id = ?`, id).Scan(&children); err != nil {
		return fmt.Errorf("update series: %w", err)
	}
	if children > 0 {
		return invalid("a series with spin-offs cannot become a spin-off")
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// SpinOffsOf returns the series whose parent is parentID, by title (case-
// insensitive), with their series fields.
func (l *Library) SpinOffsOf(ctx context.Context, parentID int64) ([]Series, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.parent_id = ?`, parentID)
	if err != nil {
		return nil, fmt.Errorf("spin-offs: %w", err)
	}
	var list []*Series
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("spin-offs: %w", err)
		}
		list = append(list, s)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("spin-offs: %w", err)
	}
	if err := attachSeriesFields(ctx, l.db, list...); err != nil {
		return nil, err
	}
	out := make([]Series, len(list))
	for i, s := range list {
		out[i] = *s
	}
	slices.SortStableFunc(out, func(a, b Series) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
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

// renumberSeries rewrites the positions of each block of a series (main
// parts, spin-off parts) densely (1..n, keeping the order). A series left
// without parts is deleted only when it holds nothing of its own (see
// deleteEmptySeriesSQL); otherwise it stays with zero parts. It reports
// whether the series still exists.
func renumberSeries(ctx context.Context, tx execer, seriesID int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id, spin_off FROM series_parts WHERE series_id = ? ORDER BY spin_off, position, item_id`, seriesID)
	if err != nil {
		return false, err
	}
	var items []int64
	var positions []int
	next := map[bool]int{}
	for rows.Next() {
		var id int64
		var spinOff bool
		if err := rows.Scan(&id, &spinOff); err != nil {
			rows.Close()
			return false, err
		}
		next[spinOff]++
		items = append(items, id)
		positions = append(positions, next[spinOff])
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	if len(items) == 0 {
		res, err := tx.ExecContext(ctx, deleteEmptySeriesSQL, seriesID)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		if err != nil || n > 0 {
			return false, err
		}
	}
	for i, id := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = ? WHERE series_id = ? AND item_id = ? AND position <> ?`, positions[i], seriesID, id, positions[i]); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE series SET updated_at = `+nowSQL+` WHERE id = ?`, seriesID)
	return true, err
}

// AddPart adds the item to the series' main parts. The item must be of the series' plugin
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
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = position + 1 WHERE series_id = ? AND spin_off = 0 AND position >= ?`, seriesID, pos); err != nil {
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

// insertPosition returns the 1-based position among the main parts for a new
// part (see AddPart).
func insertPosition(ctx context.Context, tx *sql.Tx, seriesID, itemID int64, field string) (int, error) {
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE series_id = ? AND spin_off = 0`, seriesID).Scan(&total); err != nil {
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
	rows, err := tx.QueryContext(ctx, `SELECT p.position, (SELECT f.num FROM item_fields f WHERE f.item_id = p.item_id AND f.key = ? AND f.num IS NOT NULL ORDER BY f.seq LIMIT 1) FROM series_parts p WHERE p.series_id = ? AND p.spin_off = 0 ORDER BY p.position`, field, seriesID)
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

// MovePart moves the item one place up (delta -1) or down (+1) within its
// block (main parts or spin-off parts). Moving past either end of the block
// does nothing; an item that is not a part of
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
	var spinOff bool
	err = tx.QueryRowContext(ctx, `SELECT position, spin_off FROM series_parts WHERE series_id = ? AND item_id = ?`, seriesID, itemID).Scan(&pos, &spinOff)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSeries
	}
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series_parts WHERE series_id = ? AND spin_off = ?`, seriesID, spinOff).Scan(&total); err != nil {
		return err
	}
	target := pos + delta
	if target < 1 || target > total {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET position = ? WHERE series_id = ? AND spin_off = ? AND position = ?`, pos, seriesID, spinOff, target); err != nil {
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

// SetPartSpinOff marks the part as a spin-off part (true) or a main part
// (false). A part that changes block goes to the end of the other block and
// both blocks are renumbered; the series stays even when no main part is
// left. An item that is not a part is ErrNoSeries.
func (l *Library) SetPartSpinOff(ctx context.Context, seriesID, itemID int64, spinOff bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur bool
	err = tx.QueryRowContext(ctx, `SELECT spin_off FROM series_parts WHERE series_id = ? AND item_id = ?`, seriesID, itemID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSeries
	}
	if err != nil {
		return err
	}
	if cur == spinOff {
		return nil
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM series_parts WHERE series_id = ? AND spin_off = ?`, seriesID, spinOff).Scan(&last); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series_parts SET spin_off = ?, position = ? WHERE series_id = ? AND item_id = ?`, spinOff, last+1, seriesID, itemID); err != nil {
		return err
	}
	if _, err := renumberSeries(ctx, tx, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteEmptySeriesSQL deletes the series (id bound) when it holds nothing
// of its own - no cover, description, dates, finished marker, expected parts,
// series fields or linked entry - and has no spin-off series. The caller
// knows it has no parts. Owner-curated data never vanishes with the last
// part.
const deleteEmptySeriesSQL = `DELETE FROM series WHERE id = ?1
	AND COALESCE(cover_path, '') = '' AND COALESCE(description, '') = ''
	AND started_on IS NULL AND ended_on IS NULL AND finished = 0
	AND parts_total IS NULL AND linked_item_id IS NULL
	AND NOT EXISTS (SELECT 1 FROM series_fields f WHERE f.series_id = ?1)
	AND NOT EXISTS (SELECT 1 FROM series c WHERE c.parent_id = ?1)`

// RemovePart takes the item out of the series and keeps its entry. A series
// left without parts is deleted unless it holds own data or has spin-off
// series (see renumberSeries); an item that is not a part is ErrNoSeries.
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
		// The series goes too, so the parts are not renumbered one by one.
		for _, id := range items {
			if _, err := deleteItemRow(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM series WHERE id = ?`, seriesID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeriveSeriesStatus is the status of a series from its main parts'
// statuses and the expected number of parts (0 = not set), the first rule
// that applies: all dropped is dropped; no part at all is planned; every non-dropped part completed,
// with at least expected parts completed or dropped, is completed; all planned is
// planned; any active is active; any paused is paused; otherwise (completed
// and planned parts, or fewer completed and dropped parts than expected)
// active.
func DeriveSeriesStatus(parts []Status, expected int) Status {
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
	case len(parts) == 0: // a series kept for its own data
		return Planned
	case completed > 0 && completed+dropped == len(parts) && completed+dropped >= expected:
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

// SeriesRow is one row of the series list with its resolved values (see
// ResolveSeries): Parts and Completed count the main parts (the spin-off
// parts when there is no main part), PartsTotal is the shown total (the
// larger of the expected and the counted parts), Cover the resolved cover; Updated is the
// newest update of any part (the series' own update when it has none).
type SeriesRow struct {
	Series           Series
	Parts, Completed int
	PartsTotal       int
	ParentID         int64
	Status           Status
	Cover            string
	Updated          time.Time
	// partsSeen: Updated already holds a part's update (seriesRows)
	partsSeen bool
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

// seriesRows loads every series of the plugin with resolved values and
// applies the text filter, in newest-update order. The status filter is not
// applied.
func (l *Library) seriesRows(ctx context.Context, q SeriesQuery) ([]SeriesRow, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT `+seriesCols+` FROM series s WHERE s.plugin_id = ?`, q.PluginID)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	var out []SeriesRow
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("list series: %w", err)
		}
		// Updated is the newest part update; a series without parts uses its own
		out = append(out, SeriesRow{Series: *s, ParentID: s.ParentID, Updated: s.UpdatedAt})
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	byID := make(map[int64]*SeriesRow, len(out))
	for i := range out {
		byID[out[i].Series.ID] = &out[i]
	}
	// Only what SeriesRow shows is loaded: the parts' status, update and
	// cover, the linked item's kind, cover and number fields.
	main, spinOffs := map[int64][]Row{}, map[int64][]Row{}
	pr, err := l.db.QueryContext(ctx, `SELECT sp.series_id, sp.spin_off, e.status, e.updated_at, i.cover_path
		FROM series_parts sp JOIN series s ON s.id = sp.series_id
		JOIN `+tableEntries+` e ON e.item_id = sp.item_id JOIN items i ON i.id = sp.item_id
		WHERE s.plugin_id = ? ORDER BY sp.series_id, sp.spin_off, sp.position`, q.PluginID)
	if err != nil {
		return nil, fmt.Errorf("list series parts: %w", err)
	}
	for pr.Next() {
		var id int64
		var spinOff bool
		var status, updated, cover string
		if err := pr.Scan(&id, &spinOff, &status, &updated, &cover); err != nil {
			pr.Close()
			return nil, fmt.Errorf("list series parts: %w", err)
		}
		at, err := time.Parse(time.RFC3339, updated)
		if err != nil {
			pr.Close()
			return nil, fmt.Errorf("list series parts: entry updated_at: %w", err)
		}
		if r := byID[id]; !r.partsSeen || at.After(r.Updated) {
			r.Updated, r.partsSeen = at, true
		}
		row := Row{Item: Item{CoverPath: cover}, Entry: Entry{Status: Status(status)}}
		if spinOff {
			spinOffs[id] = append(spinOffs[id], row)
		} else {
			main[id] = append(main[id], row)
		}
	}
	if err := closeRows(pr); err != nil {
		return nil, fmt.Errorf("list series parts: %w", err)
	}
	linked, err := l.seriesLinkedItems(ctx, q.PluginID)
	if err != nil {
		return nil, err
	}
	kinds := map[string]plugin.KindDef{}
	if m, ok := l.reg.Manifest(q.PluginID); ok {
		for _, k := range m.Kinds {
			kinds[k.Key] = k
		}
	}
	for i := range out {
		r := &out[i]
		// The part rows and the linked item are partial (see above and
		// seriesLinkedItems): only the outputs copied here are complete.
		res := ResolveSeries(r.Series, kinds[r.Series.Kind], linked[r.Series.ID], nil, main[r.Series.ID], spinOffs[r.Series.ID])
		parts := res.MainParts
		if parts == 0 {
			parts = res.SpinOffParts
		}
		r.Parts, r.Completed, r.PartsTotal, r.Status, r.Cover = parts, res.Completed, res.PartsTotal, res.Status, res.Cover
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

// seriesLinkedItems returns, per series of the plugin with a linked item,
// that item with only its kind, cover and number fields: a partial item,
// enough for the cover, the expected parts and the status that SeriesRow
// shows, not for the description, dates or other fields.
func (l *Library) seriesLinkedItems(ctx context.Context, pluginID string) (map[int64]*Item, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT s.id, li.kind, li.cover_path, f.key, f.num
		FROM series s JOIN items li ON li.id = s.linked_item_id
		LEFT JOIN item_fields f ON f.item_id = li.id AND f.num IS NOT NULL
		WHERE s.plugin_id = ?`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list series links: %w", err)
	}
	out := map[int64]*Item{}
	for rows.Next() {
		var id int64
		var kind, cover string
		var key sql.NullString
		var num sql.NullFloat64
		if err := rows.Scan(&id, &kind, &cover, &key, &num); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list series links: %w", err)
		}
		it := out[id]
		if it == nil {
			it = &Item{Kind: kind, CoverPath: cover, Fields: map[string]plugin.FieldValue{}}
			out[id] = it
		}
		if key.Valid && num.Valid {
			n := num.Float64
			it.Fields[key.String] = plugin.FieldValue{Num: &n}
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("list series links: %w", err)
	}
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

// SeriesPartsOrder returns the series' parts: the main parts, then the
// spin-off parts, each in position order.
func (l *Library) SeriesPartsOrder(ctx context.Context, seriesID int64) ([]SeriesPart, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT item_id, position, spin_off FROM series_parts WHERE series_id = ? ORDER BY spin_off, position`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("series parts: %w", err)
	}
	var out []SeriesPart
	for rows.Next() {
		var p SeriesPart
		if err := rows.Scan(&p.ItemID, &p.Position, &p.SpinOff); err != nil {
			rows.Close()
			return nil, fmt.Errorf("series parts: %w", err)
		}
		out = append(out, p)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("series parts: %w", err)
	}
	return out, nil
}

// SeriesParts returns the series' parts as list rows: the main parts, then
// the spin-off parts, each in position order (SeriesPartsOrder tells which
// is which).
func (l *Library) SeriesParts(ctx context.Context, seriesID int64, lang string) ([]Row, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT item_id FROM series_parts WHERE series_id = ? ORDER BY spin_off, position`, seriesID)
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
	var pluginID string
	if err := l.db.QueryRowContext(ctx, `SELECT plugin_id FROM series WHERE id = ?`, seriesID).Scan(&pluginID); err != nil {
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

// SeriesCandidates returns up to limit entries of the plugin and kind that are
// in no series and whose title contains text (the list's search rule), the
// ones whose hintField text equals seriesTitle (trimmed, ASCII
// case-insensitive) first, then by display title for lang. One query selects
// the ids; only those rows are loaded.
func (l *Library) SeriesCandidates(ctx context.Context, pluginID, kind, text, hintField, seriesTitle, lang string, limit int) ([]Row, error) {
	if limit <= 0 {
		return nil, nil
	}
	c, err := l.compile(Query{PluginID: pluginID, Kind: kind, Text: text})
	if err != nil {
		return nil, err
	}
	c.clauses = append(c.clauses, clause{"series", `NOT EXISTS (SELECT 1 FROM series_parts sp WHERE sp.item_id = items.id)`, nil})
	whereSQL, whereArgs := c.where()
	order, orderArgs, err := c.orderBy(Sort{Kind: SortTitle}, lang)
	if err != nil {
		return nil, err
	}
	if want := asciiLower(strings.TrimSpace(seriesTitle)); hintField != "" && want != "" {
		order = ` ORDER BY NOT EXISTS (SELECT 1 FROM item_fields hf WHERE hf.item_id = items.id AND hf.key = ? AND lower(trim(hf.text)) = ?), ` +
			strings.TrimPrefix(order, " ORDER BY ")
		orderArgs = append([]any{hintField, want}, orderArgs...)
	}
	return l.loadRows(ctx, c.unmatched, whereSQL, whereArgs, order, orderArgs, limit, 0, lang)
}

// ItemSeries is the series an item belongs to, its 1-based position within
// its block and whether it is a spin-off part.
type ItemSeries struct {
	UID, Title string
	Position   int
	SpinOff    bool
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
	rows, err := l.db.QueryContext(ctx, `SELECT p.item_id, s.uid, s.title, p.position, p.spin_off FROM series_parts p JOIN series s ON s.id = p.series_id WHERE p.item_id IN (`+placeholders(len(args))+`)`, args...) //nolint:gosec // G202: constant SQL and placeholders; values are bound
	if err != nil {
		return nil, fmt.Errorf("series of items: %w", err)
	}
	for rows.Next() {
		var id int64
		var is ItemSeries
		if err := rows.Scan(&id, &is.UID, &is.Title, &is.Position, &is.SpinOff); err != nil {
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

// ActiveSeries is the bounded query behind the home page: the series of the
// listed plugins whose derived status is active, newest part update first,
// at most limit. The status is computed in SQL from per-series counts of
// the main parts (all parts when there is no main part) and the set
// expected number of parts by the same rules as
// DeriveSeriesStatus and expectedParts (a series without parts is planned) (a test compares them on every
// combination of up to three part statuses, with own and linked expected
// parts and spin-off parts): not all dropped, not (some completed, the rest
// dropped and at least the expected number completed or dropped), not all planned, and
// not (paused without any active). The cover and the expected parts are
// resolved like ResolveSeries does.
func (l *Library) ActiveSeries(ctx context.Context, pluginIDs []string, limit int) ([]SeriesRow, error) {
	if len(pluginIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	// linkOK and partsKey are CASE expressions over the series' kind and the
	// linked item's kind, from each kind's SeriesLink.
	linkOK, linkArgs := "NULL", []any{}
	partsKey, partsArgs := "NULL", []any{}
	var okWhen, keyWhen []string
	for _, id := range pluginIDs {
		m, ok := l.reg.Manifest(id)
		if !ok {
			continue
		}
		for _, k := range m.Kinds {
			if k.SeriesLink == nil {
				continue
			}
			okWhen = append(okWhen, `WHEN s.plugin_id = ? AND s.kind = ? AND li.kind = ? THEN 1`)
			linkArgs = append(linkArgs, m.ID, k.Key, k.SeriesLink.Kind)
			if k.SeriesLink.PartsField != "" {
				keyWhen = append(keyWhen, `WHEN s.plugin_id = ? AND s.kind = ? AND li.kind = ? THEN ?`)
				partsArgs = append(partsArgs, m.ID, k.Key, k.SeriesLink.Kind, k.SeriesLink.PartsField)
			}
		}
	}
	if len(okWhen) > 0 {
		linkOK = `CASE ` + strings.Join(okWhen, " ") + ` END`
	}
	if len(keyWhen) > 0 {
		partsKey = `CASE ` + strings.Join(keyWhen, " ") + ` END`
	}
	// counted: a main part, or any part of a series without main parts (the
	// first part in block order is then the cover fallback either way).
	const counted = `(sp.spin_off = 0 OR hm.series_id IS NULL)`
	var args []any
	args = append(args, linkArgs...)
	args = append(args, partsArgs...)
	args = append(args, maxSeriesPartsTotal)
	for _, id := range pluginIDs {
		args = append(args, id)
	}
	args = append(args, limit)
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT `+seriesCols+`,
			COALESCE(SUM(`+counted+` AND e.item_id IS NOT NULL), 0) AS n,
			COALESCE(SUM(`+counted+` AND e.status = 'completed'), 0) AS done,
			COALESCE(MAX(e.updated_at), '') AS updated,
			COALESCE(NULLIF(s.cover_path, ''),
				CASE WHEN `+linkOK+` = 1 THEN NULLIF(li.cover_path, '') END,
				(SELECT i2.cover_path FROM series_parts sp2 JOIN `+tableEntries+` e2 ON e2.item_id = sp2.item_id
					JOIN items i2 ON i2.id = sp2.item_id WHERE sp2.series_id = s.id ORDER BY sp2.spin_off, sp2.position LIMIT 1),
				'') AS cover,
			COALESCE(NULLIF(s.parts_total, 0),
				MIN(MAX(CAST((SELECT lf.num FROM item_fields lf WHERE lf.item_id = li.id AND lf.key = `+partsKey+`) AS INTEGER), 0), ?),
				0) AS expected,
			COALESCE(SUM(`+counted+` AND e.status = 'dropped'), 0) AS dropped,
			COALESCE(SUM(`+counted+` AND e.status = 'planned'), 0) AS planned,
			COALESCE(SUM(`+counted+` AND e.status = 'active'), 0) AS active,
			COALESCE(SUM(`+counted+` AND e.status = 'paused'), 0) AS paused
		FROM series s
		LEFT JOIN series_parts sp ON sp.series_id = s.id
		LEFT JOIN `+tableEntries+` e ON e.item_id = sp.item_id
		LEFT JOIN items li ON li.id = s.linked_item_id
		LEFT JOIN (SELECT DISTINCT series_id FROM series_parts WHERE spin_off = 0) hm ON hm.series_id = s.id
		WHERE s.plugin_id IN (`+placeholders(len(pluginIDs))+`)
		GROUP BY s.id
		HAVING n > 0
		   AND NOT (n > 0 AND dropped = n)
		   AND NOT (done > 0 AND done + dropped = n AND done + dropped >= expected)
		   AND NOT (n > 0 AND planned = n)
		   AND NOT (active = 0 AND paused > 0)
		ORDER BY updated DESC, s.id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("active series: %w", err)
	}
	defer rows.Close()
	var out []SeriesRow
	for rows.Next() {
		var r SeriesRow
		var updated string
		var expected, dropped, planned, active, paused int
		s, err := scanSeries(rows, &r.Parts, &r.Completed, &updated, &r.Cover, &expected, &dropped, &planned, &active, &paused)
		if err != nil {
			return nil, fmt.Errorf("active series: %w", err)
		}
		r.Series, r.Status, r.ParentID = *s, Active, s.ParentID
		// shown like ResolveSeries: never below the counted parts
		r.PartsTotal = max(expected, r.Parts)
		if updated != "" {
			if r.Updated, err = time.Parse(time.RFC3339, updated); err != nil {
				return nil, fmt.Errorf("active series: entry updated_at: %w", err)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active series: %w", err)
	}
	return out, nil
}

// ParentCandidates returns up to limit series of the plugin that can be a
// parent: no spin-off itself, none of exclude, whose title contains text
// (case-insensitive), ordered by title.
func (l *Library) ParentCandidates(ctx context.Context, pluginID, text string, limit int, exclude ...int64) ([]Series, error) {
	if limit <= 0 {
		return nil, nil
	}
	q := `SELECT ` + seriesCols + ` FROM series s WHERE s.plugin_id = ? AND s.parent_id IS NULL AND instr(lower(s.title), lower(?)) > 0`
	args := []any{pluginID, text}
	for _, id := range exclude {
		q += ` AND s.id <> ?`
		args = append(args, id)
	}
	q += ` ORDER BY lower(s.title), s.id LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.QueryContext(ctx, q, args...) //nolint:gosec // G202: constant SQL; values are bound
	if err != nil {
		return nil, fmt.Errorf("parent candidates: %w", err)
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			return nil, fmt.Errorf("parent candidates: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}
