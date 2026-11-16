package library

// The owner's own tags: personal labels shared across all plugins, set per
// entry. A name is one line of 1 to MaxUserTagRunes runes, trimmed, without
// control characters; names are unique case-insensitively by their term key
// (the folding of genres and tags, Unicode lower case) and keep the spelling
// they were first typed in.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxUserTagRunes is the longest own tag name.
	MaxUserTagRunes = 40
	// maxEntryTags is the most own tags one entry can carry.
	maxEntryTags = 50
)

// ErrNoUserTag: the own tag does not exist.
var ErrNoUserTag = errors.New("library: no such tag")

// UserTag is an own tag with the number of entries carrying it.
type UserTag struct {
	ID    int64
	Name  string
	Count int
}

// userTagName validates and trims a tag name; it returns the name and its key.
func userTagName(raw string) (string, string, error) {
	name := strings.TrimSpace(raw)
	if err := cleanText("tag name", name, singleLine); err != nil {
		return "", "", err
	}
	if name == "" {
		return "", "", invalid("tag name is empty")
	}
	if utf8.RuneCountInString(name) > MaxUserTagRunes {
		return "", "", invalid("tag name %q is longer than %d characters", name, MaxUserTagRunes)
	}
	return name, termKey(name), nil
}

// SetEntryTags replaces the own tags of the item's entry with names (blank
// names are skipped, repeats by key dropped keeping the first). Missing tags
// are created as typed; an existing tag keeps its stored spelling. Nothing
// is written when a name is refused (ErrInvalid); an item without an entry
// is ErrNoEntry.
func (l *Library) SetEntryTags(ctx context.Context, itemID int64, names []string) error {
	type tagName struct{ name, key string }
	var want []tagName
	for _, raw := range names {
		if strings.TrimSpace(raw) == "" && utf8.ValidString(raw) {
			continue
		}
		name, key, err := userTagName(raw)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(want, func(t tagName) bool { return t.key == key }) {
			continue
		}
		if len(want) == maxEntryTags {
			return invalid("more than %d tags", maxEntryTags)
		}
		want = append(want, tagName{name, key})
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	defer tx.Rollback()
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM entries WHERE item_id = ?`, itemID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoEntry
	case err != nil:
		return fmt.Errorf("set tags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entry_user_tags WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	for _, t := range want {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_tags(name, name_key, created_at) VALUES (?, ?, `+nowSQL+`)
			ON CONFLICT(name_key) DO NOTHING`, t.name, t.key); err != nil {
			return fmt.Errorf("set tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entry_user_tags(item_id, tag_id)
			SELECT ?, id FROM user_tags WHERE name_key = ?`, itemID, t.key); err != nil {
			return fmt.Errorf("set tags: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	return nil
}

// EntryTags returns the own tag names of the given items' entries, each
// list ordered by name key; items without tags are absent.
func (l *Library) EntryTags(ctx context.Context, itemIDs []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		args[i] = id
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT x.item_id, u.name FROM entry_user_tags x JOIN user_tags u ON u.id = x.tag_id
		WHERE x.item_id IN (`+placeholders(len(args))+`) ORDER BY x.item_id, u.name_key`, args...)
	if err != nil {
		return nil, fmt.Errorf("entry tags: %w", err)
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("entry tags: %w", err)
		}
		out[id] = append(out[id], name)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("entry tags: %w", err)
	}
	return out, nil
}

// UserTags returns every own tag with its entry count, ordered by name key.
func (l *Library) UserTags(ctx context.Context) ([]UserTag, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT u.id, u.name, (SELECT COUNT(*) FROM entry_user_tags x WHERE x.tag_id = u.id)
		FROM user_tags u ORDER BY u.name_key`)
	if err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	var out []UserTag
	for rows.Next() {
		var t UserTag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("tags: %w", err)
		}
		out = append(out, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	return out, nil
}

// HasUserTags reports whether any own tag exists.
func (l *Library) HasUserTags(ctx context.Context) (bool, error) {
	var has bool
	if err := l.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_tags)`).Scan(&has); err != nil {
		return false, fmt.Errorf("tags: %w", err)
	}
	return has, nil
}

// RenameUserTag renames the tag id. A name whose key belongs to another tag
// merges: the entries of tag id move onto that tag (each once) and tag id is
// removed; the other tag keeps its spelling. ErrNoUserTag for an unknown id.
func (l *Library) RenameUserTag(ctx context.Context, id int64, name string) error {
	name, key, err := userTagName(name)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename tag: %w", err)
	}
	defer tx.Rollback()
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM user_tags WHERE id = ?`, id).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoUserTag
	case err != nil:
		return fmt.Errorf("rename tag: %w", err)
	}
	var other int64
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM user_tags WHERE name_key = ? AND id <> ?`, key, id).Scan(&other); {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `UPDATE user_tags SET name = ?, name_key = ? WHERE id = ?`, name, key, id); err != nil {
			return fmt.Errorf("rename tag: %w", err)
		}
	case err != nil:
		return fmt.Errorf("rename tag: %w", err)
	default:
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entry_user_tags(item_id, tag_id)
			SELECT item_id, ? FROM entry_user_tags WHERE tag_id = ?`, other, id); err != nil {
			return fmt.Errorf("merge tag: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_tags WHERE id = ?`, id); err != nil {
			return fmt.Errorf("merge tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rename tag: %w", err)
	}
	return nil
}

// DeleteUserTag removes the tag from every entry and deletes it.
// ErrNoUserTag for an unknown id.
func (l *Library) DeleteUserTag(ctx context.Context, id int64) error {
	res, err := l.db.ExecContext(ctx, `DELETE FROM user_tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	if n == 0 {
		return ErrNoUserTag
	}
	return nil
}

// compileUserTags is the own-tag filter: any (default) or all of the
// selected tags by name key. ok is false when nothing is selected.
func compileUserTags(vals []string, all bool) (clause, bool, error) {
	keys, nul, err := filterKeys("user_tags", "tag", vals)
	if err != nil {
		return clause{}, false, err
	}
	if len(keys) == 0 && !nul {
		return clause{}, false, nil
	}
	if len(keys) == 0 || (nul && all) {
		return clause{"user_tags", `0`, nil}, true, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	in := `x.item_id = items.id AND u.name_key IN (` + placeholders(len(keys)) + `)`
	from := `FROM entry_user_tags x JOIN user_tags u ON u.id = x.tag_id WHERE `
	if all {
		return clause{"user_tags", `(SELECT COUNT(DISTINCT u.id) ` + from + in + `) = ` + strconv.Itoa(len(keys)), args}, true, nil
	}
	return clause{"user_tags", `EXISTS (SELECT 1 ` + from + in + `)`, args}, true, nil
}

// UserTagTerms returns the own tags of the entries matching q WITHOUT its
// own-tag filter, with their entry counts, for a filter bar: ordered by
// count descending, then by name key; at most 1000 rows.
func (l *Library) UserTagTerms(ctx context.Context, q Query) ([]TermCount, error) {
	c, err := l.compile(q)
	if err != nil {
		return nil, err
	}
	whereSQL, whereArgs := c.where("user_tags")
	args := append(slices.Clone(whereArgs), maxTermsListed)
	//nolint:gosec // G202: the query builder joins constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT u.name, COUNT(DISTINCT items.id) AS n`+fromSQL+
		` JOIN entry_user_tags x ON x.item_id = items.id JOIN user_tags u ON u.id = x.tag_id`+whereSQL+
		` GROUP BY u.id ORDER BY n DESC, u.name_key LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("own tags: %w", err)
	}
	var out []TermCount
	for rows.Next() {
		var tc TermCount
		if err := rows.Scan(&tc.Term, &tc.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("own tags: %w", err)
		}
		out = append(out, tc)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("own tags: %w", err)
	}
	return out, nil
}
