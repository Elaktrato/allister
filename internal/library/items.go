package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

const (
	maxTitleRunes = 300
	maxLangLen    = 35
	maxRelations  = 200
)

// SaveOption customises SaveItem.
type SaveOption func(*saveOpts)

type saveOpts struct {
	syncState    string
	progressKeys []string // nil: the kind's defaults
}

// WithSyncState sets the initial sync_state (default "pending"). Manual
// entries use WithSyncState("done") since there is nothing to sync.
func WithSyncState(state string) SaveOption {
	return func(o *saveOpts) { o.syncState = state }
}

// WithProgressKeys sets the active progress keys (default: the kind's
// Progress.Defaults). They must be one of the kind's allowed combinations.
func WithProgressKeys(keys []string) SaveOption {
	return func(o *saveOpts) { o.progressKeys = keys }
}

type relRow struct {
	kind, source, sourceID, kindItem, coverURL, url, titles, fields string
}

// prepared is the validated, normalised form of an ItemData.
type prepared struct {
	def      plugin.KindDef
	source   string
	sourceID string
	titles   []plugin.Title
	fields   []fieldRow
	extIDs   [][2]string
	rels     []relRow
	terms    []termRow
	progress []string
}

func validLang(s string) bool {
	if s == "" || len(s) > maxLangLen {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func normalizeTitles(ts []plugin.Title) ([]plugin.Title, error) {
	var out []plugin.Title
	seen := map[plugin.Title]bool{}
	for _, t := range ts {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if err := cleanText("title", text, singleLine); err != nil {
			return nil, err
		}
		lang := strings.ToLower(strings.TrimSpace(t.Lang))
		if !validLang(lang) {
			return nil, invalid("title language %q must be letters, digits and '-'", t.Lang)
		}
		if utf8.RuneCountInString(text) > maxTitleRunes {
			return nil, invalid("a title is longer than %d characters", maxTitleRunes)
		}
		n := plugin.Title{Lang: lang, Text: text}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, invalid("an item needs at least one title")
	}
	return out, nil
}

func normalizeProgress(def plugin.KindDef, keys []string) ([]string, error) {
	if keys == nil {
		keys = def.Progress.Defaults
	}
	out := append([]string{}, keys...)
	sorted := slices.Clone(out)
	sort.Strings(sorted)
	if len(out) == 0 && len(def.Progress.Combinations) == 0 {
		return out, nil
	}
	for _, c := range def.Progress.Combinations {
		cs := slices.Clone(c)
		sort.Strings(cs)
		if slices.Equal(cs, sorted) {
			return out, nil
		}
	}
	return nil, invalid("progress keys %v are not an allowed combination for kind %q", keys, def.Key)
}

func normalizeRelations(rs []plugin.Relation) ([]relRow, error) {
	if len(rs) > maxRelations {
		return nil, invalid("more than %d relations", maxRelations)
	}
	out := make([]relRow, 0, len(rs))
	for _, r := range rs {
		if strings.TrimSpace(r.Kind) == "" {
			return nil, invalid("a relation needs a kind")
		}
		if err := checkRelation(r); err != nil {
			return nil, err
		}
		titles, err := json.Marshal(r.Item.Titles)
		if err != nil {
			return nil, invalid("relation titles: %v", err)
		}
		fields, err := json.Marshal(r.Item.Fields)
		if err != nil {
			return nil, invalid("relation fields: %v", err)
		}
		out = append(out, relRow{r.Kind, r.Item.Source, r.Item.ID, r.Item.Kind, r.Item.CoverURL, r.Item.URL, string(titles), string(fields)})
	}
	return out, nil
}

// checkRelation applies the text rules to every stored string of a relation.
func checkRelation(r plugin.Relation) error {
	for _, v := range [][2]string{{"relation kind", r.Kind}, {"relation item source", r.Item.Source},
		{"relation item id", r.Item.ID}, {"relation item kind", r.Item.Kind}} {
		if err := cleanText(v[0], v[1], singleLine); err != nil {
			return err
		}
	}
	if err := checkImageRef("relation item cover", r.Item.CoverURL); err != nil {
		return err
	}
	if err := checkHTTPURL("relation item url", r.Item.URL); err != nil {
		return err
	}
	for _, t := range r.Item.Titles {
		if err := cleanText("relation item title", t.Text, singleLine); err != nil {
			return err
		}
		if err := cleanText("relation item title language", t.Lang, singleLine); err != nil {
			return err
		}
	}
	for k, f := range r.Item.Fields {
		if err := cleanText("relation item field key", k, singleLine); err != nil {
			return err
		}
		if err := cleanText("relation item field "+k, f.Text, multiLine); err != nil {
			return err
		}
		for _, v := range f.List {
			if err := cleanText("relation item field "+k, v, singleLine); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeExternalIDs(ids map[string]string) ([][2]string, error) {
	var out [][2]string
	seen := map[string]bool{}
	for k, v := range ids {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			return nil, invalid("an external id needs a type")
		}
		if err := cleanText("external id type", k, singleLine); err != nil {
			return nil, err
		}
		if err := cleanText("external id "+k, v, singleLine); err != nil {
			return nil, err
		}
		if v == "" {
			continue
		}
		if seen[k] {
			return nil, invalid("external id type %q is given more than once", k)
		}
		seen[k] = true
		out = append(out, [2]string{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, nil
}

// prepare validates d against the kind of the registered plugin. It does not
// enforce required fields.
func (l *Library) prepare(pluginID, kind string, d plugin.ItemData, progress []string) (*prepared, error) {
	def, err := l.kindDef(pluginID, kind)
	if err != nil {
		return nil, err
	}
	if d.Kind != "" && d.Kind != kind {
		return nil, invalid("item kind %q does not match kind %q", d.Kind, kind)
	}
	p := &prepared{def: def, source: strings.TrimSpace(d.Source), sourceID: strings.TrimSpace(d.ID)}
	if p.source == "" || p.sourceID == "" {
		return nil, invalid("an item needs a source and a source id")
	}
	if err := cleanText("source", p.source, singleLine); err != nil {
		return nil, err
	}
	if err := cleanText("source id", p.sourceID, singleLine); err != nil {
		return nil, err
	}
	if err := checkHTTPURL("external url", d.ExternalURL); err != nil {
		return nil, err
	}
	if err := checkImageRef("cover", d.CoverURL); err != nil {
		return nil, err
	}
	if p.titles, err = normalizeTitles(d.Titles); err != nil {
		return nil, err
	}
	if p.fields, err = normalizeFields(def, d.Fields); err != nil {
		return nil, err
	}
	if p.extIDs, err = normalizeExternalIDs(d.ExternalIDs); err != nil {
		return nil, err
	}
	if p.rels, err = normalizeRelations(d.Relations); err != nil {
		return nil, err
	}
	if p.terms, err = normalizeTerms(def, d.Genres, d.Tags); err != nil {
		return nil, err
	}
	if p.progress, err = normalizeProgress(def, progress); err != nil {
		return nil, err
	}
	return p, nil
}

// ValidateItem runs every check SaveItem runs and additionally enforces the
// kind's required fields. Manual create and edit use it; sources often lack
// fields, so SaveItem alone accepts partial data.
func (l *Library) ValidateItem(pluginID, kind string, d plugin.ItemData) error {
	p, err := l.prepare(pluginID, kind, d, nil)
	if err != nil {
		return err
	}
	if miss := requiredMissing(p.def, p.fields); len(miss) > 0 {
		return invalid("required field(s) missing: %s", strings.Join(miss, ", "))
	}
	return nil
}

// SaveItem stores the item with all its parts in one IMMEDIATE transaction
// and returns the new id. It returns an *AlreadySavedError (matching
// ErrAlreadySaved) when (plugin, kind, source, source_id) exists, and
// ErrInvalid for data that fails validation. The cover URL is stored in
// cover_path (see the package comment). The plugin need not be enabled.
func (l *Library) SaveItem(ctx context.Context, pluginID, kind string, d plugin.ItemData, opts ...SaveOption) (int64, error) {
	o := saveOpts{syncState: "pending"}
	for _, f := range opts {
		f(&o)
	}
	if err := checkSyncState(o.syncState); err != nil {
		return 0, err
	}
	p, err := l.prepare(pluginID, kind, d, o.progressKeys)
	if err != nil {
		return 0, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}
	defer tx.Rollback()
	id, err := insertItem(ctx, tx, pluginID, kind, d, o.syncState, p)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}
	return id, nil
}

// insertItem stores the prepared item inside tx. It returns an
// *AlreadySavedError when the identity exists.
func insertItem(ctx context.Context, tx *sql.Tx, pluginID, kind string, d plugin.ItemData, syncState string, p *prepared) (int64, error) {
	pk, err := json.Marshal(p.progress)
	if err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?`,
		pluginID, kind, p.source, p.sourceID).Scan(&existing)
	if err == nil {
		return 0, &AlreadySavedError{ExistingID: existing}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("save item: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO items
		(plugin_id, kind, source, source_id, cover_path, external_url, sync_state, progress_keys)
		VALUES (?,?,?,?,?,?,?,?)`,
		pluginID, kind, p.source, p.sourceID, d.CoverURL, d.ExternalURL, syncState, string(pk))
	if err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	}
	if err := insertParts(ctx, tx, id, p); err != nil {
		return 0, err
	}
	return id, nil
}

func insertParts(ctx context.Context, tx *sql.Tx, id int64, p *prepared) error {
	for _, t := range p.titles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_titles(item_id, lang, text) VALUES (?,?,?)`, id, t.Lang, t.Text); err != nil {
			return fmt.Errorf("save titles: %w", err)
		}
	}
	for _, f := range p.fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_fields(item_id, key, vtype, text, num, date, seq) VALUES (?,?,?,?,?,?,?)`,
			id, f.key, string(f.vtype), f.text, f.num, f.date, f.seq); err != nil {
			return fmt.Errorf("save fields: %w", err)
		}
	}
	for _, e := range p.extIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_ids(item_id, type, value) VALUES (?,?,?)`, id, e[0], e[1]); err != nil {
			return fmt.Errorf("save external ids: %w", err)
		}
	}
	for i, r := range p.rels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableRelations+`
			(item_id, seq, kind, source, source_id, kind_item, cover_url, url, titles_json, fields_json)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, id, i, r.kind, r.source, r.sourceID, r.kindItem, r.coverURL, r.url, r.titles, r.fields); err != nil {
			return fmt.Errorf("save relations: %w", err)
		}
	}
	return insertTerms(ctx, tx, id, p.terms)
}

// Item returns the item with all its parts, or nil, nil when absent.
func (l *Library) Item(ctx context.Context, id int64) (*Item, error) {
	return l.loadItem(ctx, `id = ?`, id)
}

// ItemBySource returns the item with the given identity, or nil, nil.
func (l *Library) ItemBySource(ctx context.Context, pluginID, kind, source, sourceID string) (*Item, error) {
	return l.loadItem(ctx, `plugin_id = ? AND kind = ? AND source = ? AND source_id = ?`, pluginID, kind, source, sourceID)
}

// ItemByExternalID returns the (oldest) item of the plugin kind that carries
// the external id, or nil, nil.
func (l *Library) ItemByExternalID(ctx context.Context, pluginID, kind, idType, value string) (*Item, error) {
	return l.loadItem(ctx, `id = (SELECT i.id FROM items i JOIN external_ids e ON e.item_id = i.id
		WHERE i.plugin_id = ? AND i.kind = ? AND e.type = ? AND e.value = ? ORDER BY i.id LIMIT 1)`,
		pluginID, kind, idType, value)
}

func (l *Library) loadItem(ctx context.Context, where string, args ...any) (*Item, error) {
	var it Item
	var pk string
	err := l.db.QueryRowContext(ctx, `SELECT id, plugin_id, kind, source, source_id, cover_path, external_url, sync_state, progress_keys
		FROM items WHERE `+where+` LIMIT 1`, args...).Scan(
		&it.ID, &it.PluginID, &it.Kind, &it.Source, &it.SourceID, &it.CoverPath, &it.ExternalURL, &it.SyncState, &pk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load item: %w", err)
	}
	if err := json.Unmarshal([]byte(pk), &it.ProgressKeys); err != nil {
		return nil, fmt.Errorf("load item: progress keys: %w", err)
	}
	if err := l.loadParts(ctx, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

// loadParts reads the child tables of the item: one query per table.
func (l *Library) loadParts(ctx context.Context, it *Item) error {
	rows, err := l.db.QueryContext(ctx, `SELECT lang, text FROM item_titles WHERE item_id = ? ORDER BY rowid`, it.ID)
	if err != nil {
		return fmt.Errorf("load titles: %w", err)
	}
	for rows.Next() {
		var t plugin.Title
		if err := rows.Scan(&t.Lang, &t.Text); err != nil {
			rows.Close()
			return fmt.Errorf("load titles: %w", err)
		}
		it.Titles = append(it.Titles, t)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load titles: %w", err)
	}

	acc := newFieldAcc()
	rows, err = l.db.QueryContext(ctx, `SELECT key, vtype, text, num, date FROM item_fields WHERE item_id = ? ORDER BY key, seq`, it.ID)
	if err != nil {
		return fmt.Errorf("load fields: %w", err)
	}
	for rows.Next() {
		var key, vtype string
		var text, date sql.NullString
		var num sql.NullFloat64
		if err := rows.Scan(&key, &vtype, &text, &num, &date); err != nil {
			rows.Close()
			return fmt.Errorf("load fields: %w", err)
		}
		var tp, dp *string
		var np *float64
		if text.Valid {
			tp = &text.String
		}
		if date.Valid {
			dp = &date.String
		}
		if num.Valid {
			np = &num.Float64
		}
		if err := acc.add(key, vtype, tp, np, dp); err != nil {
			rows.Close()
			return fmt.Errorf("load fields: %w", err)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load fields: %w", err)
	}
	if len(acc.vals) > 0 {
		it.Fields = acc.vals
	}

	rows, err = l.db.QueryContext(ctx, `SELECT type, value FROM external_ids WHERE item_id = ?`, it.ID)
	if err != nil {
		return fmt.Errorf("load external ids: %w", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return fmt.Errorf("load external ids: %w", err)
		}
		if it.ExternalIDs == nil {
			it.ExternalIDs = map[string]string{}
		}
		it.ExternalIDs[k] = v
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load external ids: %w", err)
	}

	rows, err = l.db.QueryContext(ctx, `SELECT kind, source, source_id, kind_item, cover_url, url, titles_json, fields_json
		FROM `+tableRelations+` WHERE item_id = ? ORDER BY seq`, it.ID)
	if err != nil {
		return fmt.Errorf("load relations: %w", err)
	}
	for rows.Next() {
		var r plugin.Relation
		var tj, fj string
		if err := rows.Scan(&r.Kind, &r.Item.Source, &r.Item.ID, &r.Item.Kind, &r.Item.CoverURL, &r.Item.URL, &tj, &fj); err != nil {
			rows.Close()
			return fmt.Errorf("load relations: %w", err)
		}
		if err := json.Unmarshal([]byte(tj), &r.Item.Titles); err != nil {
			rows.Close()
			return fmt.Errorf("load relations: %w", err)
		}
		if err := json.Unmarshal([]byte(fj), &r.Item.Fields); err != nil {
			rows.Close()
			return fmt.Errorf("load relations: %w", err)
		}
		if len(r.Item.Titles) == 0 {
			r.Item.Titles = nil
		}
		if len(r.Item.Fields) == 0 {
			r.Item.Fields = nil
		}
		it.Relations = append(it.Relations, r)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load relations: %w", err)
	}

	rows, err = l.db.QueryContext(ctx, `SELECT kind, term, rank, spoiler FROM item_terms WHERE item_id = ? ORDER BY kind, seq`, it.ID)
	if err != nil {
		return fmt.Errorf("load terms: %w", err)
	}
	for rows.Next() {
		var kind, term string
		var rank sql.NullInt64
		var spoiler int
		if err := rows.Scan(&kind, &term, &rank, &spoiler); err != nil {
			rows.Close()
			return fmt.Errorf("load terms: %w", err)
		}
		if kind == "genre" {
			it.Genres = append(it.Genres, term)
			continue
		}
		t := plugin.Tag{Name: term, Spoiler: spoiler != 0}
		if rank.Valid {
			r := int(rank.Int64)
			t.Rank = &r
		}
		it.Tags = append(it.Tags, t)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load terms: %w", err)
	}
	return nil
}

// closeRows closes rows and reports the iteration error, if any.
func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}
