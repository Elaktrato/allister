package web

// Search and filters of the list page, generated from the kind's schema.
//
// URL parameters: q (title text in any language), f.<field> for every
// Filterable field (enum: one or more values; number or date: "a..b", "a..",
// "..b" or one value, with f.<field>.from / f.<field>.to as the form's
// aliases; text or url: a substring), genre and tag (repeatable, with
// genre_mode / tag_mode any|all, only when the kind enables them as
// filterable), rmin / rmax (scores 0..10), unrated=1, zero=1, unmatched=1,
// partial=1, sort (updated, title, rating, progress or a Sortable field key)
// with dir asc|desc. kind, status and page belong to parseListQuery.
//
// Every parameter is checked against the schema here and the valid ones are
// handed to the library as typed values (bound there as SQL parameters).
// Unknown keys and malformed values are dropped and named in a notice. Empty
// values are absent (a plain GET form sends every input).

import (
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

const (
	maxFilterRunes   = 200 // q and every other value are cut to this many runes
	maxTermValues    = 50  // genre or tag values per request
	maxNoticeKeys    = 5   // rejected keys named in the notice
	maxNoticeKeyRune = 40  // a rejected key is shown cut to this length
)

// listParamKeys are the keys parseListQuery reads itself.
var listParamKeys = []string{"kind", "status", "page"}

// fieldFilter is one valid field filter in canonical form: Values for an
// enum (one or more), else one value ("a..b" for a range).
type fieldFilter struct {
	Key    string
	Values []string
}

// listFilters is the valid filter and sort state of a list request.
type listFilters struct {
	Q                  string
	Fields             []fieldFilter // in schema order
	Genres, Tags       []string
	GenresAll, TagsAll bool
	RatingMin          *int
	RatingMax          *int
	Unrated, Zero      bool
	Unmatched, Partial bool
	Sort, Dir          string // "" = the default (recently updated, newest first)
}

// field returns the filter of a field key.
func (f listFilters) field(key string) (fieldFilter, bool) {
	for _, ff := range f.Fields {
		if ff.Key == key {
			return ff, true
		}
	}
	return fieldFilter{}, false
}

// values encodes the state canonically (defaults omitted).
func (f listFilters) values() url.Values {
	v := url.Values{}
	if f.Q != "" {
		v.Set("q", f.Q)
	}
	for _, ff := range f.Fields {
		v["f."+ff.Key] = slices.Clone(ff.Values)
	}
	for _, t := range []struct {
		key  string
		vals []string
		all  bool
	}{{"genre", f.Genres, f.GenresAll}, {"tag", f.Tags, f.TagsAll}} {
		if len(t.vals) > 0 {
			v[t.key] = slices.Clone(t.vals)
			if t.all {
				v.Set(t.key+"_mode", "all")
			}
		}
	}
	if f.RatingMin != nil {
		v.Set("rmin", strconv.Itoa(*f.RatingMin))
	}
	if f.RatingMax != nil {
		v.Set("rmax", strconv.Itoa(*f.RatingMax))
	}
	for _, b := range []struct {
		key string
		on  bool
	}{{"unrated", f.Unrated}, {"zero", f.Zero}, {"unmatched", f.Unmatched}, {"partial", f.Partial}} {
		if b.on {
			v.Set(b.key, "1")
		}
	}
	if f.Sort != "" {
		v.Set("sort", f.Sort)
	}
	if f.Dir != "" {
		v.Set("dir", f.Dir)
	}
	return v
}

// active reports whether anything is set (Clear filters is offered).
func (f listFilters) active() bool { return len(f.values()) > 0 }

// narrows reports whether a filter (not just the sort) restricts the rows.
func (f listFilters) narrows() bool {
	g := f
	g.Sort, g.Dir = "", ""
	return g.active()
}

// apply copies the state into a library query.
func (f listFilters) apply(k plugin.KindDef, q *library.Query) {
	q.Text = f.Q
	q.Filters = nil
	for _, ff := range f.Fields {
		fd, _ := fieldDef(k, ff.Key)
		q.Filters = append(q.Filters, library.Filter{Key: ff.Key, Op: filterOp(fd, ff.Values), Values: ff.Values})
	}
	q.Genres, q.Tags, q.GenresAll, q.TagsAll = f.Genres, f.Tags, f.GenresAll, f.TagsAll
	q.RatingMin, q.RatingMax, q.Unrated, q.RatingZero = f.RatingMin, f.RatingMax, f.Unrated, f.Zero
	q.Unmatched, q.PartialSync = f.Unmatched, f.Partial
	q.Sort = f.sort()
}

// sort is the library sort of the state.
func (f listFilters) sort() library.Sort {
	if f.Sort == "" {
		return library.Sort{}
	}
	s := library.Sort{Desc: f.Dir != "asc" && (f.Dir == "desc" || f.Sort != string(library.SortTitle))}
	switch f.Sort {
	case string(library.SortUpdated), string(library.SortTitle), string(library.SortRating), string(library.SortProgress):
		s.Kind = library.SortKind(f.Sort)
	default:
		s.Kind, s.Field = library.SortField, f.Sort
	}
	return s
}

// filterOp is the operator a field filter uses: enum and list fields match
// one of the values, number and date fields a value or a range, text and url
// fields a substring.
func filterOp(fd plugin.FieldDef, vals []string) library.FilterOp {
	switch fd.Type {
	case plugin.FieldEnum:
		if len(vals) == 1 {
			return library.OpEq
		}
		return library.OpIn
	case plugin.FieldList:
		return library.OpIn
	case plugin.FieldNumber, plugin.FieldDate:
		if strings.Contains(vals[0], "..") {
			return library.OpRange
		}
		return library.OpEq
	}
	return library.OpContains
}

// filterParams collects the non-empty values of a request, trimmed and cut
// to maxFilterRunes runes.
func filterParams(q url.Values) map[string][]string {
	out := map[string][]string{}
	for key, vals := range q {
		for _, v := range vals {
			if v = strings.TrimSpace(cutRunes(v, maxFilterRunes)); v != "" {
				out[key] = append(out[key], v)
			}
		}
	}
	return out
}

// cutRunes cuts s to at most n runes (no ellipsis: the value is used).
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// parseFilters reads the filter and sort parameters of a request for the
// kind k. It returns the valid state and the sorted list of
// rejected keys (unknown keys, or keys with malformed values).
func parseFilters(k plugin.KindDef, q url.Values) (listFilters, []string) {
	var f listFilters
	params := filterParams(q)
	rejected := map[string]bool{}
	reject := func(key string) { rejected[key] = true }
	seen := map[string]bool{}
	for _, key := range listParamKeys {
		seen[key] = true
	}
	take := func(key string) []string { seen[key] = true; return params[key] }

	if qs := take("q"); len(qs) > 0 {
		f.Q = qs[0]
	}
	for _, fd := range k.Fields {
		if !fd.Filterable {
			continue
		}
		key := "f." + fd.Key
		vals := take(key)
		ranged := fd.Type == plugin.FieldNumber || fd.Type == plugin.FieldDate
		if ranged {
			from, to := take(key+".from"), take(key+".to")
			if len(vals) > 0 { // the canonical value wins; the aliases are named
				if len(from) > 0 {
					reject(key + ".from")
				}
				if len(to) > 0 {
					reject(key + ".to")
				}
			} else if len(from) > 0 || len(to) > 0 {
				if len(from) > 1 || len(to) > 1 {
					reject(key)
					continue
				}
				vals = []string{strings.Join(from, "") + ".." + strings.Join(to, "")}
			}
		}
		if len(vals) == 0 {
			continue
		}
		if len(vals) > 1 && fd.Type != plugin.FieldEnum && fd.Type != plugin.FieldList {
			reject(key)
			continue
		}
		ff := fieldFilter{Key: fd.Key, Values: vals}
		if library.CheckFilter(k, library.Filter{Key: fd.Key, Op: filterOp(fd, vals), Values: vals}) != nil {
			reject(key)
			continue
		}
		f.Fields = append(f.Fields, ff)
	}
	for _, t := range []struct {
		key  string
		def  plugin.TermDef
		vals *[]string
		all  *bool
	}{{"genre", k.Genres, &f.Genres, &f.GenresAll}, {"tag", k.Tags, &f.Tags, &f.TagsAll}} {
		vals, mode := take(t.key), take(t.key+"_mode")
		switch {
		case len(mode) == 0, len(mode) == 1 && mode[0] == "any":
		case len(mode) == 1 && mode[0] == "all":
			*t.all = true
		default:
			reject(t.key + "_mode")
		}
		if len(vals) == 0 {
			*t.all = false
			continue
		}
		if !t.def.Enabled || !t.def.Filterable {
			reject(t.key)
			*t.all = false
			continue
		}
		// distinct values (case-insensitive), at most maxTermValues: the scan
		// stops at the first value beyond the cap, so the work stays bounded
		var keep []string
		for _, v := range vals {
			if slices.ContainsFunc(keep, func(x string) bool { return strings.EqualFold(x, v) }) {
				continue
			}
			if len(keep) == maxTermValues {
				reject(t.key)
				break
			}
			keep = append(keep, v)
		}
		*t.vals = keep
	}
	f.RatingMin = parseScore(take("rmin"), "rmin", reject)
	f.RatingMax = parseScore(take("rmax"), "rmax", reject)
	if f.RatingMin != nil && f.RatingMax != nil && *f.RatingMin > *f.RatingMax {
		f.RatingMin, f.RatingMax = nil, nil
		reject("rmin")
		reject("rmax")
	}
	for _, b := range []struct {
		key string
		on  *bool
	}{{"unrated", &f.Unrated}, {"zero", &f.Zero}, {"unmatched", &f.Unmatched}, {"partial", &f.Partial}} {
		switch vals := take(b.key); {
		case len(vals) == 0:
		case len(vals) == 1 && vals[0] == "1":
			*b.on = true
		default:
			reject(b.key)
		}
	}
	f.Sort, f.Dir = parseSort(k, take("sort"), take("dir"), reject)
	for key := range q {
		if !seen[key] {
			reject(key)
		}
	}
	keys := make([]string, 0, len(rejected))
	for key := range rejected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return f, keys
}

// parseScore reads one 0..10 score parameter.
func parseScore(vals []string, key string, reject func(string)) *int {
	if len(vals) == 0 {
		return nil
	}
	n, err := strconv.Atoi(vals[0])
	if len(vals) > 1 || err != nil || n < 0 || n > 10 {
		reject(key)
		return nil
	}
	return &n
}

// parseSort reads sort and dir. The default (recently updated, newest first)
// is "", "": so are sort=updated with dir desc or none.
func parseSort(k plugin.KindDef, sorts, dirs []string, reject func(string)) (string, string) {
	dir := ""
	switch {
	case len(dirs) == 0:
	case len(dirs) == 1 && (dirs[0] == "asc" || dirs[0] == "desc"):
		dir = dirs[0]
	default:
		reject("dir")
	}
	if len(sorts) == 0 {
		if dir == "asc" {
			return string(library.SortUpdated), dir
		}
		return "", ""
	}
	s := listFilters{Sort: sorts[0], Dir: dir}
	if len(sorts) > 1 || library.CheckSort(k, s.sort()) != nil {
		reject("sort")
		return "", ""
	}
	if s.Sort == string(library.SortUpdated) && dir != "asc" {
		return "", ""
	}
	if dir == defaultDir(s.Sort) {
		dir = ""
	}
	return s.Sort, dir
}

// defaultDir is the direction a sort uses without dir: titles A to Z,
// everything else highest (newest) first.
func defaultDir(sort string) string {
	if sort == string(library.SortTitle) {
		return "asc"
	}
	return "desc"
}

// noticeKeys returns the rejected keys shown in the notice (cut to a readable
// length) and how many more there are.
func noticeKeys(rejected []string) ([]string, int) {
	var out []string
	for _, key := range rejected {
		if len(out) == maxNoticeKeys {
			break
		}
		out = append(out, truncateRunes(key, maxNoticeKeyRune))
	}
	return out, len(rejected) - len(out)
}

// --- the filter bar ---

type option struct {
	Value, Label string
	Selected     bool
}

// filterField is the input of one Filterable field: Type "enum" (a select),
// "number" or "date" (from/to inputs) or "text".
type filterField struct {
	Label, Type      string
	Name             string // enum and text
	FromName, ToName string // number and date
	Value, From, To  string
	Options          []option
}

// termInput is the multi-select of genres or tags with the any/all switch.
type termInput struct {
	Label, Name, ModeName string
	All                   bool
	Options               []option
}

type filterBar struct {
	Action        string
	Kind, Status  string // hidden inputs (Kind only for plugins with several kinds)
	Q             string
	Fields        []filterField
	Genres, Tags  *termInput
	RatingMin     string
	RatingMax     string
	Unrated, Zero bool
	Unmatched     bool
	Partial       bool
	ShowUnmatched bool
	Sorts, Dirs   []option
	ClearURL      string // "" when nothing is set
	OOB           bool   // the Clear slot is rendered as an out-of-band swap
}

// outOfBand marks the Clear slot for an out-of-band swap. (The genre/tag
// option lists always go out of band in a rows partial: list_partial.)
func (b *filterBar) outOfBand() { b.OOB = true }

// buildFilterBar draws the bar of kind k from the schema: only Filterable
// fields, genres and tags only when enabled and filterable (with the counts
// in genres/tags; a selected term without a count shows 0).
func buildFilterBar(m plugin.Manifest, k plugin.KindDef, lq listQuery, genres, tags []library.TermCount) filterBar {
	f := lq.Filters
	b := filterBar{Action: "/" + url.PathEscape(m.ID), Status: lq.Status, Q: f.Q,
		Unrated: f.Unrated, Zero: f.Zero, Unmatched: f.Unmatched, Partial: f.Partial}
	if len(m.Kinds) > 1 {
		b.Kind = k.Key
	}
	if f.active() {
		b.ClearURL = listURL(m, k.Key, lq.Status, 1)
	}
	for _, im := range m.Importers {
		b.ShowUnmatched = b.ShowUnmatched || im.ExternalIDType != ""
	}
	if f.RatingMin != nil {
		b.RatingMin = strconv.Itoa(*f.RatingMin)
	}
	if f.RatingMax != nil {
		b.RatingMax = strconv.Itoa(*f.RatingMax)
	}
	for _, fd := range k.Fields {
		if !fd.Filterable {
			continue
		}
		cur, _ := f.field(fd.Key)
		in := filterField{Label: fd.Label, Name: "f." + fd.Key}
		switch fd.Type {
		case plugin.FieldEnum:
			in.Type = "enum"
			for _, v := range fd.EnumValues {
				in.Options = append(in.Options, option{Value: v, Label: v, Selected: slices.Contains(cur.Values, v)})
			}
		case plugin.FieldNumber, plugin.FieldDate:
			in.Type = string(fd.Type)
			in.FromName, in.ToName = in.Name+".from", in.Name+".to"
			if len(cur.Values) == 1 {
				lo, hi, ok := strings.Cut(cur.Values[0], "..")
				if !ok {
					hi = lo
				}
				in.From, in.To = lo, hi
			}
		default:
			in.Type = "text"
			in.Value = strings.Join(cur.Values, " ")
		}
		b.Fields = append(b.Fields, in)
	}
	if k.Genres.Enabled && k.Genres.Filterable {
		b.Genres = termBar(k.Genres, "genre", f.Genres, f.GenresAll, genres)
	}
	if k.Tags.Enabled && k.Tags.Filterable {
		b.Tags = termBar(k.Tags, "tag", f.Tags, f.TagsAll, tags)
	}
	b.Sorts = []option{{Value: "updated", Label: "Recently updated"}, {Value: "title", Label: "Title"}, {Value: "rating", Label: "Score"}}
	if k.Progress.PrimaryKey != "" {
		b.Sorts = append(b.Sorts, option{Value: "progress", Label: "Progress"})
	}
	for _, fd := range k.Fields {
		if fd.Sortable && library.CheckSort(k, library.Sort{Kind: library.SortField, Field: fd.Key}) == nil {
			b.Sorts = append(b.Sorts, option{Value: fd.Key, Label: fd.Label})
		}
	}
	cur := f.Sort
	if cur == "" {
		cur = "updated"
	}
	for i := range b.Sorts {
		b.Sorts[i].Selected = b.Sorts[i].Value == cur
	}
	b.Dirs = []option{{Value: "", Label: "Default order", Selected: f.Dir == ""},
		{Value: "asc", Label: "Ascending", Selected: f.Dir == "asc"}, {Value: "desc", Label: "Descending", Selected: f.Dir == "desc"}}
	return b
}

func termBar(def plugin.TermDef, name string, selected []string, all bool, counts []library.TermCount) *termInput {
	t := &termInput{Label: def.Label, Name: name, ModeName: name + "_mode", All: all}
	isSel := func(term string) bool {
		return slices.ContainsFunc(selected, func(s string) bool { return strings.EqualFold(s, term) })
	}
	for _, c := range counts {
		t.Options = append(t.Options, option{Value: c.Term, Label: c.Term + " (" + strconv.Itoa(c.Count) + ")", Selected: isSel(c.Term)})
	}
	for _, s := range selected {
		if !slices.ContainsFunc(counts, func(c library.TermCount) bool { return strings.EqualFold(c.Term, s) }) {
			t.Options = append(t.Options, option{Value: s, Label: s + " (0)", Selected: true})
		}
	}
	return t
}
