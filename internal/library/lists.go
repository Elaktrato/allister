package library

// Custom lists: named, cross-plugin collections of entries with an optional
// manual order. A name is one line of 1 to MaxListNameRunes runes, trimmed,
// without control characters; names are unique by their term key (the
// Unicode folding of own tags) and keep the spelling they were first typed
// in. An entry removed from the library leaves every list (the places
// cascade); deleting a list never touches entries. Places are numbered
// densely in the order they were added (or moved, in manual order).
//
// Entries of a plugin that is disabled or not installed stay in their lists
// but are left out of the rows and the counts.

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
)

// MaxListNameRunes is the longest custom list name.
const MaxListNameRunes = 80

// The sort modes of ListRows.
const (
	ListSortPosition = "position"
	ListSortTitle    = "title"
	ListSortAdded    = "added"
	ListSortStatus   = "status"
)

var (
	// ErrNoList: the custom list does not exist (or the entry is not in it).
	ErrNoList = errors.New("library: no such list")
	// ErrListExists: another list has this name (ignoring case).
	ErrListExists = errors.New("library: a list with this name already exists")
)

// List is a custom list with the number of its visible entries (Items) and
// how many of them present as completed (Done).
type List struct {
	ID          int64
	Name        string
	ManualOrder bool
	Items, Done int
}

// listName validates and trims a list name; it returns the name and its key.
func listName(raw string) (string, string, error) {
	name := strings.TrimSpace(raw)
	if err := cleanText("list name", name, singleLine); err != nil {
		return "", "", err
	}
	if name == "" {
		return "", "", invalid("list name is empty")
	}
	if utf8.RuneCountInString(name) > MaxListNameRunes {
		return "", "", invalid("list name %q is longer than %d characters", name, MaxListNameRunes)
	}
	return name, termKey(name), nil
}

// visiblePlugins is the SQL condition on col (a plugin id column) that keeps
// the installed, enabled plugins, with its arguments.
func (l *Library) visiblePlugins(col string) (string, []any) {
	var args []any
	for _, m := range l.reg.Manifests() {
		if l.reg.IsEnabled(m.ID) {
			args = append(args, m.ID)
		}
	}
	if len(args) == 0 {
		return `0`, nil
	}
	return col + ` IN (` + placeholders(len(args)) + `)`, args
}

// lists loads the lists matching where (with args), ordered by name key.
func (l *Library) lists(ctx context.Context, where string, args ...any) ([]List, error) {
	vis, visArgs := l.visiblePlugins("i.plugin_id")
	all := append(append(slices.Clone(visArgs), visArgs...), args...)
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT l.id, l.name, l.manual_order,
		(SELECT count(*) FROM list_items li JOIN items i ON i.id = li.item_id WHERE li.list_id = l.id AND `+vis+`),
		(SELECT count(*) FROM list_items li JOIN items i ON i.id = li.item_id JOIN `+viewEntryState+` e ON e.item_id = li.item_id
			WHERE li.list_id = l.id AND e.status = 'completed' AND `+vis+`)
		FROM lists l`+where+` ORDER BY l.name_key, l.id`, all...)
	if err != nil {
		return nil, fmt.Errorf("lists: %w", err)
	}
	var out []List
	for rows.Next() {
		var x List
		if err := rows.Scan(&x.ID, &x.Name, &x.ManualOrder, &x.Items, &x.Done); err != nil {
			rows.Close()
			return nil, fmt.Errorf("lists: %w", err)
		}
		out = append(out, x)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("lists: %w", err)
	}
	return out, nil
}

// Lists returns every custom list with its counts, ordered by name key.
func (l *Library) Lists(ctx context.Context) ([]List, error) { return l.lists(ctx, "") }

// CustomList returns one list with its counts, ErrNoList for an unknown id.
func (l *Library) CustomList(ctx context.Context, id int64) (List, error) {
	out, err := l.lists(ctx, ` WHERE l.id = ?`, id)
	if err != nil {
		return List{}, err
	}
	if len(out) == 0 {
		return List{}, ErrNoList
	}
	return out[0], nil
}

// ListsOfItem returns the lists the item is in, ordered by name key.
func (l *Library) ListsOfItem(ctx context.Context, itemID int64) ([]List, error) {
	return l.lists(ctx, ` WHERE EXISTS (SELECT 1 FROM list_items x WHERE x.list_id = l.id AND x.item_id = ?)`, itemID)
}

// nameTaken reports whether a list other than id has the name key.
func nameTaken(ctx context.Context, tx *sql.Tx, key string, id int64) (bool, error) {
	var taken bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM lists WHERE name_key = ? AND id <> ?)`, key, id).Scan(&taken)
	return taken, err
}

// CreateList creates an empty, sorted list. An invalid name is ErrInvalid,
// one another list has (ignoring case) ErrListExists.
func (l *Library) CreateList(ctx context.Context, name string) (List, error) {
	name, key, err := listName(name)
	if err != nil {
		return List{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	defer tx.Rollback()
	if taken, err := nameTaken(ctx, tx, key, 0); err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	} else if taken {
		return List{}, ErrListExists
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO lists(name, name_key, manual_order, created_at, updated_at) VALUES (?, ?, 0, `+nowSQL+`, `+nowSQL+`)`, name, key)
	if err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	return List{ID: id, Name: name}, nil
}

// RenameList renames the list. ErrInvalid for a bad name, ErrListExists for
// the name of another list, ErrNoList for an unknown id; its own name in
// another case is a rename.
func (l *Library) RenameList(ctx context.Context, id int64, name string) error {
	name, key, err := listName(name)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename list: %w", err)
	}
	defer tx.Rollback()
	if taken, err := nameTaken(ctx, tx, key, id); err != nil {
		return fmt.Errorf("rename list: %w", err)
	} else if taken {
		return ErrListExists
	}
	if err := touchList(ctx, tx, id, `name = ?, name_key = ?, `, name, key); err != nil {
		return err
	}
	return tx.Commit()
}

// touchList sets the list's updated_at, together with the assignments in set
// (each ending in ", ") and their args. An unknown id is ErrNoList.
func touchList(ctx context.Context, tx *sql.Tx, id int64, set string, args ...any) error {
	//nolint:gosec // G202: set is constant SQL of this package; values are bound
	res, err := tx.ExecContext(ctx, `UPDATE lists SET `+set+`updated_at = `+nowSQL+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if n == 0 {
		return ErrNoList
	}
	return nil
}

// SetListOrder switches the list between manual order and sorted. The
// places keep their order, so switching back and forth loses nothing.
func (l *Library) SetListOrder(ctx context.Context, id int64, manual bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("list order: %w", err)
	}
	defer tx.Rollback()
	if err := touchList(ctx, tx, id, `manual_order = ?, `, manual); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteList deletes the list; its entries stay. ErrNoList for an unknown id.
func (l *Library) DeleteList(ctx context.Context, id int64) error {
	res, err := l.db.ExecContext(ctx, `DELETE FROM lists WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete list: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete list: %w", err)
	} else if n == 0 {
		return ErrNoList
	}
	return nil
}

// listOrder returns whether the list is in manual order; ErrNoList when it
// does not exist.
func listOrder(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (bool, error) {
	var manual bool
	err := q.QueryRowContext(ctx, `SELECT manual_order FROM lists WHERE id = ?`, id).Scan(&manual)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNoList
	}
	if err != nil {
		return false, fmt.Errorf("list: %w", err)
	}
	return manual, nil
}

// hasEntry fails with ErrNoEntry unless the item has an entry.
func hasEntry(ctx context.Context, tx *sql.Tx, itemID int64) error {
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+tableEntries+` WHERE item_id = ?`, itemID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoEntry
	case err != nil:
		return fmt.Errorf("list: %w", err)
	}
	return nil
}

// joinList appends the item to the list unless it is in it already.
func joinList(ctx context.Context, tx *sql.Tx, listID, itemID int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO list_items(list_id, item_id, position, added_at)
		SELECT ?, ?, COALESCE((SELECT MAX(position) FROM list_items WHERE list_id = ?), 0) + 1, `+nowSQL+`
		ON CONFLICT(list_id, item_id) DO NOTHING`, listID, itemID, listID); err != nil {
		return fmt.Errorf("add to list: %w", err)
	}
	return touchList(ctx, tx, listID, "")
}

// AddToCustomList appends the item's entry to the list; an entry that is in
// it already stays where it is. ErrNoList for an unknown list, ErrNoEntry
// for an item without an entry.
func (l *Library) AddToCustomList(ctx context.Context, listID, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("add to list: %w", err)
	}
	defer tx.Rollback()
	if _, err := listOrder(ctx, tx, listID); err != nil {
		return err
	}
	if err := hasEntry(ctx, tx, itemID); err != nil {
		return err
	}
	if err := joinList(ctx, tx, listID, itemID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveFromCustomList takes the item out of the list (its entry stays); an
// item that is not in it is a no-op. ErrNoList for an unknown list.
func (l *Library) RemoveFromCustomList(ctx context.Context, listID, itemID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove from list: %w", err)
	}
	defer tx.Rollback()
	if _, err := listOrder(ctx, tx, listID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM list_items WHERE list_id = ? AND item_id = ?`, listID, itemID); err != nil {
		return fmt.Errorf("remove from list: %w", err)
	}
	if _, err := listMembers.renumber(ctx, tx, listID); err != nil {
		return fmt.Errorf("remove from list: %w", err)
	}
	if err := touchList(ctx, tx, listID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveInList moves the item one place up (delta -1) or down (+1) in a list
// in manual order, like a series part. Moving past either end does nothing;
// a sorted list or another delta is ErrInvalid, an unknown list or an item
// not in it ErrNoList.
func (l *Library) MoveInList(ctx context.Context, listID, itemID int64, delta int) error {
	if delta != -1 && delta != 1 {
		return invalid("move by -1 or 1")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("move in list: %w", err)
	}
	defer tx.Rollback()
	manual, err := listOrder(ctx, tx, listID)
	if err != nil {
		return err
	}
	if !manual {
		return invalid("the list is sorted, not in manual order")
	}
	found, moved, err := listMembers.move(ctx, tx, listID, itemID, delta)
	if err != nil {
		return fmt.Errorf("move in list: %w", err)
	}
	if !found {
		return ErrNoList
	}
	if !moved {
		return nil
	}
	if err := touchList(ctx, tx, listID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// SetListsOfItem saves the item's membership of the lists shown on a form:
// each shown list (and each member list, which counts as shown) the entry
// is in but that is not in member is left, each member list it is not in
// is joined (appended, in the given order; repeats ignored). Lists not shown
// keep their membership, and an unknown id (a list deleted meanwhile) is
// ignored. ErrNoEntry, with nothing changed, when the item has no entry.
func (l *Library) SetListsOfItem(ctx context.Context, itemID int64, shown, member []int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set lists: %w", err)
	}
	defer tx.Rollback()
	if err := hasEntry(ctx, tx, itemID); err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range member {
		want[id] = true
	}
	scope := map[int64]bool{}
	for _, id := range append(slices.Clone(shown), member...) {
		scope[id] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT list_id FROM list_items WHERE item_id = ?`, itemID)
	if err != nil {
		return fmt.Errorf("set lists: %w", err)
	}
	var have []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("set lists: %w", err)
		}
		have = append(have, id)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("set lists: %w", err)
	}
	for _, id := range have {
		if want[id] {
			delete(want, id)
			continue
		}
		if !scope[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM list_items WHERE list_id = ? AND item_id = ?`, id, itemID); err != nil {
			return fmt.Errorf("set lists: %w", err)
		}
		if _, err := listMembers.renumber(ctx, tx, id); err != nil {
			return fmt.Errorf("set lists: %w", err)
		}
		if err := touchList(ctx, tx, id, ""); err != nil {
			return err
		}
	}
	for _, id := range member { // in the given order, each once
		if !want[id] {
			continue
		}
		delete(want, id)
		switch _, err := listOrder(ctx, tx, id); {
		case errors.Is(err, ErrNoList):
			continue // deleted meanwhile
		case err != nil:
			return err
		}
		if err := joinList(ctx, tx, id, itemID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// listPlace is where an item sits in a list.
type listPlace struct {
	position int
	added    string
}

// ListRows returns the list's visible entries as list rows (each with its
// own plugin's title fallback; langs maps a plugin id to its title
// language). sort is ListSortPosition (only for a list in manual order),
// ListSortTitle (lower-cased display title), ListSortAdded (newest first) or
// ListSortStatus (the display status in Statuses order, then title); ""
// is position for a list in manual order, else title. Another sort is
// ErrInvalid, an unknown list ErrNoList. One query per plugin in the list
// loads the rows.
func (l *Library) ListRows(ctx context.Context, listID int64, sort string, langs map[string]string) ([]Row, error) {
	manual, err := listOrder(ctx, l.db, listID)
	if err != nil {
		return nil, err
	}
	switch {
	case sort == "" && manual:
		sort = ListSortPosition
	case sort == "":
		sort = ListSortTitle
	case sort == ListSortPosition && !manual:
		return nil, invalid("the list is sorted, not in manual order")
	case sort != ListSortPosition && sort != ListSortTitle && sort != ListSortAdded && sort != ListSortStatus:
		return nil, invalid("sort %q must be one of position, title, added, status", sort)
	}
	vis, visArgs := l.visiblePlugins("i.plugin_id")
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT li.item_id, i.plugin_id, li.position, li.added_at
		FROM list_items li JOIN items i ON i.id = li.item_id WHERE li.list_id = ? AND `+vis, append([]any{listID}, visArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("list rows: %w", err)
	}
	places := map[int64]listPlace{}
	byPlugin := map[string][]int64{}
	var plugins []string
	for rows.Next() {
		var id int64
		var pluginID string
		var p listPlace
		if err := rows.Scan(&id, &pluginID, &p.position, &p.added); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list rows: %w", err)
		}
		places[id] = p
		if _, ok := byPlugin[pluginID]; !ok {
			plugins = append(plugins, pluginID)
		}
		byPlugin[pluginID] = append(byPlugin[pluginID], id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("list rows: %w", err)
	}
	out, err := l.rowsOfPlugins(ctx, plugins, byPlugin, langs)
	if err != nil {
		return nil, err
	}
	rank := func(s Status) int { return slices.Index(Statuses(), s) }
	byTitle := func(a, b Row) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.Item.ID, b.Item.ID))
	}
	slices.SortFunc(out, func(a, b Row) int {
		pa, pb := places[a.Item.ID], places[b.Item.ID]
		switch sort {
		case ListSortPosition:
			return cmp.Compare(pa.position, pb.position)
		case ListSortAdded:
			return cmp.Or(compareStamps(pb.added, pa.added), cmp.Compare(pb.position, pa.position))
		case ListSortStatus:
			return cmp.Or(cmp.Compare(rank(a.Entry.Status), rank(b.Entry.Status)), byTitle(a, b))
		}
		return byTitle(a, b)
	})
	return out, nil
}

// compareStamps compares two stored RFC 3339 times (a malformed one sorts
// as the zero time).
func compareStamps(a, b string) int {
	ta, _ := time.Parse(time.RFC3339, a)
	tb, _ := time.Parse(time.RFC3339, b)
	return ta.Compare(tb)
}

// rowsOfPlugins loads the list rows of the given items, one query per
// plugin (each with its own unmatched rule and title language), in the
// order of plugins.
func (l *Library) rowsOfPlugins(ctx context.Context, plugins []string, byPlugin map[string][]int64, langs map[string]string) ([]Row, error) {
	var out []Row
	for _, pluginID := range plugins {
		ids := byPlugin[pluginID]
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		man, _ := l.reg.Manifest(pluginID)
		loaded, err := l.loadRows(ctx, unmatchedClause(man), ` WHERE items.id IN (`+placeholders(len(ids))+`)`, args, ` ORDER BY items.id`, nil, len(ids), 0, langs[pluginID])
		if err != nil {
			return nil, err
		}
		out = append(out, loaded...)
	}
	return out, nil
}

// SearchLibrary returns up to limit entries of the enabled plugins whose
// stored titles contain text (the list's search rule: trimmed, ASCII
// case-insensitive, % _ and \ literal), ordered by display title (langs as
// for ListRows). A blank text finds nothing.
func (l *Library) SearchLibrary(ctx context.Context, text string, langs map[string]string, limit int) ([]Row, error) {
	text = strings.TrimSpace(truncateRunes(text, maxFilterRunes))
	if text == "" || limit <= 0 || strings.ContainsRune(text, 0) {
		return nil, nil
	}
	vis, visArgs := l.visiblePlugins("items.plugin_id")
	args := append(append([]any{containsPattern(text)}, visArgs...), limit)
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT items.id, items.plugin_id`+fromSQL+`
		WHERE EXISTS (SELECT 1 FROM item_titles t WHERE t.item_id = items.id AND lower(t.text) LIKE ? ESCAPE '\') AND `+vis+`
		ORDER BY (SELECT lower(t.text) FROM item_titles t WHERE t.item_id = items.id ORDER BY t.rowid LIMIT 1), items.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search library: %w", err)
	}
	byPlugin := map[string][]int64{}
	var plugins []string
	for rows.Next() {
		var id int64
		var pluginID string
		if err := rows.Scan(&id, &pluginID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("search library: %w", err)
		}
		if _, ok := byPlugin[pluginID]; !ok {
			plugins = append(plugins, pluginID)
		}
		byPlugin[pluginID] = append(byPlugin[pluginID], id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("search library: %w", err)
	}
	out, err := l.rowsOfPlugins(ctx, plugins, byPlugin, langs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Row) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.Item.ID, b.Item.ID))
	})
	return out, nil
}
