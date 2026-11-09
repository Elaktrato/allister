package library

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// FilterOp is the operator of a field Filter.
type FilterOp string

const (
	// OpEq: the single value equals the field value (enum, number, date).
	OpEq FilterOp = "eq"
	// OpIn: the field value is one of the values (enum); for a list field any
	// listed value matches.
	OpIn FilterOp = "in"
	// OpRange: one value "a..b", "a.." or "..b" (number, date).
	OpRange FilterOp = "range"
	// OpContains: case-insensitive substring (text, url).
	OpContains FilterOp = "contains"
)

// Filter restricts a Filterable field of the kind. Values are strings; their
// meaning depends on the op and the field type (see FilterOp). All filters of a
// query must match (AND).
type Filter struct {
	Key    string
	Op     FilterOp
	Values []string
}

// SortKind selects the sort key of a Query.
type SortKind string

const (
	// SortUpdated sorts by the entry's last update.
	SortUpdated SortKind = "updated"
	// SortTitle sorts by the lower-cased title: the title in the caller's
	// language when the item has one, else its first stored title. (The
	// resolved display title, with its ja/ja-latn/en fallbacks, is not available
	// in SQL.) Lower-casing is ASCII-only.
	SortTitle SortKind = "title"
	// SortRating sorts by rating, unrated entries last.
	SortRating SortKind = "rating"
	// SortProgress sorts by the value of the kind's primary progress key (when
	// active for the item), items without a value last.
	SortProgress SortKind = "progress"
	// SortField sorts by a Sortable number, date or text field, items without a
	// value last.
	SortField SortKind = "field"
)

// Sort orders the rows. The zero Sort (empty Kind) is the default: newest
// update first, ignoring Desc. An explicit Kind follows Desc (false =
// ascending; for SortUpdated that means oldest first). Every order ends with
// the item id DESC so it is total and stable.
type Sort struct {
	Kind  SortKind
	Field string // SortField only
	Desc  bool
}

// ProgressState filters by the progress of the item's active tracks, with the
// semantics of the Finished and NotStarted helpers.
type ProgressState string

const (
	ProgressAny        ProgressState = "any" // also the zero value
	ProgressNotStarted ProgressState = "not_started"
	ProgressInProgress ProgressState = "in_progress" // neither not started nor finished
	ProgressFinished   ProgressState = "finished"
)

// Query selects entries of ONE plugin kind (PluginID and Kind are required).
//
// Text is a case-insensitive substring over all stored titles (longer than 200
// runes is truncated); % _ and \ are literal. Case-insensitivity is ASCII
// only (SQLite lower()); other scripts match by exact case.
//
// Genres and Tags match case-insensitively on the term key; "any" (default)
// needs one selected term, GenresAll/TagsAll every selected term.
//
// The rating conditions RatingMin/RatingMax (an inclusive range), Unrated
// (rating NULL) and RatingZero (rating explicitly 0) are alternatives: an
// entry matches when it satisfies ANY selected one. Unmatched and PartialSync
// narrow the rows (AND with everything else).
type Query struct {
	PluginID, Kind     string
	Text               string
	Status             *Status
	Filters            []Filter
	Genres, Tags       []string
	GenresAll, TagsAll bool
	RatingMin          *int
	RatingMax          *int
	Unrated            bool
	RatingZero         bool
	Unmatched          bool
	PartialSync        bool
	Progress           ProgressState
	Sort               Sort
	Page, PageSize     int
}

// FilterError is the typed error for a rejected filter, term filter or sort:
// an unknown key, an operator that does not suit the field type, a malformed
// value or a limit that is exceeded. It matches ErrInvalid. Callers decide
// whether to ignore the offending part of the query.
type FilterError struct{ Key, Reason string }

func (e *FilterError) Error() string {
	return fmt.Sprintf("%s: filter %q: %s", ErrInvalid.Error(), e.Key, e.Reason)
}

// Is makes errors.Is(err, ErrInvalid) true.
func (e *FilterError) Is(target error) bool { return target == ErrInvalid }

func filterErr(key, format string, a ...any) error {
	return &FilterError{Key: key, Reason: fmt.Sprintf(format, a...)}
}

// RowFlags marks list rows that need attention.
type RowFlags struct {
	// Unmatched: a manual item carrying the external id of an importer of its
	// plugin, i.e. an import row that matched nothing at the source.
	Unmatched bool
	// PartialSync: sync_state is not "done".
	PartialSync bool
}

// Row is one list row. Item has titles, fields, genres, tags, cover and sync
// state (no relations, external ids or credits); Entry.Progress holds the
// active keys only. Title is resolved for the requested language.
type Row struct {
	Item  Item
	Entry Entry
	Title string
	Flags RowFlags
}

// Page is one page of rows. PageNo is the page actually returned (requests
// beyond the last page return the last page); Pages is at least 1.
type Page struct {
	Rows                 []Row
	Total, PageNo, Pages int
}

// TermCount is a genre or tag with the number of matching items.
type TermCount struct {
	Term  string
	Count int
}

const (
	defaultPageSize = 100
	maxPageSize     = 200
	maxFilters      = 20
	maxFilterRunes  = 200
	maxFilterValues = 50
	maxTermsListed  = 1000
)

var titleFallback = []string{"ja", "ja-latn", "en"}

// ResolveTitle returns the title in the preferred language. If it is missing it
// falls back to native (ja), romaji (ja-latn), English (en), then the first
// title. It returns "" only for an empty slice.
func ResolveTitle(ts []plugin.Title, preferred string) string {
	for _, lang := range append([]string{preferred}, titleFallback...) {
		for _, t := range ts {
			if t.Lang == lang {
				return t.Text
			}
		}
	}
	if len(ts) > 0 {
		return ts[0].Text
	}
	return ""
}

// clause is one WHERE condition with its bound arguments. Only constants and
// placeholders are ever concatenated into SQL; keys and values are arguments.
type clause struct {
	name string
	sql  string
	args []any
}

type compiled struct {
	pluginID, kind string
	def            plugin.KindDef
	man            plugin.Manifest
	clauses        []clause
	unmatched      clause
}

const fromSQL = ` FROM items JOIN ` + tableEntries + ` e ON e.item_id = items.id`

// where renders the conditions, skipping the named clauses.
func (c *compiled) where(skip ...string) (string, []any) {
	parts := []string{"items.plugin_id = ?", "items.kind = ?"}
	args := []any{c.pluginID, c.kind}
	for _, cl := range c.clauses {
		if slices.Contains(skip, cl.name) {
			continue
		}
		parts = append(parts, "("+cl.sql+")")
		args = append(args, cl.args...)
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// asciiLower lower-cases A-Z only, matching SQLite's lower().
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// containsPattern is the LIKE pattern for a case-insensitive substring match
// (use with ESCAPE '\' against lower(column)).
func containsPattern(s string) string {
	return "%" + likeEscaper.Replace(asciiLower(s)) + "%"
}

func (l *Library) compile(q Query) (*compiled, error) {
	def, err := l.kindDef(q.PluginID, q.Kind)
	if err != nil {
		return nil, err
	}
	man, _ := l.reg.Manifest(q.PluginID)
	c := &compiled{pluginID: q.PluginID, kind: q.Kind, def: def, man: man}
	add := func(name, sql string, args ...any) { c.clauses = append(c.clauses, clause{name, sql, args}) }

	if text := strings.TrimSpace(truncateRunes(q.Text, maxFilterRunes)); strings.ContainsRune(text, 0) {
		// a NUL would end the bound string early in SQLite; no stored title has one
		add("text", `0`)
	} else if text != "" {
		add("text", `EXISTS (SELECT 1 FROM item_titles t WHERE t.item_id = items.id AND lower(t.text) LIKE ? ESCAPE '\')`, containsPattern(text))
	}
	if q.Status != nil {
		if !validStatus(*q.Status) {
			return nil, invalid("status %q must be one of planned, active, paused, completed, dropped", *q.Status)
		}
		add("status", `e.status = ?`, string(*q.Status))
	}
	if len(q.Filters) > maxFilters {
		return nil, filterErr("", "more than %d filters", maxFilters)
	}
	for _, f := range q.Filters {
		cl, err := compileFilter(def, f)
		if err != nil {
			return nil, err
		}
		c.clauses = append(c.clauses, cl)
	}
	for _, t := range []struct {
		name, kind string
		vals       []string
		all        bool
		def        plugin.TermDef
	}{{"genres", "genre", q.Genres, q.GenresAll, def.Genres}, {"tags", "tag", q.Tags, q.TagsAll, def.Tags}} {
		cl, ok, err := compileTerms(t.name, t.kind, t.vals, t.all, t.def)
		if err != nil {
			return nil, err
		}
		if ok {
			c.clauses = append(c.clauses, cl)
		}
	}
	if err := c.compileRating(q); err != nil {
		return nil, err
	}
	c.unmatched = unmatchedClause(man)
	if q.Unmatched {
		c.clauses = append(c.clauses, clause{"unmatched", c.unmatched.sql, c.unmatched.args})
	}
	if q.PartialSync {
		add("partial", `items.sync_state <> 'done'`)
	}
	if err := c.compileProgress(q.Progress); err != nil {
		return nil, err
	}
	if _, _, err := c.orderBy(q.Sort, ""); err != nil { // reject a bad sort here, for Counts and Terms too
		return nil, err
	}
	return c, nil
}

func (c *compiled) compileRating(q Query) error {
	if q.RatingMin != nil && (*q.RatingMin < 0 || *q.RatingMin > 10) {
		return invalid("minimum rating %d must be between 0 and 10", *q.RatingMin)
	}
	if q.RatingMax != nil && (*q.RatingMax < 0 || *q.RatingMax > 10) {
		return invalid("maximum rating %d must be between 0 and 10", *q.RatingMax)
	}
	if q.RatingMin != nil && q.RatingMax != nil && *q.RatingMin > *q.RatingMax {
		return invalid("minimum rating %d is above the maximum %d", *q.RatingMin, *q.RatingMax)
	}
	var alts []string
	var args []any
	switch {
	case q.RatingMin != nil && q.RatingMax != nil:
		alts, args = append(alts, `e.rating BETWEEN ? AND ?`), append(args, *q.RatingMin, *q.RatingMax)
	case q.RatingMin != nil:
		alts, args = append(alts, `e.rating >= ?`), append(args, *q.RatingMin)
	case q.RatingMax != nil:
		alts, args = append(alts, `e.rating <= ?`), append(args, *q.RatingMax)
	}
	if q.Unrated {
		alts = append(alts, `e.rating IS NULL`)
	}
	if q.RatingZero {
		alts = append(alts, `e.rating = 0`)
	}
	if len(alts) > 0 {
		c.clauses = append(c.clauses, clause{"rating", strings.Join(alts, " OR "), args})
	}
	return nil
}

// unmatchedClause: a manual item with an external id of a type that an importer
// of its plugin declares.
func unmatchedClause(man plugin.Manifest) clause {
	var types []string
	for _, im := range man.Importers {
		if im.ExternalIDType != "" && !slices.Contains(types, im.ExternalIDType) {
			types = append(types, im.ExternalIDType)
		}
	}
	if len(types) == 0 {
		return clause{sql: `0`}
	}
	args := make([]any, len(types))
	for i, t := range types {
		args[i] = t
	}
	return clause{sql: `(items.source = 'manual' AND EXISTS (SELECT 1 FROM external_ids x WHERE x.item_id = items.id AND x.type IN (` + placeholders(len(types)) + `)))`, args: args}
}

func compileTerms(name, kind string, vals []string, all bool, td plugin.TermDef) (clause, bool, error) {
	var keys []string
	nul := false
	for _, v := range vals {
		k := strings.ToLower(strings.TrimSpace(truncateRunes(v, maxFilterRunes)))
		switch {
		case k == "":
		case strings.ContainsRune(k, 0):
			nul = true // a NUL would end the bound string early; no stored term has one
		case !slices.Contains(keys, k):
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 && !nul {
		return clause{}, false, nil
	}
	if !td.Enabled || !td.Filterable {
		return clause{}, false, filterErr(name, "the kind does not offer a %s filter", kind)
	}
	if len(keys) > maxFilterValues {
		return clause{}, false, filterErr(name, "more than %d %s values", maxFilterValues, kind)
	}
	if len(keys) == 0 || (nul && all) {
		return clause{name, `0`, nil}, true, nil
	}
	args := []any{kind}
	for _, k := range keys {
		args = append(args, k)
	}
	in := `x.item_id = items.id AND x.kind = ? AND x.term_key IN (` + placeholders(len(keys)) + `)`
	if all {
		return clause{name, `(SELECT COUNT(DISTINCT x.term_key) FROM item_terms x WHERE ` + in + `) = ` + strconv.Itoa(len(keys)), args}, true, nil
	}
	return clause{name, `EXISTS (SELECT 1 FROM item_terms x WHERE ` + in + `)`, args}, true, nil
}

func compileFilter(def plugin.KindDef, f Filter) (clause, error) {
	var fd *plugin.FieldDef
	for i := range def.Fields {
		if def.Fields[i].Key == f.Key {
			fd = &def.Fields[i]
		}
	}
	if fd == nil || !fd.Filterable {
		return clause{}, filterErr(f.Key, "no filterable field of this name in kind %q", def.Key)
	}
	if len(f.Values) == 0 || len(f.Values) > maxFilterValues {
		return clause{}, filterErr(f.Key, "needs between 1 and %d values", maxFilterValues)
	}
	for _, v := range f.Values {
		if v == "" {
			return clause{}, filterErr(f.Key, "empty value")
		}
		if strings.ContainsRune(v, 0) {
			return clause{}, filterErr(f.Key, "a value contains a NUL character")
		}
		if utf8.RuneCountInString(v) > maxFilterRunes {
			return clause{}, filterErr(f.Key, "a value is longer than %d characters", maxFilterRunes)
		}
	}
	bad := func() error { return filterErr(f.Key, "operator %q does not suit a %s field", f.Op, fd.Type) }
	single := func() (string, error) {
		if len(f.Values) != 1 {
			return "", filterErr(f.Key, "operator %q takes exactly one value", f.Op)
		}
		return f.Values[0], nil
	}
	var cond string
	args := []any{f.Key}
	switch fd.Type {
	case plugin.FieldEnum:
		for _, v := range f.Values {
			if !slices.Contains(fd.EnumValues, v) {
				return clause{}, filterErr(f.Key, "%q is not one of %s", v, strings.Join(fd.EnumValues, ", "))
			}
		}
		switch f.Op {
		case OpEq:
			v, err := single()
			if err != nil {
				return clause{}, err
			}
			cond, args = `f.text = ?`, append(args, v)
		case OpIn:
			cond = `f.text IN (` + placeholders(len(f.Values)) + `)`
			args = appendStrings(args, f.Values)
		default:
			return clause{}, bad()
		}
	case plugin.FieldList:
		if f.Op != OpIn {
			return clause{}, bad()
		}
		cond = `f.text IN (` + placeholders(len(f.Values)) + `)`
		args = appendStrings(args, f.Values)
	case plugin.FieldText, plugin.FieldURL:
		if f.Op != OpContains {
			return clause{}, bad()
		}
		v, err := single()
		if err != nil {
			return clause{}, err
		}
		cond, args = `lower(f.text) LIKE ? ESCAPE '\'`, append(args, containsPattern(v))
	case plugin.FieldNumber, plugin.FieldDate:
		col, parse := `f.num`, parseNumber
		if fd.Type == plugin.FieldDate {
			col, parse = `f.date`, parseDate
		}
		v, err := single()
		if err != nil {
			return clause{}, err
		}
		switch f.Op {
		case OpEq:
			x, err := parse(v)
			if err != nil {
				return clause{}, filterErr(f.Key, "%v", err)
			}
			cond, args = col+` = ?`, append(args, x)
		case OpRange:
			lo, hi, ok := strings.Cut(v, "..")
			if !ok || (lo == "" && hi == "") {
				return clause{}, filterErr(f.Key, "a range looks like a..b, a.. or ..b")
			}
			var conds []string
			var bounds []any
			for _, b := range []struct{ s, op string }{{lo, ">="}, {hi, "<="}} {
				if b.s == "" {
					continue
				}
				x, err := parse(b.s)
				if err != nil {
					return clause{}, filterErr(f.Key, "%v", err)
				}
				conds, bounds = append(conds, col+" "+b.op+" ?"), append(bounds, x)
			}
			if len(bounds) == 2 && greater(bounds[0], bounds[1]) {
				return clause{}, filterErr(f.Key, "the range start is above its end")
			}
			cond, args = strings.Join(conds, " AND "), append(args, bounds...)
		default:
			return clause{}, bad()
		}
	default:
		return clause{}, filterErr(f.Key, "field type %q cannot be filtered", fd.Type)
	}
	return clause{"filter", `EXISTS (SELECT 1 FROM item_fields f WHERE f.item_id = items.id AND f.key = ? AND ` + cond + `)`, args}, nil
}

func appendStrings(args []any, vals []string) []any {
	for _, v := range vals {
		args = append(args, v)
	}
	return args
}

func greater(a, b any) bool {
	switch x := a.(type) {
	case float64:
		return x > b.(float64)
	case string:
		return x > b.(string)
	}
	return false
}

func parseNumber(s string) (any, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf("%q is not a number", s)
	}
	return f, nil
}

func parseDate(s string) (any, error) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("%q is not a date (YYYY-MM-DD)", s)
	}
	return t.Format(dateLayout), nil
}

// compileProgress adds the progress-state condition. For every eligible key
// of the kind it builds, with the key as a bound argument, "is active for the
// item", the stored value (absent = 0) and the total (percent 100, a total
// field's numeric value, else none), exactly mirroring Finished and NotStarted.
func (c *compiled) compileProgress(st ProgressState) error {
	switch st {
	case "", ProgressAny:
		return nil
	case ProgressNotStarted, ProgressInProgress, ProgressFinished:
	default:
		return filterErr("progress", "unknown progress state %q", st)
	}
	elig := c.def.Progress.Eligible
	notStarted := func() clause {
		var parts []string
		var args []any
		for _, d := range elig {
			parts = append(parts, `NOT (`+activeSQL+` AND `+valueSQL+` <> 0)`)
			args = append(args, d.Key, d.Key)
		}
		if len(parts) == 0 {
			return clause{sql: `1`}
		}
		return clause{sql: strings.Join(parts, " AND "), args: args}
	}
	finished := func() clause {
		var anyB, allB []string
		var anyArgs, allArgs []any
		for _, d := range elig {
			total, bounded, targs := totalSQL(d)
			anyB = append(anyB, `(`+activeSQL+` AND `+bounded+`)`)
			anyArgs = append(anyArgs, d.Key)
			anyArgs = append(anyArgs, targs...)
			allB = append(allB, `(NOT (`+activeSQL+` AND `+bounded+`) OR `+valueSQL+` >= `+total+`)`)
			allArgs = append(allArgs, d.Key)
			allArgs = append(allArgs, targs...)
			allArgs = append(allArgs, d.Key)
			allArgs = append(allArgs, targs...)
		}
		if len(elig) == 0 {
			return clause{sql: `0`}
		}
		return clause{sql: `(` + strings.Join(anyB, " OR ") + `) AND ` + strings.Join(allB, " AND "), args: append(anyArgs, allArgs...)}
	}
	var cl clause
	switch st {
	case ProgressNotStarted:
		cl = notStarted()
	case ProgressFinished:
		cl = finished()
	case ProgressInProgress:
		n, f := notStarted(), finished()
		cl = clause{sql: `NOT (` + n.sql + `) AND NOT (` + f.sql + `)`, args: append(n.args, f.args...)}
	}
	cl.name = "progress"
	c.clauses = append(c.clauses, cl)
	return nil
}

const (
	activeSQL = `EXISTS (SELECT 1 FROM json_each(items.progress_keys) j WHERE j.value = ?)`
	valueSQL  = `COALESCE((SELECT p.value FROM ` + tableEntryProgress + ` p WHERE p.item_id = items.id AND p.key = ?), 0)`
)

// totalSQL returns the SQL of a track's total and of "is bounded" and the
// arguments each of the two needs (the same list for either).
func totalSQL(d plugin.ProgressDef) (total, bounded string, args []any) {
	switch {
	case d.Type == plugin.Percent:
		return `100`, `1`, nil
	case d.TotalField != "":
		t := `(SELECT f.num FROM item_fields f WHERE f.item_id = items.id AND f.key = ? AND f.num IS NOT NULL ORDER BY f.seq LIMIT 1)`
		return t, t + ` IS NOT NULL`, []any{d.TotalField}
	}
	return `NULL`, `0`, nil
}

func (c *compiled) orderBy(s Sort, lang string) (string, []any, error) {
	dir := "ASC"
	if s.Desc {
		dir = "DESC"
	}
	var expr string
	var args []any
	nullsLast := false
	switch s.Kind {
	case "":
		return " ORDER BY e.updated_at DESC, items.id DESC", nil, nil
	case SortUpdated:
		expr = `e.updated_at`
	case SortTitle:
		expr = `lower(COALESCE((SELECT t.text FROM item_titles t WHERE t.item_id = items.id AND t.lang = ? ORDER BY t.rowid LIMIT 1),
			(SELECT t.text FROM item_titles t WHERE t.item_id = items.id ORDER BY t.rowid LIMIT 1), ''))`
		args = []any{lang}
	case SortRating:
		expr, nullsLast = `e.rating`, true
	case SortProgress:
		key := c.def.Progress.PrimaryKey
		if key == "" {
			return "", nil, filterErr("progress", "the kind has no primary progress to sort by")
		}
		expr = `CASE WHEN ` + activeSQL + ` THEN (SELECT p.value FROM ` + tableEntryProgress + ` p WHERE p.item_id = items.id AND p.key = ?) END`
		args, nullsLast = []any{key, key}, true
	case SortField:
		var fd *plugin.FieldDef
		for i := range c.def.Fields {
			if c.def.Fields[i].Key == s.Field {
				fd = &c.def.Fields[i]
			}
		}
		if fd == nil || !fd.Sortable {
			return "", nil, filterErr(s.Field, "no sortable field of this name in kind %q", c.kind)
		}
		col := ""
		switch fd.Type {
		case plugin.FieldNumber:
			col = `f.num`
		case plugin.FieldDate:
			col = `f.date`
		case plugin.FieldText, plugin.FieldEnum, plugin.FieldURL:
			col = `lower(f.text)`
		default:
			return "", nil, filterErr(s.Field, "a %s field cannot be sorted", fd.Type)
		}
		expr = `(SELECT ` + col + ` FROM item_fields f WHERE f.item_id = items.id AND f.key = ? ORDER BY f.seq LIMIT 1)`
		args, nullsLast = []any{s.Field}, true
	default:
		return "", nil, filterErr(string(s.Kind), "unknown sort")
	}
	if nullsLast {
		// the expression appears twice, so its arguments do too
		return ` ORDER BY (` + expr + `) IS NULL, ` + expr + ` ` + dir + `, items.id DESC`, append(slices.Clone(args), args...), nil
	}
	return ` ORDER BY ` + expr + ` ` + dir + `, items.id DESC`, args, nil
}

// List returns one page of the user's entries matching q. Titles are
// resolved for lang. Rows are loaded with a fixed number of queries per page.
// Page size defaults to 100 and is capped at 200; a page beyond the last
// returns the last page, a page below 1 the first.
func (l *Library) List(ctx context.Context, q Query, lang string) (Page, error) {
	c, err := l.compile(q)
	if err != nil {
		return Page{}, err
	}
	whereSQL, whereArgs := c.where()
	order, orderArgs, err := c.orderBy(q.Sort, lang)
	if err != nil {
		return Page{}, err
	}
	var total int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*)`+fromSQL+whereSQL, whereArgs...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("list: %w", err)
	}
	size := q.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)
	pages := max(1, (total+size-1)/size)
	pageNo := min(max(q.Page, 1), pages)
	pg := Page{Total: total, PageNo: pageNo, Pages: pages}
	if total == 0 {
		return pg, nil
	}
	args := append(slices.Clone(c.unmatched.args), whereArgs...)
	args = append(args, orderArgs...)
	args = append(args, size, (pageNo-1)*size)
	rows, err := l.db.QueryContext(ctx, `SELECT items.id, items.plugin_id, items.kind, items.source, items.source_id, items.cover_path,
		items.external_url, items.sync_state, items.progress_keys, e.status, e.rating, e.comment, e.started_on, e.finished_on, e.updated_at,
		(`+c.unmatched.sql+`), (items.sync_state <> 'done')`+fromSQL+whereSQL+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list: %w", err)
	}
	byID := map[int64]*Row{}
	var ids []any
	for rows.Next() {
		var r Row
		var pk, status, updated string
		var rating sql.NullInt64
		var started, finished sql.NullString
		if err := rows.Scan(&r.Item.ID, &r.Item.PluginID, &r.Item.Kind, &r.Item.Source, &r.Item.SourceID, &r.Item.CoverPath,
			&r.Item.ExternalURL, &r.Item.SyncState, &pk, &status, &rating, &r.Entry.Comment, &started, &finished, &updated,
			&r.Flags.Unmatched, &r.Flags.PartialSync); err != nil {
			rows.Close()
			return Page{}, fmt.Errorf("list: %w", err)
		}
		if err := decodeKeys(pk, &r.Item.ProgressKeys); err != nil {
			rows.Close()
			return Page{}, err
		}
		r.Entry.ItemID = r.Item.ID
		r.Entry.Progress = map[string]float64{}
		if err := r.Entry.fill(status, rating, started, finished, updated); err != nil {
			rows.Close()
			return Page{}, err
		}
		pg.Rows = append(pg.Rows, r)
		ids = append(ids, r.Item.ID)
	}
	if err := closeRows(rows); err != nil {
		return Page{}, fmt.Errorf("list: %w", err)
	}
	for i := range pg.Rows {
		byID[pg.Rows[i].Item.ID] = &pg.Rows[i]
	}
	if err := l.loadRowParts(ctx, ids, byID); err != nil {
		return Page{}, err
	}
	for i := range pg.Rows {
		pg.Rows[i].Title = ResolveTitle(pg.Rows[i].Item.Titles, lang)
	}
	return pg, nil
}

// loadRowParts loads titles, fields, terms and progress of the listed items
// with one query each.
func (l *Library) loadRowParts(ctx context.Context, ids []any, byID map[int64]*Row) error {
	in := ` WHERE item_id IN (` + placeholders(len(ids)) + `)`
	query := func(what, q string, each func(*sql.Rows) error) error {
		rows, err := l.db.QueryContext(ctx, q, ids...)
		if err != nil {
			return fmt.Errorf("load %s: %w", what, err)
		}
		for rows.Next() {
			if err := each(rows); err != nil {
				rows.Close()
				return fmt.Errorf("load %s: %w", what, err)
			}
		}
		if err := closeRows(rows); err != nil {
			return fmt.Errorf("load %s: %w", what, err)
		}
		return nil
	}
	if err := query("titles", `SELECT item_id, lang, text FROM item_titles`+in+` ORDER BY rowid`, func(r *sql.Rows) error {
		var id int64
		var t plugin.Title
		if err := r.Scan(&id, &t.Lang, &t.Text); err != nil {
			return err
		}
		byID[id].Item.Titles = append(byID[id].Item.Titles, t)
		return nil
	}); err != nil {
		return err
	}
	accs := map[int64]*fieldAcc{}
	if err := query("fields", `SELECT item_id, key, vtype, text, num, date FROM item_fields`+in+` ORDER BY item_id, key, seq`, func(r *sql.Rows) error {
		var id int64
		var key, vtype string
		var text, date sql.NullString
		var num sql.NullFloat64
		if err := r.Scan(&id, &key, &vtype, &text, &num, &date); err != nil {
			return err
		}
		if accs[id] == nil {
			accs[id] = newFieldAcc()
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
		return accs[id].add(key, vtype, tp, np, dp)
	}); err != nil {
		return err
	}
	for id, a := range accs {
		byID[id].Item.Fields = a.vals
	}
	if err := query("terms", `SELECT item_id, kind, term, rank, spoiler FROM item_terms`+in+` ORDER BY item_id, kind, seq`, func(r *sql.Rows) error {
		var id int64
		var kind, term string
		var rank sql.NullInt64
		var spoiler int
		if err := r.Scan(&id, &kind, &term, &rank, &spoiler); err != nil {
			return err
		}
		it := &byID[id].Item
		if kind == "genre" {
			it.Genres = append(it.Genres, term)
			return nil
		}
		t := plugin.Tag{Name: term, Spoiler: spoiler != 0}
		if rank.Valid {
			rk := int(rank.Int64)
			t.Rank = &rk
		}
		it.Tags = append(it.Tags, t)
		return nil
	}); err != nil {
		return err
	}
	return query("progress", `SELECT item_id, key, value FROM `+tableEntryProgress+in, func(r *sql.Rows) error {
		var id int64
		var k string
		var v float64
		if err := r.Scan(&id, &k, &v); err != nil {
			return err
		}
		if row := byID[id]; slices.Contains(row.Item.ProgressKeys, k) {
			row.Entry.Progress[k] = v
		}
		return nil
	})
}

// Counts returns the number of matching entries per status (all five
// statuses are present). The query's Status filter is ignored; every other
// filter applies.
func (l *Library) Counts(ctx context.Context, q Query) (map[Status]int, error) {
	c, err := l.compile(q)
	if err != nil {
		return nil, err
	}
	whereSQL, args := c.where("status")
	rows, err := l.db.QueryContext(ctx, `SELECT e.status, COUNT(*)`+fromSQL+whereSQL+` GROUP BY e.status`, args...)
	if err != nil {
		return nil, fmt.Errorf("counts: %w", err)
	}
	out := map[Status]int{}
	for _, s := range Statuses() {
		out[s] = 0
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("counts: %w", err)
		}
		out[Status(s)] = n
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("counts: %w", err)
	}
	return out, nil
}

// Terms returns the genres (kind "genre") or tags (kind "tag") of the
// matching entries with their item counts, for a filter bar: the items are
// those matching q WITHOUT that term type's own filter (genre counts ignore
// the genre filter, tag counts the tag filter). Ordered by count descending,
// then by the lower-cased term; the display spelling is the first stored
// spelling in binary order (MIN). Spoiler tags count like any other. A kind
// that does not enable the term type yields no rows.
func (l *Library) Terms(ctx context.Context, q Query, kind string) ([]TermCount, error) {
	var skip string
	switch kind {
	case "genre":
		skip = "genres"
	case "tag":
		skip = "tags"
	default:
		return nil, invalid("term kind %q must be genre or tag", kind)
	}
	c, err := l.compile(q)
	if err != nil {
		return nil, err
	}
	if (kind == "genre" && !c.def.Genres.Enabled) || (kind == "tag" && !c.def.Tags.Enabled) {
		return nil, nil
	}
	whereSQL, whereArgs := c.where(skip)
	args := append([]any{kind}, whereArgs...)
	args = append(args, maxTermsListed)
	rows, err := l.db.QueryContext(ctx, `SELECT MIN(t.term), COUNT(DISTINCT items.id) AS n`+fromSQL+
		` JOIN item_terms t ON t.item_id = items.id AND t.kind = ?`+whereSQL+
		` GROUP BY t.term_key ORDER BY n DESC, t.term_key LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("terms: %w", err)
	}
	var out []TermCount
	for rows.Next() {
		var tc TermCount
		if err := rows.Scan(&tc.Term, &tc.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("terms: %w", err)
		}
		out = append(out, tc)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("terms: %w", err)
	}
	return out, nil
}
