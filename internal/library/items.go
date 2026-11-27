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
	maxLinks      = 20
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
	banner   string
	trailer  *plugin.Trailer // nil: none
	links    []plugin.Link
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

// CombinationIndex returns the index of the kind's allowed progress
// combination that keys equal as a set (each key once, any order), or -1
// when keys match none. It is the one combination check of the core.
func CombinationIndex(def plugin.KindDef, keys []string) int {
	sorted := slices.Clone(keys)
	sort.Strings(sorted)
	for i, c := range def.Progress.Combinations {
		cs := slices.Clone(c)
		sort.Strings(cs)
		if slices.Equal(cs, sorted) {
			return i
		}
	}
	return -1
}

func normalizeProgress(def plugin.KindDef, keys []string) ([]string, error) {
	if keys == nil {
		keys = def.Progress.Defaults
	}
	out := append([]string{}, keys...)
	if len(out) == 0 && len(def.Progress.Combinations) == 0 {
		return out, nil
	}
	if CombinationIndex(def, out) >= 0 {
		return out, nil
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
	if p.banner, p.trailer, err = normalizeBannerTrailer(d.BannerURL, d.Trailer); err != nil {
		return nil, err
	}
	if p.links, err = normalizeLinks(d.Links); err != nil {
		return nil, err
	}
	return p, nil
}

// normalizeBannerTrailer checks the banner and the trailer. Each image is
// empty, an https URL or a local file name (a downloaded image); the
// trailer link is an https URL, and a trailer without link or thumbnail is
// none.
func normalizeBannerTrailer(banner string, tr *plugin.Trailer) (string, *plugin.Trailer, error) {
	banner = strings.TrimSpace(banner)
	if err := checkHTTPSImageRef("banner", banner); err != nil {
		return "", nil, err
	}
	if tr == nil {
		return banner, nil, nil
	}
	t := plugin.Trailer{URL: strings.TrimSpace(tr.URL), ThumbnailURL: strings.TrimSpace(tr.ThumbnailURL)}
	if t.URL == "" && t.ThumbnailURL == "" {
		return banner, nil, nil
	}
	if t.URL == "" {
		return "", nil, invalid("a trailer needs a link")
	}
	if err := checkHTTPSURL("trailer url", t.URL); err != nil {
		return "", nil, err
	}
	if err := checkHTTPSImageRef("trailer thumbnail", t.ThumbnailURL); err != nil {
		return "", nil, err
	}
	return banner, &t, nil
}

// normalizeLinks checks the item's labelled links: at most maxLinks, each
// with a one-line label and an https URL. Their order is kept.
func normalizeLinks(ls []plugin.Link) ([]plugin.Link, error) {
	if len(ls) > maxLinks {
		return nil, invalid("more than %d links", maxLinks)
	}
	var out []plugin.Link
	for _, l := range ls {
		n := plugin.Link{Label: strings.TrimSpace(l.Label), URL: strings.TrimSpace(l.URL)}
		if n.Label == "" {
			return nil, invalid("a link needs a label")
		}
		if err := cleanText("link label", n.Label, singleLine); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(n.Label) > maxTitleRunes {
			return nil, invalid("a link label is longer than %d characters", maxTitleRunes)
		}
		if n.URL == "" {
			return nil, invalid("link %q needs a URL", n.Label)
		}
		if err := checkHTTPSURL("link "+n.Label, n.URL); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
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
	if existing, err := identityOwner(ctx, tx, pluginID, kind, p.source, p.sourceID); err != nil {
		return 0, fmt.Errorf("save item: %w", err)
	} else if existing != 0 {
		return 0, &AlreadySavedError{ExistingID: existing}
	}
	trailerURL, trailerThumb := trailerColumns(p.trailer)
	res, err := tx.ExecContext(ctx, `INSERT INTO items
		(plugin_id, kind, source, source_id, cover_path, external_url, sync_state, progress_keys, banner_path, trailer_url, trailer_thumb)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		pluginID, kind, p.source, p.sourceID, d.CoverURL, d.ExternalURL, syncState, string(pk), p.banner, trailerURL, trailerThumb)
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

// trailerColumns are the stored trailer_url and trailer_thumb of t.
func trailerColumns(t *plugin.Trailer) (string, string) {
	if t == nil {
		return "", ""
	}
	return t.URL, t.ThumbnailURL
}

func insertLinks(ctx context.Context, tx *sql.Tx, id int64, links []plugin.Link) error {
	for i, l := range links {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_links(item_id, seq, label, url) VALUES (?,?,?,?)`, id, i, l.Label, l.URL); err != nil {
			return fmt.Errorf("save links: %w", err)
		}
	}
	return nil
}

func insertTitles(ctx context.Context, tx *sql.Tx, id int64, titles []plugin.Title) error {
	for _, t := range titles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_titles(item_id, lang, text) VALUES (?,?,?)`, id, t.Lang, t.Text); err != nil {
			return fmt.Errorf("save titles: %w", err)
		}
	}
	return nil
}

func insertFields(ctx context.Context, tx *sql.Tx, id int64, rows []fieldRow) error {
	for _, f := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_fields(item_id, key, vtype, text, num, date, seq) VALUES (?,?,?,?,?,?,?)`,
			id, f.key, string(f.vtype), f.text, f.num, f.date, f.seq); err != nil {
			return fmt.Errorf("save fields: %w", err)
		}
	}
	return nil
}

func insertParts(ctx context.Context, tx *sql.Tx, id int64, p *prepared) error {
	if err := insertTitles(ctx, tx, id, p.titles); err != nil {
		return err
	}
	if err := insertFields(ctx, tx, id, p.fields); err != nil {
		return err
	}
	for _, e := range p.extIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_ids(item_id, type, value) VALUES (?,?,?)`, id, e[0], e[1]); err != nil {
			return fmt.Errorf("save external ids: %w", err)
		}
	}
	if err := insertRelations(ctx, tx, id, p.rels); err != nil {
		return err
	}
	if err := insertLinks(ctx, tx, id, p.links); err != nil {
		return err
	}
	return insertTerms(ctx, tx, id, p.terms)
}

func insertRelations(ctx context.Context, tx *sql.Tx, id int64, rels []relRow) error {
	for i, r := range rels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableRelations+`
			(item_id, seq, kind, source, source_id, kind_item, cover_url, url, titles_json, fields_json)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, id, i, r.kind, r.source, r.sourceID, r.kindItem, r.coverURL, r.url, r.titles, r.fields); err != nil {
			return fmt.Errorf("save relations: %w", err)
		}
	}
	return nil
}

// Item returns the item with all its parts, or nil, nil when absent.
func (l *Library) Item(ctx context.Context, id int64) (*Item, error) {
	return l.loadItem(ctx, l.db, `id = ?`, id)
}

// identityWhere selects the id of the item that owns an identity (plugin,
// kind, source, source id): the item with it, else the item it is an alias
// of (a merge keeps the identity of the item merged away). Its arguments are
// the identity twice.
const identityWhere = `id = COALESCE(
	(SELECT id FROM items WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?),
	(SELECT item_id FROM item_aliases WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?))`

// identityOwner returns the id of the item that owns the identity, or 0.
func identityOwner(ctx context.Context, q queryer, pluginID, kind, source, sourceID string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM items WHERE `+identityWhere,
		pluginID, kind, source, sourceID, pluginID, kind, source, sourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ItemBySource returns the item with the given identity, or the item it is
// an alias of, or nil, nil.
func (l *Library) ItemBySource(ctx context.Context, pluginID, kind, source, sourceID string) (*Item, error) {
	return l.loadItem(ctx, l.db, identityWhere, pluginID, kind, source, sourceID, pluginID, kind, source, sourceID)
}

// ItemByPluginSource returns the plugin's items of every kind with the given
// source identity, or that it is an alias of, ordered by id (several only
// for a plugin with several kinds); none is an empty result, not an error.
func (l *Library) ItemByPluginSource(ctx context.Context, pluginID, source, sourceID string) ([]Item, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND source = ? AND source_id = ?
		UNION SELECT item_id FROM item_aliases WHERE plugin_id = ? AND source = ? AND source_id = ? ORDER BY 1`,
		pluginID, source, sourceID, pluginID, source, sourceID)
	if err != nil {
		return nil, fmt.Errorf("items by source: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("items by source: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("items by source: %w", err)
	}
	var out []Item
	for _, id := range ids {
		it, err := l.Item(ctx, id)
		if err != nil {
			return nil, err
		}
		if it != nil { // deleted meanwhile
			out = append(out, *it)
		}
	}
	return out, nil
}

// ItemByExternalID returns the (oldest) item of the plugin kind that carries
// the external id, or nil, nil.
func (l *Library) ItemByExternalID(ctx context.Context, pluginID, kind, idType, value string) (*Item, error) {
	return l.loadItem(ctx, l.db, `id = (SELECT i.id FROM items i JOIN external_ids e ON e.item_id = i.id
		WHERE i.plugin_id = ? AND i.kind = ? AND e.type = ? AND e.value = ? ORDER BY i.id LIMIT 1)`,
		pluginID, kind, idType, value)
}

// loadItem reads the item through q. Stored field values are read as the
// kind's current field type where the two differ compatibly (listCompat).
func (l *Library) loadItem(ctx context.Context, q queryer, where string, args ...any) (*Item, error) {
	var it Item
	var pk string
	var tr plugin.Trailer
	err := q.QueryRowContext(ctx, `SELECT id, plugin_id, kind, source, source_id, cover_path, external_url, sync_state, progress_keys,
		banner_path, trailer_url, trailer_thumb
		FROM items WHERE `+where+` LIMIT 1`, args...).Scan(
		&it.ID, &it.PluginID, &it.Kind, &it.Source, &it.SourceID, &it.CoverPath, &it.ExternalURL, &it.SyncState, &pk,
		&it.BannerPath, &tr.URL, &tr.ThumbnailURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load item: %w", err)
	}
	if err := json.Unmarshal([]byte(pk), &it.ProgressKeys); err != nil {
		return nil, fmt.Errorf("load item: progress keys: %w", err)
	}
	if tr.URL != "" {
		it.Trailer = &tr
	}
	if err := loadParts(ctx, q, &it); err != nil {
		return nil, err
	}
	l.listCompat(it.PluginID, it.Kind, it.Fields)
	return &it, nil
}

// loadParts reads the child tables of the item: one query per table.
func loadParts(ctx context.Context, q queryer, it *Item) error {
	rows, err := q.QueryContext(ctx, `SELECT lang, text FROM item_titles WHERE item_id = ? ORDER BY rowid`, it.ID)
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
	rows, err = q.QueryContext(ctx, `SELECT key, vtype, text, num, date FROM item_fields WHERE item_id = ? ORDER BY key, seq`, it.ID)
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

	rows, err = q.QueryContext(ctx, `SELECT type, value FROM external_ids WHERE item_id = ?`, it.ID)
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

	rows, err = q.QueryContext(ctx, `SELECT kind, source, source_id, kind_item, cover_url, url, titles_json, fields_json
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

	rows, err = q.QueryContext(ctx, `SELECT label, url FROM item_links WHERE item_id = ? ORDER BY seq`, it.ID)
	if err != nil {
		return fmt.Errorf("load links: %w", err)
	}
	for rows.Next() {
		var lk plugin.Link
		if err := rows.Scan(&lk.Label, &lk.URL); err != nil {
			rows.Close()
			return fmt.Errorf("load links: %w", err)
		}
		it.Links = append(it.Links, lk)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("load links: %w", err)
	}

	rows, err = q.QueryContext(ctx, `SELECT kind, term, rank, spoiler FROM item_terms WHERE item_id = ? ORDER BY kind, seq`, it.ID)
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
