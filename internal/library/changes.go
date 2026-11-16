package library

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// Change is one part of an item whose incoming data (a source's answer, or
// another item) differs from the stored data. Key is stable: "field.<key>",
// "titles", "genres", "tags", "cover", "relations" or "credits". Stored and
// Incoming are plain display strings; Added and Removed hold the item labels
// of a list part ("lang: text" titles, "kind: title" relations, "role: name"
// credits). StoredEmpty reports that the stored side has no value.
type Change struct {
	Key, Label       string
	Stored, Incoming string
	StoredEmpty      bool
	Added, Removed   []string
}

// changePart is one comparable part: its Change, whether it differs, the
// validation error of its incoming value and how to write that value.
type changePart struct {
	change  Change
	differs bool
	err     error
	write   func(ctx context.Context, tx *sql.Tx) error
}

// listItem is one entry of a list part: the key it is compared by and the
// label it is shown with.
type listItem struct{ key, label string }

// ItemChanges compares the incoming data with the stored item and returns one
// Change per differing part, ordered titles, fields (manifest order), genres,
// tags, cover, relations, credits. A missing or empty incoming value is never
// a difference: a field absent from in.Fields or without a value, empty
// titles, genres, tags or relations, an empty cover URL, and credits unless
// withCredits is set and the list is non-empty. Values are compared after the
// normalisation SaveItem applies; field keys the kind does not know are
// dropped. Lists are compared as sets (a different order alone is no
// difference). ErrNoEntry for an unknown item, ErrInvalid when an incoming
// value cannot be stored.
func (l *Library) ItemChanges(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool) ([]Change, error) {
	_, parts, err := l.changeParts(ctx, l.db, itemID, in, credits, withCredits)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, p := range parts {
		if p.err != nil {
			return nil, p.err
		}
		if p.differs {
			out = append(out, p.change)
		}
	}
	return out, nil
}

// ApplyChanges writes the parts named by keys (Change keys) from the incoming
// data, in ONE transaction that recomputes the differences first: a selected
// key that no longer differs is skipped and not counted. It returns the
// number of parts written and bumps the item's updated_at when that is above
// zero. Every selected value is validated before the first write; an unknown
// key or an incoming value that cannot be stored is ErrInvalid and nothing is
// written. Taking the cover stores the incoming URL and marks the item's sync
// pending, so the cover download fetches it (as after an add). ErrNoEntry for
// an unknown item.
func (l *Library) ApplyChanges(ctx context.Context, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool, keys []string) (int, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("apply changes: %w", err)
	}
	defer tx.Rollback()
	def, parts, err := l.changeParts(ctx, tx, itemID, in, credits, withCredits)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{"titles": true, "genres": true, "tags": true, "cover": true, "relations": true, "credits": true}
	for _, fd := range def.Fields {
		known["field."+fd.Key] = true
	}
	selected := map[string]bool{}
	for _, k := range keys {
		if !known[k] {
			return 0, invalid("unknown change %q", k)
		}
		selected[k] = true
	}
	var todo []changePart
	for _, p := range parts {
		if !selected[p.change.Key] {
			continue
		}
		if p.err != nil {
			return 0, p.err
		}
		if p.differs {
			todo = append(todo, p)
		}
	}
	for _, p := range todo {
		if err := p.write(ctx, tx); err != nil {
			return 0, err
		}
	}
	if len(todo) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET updated_at = `+nowSQL+` WHERE id = ?`, itemID); err != nil {
			return 0, fmt.Errorf("apply changes: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("apply changes: %w", err)
	}
	return len(todo), nil
}

// changeParts loads the item through q and builds its comparable parts in
// Change order. Only database errors and ErrNoEntry are returned; the
// validation error of an incoming value is kept on its part.
func (l *Library) changeParts(ctx context.Context, q queryer, itemID int64, in plugin.ItemData, credits []plugin.Credit, withCredits bool) (plugin.KindDef, []changePart, error) {
	it, err := loadItem(ctx, q, `id = ?`, itemID)
	if err != nil {
		return plugin.KindDef{}, nil, err
	}
	if it == nil {
		return plugin.KindDef{}, nil, ErrNoEntry
	}
	def, err := l.kindDef(it.PluginID, it.Kind)
	if err != nil {
		return plugin.KindDef{}, nil, err
	}
	var parts []changePart
	if p, ok := titlesPart(it, in.Titles); ok {
		parts = append(parts, p)
	}
	for _, fd := range def.Fields {
		if p, ok := fieldPart(it, fd, in.Fields); ok {
			parts = append(parts, p)
		}
	}
	if p, ok := genresPart(def, it, in.Genres); ok {
		parts = append(parts, p)
	}
	if p, ok := tagsPart(def, it, in.Tags); ok {
		parts = append(parts, p)
	}
	if p, ok := coverPart(it, in.CoverURL); ok {
		parts = append(parts, p)
	}
	if p, ok := relationsPart(it, in.Relations); ok {
		parts = append(parts, p)
	}
	if withCredits && len(credits) > 0 {
		stored, err := loadCredits(ctx, q, itemID)
		if err != nil {
			return plugin.KindDef{}, nil, err
		}
		parts = append(parts, creditsPart(it, stored, credits))
	}
	return def, parts, nil
}

// listPart fills a list part's Change from the stored and incoming items.
func listPart(key, label string, stored, incoming []listItem) changePart {
	added, removed, differs := diffLists(stored, incoming)
	return changePart{
		change: Change{Key: key, Label: label, Stored: joinLabels(stored), Incoming: joinLabels(incoming),
			StoredEmpty: len(stored) == 0, Added: added, Removed: removed},
		differs: differs,
	}
}

// diffLists compares two lists as multisets of their keys: the labels of
// incoming items missing from stored (added) and of stored items missing from
// incoming (removed), each in its list's order.
func diffLists(stored, incoming []listItem) (added, removed []string, differs bool) {
	left := map[string]int{}
	for _, s := range stored {
		left[s.key]++
	}
	for _, n := range incoming {
		if left[n.key] > 0 {
			left[n.key]--
			continue
		}
		added = append(added, n.label)
	}
	right := map[string]int{}
	for _, n := range incoming {
		right[n.key]++
	}
	for _, s := range stored {
		if right[s.key] > 0 {
			right[s.key]--
			continue
		}
		removed = append(removed, s.label)
	}
	return added, removed, len(added) > 0 || len(removed) > 0
}

func joinLabels(items []listItem) string {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	return strings.Join(labels, "; ")
}

func titleItems(ts []plugin.Title) []listItem {
	out := make([]listItem, len(ts))
	for i, t := range ts {
		out[i] = listItem{key: t.Lang + "\x00" + t.Text, label: t.Lang + ": " + t.Text}
	}
	return out
}

func titlesPart(it *Item, in []plugin.Title) (changePart, bool) {
	blank := true
	for _, t := range in {
		if strings.TrimSpace(t.Text) != "" {
			blank = false
			break
		}
	}
	if blank {
		return changePart{}, false
	}
	titles, err := normalizeTitles(in)
	if err != nil {
		return changePart{change: Change{Key: "titles"}, err: err}, true
	}
	p := listPart("titles", "Titles", titleItems(it.Titles), titleItems(titles))
	id := it.ID
	p.write = func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_titles WHERE item_id = ?`, id); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		return insertTitles(ctx, tx, id, titles)
	}
	return p, true
}

func fieldPart(it *Item, fd plugin.FieldDef, in map[string]plugin.FieldValue) (changePart, bool) {
	v, ok := in[fd.Key]
	if !ok {
		return changePart{}, false
	}
	key := "field." + fd.Key
	rows, err := fieldRows(fd, v)
	if err != nil {
		return changePart{change: Change{Key: key, Label: fd.Label}, err: err}, true
	}
	if len(rows) == 0 {
		return changePart{}, false
	}
	sv, has := it.Fields[fd.Key]
	differs := true
	if has {
		if stored, err := fieldRows(fd, sv); err == nil && fieldRowsEqual(stored, rows) {
			differs = false
		}
	}
	p := changePart{
		change:  Change{Key: key, Label: fd.Label, Stored: fieldValueText(sv), Incoming: fieldRowsText(rows), StoredEmpty: !has},
		differs: differs,
	}
	id := it.ID
	p.write = func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_fields WHERE item_id = ? AND key = ?`, id, fd.Key); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		return insertFields(ctx, tx, id, rows)
	}
	return p, true
}

func fieldRowsEqual(a, b []fieldRow) bool {
	if len(a) != len(b) {
		return false
	}
	eqS := func(x, y *string) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	for i := range a {
		x, y := a[i], b[i]
		if x.key != y.key || x.vtype != y.vtype || x.seq != y.seq || !eqS(x.text, y.text) || !eqS(x.date, y.date) ||
			(x.num == nil) != (y.num == nil) || (x.num != nil && *x.num != *y.num) {
			return false
		}
	}
	return true
}

func formatNum(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// fieldRowsText is the plain display text of normalised field rows: the
// text, the number, the date, or a list's values joined by ", ".
func fieldRowsText(rows []fieldRow) string {
	vals := make([]string, 0, len(rows))
	for _, r := range rows {
		switch {
		case r.num != nil:
			vals = append(vals, formatNum(*r.num))
		case r.date != nil:
			vals = append(vals, *r.date)
		case r.text != nil:
			vals = append(vals, *r.text)
		}
	}
	return strings.Join(vals, ", ")
}

// fieldValueText is fieldRowsText for a stored value.
func fieldValueText(v plugin.FieldValue) string {
	switch {
	case v.Num != nil:
		return formatNum(*v.Num)
	case v.Date != nil:
		return v.Date.Format(dateLayout)
	case len(v.List) > 0:
		return strings.Join(v.List, ", ")
	}
	return v.Text
}

// termsPart builds the genres or tags part from normalised rows.
func termsPart(key, label, kind string, itemID int64, stored []listItem, rows []termRow) changePart {
	incoming := make([]listItem, len(rows))
	for i, r := range rows {
		incoming[i] = listItem{key: termCompareKey(r.term, r.rank, r.spoiler), label: r.term}
	}
	p := listPart(key, label, stored, incoming)
	p.write = func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_terms WHERE item_id = ? AND kind = ?`, itemID, kind); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		return insertTerms(ctx, tx, itemID, rows)
	}
	return p
}

func termCompareKey(term string, rank *int, spoiler bool) string {
	r := ""
	if rank != nil {
		r = strconv.Itoa(*rank)
	}
	return term + "\x00" + r + "\x00" + strconv.FormatBool(spoiler)
}

func termLabel(t plugin.TermDef, fallback string) string {
	if t.Label != "" {
		return t.Label
	}
	return fallback
}

func genresPart(def plugin.KindDef, it *Item, in []string) (changePart, bool) {
	if len(in) == 0 {
		return changePart{}, false
	}
	label := termLabel(def.Genres, "Genres")
	rows, err := normalizeTerms(def, in, nil)
	if err != nil {
		return changePart{change: Change{Key: "genres", Label: label}, err: err}, true
	}
	if len(rows) == 0 {
		return changePart{}, false
	}
	stored := make([]listItem, len(it.Genres))
	for i, g := range it.Genres {
		stored[i] = listItem{key: termCompareKey(g, nil, false), label: g}
	}
	return termsPart("genres", label, "genre", it.ID, stored, rows), true
}

func tagsPart(def plugin.KindDef, it *Item, in []plugin.Tag) (changePart, bool) {
	if len(in) == 0 {
		return changePart{}, false
	}
	label := termLabel(def.Tags, "Tags")
	rows, err := normalizeTerms(def, nil, in)
	if err != nil {
		return changePart{change: Change{Key: "tags", Label: label}, err: err}, true
	}
	if len(rows) == 0 {
		return changePart{}, false
	}
	stored := make([]listItem, len(it.Tags))
	for i, t := range it.Tags {
		stored[i] = listItem{key: termCompareKey(t.Name, t.Rank, t.Spoiler), label: t.Name}
	}
	return termsPart("tags", label, "tag", it.ID, stored, rows), true
}

// coverText shows an image reference: a URL as it is, a local file as
// "local cover".
func coverText(s string) string {
	if s == "" || isRemoteRef(s) {
		return s
	}
	return "local cover"
}

func isRemoteRef(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func coverPart(it *Item, in string) (changePart, bool) {
	if in == "" {
		return changePart{}, false
	}
	if err := checkImageRef("cover", in); err != nil {
		return changePart{change: Change{Key: "cover", Label: "Cover"}, err: err}, true
	}
	id := it.ID
	return changePart{
		change:  Change{Key: "cover", Label: "Cover", Stored: coverText(it.CoverPath), Incoming: coverText(in), StoredEmpty: it.CoverPath == ""},
		differs: in != it.CoverPath,
		write: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET cover_path = ?, sync_state = 'pending' WHERE id = ?`, in, id); err != nil {
				return fmt.Errorf("apply changes: %w", err)
			}
			return nil
		},
	}, true
}

// relationItems keys normalised relation rows by every stored column (an
// empty JSON list or object counts as none) and labels them "kind: title".
func relationItems(rows []relRow, rels []plugin.Relation) []listItem {
	canon := func(s string) string {
		if s == "null" || s == "[]" || s == "{}" {
			return ""
		}
		return s
	}
	out := make([]listItem, len(rows))
	for i, r := range rows {
		name := r.sourceID
		for _, t := range rels[i].Item.Titles {
			if strings.TrimSpace(t.Text) != "" {
				name = t.Text
				break
			}
		}
		out[i] = listItem{
			key:   strings.Join([]string{r.kind, r.source, r.sourceID, r.kindItem, r.coverURL, r.url, canon(r.titles), canon(r.fields)}, "\x00"),
			label: r.kind + ": " + name,
		}
	}
	return out
}

func relationsPart(it *Item, in []plugin.Relation) (changePart, bool) {
	if len(in) == 0 {
		return changePart{}, false
	}
	rows, err := normalizeRelations(in)
	if err != nil {
		return changePart{change: Change{Key: "relations", Label: "Relations"}, err: err}, true
	}
	var stored []listItem
	if srows, err := normalizeRelations(it.Relations); err == nil {
		stored = relationItems(srows, it.Relations)
	} else {
		// stored data the rules no longer accept: show it, always differing
		for _, r := range it.Relations {
			stored = append(stored, listItem{key: "\x00invalid", label: r.Kind + ": " + r.Item.ID})
		}
	}
	p := listPart("relations", "Relations", stored, relationItems(rows, in))
	id := it.ID
	p.write = func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+tableRelations+` WHERE item_id = ?`, id); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		return insertRelations(ctx, tx, id, rows)
	}
	return p, true
}

// creditKey compares a credit by role, detail, its people's ids and the
// counterpart role; a person's name counts only when the incoming side names
// one (an empty name keeps the stored one on save).
func creditKey(role, detail, personID, name, withID, withName, withRole string) string {
	return strings.Join([]string{role, detail, strings.TrimSpace(personID), strings.TrimSpace(name),
		strings.TrimSpace(withID), strings.TrimSpace(withName), withRole}, "\x00")
}

func creditLabel(role, name, withName string) string {
	s := role + ": " + name
	if withName != "" {
		s += " (" + withName + ")"
	}
	return s
}

func creditsPart(it *Item, stored []SavedCredit, in []plugin.Credit) changePart {
	if err := checkCredits(in); err != nil {
		return changePart{change: Change{Key: "credits", Label: "Credits"}, err: err}
	}
	byID := map[string]string{} // stored person source id -> name
	for _, c := range stored {
		byID[c.Person.SourceID] = c.Person.Name
		if c.With != nil {
			byID[c.With.SourceID] = c.With.Name
		}
	}
	// an incoming person without a name shows (and compares) with the stored name
	nameOf := func(p plugin.PersonRef) string {
		if n := strings.TrimSpace(p.Name); n != "" {
			return n
		}
		return byID[strings.TrimSpace(p.ID)]
	}
	inc := make([]listItem, len(in))
	for i, c := range in {
		var withID, withName string
		if c.With != nil {
			withID, withName = c.With.ID, nameOf(*c.With)
		}
		name := nameOf(c.Person)
		inc[i] = listItem{key: creditKey(c.Role, c.Detail, c.Person.ID, name, withID, withName, c.WithRole),
			label: creditLabel(c.Role, name, withName)}
	}
	st := make([]listItem, len(stored))
	for i, c := range stored {
		var withID, withName string
		if c.With != nil {
			withID, withName = c.With.SourceID, c.With.Name
		}
		st[i] = listItem{key: creditKey(c.Role, c.Detail, c.Person.SourceID, c.Person.Name, withID, withName, c.WithRole),
			label: creditLabel(c.Role, c.Person.Name, withName)}
	}
	p := listPart("credits", "Credits", st, inc)
	id, pluginID, source := it.ID, it.PluginID, it.Source
	p.write = func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM credits WHERE item_id = ?`, id); err != nil {
			return fmt.Errorf("apply changes: %w", err)
		}
		return insertCredits(ctx, tx, id, pluginID, source, in)
	}
	return p
}
