package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxTermRunes = 60
	maxGenres    = 50
	maxTags      = 100
)

type termRow struct {
	kind    string // "genre" or "tag"
	term    string
	key     string
	rank    *int
	spoiler bool
	seq     int
}

// normalizeTerms validates genres and tags against the kind: a kind that does
// not enable them rejects any; terms are trimmed, blanks skipped, duplicates
// removed case-insensitively keeping the first spelling.
func normalizeTerms(def plugin.KindDef, genres []string, tags []plugin.Tag) ([]termRow, error) {
	if len(genres) > 0 && !def.Genres.Enabled {
		return nil, invalid("kind %q does not enable genres", def.Key)
	}
	if len(tags) > 0 && !def.Tags.Enabled {
		return nil, invalid("kind %q does not enable tags", def.Key)
	}
	var out []termRow
	seen := map[string]bool{}
	check := func(kind, name string, rank *int, spoiler bool, max int, n *int) error {
		t := strings.TrimSpace(name)
		if t == "" {
			return nil
		}
		if err := cleanText(kind, t, singleLine); err != nil {
			return err
		}
		if utf8.RuneCountInString(t) > maxTermRunes {
			return invalid("%s %q is longer than %d characters", kind, t, maxTermRunes)
		}
		if rank != nil && (*rank < 0 || *rank > 100) {
			return invalid("rank of tag %q must be between 0 and 100", t)
		}
		key := strings.ToLower(t)
		if seen[kind+"\x00"+key] {
			return nil
		}
		seen[kind+"\x00"+key] = true
		if *n >= max {
			return invalid("more than %d %ss", max, kind)
		}
		out = append(out, termRow{kind: kind, term: t, key: key, rank: rank, spoiler: spoiler, seq: *n})
		*n++
		return nil
	}
	var ng, nt int
	for _, g := range genres {
		if err := check("genre", g, nil, false, maxGenres, &ng); err != nil {
			return nil, err
		}
	}
	for _, tg := range tags {
		if err := check("tag", tg.Name, tg.Rank, tg.Spoiler, maxTags, &nt); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func insertTerms(ctx context.Context, tx *sql.Tx, itemID int64, rows []termRow) error {
	for _, r := range rows {
		var rank any
		if r.rank != nil {
			rank = *r.rank
		}
		spoiler := 0
		if r.spoiler {
			spoiler = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_terms(item_id, kind, term, term_key, rank, spoiler, seq)
			VALUES (?,?,?,?,?,?,?)`, itemID, r.kind, r.term, r.key, rank, spoiler, r.seq); err != nil {
			return fmt.Errorf("save terms: %w", err)
		}
	}
	return nil
}

// SetTerms replaces the item's genres and tags in one transaction, with the
// same validation as SaveItem. It returns ErrNoEntry for an unknown item.
func (l *Library) SetTerms(ctx context.Context, itemID int64, genres []string, tags []plugin.Tag) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set terms: %w", err)
	}
	defer tx.Rollback()
	var pluginID, kind string
	switch err := tx.QueryRowContext(ctx, `SELECT plugin_id, kind FROM items WHERE id = ?`, itemID).Scan(&pluginID, &kind); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoEntry
	case err != nil:
		return fmt.Errorf("set terms: %w", err)
	}
	def, err := l.kindDef(pluginID, kind)
	if err != nil {
		return err
	}
	rows, err := normalizeTerms(def, genres, tags)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_terms WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("set terms: %w", err)
	}
	if err := insertTerms(ctx, tx, itemID, rows); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET updated_at = `+nowSQL+` WHERE id = ?`, itemID); err != nil {
		return fmt.Errorf("set terms: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set terms: %w", err)
	}
	return nil
}
