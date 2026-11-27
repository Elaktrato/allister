package web

// The backlog randomiser: GET /{plugin}/pick draws one entry at random from
// what the list's own query matches (a request without a status means
// planned), narrowed by the length filter (len, len_min, len_max), which only
// the pick applies. htmx gets the result panel alone; a plain request gets the
// list page with the panel filled, so a pick URL is a working link.
// POST /list/{id}/start (the pick's Start button) moves a planned entry to
// active.

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// defaultPickCap bounds the entries loaded for one draw (server.pickCap, which
// tests lower).
const defaultPickCap = 20000

// lengthParams are the URL parameters of the length filter.
var lengthParams = []string{"len", "len_min", "len_max"}

// lengthFilter is a valid length filter: Measure (zero value: none) and the
// bounds, in minutes or units by the measure.
type lengthFilter struct {
	Measure  plugin.LengthDef
	Min, Max *float64
}

// active reports whether the filter excludes anything.
func (f lengthFilter) active() bool {
	return f.Measure.Key != "" && (f.Min != nil || f.Max != nil)
}

// values are the filter's URL parameters (none without a measure).
func (f lengthFilter) values() url.Values {
	q := url.Values{}
	if f.Measure.Key == "" {
		return q
	}
	q.Set("len", f.Measure.Key)
	if f.Min != nil {
		q.Set("len_min", fmtNum(*f.Min))
	}
	if f.Max != nil {
		q.Set("len_max", fmtNum(*f.Max))
	}
	return q
}

// withoutLengthParams is q without the length parameters (parseFilters would
// reject them as unknown).
func withoutLengthParams(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		if !slices.Contains(lengthParams, k) {
			out[k] = v
		}
	}
	return out
}

// parseLengthFilter reads len, len_min and len_max for kind k. Whatever is
// unknown or invalid (no such measure on the kind, a bound that is no
// non-negative number, min above max, a repeated key) is left out of the
// filter and named in the rejected keys; empty values count as absent.
func parseLengthFilter(k plugin.KindDef, q url.Values) (lengthFilter, []string) {
	params := filterParams(q)
	var rejected []string
	rejectBounds := func() {
		for _, key := range lengthParams[1:] {
			if len(params[key]) > 0 {
				rejected = append(rejected, key)
			}
		}
	}
	vals := params["len"]
	if len(vals) == 0 {
		rejectBounds()
		return lengthFilter{}, rejected
	}
	i := slices.IndexFunc(k.Length, func(d plugin.LengthDef) bool { return d.Key == vals[0] })
	if len(vals) > 1 || i < 0 {
		rejected = append(rejected, "len")
		rejectBounds()
		return lengthFilter{}, rejected
	}
	f := lengthFilter{Measure: k.Length[i]}
	bound := func(key string) *float64 {
		bv := params[key]
		if len(bv) == 0 {
			return nil
		}
		n, err := strconv.ParseFloat(bv[0], 64)
		if len(bv) > 1 || err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			rejected = append(rejected, key)
			return nil
		}
		return &n
	}
	f.Min, f.Max = bound("len_min"), bound("len_max")
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		f.Min, f.Max = nil, nil
		rejected = append(rejected, "len_min", "len_max")
	}
	return f, rejected
}

// numberFields are the numeric values of an item's fields.
func numberFields(fields map[string]plugin.FieldValue) map[string]float64 {
	out := make(map[string]float64, len(fields))
	for k, v := range fields {
		if v.Num != nil {
			out[k] = *v.Num
		}
	}
	return out
}

// entryLength is the length of an item by measure m, in minutes or units. It
// reports false when the data is missing or 0 (a field of 0 is unset).
func entryLength(m plugin.LengthDef, fields map[string]float64) (float64, bool) {
	switch {
	case m.Field != "":
		v := fields[m.Field]
		return v, v > 0
	case m.UnitsField != "" && m.PerUnitField != "":
		u, p := fields[m.UnitsField], fields[m.PerUnitField]
		return u * p, u > 0 && p > 0
	case m.UnitsField != "":
		v := fields[m.UnitsField]
		return v, v > 0
	}
	return 0, false
}

// lengthUnit is the label of a measure's bound inputs: "min", or the unit of
// the units field.
func lengthUnit(k plugin.KindDef, m plugin.LengthDef) string {
	if m.InMinutes() {
		return "min"
	}
	if fd, ok := fieldDef(k, m.UnitsField); ok && fd.Unit != "" {
		return fd.Unit
	}
	return "units"
}

// lengthText formats a length of measure m: hours and minutes, or the number
// with the unit.
func lengthText(k plugin.KindDef, m plugin.LengthDef, v float64) string {
	if !m.InMinutes() {
		return fmtNum(v) + " " + lengthUnit(k, m)
	}
	mins := int64(math.Round(v))
	if mins < 60 {
		return strconv.FormatInt(mins, 10) + " min"
	}
	if mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dh %dm", mins/60, mins%60)
}

// lengthBar is the length controls of the filter panel.
type lengthBar struct {
	Measures []option
	Min, Max string
}

// buildLengthBar is the controls of kind k (nil when it declares no length).
func buildLengthBar(k plugin.KindDef, f lengthFilter) *lengthBar {
	if len(k.Length) == 0 {
		return nil
	}
	b := &lengthBar{Measures: []option{{Value: "", Label: "Any", Selected: f.Measure.Key == ""}}}
	for _, d := range k.Length {
		b.Measures = append(b.Measures, option{Value: d.Key, Label: d.Label + " (" + lengthUnit(k, d) + ")", Selected: d.Key == f.Measure.Key})
	}
	if f.Min != nil {
		b.Min = fmtNum(*f.Min)
	}
	if f.Max != nil {
		b.Max = fmtNum(*f.Max)
	}
	return b
}

// pickCard is the drawn entry.
type pickCard struct {
	Title, Href, Cover, Status string
	Genres                     []string
	Lengths                    []string // "Label: value" of each measure the entry has
	CanStart                   bool
	StartURL                   string
}

// pickView is the result panel.
type pickView struct {
	Card       *pickCard
	Empty      bool
	Noun       string   // the plural kind label, lower case
	Filters    []string // what the empty state names
	RollURL    string
	Candidates int
	Capped     bool
	CapLimit   int
	Notice     []string // rejected keys, only in the panel of a partial
	NoticeMore int
}

// pickURL is the canonical pick URL of a parsed query: the kind, the status
// (always), the filters and the length filter, without the page.
func pickURL(m plugin.Manifest, k plugin.KindDef, lq listQuery) string {
	q := lq.Filters.values()
	if len(m.Kinds) > 1 {
		q.Set("kind", k.Key)
	}
	q.Set("status", statusParam(lq.Status))
	for k, v := range lq.Length.values() {
		q[k] = v
	}
	return "/" + url.PathEscape(m.ID) + "/pick?" + q.Encode()
}

// statusParam is the status parameter of a parsed status: "all" for none.
func statusParam(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// describeFilters names what a pick request narrows by, for the empty state.
func describeFilters(m plugin.Manifest, k plugin.KindDef, lq listQuery) []string {
	var out []string
	if lq.Status != "" && lq.Status != "all" {
		out = append(out, "Status: "+statusLabel(k, library.Status(lq.Status)))
	}
	f := lq.Filters
	if f.Q != "" {
		out = append(out, "Search: "+strconv.Quote(f.Q))
	}
	for _, ff := range f.Fields {
		label := ff.Key
		if fd, ok := fieldDef(k, ff.Key); ok {
			label = fd.Label
		}
		out = append(out, label+": "+strings.ReplaceAll(strings.Join(ff.Values, ", "), "..", "–"))
	}
	if len(f.Genres) > 0 {
		out = append(out, k.Genres.Label+": "+strings.Join(f.Genres, ", "))
	}
	if len(f.Tags) > 0 {
		out = append(out, k.Tags.Label+": "+strings.Join(f.Tags, ", "))
	}
	if len(f.UserTags) > 0 {
		out = append(out, "Own tags: "+strings.Join(f.UserTags, ", "))
	}
	if f.Progress != "" {
		out = append(out, "Progress: "+progressStateLabels[f.Progress])
	}
	if f.RatingMin != nil || f.RatingMax != nil {
		out = append(out, "Score: "+boundsText(f.RatingMin, f.RatingMax, func(i int) string { return strconv.Itoa(i) }))
	}
	for _, b := range []struct {
		on    bool
		label string
	}{{f.Unrated, "Unrated"}, {f.Zero, "Scored 0"}, {f.Unmatched, "Unmatched"}, {f.Partial, "Partially synced"}} {
		if b.on {
			out = append(out, b.label)
		}
	}
	if lq.Length.active() {
		out = append(out, lq.Length.Measure.Label+": "+boundsText(lq.Length.Min, lq.Length.Max, func(v float64) string {
			return lengthText(k, lq.Length.Measure, v)
		}))
	}
	return out
}

// boundsText is "a to b", "a or more" or "b or less".
func boundsText[T int | float64](lo, hi *T, f func(T) string) string {
	switch {
	case lo != nil && hi != nil:
		return f(*lo) + " to " + f(*hi)
	case lo != nil:
		return f(*lo) + " or more"
	case hi != nil:
		return f(*hi) + " or less"
	}
	return ""
}

// buildPick loads the matching entries and draws one.
func (s *server) buildPick(r *http.Request, m plugin.Manifest, lq listQuery) (*pickView, error) {
	ctx := r.Context()
	k, _ := kindDef(m, lq.Kind)
	q := s.listLibraryQuery(ctx, m, k, lq)
	q.Sort = library.Sort{Kind: library.SortTitle} // a stable order for the draw
	rows, err := s.deps.Library.Rows(ctx, q, s.titleLang(ctx, m), s.pickCap+1, 0)
	if errors.Is(err, library.ErrInvalid) && lq.Filters.active() {
		// parseFilters checks every part against the schema: a safety net
		log.Printf("pick %s: filters refused: %s", m.ID, logSafe(err))
		lq.Filters, lq.Rejected = listFilters{}, append(lq.Rejected, "filters")
		return s.buildPick(r, m, lq)
	}
	if err != nil {
		return nil, err
	}
	pv := &pickView{Noun: lowerFirst(k.LabelPlural), RollURL: pickURL(m, k, lq), CapLimit: s.pickCap}
	if len(rows) > s.pickCap {
		rows, pv.Capped = rows[:s.pickCap], true
	}
	if lq.Length.active() {
		rows = slices.DeleteFunc(rows, func(row library.Row) bool {
			v, ok := entryLength(lq.Length.Measure, numberFields(row.Item.Fields))
			return !ok || (lq.Length.Min != nil && v < *lq.Length.Min) || (lq.Length.Max != nil && v > *lq.Length.Max)
		})
	}
	pv.Candidates = len(rows)
	if len(rows) == 0 {
		pv.Empty, pv.Filters = true, describeFilters(m, k, lq)
		return pv, nil
	}
	i := s.rand(len(rows))
	if i < 0 || i >= len(rows) {
		i = 0
	}
	row := rows[i]
	it := row.Item
	id := strconv.FormatInt(it.ID, 10)
	c := &pickCard{Title: row.Title, Href: itemURL(m, it.Source, it.SourceID, it.Kind), Cover: covers.Src(it.CoverPath),
		Status: statusLabel(k, row.Entry.Status), Genres: it.Genres,
		CanStart: row.Entry.Status == library.Planned, StartURL: "/list/" + id + "/start"}
	if c.Title == "" {
		c.Title = "(untitled)"
	}
	nums := numberFields(it.Fields)
	for _, d := range k.Length {
		if v, ok := entryLength(d, nums); ok {
			c.Lengths = append(c.Lengths, d.Label+": "+lengthText(k, d, v))
		}
	}
	pv.Card = c
	return pv, nil
}

func (s *server) getPick(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	vals, err := url.ParseQuery(r.URL.RawQuery)
	lq := parseListQuery(m, vals)
	if err != nil {
		lq.Rejected = append(lq.Rejected, "(unreadable parameter)")
	}
	lq.Tab = "" // the pick draws entries, never series
	if !vals.Has("status") {
		lq.Status = string(library.Planned)
	}
	lq.View = s.resolveView(r.Context(), m, lq.View)
	w.Header().Set("Vary", "HX-Request, HX-Target, HX-History-Restore-Request")
	pv, err := s.buildPick(r, m, lq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if isPartialRequest(r) {
		pv.Notice, pv.NoticeMore = noticeKeys(lq.Rejected)
		s.renderPartial(w, "list", "pick_result", pv)
		return
	}
	// the list page shows the rejected keys in its own notice
	s.showListPick(w, r, m, lq, pv)
}

// postStart moves a planned entry to active (the pick's Start button) and
// answers 303 to the item. An entry in any other status is left as it is.
func (s *server) postStart(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	t, ok := s.loadEntry(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	e, err := s.deps.Library.Entry(ctx, ref.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if e == nil {
		s.entryNotFound(w, r)
		return
	}
	if e.Status == library.Planned {
		e.Status = library.Active
		switch err := s.deps.Library.UpdateEntry(ctx, *e, nil); {
		case errors.Is(err, library.ErrNoEntry):
			s.entryNotFound(w, r)
			return
		case errors.Is(err, library.ErrInvalid):
			s.errorPage(w, r, http.StatusUnprocessableEntity, "Cannot start", invalidMessage(err), "")
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
	}
	it := t.row.Item
	http.Redirect(w, r, itemURL(t.man, it.Source, it.SourceID, it.Kind), http.StatusSeeOther)
}
