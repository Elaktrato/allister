package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// notesDivider separates the survivor's notes from the merged item's notes.
const notesDivider = "\n\n---\n\n"

// MergeItems merges the item otherID into keepID and deletes otherID, in ONE
// transaction; any error rolls everything back. Both items must exist, be
// different and be of the same plugin and kind (ErrInvalid otherwise).
//
//   - keys name the parts of the other item's data (Change keys, see
//     ApplyChanges) that replace the survivor's. Credits are compared only
//     when both items' people come from the same source: a person id means
//     nothing to another source. Covers are compared by their stored values
//     (a local file differs from another file), and taking the other's
//     cover keeps its recorded origin.
//   - The other item's runs join the survivor's, ordered by start day (the
//     run's creation day when it has none; on a tie the survivor's first,
//     then by their own number) and renumbered 1..n. Of the planned, active
//     and paused runs only the latest stays open and is numbered last (an
//     open run must be the latest); the others become dropped, without an
//     event. Progress values (every key) move with their run as
//     stored, bounded by no total; so do the runs' unit notes and events.
//     The other item's events without a run move too. The display run is
//     refreshed.
//   - When only the other item has an entry the survivor gets one with its
//     notes; when both have one the other's notes are appended after a
//     divider line (none when either side is empty); joined notes longer
//     than MaxNotesRunes are refused (ErrInvalid).
//   - Own tags and custom lists: union. A list keeps the survivor's place
//     when both are members, else the other's.
//   - Series: the survivor keeps its own; when only the other is a part, the
//     survivor takes its place.
//   - External ids of the types the survivor lacks are added.
//   - The other item's identity becomes an alias of the survivor (so
//     ItemBySource and the duplicate check find the survivor), and its own
//     aliases move along, unless it is a manual item. A manual survivor of a
//     sourced item takes over that item's source and source id (and its
//     external URL when the survivor has none) instead, and no alias is
//     kept.
func (l *Library) MergeItems(ctx context.Context, keepID, otherID int64, keys []string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("merge items: %w", err)
	}
	defer tx.Rollback()
	p, err := loadMergePair(ctx, tx, keepID, otherID)
	if err != nil {
		return err
	}
	keep, other, in, credits := p.keep, p.other, p.in, p.credits
	if err := mergeEntry(ctx, tx, keepID, otherID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_ids(item_id, type, value)
		SELECT ?, type, value FROM external_ids o WHERE o.item_id = ?
		AND NOT EXISTS (SELECT 1 FROM external_ids k WHERE k.item_id = ? AND k.type = o.type)`, keepID, otherID, keepID); err != nil {
		return fmt.Errorf("merge items: external ids: %w", err)
	}
	takeOver := keep.Source == manualSource && other.Source != manualSource
	if _, err := tx.ExecContext(ctx, `UPDATE item_aliases SET item_id = ? WHERE item_id = ?`, keepID, otherID); err != nil {
		return fmt.Errorf("merge items: aliases: %w", err)
	}
	if other.Source != manualSource && !takeOver {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_aliases(item_id, plugin_id, kind, source, source_id) VALUES (?,?,?,?,?)`,
			keepID, other.PluginID, other.Kind, other.Source, other.SourceID); err != nil {
			return fmt.Errorf("merge items: alias: %w", err)
		}
	}
	lists, err := mergeIDsTx(ctx, tx, `SELECT list_id FROM list_items WHERE item_id = ?`, otherID)
	if err != nil {
		return err
	}
	if err := deleteItemTx(ctx, tx, otherID); err != nil {
		return err
	}
	for _, id := range lists {
		if _, err := listMembers.renumber(ctx, tx, id); err != nil {
			return fmt.Errorf("merge items: list: %w", err)
		}
	}
	if takeOver {
		// the other item is gone: its identity is free for the survivor
		if _, err := tx.ExecContext(ctx, `UPDATE items SET source = ?, source_id = ?,
			external_url = CASE WHEN external_url = '' THEN ? ELSE external_url END, updated_at = `+nowSQL+` WHERE id = ?`,
			other.Source, other.SourceID, other.ExternalURL, keepID); err != nil {
			return fmt.Errorf("merge items: source link: %w", err)
		}
	}
	if _, err := l.applyChangesTx(ctx, tx, keepID, in, credits, p.withCredits(), keys, &mergeCover{source: p.coverSource}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("merge items: %w", err)
	}
	return nil
}

// MergeChanges returns the parts of the other item's data that differ from
// the survivor's: the choices MergeItems offers for the same pair (its keys),
// credits only when MergeItems would compare them. It changes nothing and
// refuses the pairs MergeItems refuses (ErrInvalid).
func (l *Library) MergeChanges(ctx context.Context, keepID, otherID int64) ([]Change, error) {
	p, err := loadMergePair(ctx, l.db, keepID, otherID)
	if err != nil {
		return nil, err
	}
	return l.itemChanges(ctx, keepID, p.in, p.credits, p.withCredits(), &mergeCover{source: p.coverSource})
}

// mergePair is the survivor and the other item of a merge, with the other
// item as incoming data.
type mergePair struct {
	keep, other *Item
	in          plugin.ItemData
	credits     []plugin.Credit
	coverSource string // the other item's cover origin
}

// withCredits: the survivor's people come from the other item's source
// after the merge (the same source, or a manual survivor that takes the
// other's source link), so a person id means the same to both.
func (p mergePair) withCredits() bool {
	return p.keep.Source == p.other.Source || (p.keep.Source == manualSource && p.other.Source != manualSource)
}

// loadMergePair reads both items through q and refuses what cannot be
// merged: the same item, an unknown one, or items of another plugin or kind
// (ErrInvalid).
func loadMergePair(ctx context.Context, q queryer, keepID, otherID int64) (mergePair, error) {
	if keepID == otherID {
		return mergePair{}, invalid("an item cannot be merged with itself")
	}
	keep, err := loadItem(ctx, q, `id = ?`, keepID)
	if err != nil {
		return mergePair{}, err
	}
	other, in, credits, err := itemDataTx(ctx, q, otherID)
	if err != nil {
		return mergePair{}, err
	}
	switch {
	case keep == nil || other == nil:
		return mergePair{}, invalid("unknown item")
	case keep.PluginID != other.PluginID || keep.Kind != other.Kind:
		return mergePair{}, invalid("only items of the same kind can be merged")
	}
	var coverSource string
	if err := q.QueryRowContext(ctx, `SELECT cover_source FROM items WHERE id = ?`, otherID).Scan(&coverSource); err != nil {
		return mergePair{}, fmt.Errorf("merge items: %w", err)
	}
	return mergePair{keep: keep, other: other, in: in, credits: credits, coverSource: coverSource}, nil
}

// itemDataTx reads the item through q as incoming data for ApplyChanges:
// its parts as plugin.ItemData and its credits. All are zero (and the item
// nil) for an unknown id.
func itemDataTx(ctx context.Context, q queryer, id int64) (*Item, plugin.ItemData, []plugin.Credit, error) {
	it, err := loadItem(ctx, q, `id = ?`, id)
	if err != nil || it == nil {
		return nil, plugin.ItemData{}, nil, err
	}
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: it.Source, ID: it.SourceID, Kind: it.Kind, Titles: it.Titles, Fields: it.Fields, CoverURL: it.CoverPath},
		ExternalURL: it.ExternalURL, ExternalIDs: it.ExternalIDs, Relations: it.Relations, Genres: it.Genres, Tags: it.Tags,
	}
	saved, err := loadCredits(ctx, q, id)
	if err != nil {
		return nil, plugin.ItemData{}, nil, err
	}
	ref := func(p Person) plugin.PersonRef {
		return plugin.PersonRef{ID: p.SourceID, Name: p.Name, ImageURL: p.ImagePath, URL: p.ExternalURL}
	}
	var credits []plugin.Credit
	for _, c := range saved {
		pc := plugin.Credit{Role: c.Role, Detail: c.Detail, Person: ref(c.Person), WithRole: c.WithRole}
		if c.With != nil {
			w := ref(*c.With)
			pc.With = &w
		}
		credits = append(credits, pc)
	}
	return it, d, credits, nil
}

// mergeIDsTx returns the ids the one-column query selects.
func mergeIDsTx(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("merge items: %w", err)
	}
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("merge items: %w", err)
		}
		out = append(out, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("merge items: %w", err)
	}
	return out, nil
}

// mergeEntry moves the other item's entry data to the survivor (see
// MergeItems): the entry and its notes, the runs with their progress, unit
// notes and events, the other item's orphan events, own tags, list places
// and its series place. The other item's rows that stay behind go with its
// deletion.
func mergeEntry(ctx context.Context, tx *sql.Tx, keepID, otherID int64) error {
	// every event moves; the run ids stay, so a run's events follow it
	if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntryEvents+` SET item_id = ? WHERE item_id = ?`, keepID, otherID); err != nil {
		return fmt.Errorf("merge items: events: %w", err)
	}
	var notes, added, updated string
	switch err := tx.QueryRowContext(ctx, `SELECT notes, added_at, updated_at FROM `+tableEntries+` WHERE item_id = ?`, otherID).
		Scan(&notes, &added, &updated); {
	case errors.Is(err, sql.ErrNoRows):
		return nil // no entry: nothing else to move
	case err != nil:
		return fmt.Errorf("merge items: entry: %w", err)
	}
	var keepNotes string
	switch err := tx.QueryRowContext(ctx, `SELECT notes FROM `+tableEntries+` WHERE item_id = ?`, keepID).Scan(&keepNotes); {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntries+`(item_id, notes, added_at, updated_at) VALUES (?,?,?,?)`,
			keepID, notes, added, updated); err != nil {
			return fmt.Errorf("merge items: entry: %w", err)
		}
	case err != nil:
		return fmt.Errorf("merge items: entry: %w", err)
	default:
		joined := keepNotes
		switch {
		case keepNotes == "":
			joined = notes
		case notes != "":
			joined = keepNotes + notesDivider + notes
		}
		if utf8.RuneCountInString(joined) > MaxNotesRunes {
			return invalid("the merged notes would be longer than %d characters; shorten one of them first", MaxNotesRunes)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET notes = ?, added_at = MIN(added_at, ?), updated_at = MAX(updated_at, ?)
			WHERE item_id = ?`, joined, added, updated, keepID); err != nil {
			return fmt.Errorf("merge items: entry: %w", err)
		}
	}
	if err := mergeRuns(ctx, tx, keepID, otherID); err != nil {
		return err
	}
	for _, q := range []string{
		`INSERT INTO entry_user_tags(item_id, tag_id) SELECT ?, tag_id FROM entry_user_tags WHERE item_id = ?
			ON CONFLICT(item_id, tag_id) DO NOTHING`,
		// a list the survivor is in keeps its place; the other's place
		// counts only when the survivor is not a member (positions are
		// renumbered once the other item is gone)
		`INSERT INTO list_items(list_id, item_id, position, added_at) SELECT list_id, ?, position, added_at FROM list_items WHERE item_id = ?
			ON CONFLICT(list_id, item_id) DO NOTHING`,
		// series_parts.item_id is unique: the survivor takes the other's
		// place only when it is in no series
		`UPDATE series_parts SET item_id = ?1 WHERE item_id = ?2 AND NOT EXISTS (SELECT 1 FROM series_parts WHERE item_id = ?1)`,
	} {
		if _, err := tx.ExecContext(ctx, q, keepID, otherID); err != nil {
			return fmt.Errorf("merge items: %w", err)
		}
	}
	return refreshDisplayRun(ctx, tx, keepID)
}

// mergeRun is one run of either item as the merge orders it.
type mergeRun struct {
	id     int64
	kept   bool // a run of the survivor
	number int
	day    string // the start day, else the creation day
	open   bool   // planned, active or paused
}

// mergeRuns moves the other item's runs to the survivor and renumbers them
// all (see MergeItems). The survivor's entry exists already.
func mergeRuns(ctx context.Context, tx *sql.Tx, keepID, otherID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, item_id, number, COALESCE(started_on, substr(created_at, 1, 10)), status
		FROM `+tableRuns+` WHERE item_id IN (?, ?)`, keepID, otherID)
	if err != nil {
		return fmt.Errorf("merge items: runs: %w", err)
	}
	var runs []mergeRun
	for rows.Next() {
		var r mergeRun
		var item int64
		var status string
		if err := rows.Scan(&r.id, &item, &r.number, &r.day, &status); err != nil {
			rows.Close()
			return fmt.Errorf("merge items: runs: %w", err)
		}
		r.kept = item == keepID
		switch Status(status) {
		case Planned, Active, Paused:
			r.open = true
		}
		runs = append(runs, r)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("merge items: runs: %w", err)
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if a.day != b.day {
			return a.day < b.day
		}
		if a.kept != b.kept {
			return a.kept
		}
		return a.number < b.number
	})
	// the open run that is latest in date order stays open and goes last
	// (an open run must be the latest run); the others keep date order
	last := -1
	for i, r := range runs {
		if r.open {
			last = i
		}
	}
	if last >= 0 {
		open := runs[last]
		runs = append(append(runs[:last:last], runs[last+1:]...), open)
		last = len(runs) - 1
	}
	// the unique (item_id, number) index: every row first moves above every
	// number in use, then down to its place, one row at a time
	n := len(runs)
	for i, r := range runs {
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableRuns+` SET item_id = ?, number = ? WHERE id = ?`, keepID, 2*n+i+1, r.id); err != nil {
			return fmt.Errorf("merge items: runs: %w", err)
		}
	}
	for i, r := range runs {
		set, args := `number = ?`, []any{i + 1}
		if r.open && i != last {
			set += `, status = ?`
			args = append(args, string(Dropped))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+tableRuns+` SET `+set+` WHERE id = ?`, append(args, r.id)...); err != nil { //nolint:gosec // G202: constant SQL; values are bound
			return fmt.Errorf("merge items: runs: %w", err)
		}
	}
	return nil
}
