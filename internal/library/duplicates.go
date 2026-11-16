package library

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/allister/allister/internal/plugin"
)

// DuplicatePair is two items of one plugin and kind that are probably the
// same thing. A < B and at least one of them is in the library. Reason is
// "id:<type>" (a shared external id of that type) or "title" (equal
// normalised title and, for kinds with a year field, equal year).
type DuplicatePair struct {
	A, B   int64
	Reason string
}

// normalTitle folds a title for comparison: the same case folding as terms,
// every run of characters that are neither letters nor digits becomes one
// space, and the result is trimmed.
func normalTitle(s string) string {
	var b strings.Builder
	space := false
	for _, r := range termKey(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

type pairKey struct{ a, b int64 }

// Duplicates lists the likely duplicate pairs of a plugin, external-id
// reasons first (then by item ids), without dismissed pairs.
func (l *Library) Duplicates(ctx context.Context, pluginID string) ([]DuplicatePair, error) {
	seen := map[pairKey]bool{}
	var out []DuplicatePair
	add := func(a, b int64, reason string) {
		if a > b {
			a, b = b, a
		}
		k := pairKey{a, b}
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, DuplicatePair{A: a, B: b, Reason: reason})
	}
	dismissed := map[pairKey]bool{}
	if err := l.scanRows(ctx, `SELECT d.item_a, d.item_b FROM dismissed_duplicates d JOIN items i ON i.id = d.item_a WHERE i.plugin_id = ?`, []any{pluginID},
		func(r *sql.Rows) error {
			var k pairKey
			if err := r.Scan(&k.a, &k.b); err != nil {
				return err
			}
			dismissed[k] = true
			return nil
		}); err != nil {
		return nil, err
	}
	if err := l.scanRows(ctx, `SELECT x.item_id, y.item_id, x.type
FROM external_ids x
JOIN external_ids y ON y.type = x.type AND y.value = x.value AND y.item_id > x.item_id
JOIN items a ON a.id = x.item_id
JOIN items b ON b.id = y.item_id
WHERE a.plugin_id = ?1 AND b.plugin_id = ?1 AND a.kind = b.kind
  AND (EXISTS (SELECT 1 FROM entries WHERE item_id = a.id) OR EXISTS (SELECT 1 FROM entries WHERE item_id = b.id))
ORDER BY x.type, x.item_id, y.item_id`, []any{pluginID},
		func(r *sql.Rows) error {
			var a, b int64
			var typ string
			if err := r.Scan(&a, &b, &typ); err != nil {
				return err
			}
			if !dismissed[pairKey{a, b}] {
				add(a, b, "id:"+typ)
			}
			return nil
		}); err != nil {
		return nil, err
	}

	// Title (and year) groups, over the plugin's items.
	type cand struct {
		id    int64
		inLib bool
	}
	groups := map[string][]cand{}
	years := map[int64]float64{}
	if err := l.scanRows(ctx, `SELECT f.item_id, f.num FROM item_fields f JOIN items i ON i.id = f.item_id
WHERE i.plugin_id = ? AND f.key = 'year' AND f.num IS NOT NULL`, []any{pluginID},
		func(r *sql.Rows) error {
			var id int64
			var y float64
			if err := r.Scan(&id, &y); err != nil {
				return err
			}
			years[id] = y
			return nil
		}); err != nil {
		return nil, err
	}
	hasYear := map[string]bool{}
	titles := map[int64][]plugin.Title{}
	if err := l.scanRows(ctx, `SELECT t.item_id, t.lang, t.text FROM item_titles t JOIN items i ON i.id = t.item_id
WHERE i.plugin_id = ? ORDER BY t.rowid`, []any{pluginID},
		func(r *sql.Rows) error {
			var id int64
			var t plugin.Title
			if err := r.Scan(&id, &t.Lang, &t.Text); err != nil {
				return err
			}
			titles[id] = append(titles[id], t)
			return nil
		}); err != nil {
		return nil, err
	}
	fallbacks := map[string][]string{}
	if err := l.scanRows(ctx, `SELECT i.id, i.kind, EXISTS (SELECT 1 FROM entries e WHERE e.item_id = i.id) FROM items i WHERE i.plugin_id = ? ORDER BY i.id`, []any{pluginID},
		func(r *sql.Rows) error {
			var id int64
			var kind string
			var in bool
			if err := r.Scan(&id, &kind, &in); err != nil {
				return err
			}
			fb, ok := fallbacks[kind]
			if !ok {
				fb = l.titleFallback(pluginID, kind)
				fallbacks[kind] = fb
				hasYear[kind] = l.kindHasYear(pluginID, kind)
			}
			key := normalTitle(ResolveTitle(titles[id], "", fb))
			if key == "" {
				return nil
			}
			if hasYear[kind] {
				y, ok := years[id]
				if !ok {
					return nil
				}
				key += "\x00" + strconv.FormatFloat(y, 'f', -1, 64)
			}
			gk := kind + "\x00" + key
			groups[gk] = append(groups[gk], cand{id, in})
			return nil
		}); err != nil {
		return nil, err
	}
	for _, g := range groups {
		for i := range g {
			for j := i + 1; j < len(g); j++ {
				a, b := g[i], g[j]
				if !a.inLib && !b.inLib {
					continue
				}
				lo, hi := a.id, b.id
				if lo > hi {
					lo, hi = hi, lo
				}
				if dismissed[pairKey{lo, hi}] {
					continue
				}
				add(lo, hi, "title")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ii, jj := strings.HasPrefix(out[i].Reason, "id:"), strings.HasPrefix(out[j].Reason, "id:")
		if ii != jj {
			return ii
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out, nil
}

// DismissDuplicate hides a pair from Duplicates for good. Either order; a
// second dismissal is a no-op; the same id twice or an unknown item is
// ErrInvalid.
func (l *Library) DismissDuplicate(ctx context.Context, a, b int64) error {
	if a == b {
		return invalid("an item is not a duplicate of itself")
	}
	if a > b {
		a, b = b, a
	}
	var n int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id IN (?, ?)`, a, b).Scan(&n); err != nil {
		return err
	}
	if n != 2 {
		return invalid("unknown item")
	}
	_, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO dismissed_duplicates(item_a, item_b, dismissed_at)
VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, a, b)
	return err
}

// scanRows runs a query and calls scan for every row.
func (l *Library) scanRows(ctx context.Context, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// kindHasYear reports whether the kind declares a number field "year"; only
// then must two items agree on it to match by title.
func (l *Library) kindHasYear(pluginID, kind string) bool {
	k, err := l.kindDef(pluginID, kind)
	if err != nil {
		return false
	}
	for _, f := range k.Fields {
		if f.Key == "year" && f.Type == plugin.FieldNumber {
			return true
		}
	}
	return false
}
