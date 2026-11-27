package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// FillFromSource fills the item from its source's answer without changing
// a stored value, in ONE transaction: a single value (a non-list field, the
// cover, the banner, the trailer) is written only where the item has none
// (a stored remote cover URL counts as set); a list gets the incoming
// elements it lacks appended after its stored ones, and nothing is removed,
// reordered or changed. A title of a real language is appended only when
// the item has none in that language, an alternative title
// (plugin.AltTitleLang) when its text is missing; list field and genre
// values match by value, tags by name (a stored tag keeps its rank), links
// by URL, relations by kind, source and id, and credits by role, detail,
// the people's ids and the counterpart role (never by a person's name);
// credits only when withCredits is set, and a person already stored keeps
// its name and URL. An append stops at the list's stored maximum without
// error. It returns the keys written (Change keys, in Change order, with
// "credits" last when credits were appended); none means nothing changed.
// A write bumps the item's updated_at like ApplyChanges, never
// touches its entry (no entry event) and enqueues no image download (the
// caller's sync does). Every incoming value is validated first, like
// ItemChanges: one that cannot be stored is ErrInvalid and nothing is
// written. ErrNoEntry for an unknown item.
func (l *Library) FillFromSource(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool) (applied []string, err error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fill from source: %w", err)
	}
	defer tx.Rollback()
	def, parts, err := l.changeParts(ctx, tx, itemID, in, credits, withCredits, nil)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		if p.err != nil {
			return nil, p.err
		}
	}
	it, err := l.loadItem(ctx, tx, `id = ?`, itemID)
	if err != nil {
		return nil, err
	}
	merged := fillMerged(def, it, in)
	keys := []string{"titles", "genres", "tags", "cover", "banner", "trailer", "links", "relations"}
	for _, fd := range def.Fields {
		keys = append(keys, "field."+fd.Key)
	}
	applied, err = l.applyChangesTx(ctx, tx, itemID, merged, nil, false, keys, nil)
	if err != nil {
		return nil, err
	}
	if withCredits && len(credits) > 0 {
		added, err := fillCredits(ctx, tx, it, credits)
		if err != nil {
			return nil, err
		}
		if added > 0 && len(applied) == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET updated_at = `+nowSQL+` WHERE id = ?`, itemID); err != nil {
				return nil, fmt.Errorf("fill from source: %w", err)
			}
		}
		if added > 0 {
			applied = append(applied, "credits")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("fill from source: %w", err)
	}
	return applied, nil
}

// fillMerged is the item data a fill writes through the whole-part writes of
// ApplyChanges: each part the fill changes holds the stored value with the
// missing incoming elements appended (or the incoming single value where
// none is stored); a part it leaves alone is empty, which is never a
// difference. The incoming values are already validated.
func fillMerged(def plugin.KindDef, it *Item, in plugin.ItemData) plugin.ItemData {
	var out plugin.ItemData
	if titles, err := normalizeTitles(in.Titles); err == nil {
		out.Titles = fillTitles(it.Titles, titles)
	}
	for _, fd := range def.Fields {
		if f, ok := fillField(fd, it.Fields, in.Fields); ok {
			if out.Fields == nil {
				out.Fields = map[string]plugin.FieldValue{}
			}
			out.Fields[fd.Key] = f
		}
	}
	if rows, err := normalizeTerms(def, in.Genres, nil); err == nil {
		var genres []string
		for _, r := range rows {
			genres = append(genres, r.term)
		}
		out.Genres = appendMissing(it.Genres, genres, maxGenres, func(g string) string { return termKey(g) })
	}
	if rows, err := normalizeTerms(def, nil, in.Tags); err == nil {
		var tags []plugin.Tag
		for _, r := range rows {
			tags = append(tags, plugin.Tag{Name: r.term, Rank: r.rank, Spoiler: r.spoiler})
		}
		out.Tags = appendMissing(it.Tags, tags, maxTags, func(t plugin.Tag) string { return termKey(t.Name) })
	}
	if it.CoverPath == "" {
		out.CoverURL = in.CoverURL
	}
	if it.BannerPath == "" {
		out.BannerURL = in.BannerURL
	}
	if it.Trailer == nil {
		out.Trailer = in.Trailer
	}
	if links, err := normalizeLinks(in.Links); err == nil {
		out.Links = appendMissing(it.Links, links, maxLinks, func(k plugin.Link) string { return k.URL })
	}
	// stored relations the rules no longer accept are left alone
	if _, err := normalizeRelations(it.Relations); err == nil {
		out.Relations = appendMissing(it.Relations, in.Relations, maxRelations, func(r plugin.Relation) string {
			return strings.Join([]string{r.Kind, r.Item.Source, r.Item.ID}, "\x00")
		})
	}
	return out
}

// fillTitles is the stored titles with the incoming titles of each real
// language the item has no title in, and the incoming alternative titles
// whose text it lacks, appended; nil when none is.
func fillTitles(stored, in []plugin.Title) []plugin.Title {
	langs := map[string]bool{}
	for _, t := range stored {
		langs[t.Lang] = true
	}
	var add []plugin.Title
	for _, t := range in {
		if t.Lang == plugin.AltTitleLang || !langs[t.Lang] {
			add = append(add, t)
		}
	}
	return appendMissing(stored, add, 0, func(t plugin.Title) string { return t.Lang + "\x00" + t.Text })
}

// fillField is the value a fill writes to one field: the incoming value
// where none is stored or the field is a SourceRefresh statistic, a stored
// list with the missing incoming values appended (up to maxListValues), or
// none (ok false).
func fillField(fd plugin.FieldDef, stored, in map[string]plugin.FieldValue) (plugin.FieldValue, bool) {
	v, ok := in[fd.Key]
	if !ok {
		return plugin.FieldValue{}, false
	}
	rows, err := fieldRows(fd, v)
	if err != nil || len(rows) == 0 {
		return plugin.FieldValue{}, false
	}
	sv, has := stored[fd.Key]
	if !has {
		return v, true
	}
	if fd.SourceRefresh && !fd.Editable {
		// a value the source keeps current: the incoming one replaces it
		// (an equal value is no difference, so nothing is written)
		return v, true
	}
	if fd.Type != plugin.FieldList || len(sv.List) == 0 {
		return plugin.FieldValue{}, false
	}
	vals := make([]string, len(rows))
	for i, r := range rows {
		vals[i] = *r.text
	}
	list := appendMissing(sv.List, vals, maxListValues, func(s string) string { return s })
	if list == nil {
		return plugin.FieldValue{}, false
	}
	return plugin.FieldValue{List: list}, true
}

// appendMissing returns stored with the incoming elements whose key it
// lacks appended in their order (each key once), stopping at limit
// elements (0: none), or nil when nothing is appended.
func appendMissing[T any](stored, incoming []T, limit int, key func(T) string) []T {
	have := make(map[string]bool, len(stored))
	for _, s := range stored {
		have[key(s)] = true
	}
	out := append([]T(nil), stored...)
	for _, n := range incoming {
		if limit > 0 && len(out) >= limit {
			break
		}
		k := key(n)
		if have[k] {
			continue
		}
		have[k] = true
		out = append(out, n)
	}
	if len(out) == len(stored) {
		return nil
	}
	return out
}

// fillCreditKey is a credit's identity in a fill: role, detail, the
// people's ids and the counterpart role, never a person's name.
func fillCreditKey(role, detail, personID, withID, withRole string) string {
	return strings.Join([]string{role, detail, strings.TrimSpace(personID), strings.TrimSpace(withID), withRole}, "\x00")
}

// fillCredits appends the incoming credits the item lacks (by
// fillCreditKey) after its stored ones, up to maxCredits, and returns how
// many it wrote. People already stored are left as they are; only new ones
// are inserted. The incoming credits are already validated.
func fillCredits(ctx context.Context, tx *sql.Tx, it *Item, in []plugin.Credit) (int, error) {
	stored, err := loadCredits(ctx, tx, it.ID)
	if err != nil {
		return 0, err
	}
	have := make(map[string]bool, len(stored))
	for _, c := range stored {
		withID := ""
		if c.With != nil {
			withID = c.With.SourceID
		}
		have[fillCreditKey(c.Role, c.Detail, c.Person.SourceID, withID, c.WithRole)] = true
	}
	var add []plugin.Credit
	for _, c := range in {
		if len(stored)+len(add) >= maxCredits {
			break
		}
		withID := ""
		if c.With != nil {
			withID = c.With.ID
		}
		k := fillCreditKey(c.Role, c.Detail, c.Person.ID, withID, c.WithRole)
		if have[k] {
			continue
		}
		have[k] = true
		add = append(add, c)
	}
	if len(add) == 0 {
		return 0, nil
	}
	next := 0
	if len(stored) > 0 {
		next = stored[len(stored)-1].Seq + 1
	}
	if err := insertCreditsFrom(ctx, tx, it.ID, it.PluginID, it.Source, add, next, ensurePerson); err != nil {
		return 0, err
	}
	return len(add), nil
}
