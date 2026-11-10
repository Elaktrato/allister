package web

// The generic list page GET /{plugin}?status=&page=&kind=: one kind of one
// plugin at a time (the first kind unless ?kind= names another kind of a
// plugin with several), tabs per core status class with the kind's status
// labels and counts, 100 rows per page. Rows are built by rowView and
// rendered by the list_row template, shared with the +1 response.

import (
	"errors"
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
// for All.
type listQuery struct {
	Kind, Status string
	Page         int
}

// listURL builds the list URL of a plugin for a kind (only for plugins with
// several kinds), a status filter and a page (page 1 omitted).
func listURL(m plugin.Manifest, kind, status string, page int) string {
	q := url.Values{}
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
// means All) and the requested page (invalid values mean 1, an overflowing
// number the last page) from a query.
func parseListQuery(m plugin.Manifest, q url.Values) listQuery {
	lq := listQuery{Kind: m.Kinds[0].Key, Status: q.Get("status")}
	if k := q.Get("kind"); len(m.Kinds) > 1 && hasKind(m, k) {
		lq.Kind = k
	}
	if !slices.Contains(library.Statuses(), library.Status(lq.Status)) {
		lq.Status = ""
	}
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
	counts, err := s.deps.Library.Counts(ctx, library.Query{PluginID: m.ID, Kind: k.Key})
	if err != nil {
		return listView{}, err
	}
	all := 0
	for _, n := range counts {
		all += n
	}
	q := library.Query{PluginID: m.ID, Kind: k.Key, Page: lq.Page, PageSize: listPageSize}
	if lq.Status != "" {
		st := library.Status(lq.Status)
		q.Status = &st
	}
	pg, err := s.deps.Library.List(ctx, q, s.deps.Settings.TitleLang(ctx))
	if err != nil {
		return listView{}, err
	}
	ret := listURL(m, k.Key, lq.Status, pg.PageNo)
	v := listView{Heading: k.LabelPlural, AnyEntries: all > 0, Total: pg.Total,
		SearchURL: "/" + url.PathEscape(m.ID) + "/search", EmptyNoun: lowerFirst(k.LabelPlural), Exports: s.exportLinks(m)}
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
		v.PrevURL = listURL(m, k.Key, lq.Status, pg.PageNo-1)
	}
	if pg.PageNo < pg.Pages {
		v.NextURL = listURL(m, k.Key, lq.Status, pg.PageNo+1)
	}
	v.Tabs = append(v.Tabs, tabView{Label: "All", Count: all, Href: listURL(m, k.Key, "", 1), Current: lq.Status == ""})
	for _, st := range listTabOrder {
		v.Tabs = append(v.Tabs, tabView{Label: statusLabel(k, st), Count: counts[st], Href: listURL(m, k.Key, string(st), 1),
			Current: string(st) == lq.Status})
	}
	if len(m.Kinds) > 1 {
		for _, mk := range m.Kinds {
			v.Kinds = append(v.Kinds, tabView{Label: mk.LabelPlural, Href: listURL(m, mk.Key, "", 1), Current: mk.Key == k.Key})
		}
	}
	return v, nil
}

// getList renders the list page. It is always the full page: nothing
// requests it as a fragment (the responses still carry Vary: HX-Request).
func (s *server) getList(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	s.showList(w, r, m, parseListQuery(m, r.URL.Query()), nil, http.StatusOK)
}

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
