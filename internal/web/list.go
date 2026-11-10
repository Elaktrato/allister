package web

// The generic list page GET /{plugin}?status=&page=&kind=: one kind of one
// plugin at a time (the first kind unless ?kind= names another kind of a
// plugin with several), tabs per core status class with the kind's status
// labels and counts, 100 rows per page, and the schema-generated search and
// filters of filters.go (kept by the tab, pagination and row links). Rows
// are built by rowView and rendered by the list_row template, shared with
// the +1 response.

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

const listPageSize = 100

// listTabOrder is the order of the status tabs after "All".
var listTabOrder = []library.Status{library.Active, library.Completed, library.Planned, library.Paused, library.Dropped}

type tabView struct {
	Label, Href string
	Count       int
	Current     bool
	Status      string // the core status class ("" for All), the tab's colour
}

// progressCell is one active progress track of a row.
type progressCell struct{ Label, Value string }

// rowView is one table row; Return is the list URL the row's forms send back.
type rowView struct {
	Title, Href, Cover string
	Status             string
	StatusKey          string    // the core status class, the card corner's color
	Cells              []rowCell // the visible columns, in order
	Updated            string    // the day the entry was last changed (2006-01-02)
	Progress           []progressCell
	ShowLabels         bool // more than one track: labels are visible
	BarPct             int  // the card's progress bar (0-100; -1: no bounded total)
	CanIncrement       bool
	IncrementLabel     string
	Score, ScoreAttr   string
	PartialSync        bool
	Unmatched          bool
	UnmatchedHint      string
	Return             string
	IncrementURL       string
	EditURL, RemoveURL string
	RemoveConfirm      string
}

// headView is one cell of the table head: Href ("" when the column does
// not sort) sorts by the column, toggling the direction of the sorted
// column; Sort is the aria-sort of the sorted column ("ascending" or
// "descending", "" for the others).
type headView struct {
	Label, ID, Href, Sort string
	Hide                  string // "sm", "md" or "lg": the column shows from that screen width on
}

// listHead is the table head: the title and the visible columns, in order.
type listHead struct {
	Title   headView
	Columns []headView
}

type listView struct {
	Heading              string // the kind's plural label
	AddLabel             string // "Add book": the search link's text
	Kinds                []tabView
	Head                 listHead
	Tabs                 []tabView
	TabsOOB              bool   // the tabs ride along with a rows partial
	PushURL              string // the canonical URL of what is shown (filters applied, page clamped)
	View                 string // "cards" or "table": how the rows are shown
	Filters              filterBar
	Filtered             bool     // a filter narrows the rows
	Notice               []string // rejected parameter keys (shown escaped)
	NoticeMore           int      // rejected keys beyond Notice
	Rows                 []rowView
	AnyEntries           bool
	Total, From, To      int
	PrevURL, NextURL     string
	ViewCardsURL         string // the view switch: the same list in cards
	ViewTableURL         string // … and in the table
	SearchURL, ManualURL string
	EmptyNoun            string
	Dialog               *entryDialog
	// Exports links the plugin's exporters (the whole list of the plugin).
	Exports []exportLink
}

// listQuery is a parsed list request: Kind is the kind key shown, Status ""
// for All, "all" for an explicit All, View "cards" or "table", Filters the
// valid search, filter and sort state and Rejected the parameter keys that
// were ignored.
type listQuery struct {
	Kind, Status, View string
	Page               int
	Filters            listFilters
	Rejected           []string
}

// listURL builds the list URL of a plugin for a kind (only for plugins with
// several kinds), a status filter, a view and a page (page 1 omitted).
func listURL(m plugin.Manifest, kind, status, view string, page int) string {
	return filteredListURL(m, kind, status, view, page, listFilters{})
}

// filteredListURL is listURL keeping the filters f (canonical form).
func filteredListURL(m plugin.Manifest, kind, status, view string, page int, f listFilters) string {
	q := f.values()
	if len(m.Kinds) > 1 && kind != "" {
		q.Set("kind", kind)
	}
	if status != "" {
		q.Set("status", status)
	}
	if view != "" {
		q.Set("view", view)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	base := "/" + url.PathEscape(m.ID)
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// parseListQuery reads the kind (unknown values and single-kind plugins mean
// the first kind), the status filter ("all" is an explicit All, anything
// but a core status class means All), the view ("cards" or "table",
// anything else means the plugin's default), the requested page (invalid
// values mean 1, an overflowing number the last page) and the filters
// (parseFilters) from a query. An unknown kind, status or view is named in
// Rejected like any other ignored key.
func parseListQuery(m plugin.Manifest, q url.Values) listQuery {
	lq := listQuery{Kind: m.Kinds[0].Key, Status: q.Get("status")}
	k := q.Get("kind")
	switch {
	case len(m.Kinds) > 1 && hasKind(m, k):
		lq.Kind = k
	case k != "" && k != lq.Kind:
		lq.Rejected = append(lq.Rejected, "kind")
	}
	switch {
	case lq.Status == "all":
	case slices.Contains(library.Statuses(), library.Status(lq.Status)):
	default:
		if lq.Status != "" {
			lq.Rejected = append(lq.Rejected, "status")
		}
		lq.Status = ""
	}
	switch v := q.Get("view"); v {
	case "", "cards", "table":
		lq.View = v
	default:
		lq.Rejected = append(lq.Rejected, "view")
	}
	kd, _ := kindDef(m, lq.Kind)
	var rejected []string
	lq.Filters, rejected = parseFilters(kd, q)
	lq.Rejected = append(lq.Rejected, rejected...)
	slices.Sort(lq.Rejected)
	raw := q.Get("page")
	page, err := strconv.Atoi(raw)
	switch {
	case errors.Is(err, strconv.ErrRange) && isASCIIDigits(raw, 0):
		page = math.MaxInt32 // overflow: clamped to the last page by the library
	case err != nil || page < 1:
		page = 1
	}
	lq.Page = page
	return lq
}

// typeField is the field shown as the type column: the kind's first Show
// enum field, else its first Show text field.
func typeField(k plugin.KindDef) (plugin.FieldDef, bool) {
	for _, t := range []plugin.FieldType{plugin.FieldEnum, plugin.FieldText} {
		for _, fd := range k.Fields {
			if fd.Show && fd.Type == t {
				return fd, true
			}
		}
	}
	return plugin.FieldDef{}, false
}

// sortFields are the fields shown as sortable columns: the kind's Sortable
// fields the library can sort by, except the type column's field and a key
// that a core sort name (title, rating, ...) would shadow.
func sortFields(k plugin.KindDef) []plugin.FieldDef {
	tf, hasType := typeField(k)
	var out []plugin.FieldDef
	for _, fd := range k.Fields {
		if !fd.Sortable || (hasType && fd.Key == tf.Key) || coreSort(fd.Key) ||
			library.CheckSort(k, library.Sort{Kind: library.SortField, Field: fd.Key}) != nil {
			continue
		}
		out = append(out, fd)
	}
	return out
}

// coreSort reports whether key is a core sort name.
func coreSort(key string) bool {
	switch library.SortKind(key) {
	case library.SortUpdated, library.SortTitle, library.SortRating, library.SortProgress:
		return true
	}
	return false
}

// sortHead builds the head cell of the column sorting by key. The sorted
// column links to the other direction, every other column to its default
// direction (titles A to Z, the rest highest first); the links keep the
// filters, kind, status and view and start at page 1 (filteredListURL), in
// the canonical form parseSort reads.
func sortHead(m plugin.Manifest, kind, status, view string, f listFilters, key, label string) headView {
	cur, dir := f.Sort, f.Dir
	if cur == "" {
		cur = string(library.SortUpdated)
	}
	if dir == "" {
		dir = defaultDir(cur)
	}
	h := headView{Label: label, ID: "sort-" + key}
	next := defaultDir(key)
	if cur == key {
		h.Sort, next = "descending", "asc"
		if dir == "asc" {
			h.Sort, next = "ascending", "desc"
		}
	}
	g := f
	g.Sort, g.Dir = key, next
	if next == defaultDir(key) {
		g.Dir = ""
	}
	if key == string(library.SortUpdated) && g.Dir == "" { // the default order
		g.Sort = ""
	}
	h.Href = filteredListURL(m, kind, status, view, 1, g)
	return h
}

// primaryTrack is the kind's +1 track when it is a count or pages track
// active for the item.
func primaryTrack(k plugin.KindDef, keys []string) (plugin.ProgressDef, bool) {
	def, ok := progressDefOf(k, k.Progress.PrimaryKey)
	if !ok || !slices.Contains(keys, def.Key) || (def.Type != plugin.Count && def.Type != plugin.Pages) {
		return plugin.ProgressDef{}, false
	}
	return def, true
}

// rowView builds the row of one list entry with the columns cols. It is THE
// row builder: the list page and the +1 response both render its result
// with list_row.
func (s *server) rowView(m plugin.Manifest, row library.Row, ret string, cols []listColumn) rowView {
	k, ok := kindDef(m, row.Item.Kind)
	if !ok {
		k = m.Kinds[0]
	}
	it, e := row.Item, row.Entry
	id := strconv.FormatInt(it.ID, 10)
	v := rowView{
		Title: row.Title, Href: itemURL(m, it.Source, it.SourceID, it.Kind), Cover: covers.Src(it.CoverPath),
		Status: statusLabel(k, e.Status), StatusKey: string(e.Status), Score: "–", ScoreAttr: "none",
		BarPct:      -1,
		PartialSync: row.Flags.PartialSync, Unmatched: row.Flags.Unmatched, UnmatchedHint: unmatchedRowHint(m),
		Return: ret, IncrementURL: "/list/" + id + "/increment", RemoveURL: "/list/" + id + "/remove",
		EditURL:       "/list/" + id + "/edit?" + url.Values{"return": {ret}}.Encode(),
		RemoveConfirm: "Remove this " + lowerFirst(k.Label) + " and all its saved details from your list?",
	}
	if v.Title == "" {
		v.Title = "(untitled)"
	}
	for _, c := range cols {
		cell := rowCell{Core: c.Core, Hide: c.Hide}
		if c.Core == "" {
			cell.Text, _ = fieldText(c.Field, it.Fields[c.Key])
		}
		v.Cells = append(v.Cells, cell)
	}
	if !e.UpdatedAt.IsZero() {
		v.Updated = e.UpdatedAt.Format("2006-01-02")
	}
	for _, def := range k.Progress.Eligible {
		if slices.Contains(it.ProgressKeys, def.Key) {
			v.Progress = append(v.Progress, progressCell{Label: def.Label, Value: fmtProgress(def, e.Progress[def.Key], it.Fields)})
		}
	}
	v.ShowLabels = len(v.Progress) > 1
	if def, ok := primaryTrack(k, it.ProgressKeys); ok {
		total, bounded := progressTotal(def, it.Fields)
		v.CanIncrement = !bounded || e.Progress[def.Key] < total
		v.IncrementLabel = "Add one " + lowerFirst(def.Label) + " to " + v.Title
		if bounded && total > 0 {
			v.BarPct = min(100, max(0, int(e.Progress[def.Key]/total*100)))
		}
	}
	if e.Rating != nil {
		v.Score = strconv.Itoa(*e.Rating)
		v.ScoreAttr = v.Score
	}
	return v
}

// unmatchedRowHint explains the "unmatched" badge.
func unmatchedRowHint(m plugin.Manifest) string {
	if len(m.Sources) > 0 && m.Sources[0].Name != "" {
		return "Added manually; no " + m.Sources[0].Name + " match yet"
	}
	return "Added manually; not matched yet"
}

// buildList loads the list page of a plugin for a parsed query.
func (s *server) buildList(r *http.Request, m plugin.Manifest, lq listQuery) (listView, error) {
	ctx := r.Context()
	k, _ := kindDef(m, lq.Kind)
	f := lq.Filters
	q := library.Query{PluginID: m.ID, Kind: k.Key, Page: lq.Page, PageSize: listPageSize}
	f.apply(k, &q)
	if lq.Status != "" && lq.Status != "all" {
		st := library.Status(lq.Status)
		q.Status = &st
	}
	counts, err := s.deps.Library.Counts(ctx, q)
	if errors.Is(err, library.ErrInvalid) && f.active() {
		// parseFilters checks every part against the schema, so this is a
		// safety net: show the list without filters and say so
		log.Printf("list %s: filters refused: %v", m.ID, err)
		lq.Filters, lq.Rejected = listFilters{}, append(lq.Rejected, "filters")
		return s.buildList(r, m, lq)
	}
	if err != nil {
		return listView{}, err
	}
	all := 0
	for _, n := range counts {
		all += n
	}
	anyEntries := all > 0
	if !anyEntries && f.narrows() {
		plain, err := s.deps.Library.Counts(ctx, library.Query{PluginID: m.ID, Kind: k.Key})
		if err != nil {
			return listView{}, err
		}
		for _, n := range plain {
			anyEntries = anyEntries || n > 0
		}
	}
	pg, err := s.deps.Library.List(ctx, q, s.titleLang(ctx, m))
	if err != nil {
		return listView{}, err
	}
	var genres, tags []library.TermCount
	if anyEntries && k.Genres.Enabled && k.Genres.Filterable {
		if genres, err = s.deps.Library.Terms(ctx, q, "genre"); err != nil {
			return listView{}, err
		}
	}
	if anyEntries && k.Tags.Enabled && k.Tags.Filterable {
		if tags, err = s.deps.Library.Terms(ctx, q, "tag"); err != nil {
			return listView{}, err
		}
	}
	ret := filteredListURL(m, k.Key, lq.Status, lq.View, pg.PageNo, f)
	v := listView{Heading: k.LabelPlural, AnyEntries: anyEntries, Total: pg.Total, Filtered: f.narrows(), PushURL: ret, View: lq.View,
		Filters:   buildFilterBar(m, k, lq, genres, tags),
		SearchURL: "/" + url.PathEscape(m.ID) + "/search", EmptyNoun: lowerFirst(k.LabelPlural), Exports: s.exportLinks(m)}
	v.Notice, v.NoticeMore = noticeKeys(lq.Rejected)
	// the manual-add link only while the plugin has the manual route
	if manual := "/" + m.ID + "/manual/new"; slices.Contains(s.pluginRoutes[m.ID], "GET "+manual) {
		v.ManualURL = manual
	}
	v.AddLabel = "Add " + lowerFirst(k.Label)
	// the head sorts only by visible columns; the compact sort control
	// (sortOptions) keeps every sort key
	cols := s.visibleColumns(ctx, m, k)
	v.Head.Title = sortHead(m, k.Key, lq.Status, lq.View, f, string(library.SortTitle), "Title")
	for _, c := range cols {
		h := headView{Label: c.Label}
		if key := columnSort(k, c); key != "" {
			h = sortHead(m, k.Key, lq.Status, lq.View, f, key, c.Label)
		}
		h.Hide = c.Hide
		v.Head.Columns = append(v.Head.Columns, h)
	}
	for _, row := range pg.Rows {
		v.Rows = append(v.Rows, s.rowView(m, row, ret, cols))
	}
	if pg.Total > 0 {
		v.From, v.To = (pg.PageNo-1)*listPageSize+1, (pg.PageNo-1)*listPageSize+len(pg.Rows)
	}
	if pg.PageNo > 1 {
		v.PrevURL = filteredListURL(m, k.Key, lq.Status, lq.View, pg.PageNo-1, f)
	}
	if pg.PageNo < pg.Pages {
		v.NextURL = filteredListURL(m, k.Key, lq.Status, lq.View, pg.PageNo+1, f)
	}
	// the view switch keeps the filters, the status and the page
	v.ViewCardsURL = filteredListURL(m, k.Key, lq.Status, "cards", pg.PageNo, f)
	v.ViewTableURL = filteredListURL(m, k.Key, lq.Status, "table", pg.PageNo, f)
	v.Tabs = append(v.Tabs, tabView{Label: "All", Count: all,
		Href: filteredListURL(m, k.Key, "all", lq.View, 1, f), Current: lq.Status == "" || lq.Status == "all"})
	for _, st := range listTabOrder {
		v.Tabs = append(v.Tabs, tabView{Label: statusLabel(k, st), Count: counts[st],
			Href:    filteredListURL(m, k.Key, string(st), lq.View, 1, f),
			Current: string(st) == lq.Status, Status: string(st)})
	}
	// another kind has another schema: its link starts without filters
	if len(m.Kinds) > 1 {
		for _, mk := range m.Kinds {
			v.Kinds = append(v.Kinds, tabView{Label: mk.LabelPlural, Href: listURL(m, mk.Key, "", lq.View, 1), Current: mk.Key == k.Key})
		}
	}
	return v, nil
}

// resolveView returns the view the list opens in: the URL's view, else the
// plugin's home setting, else cards.
func (s *server) resolveView(ctx context.Context, m plugin.Manifest, view string) string {
	if view == "table" {
		return "table"
	}
	if view == "cards" {
		return "cards"
	}
	return s.deps.Settings.HomeView(ctx, m.ID)
}

// homeStatus returns the status class the plugin's list opens with when the
// URL names none ("" for every status); a stored value that is not a core
// status counts as none.
func (s *server) homeStatus(ctx context.Context, m plugin.Manifest) string {
	st := s.deps.Settings.HomeStatuses(ctx, m.ID)
	if st == "" || !slices.Contains(library.Statuses(), library.Status(st)) {
		return ""
	}
	return st
}

// getList renders the list page. The filter bar's htmx requests (HX-Target
// list-rows) get only the rows region, with the tabs, the Clear link and the
// genre/tag option lists as out-of-band swaps and the canonical URL for the
// address bar; every other request, a history restore included, gets the
// full page. Both carry Vary: HX-Request, HX-Target, HX-History-Restore-Request.
func (s *server) getList(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	vals, err := url.ParseQuery(r.URL.RawQuery) // keeps the readable pairs
	lq := parseListQuery(m, vals)
	// the plugin's home settings fill what the URL does not name: the view
	// (cards by default) and, when no status is given at all, the status
	lq.View = s.resolveView(r.Context(), m, lq.View)
	if !vals.Has("status") {
		if st := s.homeStatus(r.Context(), m); st != "" {
			lq.Status = st
		}
	}
	// the answer depends on all three (rows partial, full page, restore)
	w.Header().Set("Vary", "HX-Request, HX-Target, HX-History-Restore-Request")
	if err != nil {
		lq.Rejected = append(lq.Rejected, "(unreadable parameter)")
	}
	if isPartialRequest(r) && r.Header.Get("HX-Target") == listRowsID {
		v, err := s.buildList(r, m, lq)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		v.TabsOOB = true
		v.Filters.outOfBand()
		w.Header().Set("HX-Push-Url", v.PushURL)
		s.renderPartial(w, "list", "list_partial", v)
		return
	}
	s.showList(w, r, m, lq, nil, http.StatusOK)
}

// listRowsID is the id of the rows region the filter bar targets.
const listRowsID = "list-rows"

// showList renders the full list page, with the edit dialog open when dialog is set.
func (s *server) showList(w http.ResponseWriter, r *http.Request, m plugin.Manifest, lq listQuery, dialog *entryDialog, code int) {
	v, err := s.buildList(r, m, lq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v.Dialog = dialog
	s.renderStatus(w, r, code, "list", v)
}
