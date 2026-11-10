package web

// The generic list page GET /{plugin}?status=&page=&kind=: one kind of one
// plugin at a time (the first kind unless ?kind= names another kind of a
// plugin with several), tabs per core status class with the kind's status
// labels and counts, 100 rows per page, and the schema-generated search and
// filters of filters.go (kept by the tab, pagination and row links). Rows
// are built by rowView and rendered by the list_row template, shared with
// the +1 response.

import (
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
}

// progressCell is one active progress track of a row.
type progressCell struct{ Label, Value string }

// rowView is one table row; Return is the list URL the row's forms send back.
type rowView struct {
	Title, Href, Cover string
	HasType            bool
	Type, Status       string
	Progress           []progressCell
	ShowLabels         bool // more than one track: labels are visible
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

type listView struct {
	Heading              string // the kind's plural label
	Kinds                []tabView
	TypeHeader           string // "" without a type column
	Tabs                 []tabView
	TabsOOB              bool // the tabs ride along with a rows partial
	Filters              filterBar
	Filtered             bool     // a filter narrows the rows
	Notice               []string // rejected parameter keys (shown escaped)
	NoticeMore           int      // rejected keys beyond Notice
	Rows                 []rowView
	AnyEntries           bool
	Total, From, To      int
	PrevURL, NextURL     string
	SearchURL, ManualURL string
	EmptyNoun            string
	Dialog               *entryDialog
	// Exports links the plugin's exporters (the whole list of the plugin).
	Exports []exportLink
}

// listQuery is a parsed list request: Kind is the kind key shown, Status ""
// for All, Filters the valid search, filter and sort state and Rejected the
// parameter keys that were ignored.
type listQuery struct {
	Kind, Status string
	Page         int
	Filters      listFilters
	Rejected     []string
}

// listURL builds the list URL of a plugin for a kind (only for plugins with
// several kinds), a status filter and a page (page 1 omitted).
func listURL(m plugin.Manifest, kind, status string, page int) string {
	return filteredListURL(m, kind, status, page, listFilters{})
}

// filteredListURL is listURL keeping the filters f (canonical form).
func filteredListURL(m plugin.Manifest, kind, status string, page int, f listFilters) string {
	q := f.values()
	if len(m.Kinds) > 1 && kind != "" {
		q.Set("kind", kind)
	}
	if status != "" {
		q.Set("status", status)
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
// the first kind), the status filter (anything but a core status class
// means All), the requested page (invalid values mean 1, an overflowing
// number the last page) and the filters (parseFilters) from a query. An
// unknown kind or status is named in Rejected like any other ignored key.
func parseListQuery(m plugin.Manifest, q url.Values) listQuery {
	lq := listQuery{Kind: m.Kinds[0].Key, Status: q.Get("status")}
	k := q.Get("kind")
	switch {
	case len(m.Kinds) > 1 && hasKind(m, k):
		lq.Kind = k
	case k != "" && k != lq.Kind:
		lq.Rejected = append(lq.Rejected, "kind")
	}
	if !slices.Contains(library.Statuses(), library.Status(lq.Status)) {
		if lq.Status != "" {
			lq.Rejected = append(lq.Rejected, "status")
		}
		lq.Status = ""
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

// primaryTrack is the kind's +1 track when it is a count or pages track
// active for the item.
func primaryTrack(k plugin.KindDef, keys []string) (plugin.ProgressDef, bool) {
	def, ok := progressDefOf(k, k.Progress.PrimaryKey)
	if !ok || !slices.Contains(keys, def.Key) || (def.Type != plugin.Count && def.Type != plugin.Pages) {
		return plugin.ProgressDef{}, false
	}
	return def, true
}

// rowView builds the row of one list entry. It is THE row builder: the list
// page and the +1 response both render its result with list_row.
func (s *server) rowView(m plugin.Manifest, row library.Row, ret string) rowView {
	k, ok := kindDef(m, row.Item.Kind)
	if !ok {
		k = m.Kinds[0]
	}
	it, e := row.Item, row.Entry
	id := strconv.FormatInt(it.ID, 10)
	v := rowView{
		Title: row.Title, Href: itemURL(m, it.Source, it.SourceID, it.Kind), Cover: covers.Src(it.CoverPath),
		Status: statusLabel(k, e.Status), Score: "–", ScoreAttr: "none",
		PartialSync: row.Flags.PartialSync, Unmatched: row.Flags.Unmatched, UnmatchedHint: unmatchedRowHint(m),
		Return: ret, IncrementURL: "/list/" + id + "/increment", RemoveURL: "/list/" + id + "/remove",
		EditURL:       "/list/" + id + "/edit?" + url.Values{"return": {ret}}.Encode(),
		RemoveConfirm: "Remove this " + lowerFirst(k.Label) + " and all its saved details from your list?",
	}
	if v.Title == "" {
		v.Title = "(untitled)"
	}
	if fd, ok := typeField(k); ok {
		v.HasType, v.Type = true, it.Fields[fd.Key].Text
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
	if lq.Status != "" {
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
	pg, err := s.deps.Library.List(ctx, q, s.deps.Settings.TitleLang(ctx))
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
	ret := filteredListURL(m, k.Key, lq.Status, pg.PageNo, f)
	v := listView{Heading: k.LabelPlural, AnyEntries: anyEntries, Total: pg.Total, Filtered: f.narrows(),
		Filters:   buildFilterBar(m, k, lq, genres, tags),
		SearchURL: "/" + url.PathEscape(m.ID) + "/search", EmptyNoun: lowerFirst(k.LabelPlural), Exports: s.exportLinks(m)}
	v.Notice, v.NoticeMore = noticeKeys(lq.Rejected)
	// the manual-add link only while the plugin has the manual route
	if manual := "/" + m.ID + "/manual/new"; slices.Contains(s.pluginRoutes[m.ID], "GET "+manual) {
		v.ManualURL = manual
	}
	if fd, ok := typeField(k); ok {
		v.TypeHeader = fd.Label
	}
	for _, row := range pg.Rows {
		v.Rows = append(v.Rows, s.rowView(m, row, ret))
	}
	if pg.Total > 0 {
		v.From, v.To = (pg.PageNo-1)*listPageSize+1, (pg.PageNo-1)*listPageSize+len(pg.Rows)
	}
	if pg.PageNo > 1 {
		v.PrevURL = filteredListURL(m, k.Key, lq.Status, pg.PageNo-1, f)
	}
	if pg.PageNo < pg.Pages {
		v.NextURL = filteredListURL(m, k.Key, lq.Status, pg.PageNo+1, f)
	}
	v.Tabs = append(v.Tabs, tabView{Label: "All", Count: all, Href: filteredListURL(m, k.Key, "", 1, f), Current: lq.Status == ""})
	for _, st := range listTabOrder {
		v.Tabs = append(v.Tabs, tabView{Label: statusLabel(k, st), Count: counts[st], Href: filteredListURL(m, k.Key, string(st), 1, f),
			Current: string(st) == lq.Status})
	}
	// another kind has another schema: its link starts without filters
	if len(m.Kinds) > 1 {
		for _, mk := range m.Kinds {
			v.Kinds = append(v.Kinds, tabView{Label: mk.LabelPlural, Href: listURL(m, mk.Key, "", 1), Current: mk.Key == k.Key})
		}
	}
	return v, nil
}

// getList renders the list page. The filter bar's htmx requests (HX-Target
// list-rows) get only the rows region, with the tabs as an out-of-band swap
// and the canonical URL for the address bar; every other request, a history
// restore included, gets the full page (both carry Vary: HX-Request).
func (s *server) getList(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	vals, err := url.ParseQuery(r.URL.RawQuery) // keeps the readable pairs
	lq := parseListQuery(m, vals)
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
		w.Header().Set("HX-Push-Url", filteredListURL(m, lq.Kind, lq.Status, lq.Page, lq.Filters))
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
