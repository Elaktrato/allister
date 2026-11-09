package web

import (
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/store"
	"github.com/allister/allister/internal/titles"
)

const listPageSize = 100

// tabs in display order; "" is the All tab.
var listTabs = []struct {
	Status store.Status
	Label  string
}{
	{"", "All"},
	{store.Watching, "Watching"},
	{store.Completed, "Completed"},
	{store.PlanToWatch, "Plan to watch"},
	{store.Paused, "Paused"},
	{store.Dropped, "Dropped"},
}

type tabView struct {
	Label, Href string
	Count       int
	Current     bool
}

// rowView is one table row; Return is the list URL the row's forms send back.
type rowView struct {
	AnimeID               int64
	Title, Href, Cover    string
	Type, Status          string
	Progress              int
	Total                 string // "?" when unknown
	AtTotal               bool
	Score, ScoreAttr      string
	PartialSync, Unmatch  bool
	Return                string
	IncrementURL, EditURL string
	RemoveURL             string
}

type listView struct {
	Tabs                    []tabView
	Rows                    []rowView
	AnyEntries              bool
	Total, From, To         int
	Page, Pages             int
	PrevURL, NextURL        string
	Return                  string
	Dialog                  *legacyEntryForm
	DialogReturn, ListTitle string
}

// listURL builds the list URL for a status filter and page (page 1 omitted).
func listURL(status string, page int) string {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/anime"
	}
	return "/anime?" + q.Encode()
}

// parseListQuery reads the status filter (unknown values mean All) and the
// requested page (invalid values mean 1) from a query.
func parseListQuery(q url.Values) (string, int) {
	status := q.Get("status")
	if _, ok := statusLabels[store.Status(status)]; !ok {
		status = ""
	}
	raw := q.Get("page")
	page, err := strconv.Atoi(raw)
	switch {
	case errors.Is(err, strconv.ErrRange) && isASCIIDigits(raw, 0):
		page = math.MaxInt32 // overflow: clamped to the last page by the caller
	case err != nil || page < 1:
		page = 1
	}
	return status, page
}

func (s *server) rowView(lang string, r store.ListRow, ret string) rowView {
	id := strconv.FormatInt(r.AnimeID, 10)
	v := rowView{
		AnimeID: r.AnimeID, Title: titles.Resolve(r.Titles, lang), Href: animeURL(string(r.Source), r.SourceID),
		Cover: covers.Src(r.CoverPath), Type: r.Type, Status: statusLabels[r.Entry.Status],
		Progress: r.Entry.Progress, Total: "?", Score: "–", ScoreAttr: "none",
		PartialSync: r.SyncState != "done", Unmatch: r.Source == "manual" && r.MALID != "",
		Return: ret, IncrementURL: "/list/" + id + "/increment", RemoveURL: "/list/" + id + "/remove",
		EditURL: "/list/" + id + "/edit?" + url.Values{"return": {ret}}.Encode(),
	}
	if r.Episodes != nil {
		v.Total = strconv.Itoa(*r.Episodes)
		v.AtTotal = r.Entry.Progress >= *r.Episodes
	}
	if r.Entry.Rating != nil {
		v.Score = strconv.Itoa(*r.Entry.Rating)
		v.ScoreAttr = v.Score
	}
	if v.Title == "" {
		v.Title = "(untitled)"
	}
	return v
}

// buildList loads the list page for a status filter and page.
func (s *server) buildList(r *http.Request, status string, page int) (listView, error) {
	ctx := r.Context()
	counts, err := s.deps.Store.Counts(ctx)
	if err != nil {
		return listView{}, err
	}
	all := 0
	for _, n := range counts {
		all += n
	}
	total := all
	var filter *store.Status
	if status != "" {
		st := store.Status(status)
		filter, total = &st, counts[st]
	}
	pages := (total + listPageSize - 1) / listPageSize
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	rows, err := s.deps.Store.ListPage(ctx, filter, listPageSize, (page-1)*listPageSize)
	if err != nil {
		return listView{}, err
	}
	ret := listURL(status, page)
	lang := s.deps.Settings.TitleLang(ctx)
	v := listView{AnyEntries: all > 0, Total: total, Page: page, Pages: pages, Return: ret}
	for _, row := range rows {
		v.Rows = append(v.Rows, s.rowView(lang, row, ret))
	}
	if total > 0 {
		v.From, v.To = (page-1)*listPageSize+1, (page-1)*listPageSize+len(rows)
	}
	if page > 1 {
		v.PrevURL = listURL(status, page-1)
	}
	if page < pages {
		v.NextURL = listURL(status, page+1)
	}
	for _, t := range listTabs {
		n := all
		if t.Status != "" {
			n = counts[t.Status]
		}
		v.Tabs = append(v.Tabs, tabView{Label: t.Label, Count: n, Href: listURL(string(t.Status), 1), Current: string(t.Status) == status})
	}
	return v, nil
}

func (s *server) getList(w http.ResponseWriter, r *http.Request) {
	status, page := parseListQuery(r.URL.Query())
	s.showList(w, r, status, page, nil, http.StatusOK)
}

// showList renders the full list page, with the edit dialog open when dialog is set.
func (s *server) showList(w http.ResponseWriter, r *http.Request, status string, page int, dialog *legacyEntryForm, code int) {
	v, err := s.buildList(r, status, page)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	v.Dialog = dialog
	s.renderStatus(w, r, code, "list", v)
}

// entryID parses the {id} wildcard; it writes a 404 page and returns false
// for a non-numeric id.
func (s *server) entryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	if !isASCIIDigits(raw, maxIDDigits) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such anime.", "")
		return 0, false
	}
	id, _ := strconv.ParseInt(raw, 10, 64)
	return id, true
}

// entryRow loads the row of {id}; unknown ids and anime without a list entry
// get a 404 page.
func (s *server) entryRow(w http.ResponseWriter, r *http.Request, id int64) (store.ListRow, bool) {
	row, err := s.deps.Store.GetListRow(r.Context(), id)
	if err != nil {
		s.fail(w, r, err, "")
		return store.ListRow{}, false
	}
	if row == nil {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in your list.", "")
		return store.ListRow{}, false
	}
	return *row, true
}

// done answers a successful change: HX-Redirect with 204 for htmx (a 303
// target would be swapped into the dialog), 303 otherwise.
func (s *server) done(w http.ResponseWriter, r *http.Request, ret string) {
	if isPartialRequest(r) {
		w.Header().Set("HX-Redirect", ret)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

func (s *server) postIncrement(w http.ResponseWriter, r *http.Request) {
	id, ok := s.entryID(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	ret := safeReturn(r.PostFormValue("return"))
	if err := s.deps.Store.IncrementProgress(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNoEntry) {
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in your list.", "")
			return
		}
		s.fail(w, r, err, "")
		return
	}
	if !isPartialRequest(r) {
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	row, err := s.deps.Store.GetListRow(r.Context(), id)
	if err != nil || row == nil {
		s.fail(w, r, errors.Join(errors.New("increment: reload row"), err), "")
		return
	}
	s.renderPartial(w, "list", "list_row", s.rowView(s.deps.Settings.TitleLang(r.Context()), *row, ret))
}

// editForm prefills the dialog from the current entry.
//
// TODO(Task 9): the generic edit dialog must prefill stored duration
// progress (whole seconds) as h:mm:ss (fmtDuration) so it round-trips
// through parseEntryForm's duration parser.
func (s *server) editForm(row store.ListRow, ret string) legacyEntryForm {
	e := row.Entry
	f := legacyEntryForm{
		Action: "/list/" + strconv.FormatInt(row.AnimeID, 10) + "/edit", CancelURL: ret, Return: ret,
		Heading: "Edit list entry", Submit: "Save", Status: string(e.Status),
		Progress: strconv.Itoa(e.Progress), StartedOn: fmtDay(e.StartedOn), FinishedOn: fmtDay(e.FinishedOn),
		Comment: e.Comment, Episodes: row.Episodes,
	}
	if e.Rating != nil {
		f.Rating = strconv.Itoa(*e.Rating)
	}
	return f
}

// editResponse writes the edit dialog (partial) or the full list with the
// dialog open (no JavaScript).
func (s *server) editResponse(w http.ResponseWriter, r *http.Request, f legacyEntryForm, code int) {
	f = f.withStatuses()
	if isPartialRequest(r) {
		s.renderPartialStatus(w, code, "list", "legacy_entry_dialog", f)
		return
	}
	var status string
	page := 1
	if u, err := url.Parse(f.Return); err == nil {
		status, page = parseListQuery(u.Query())
	}
	s.showList(w, r, status, page, &f, code)
}

func (s *server) getEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := s.entryID(w, r)
	if !ok {
		return
	}
	row, ok := s.entryRow(w, r, id)
	if !ok {
		return
	}
	s.editResponse(w, r, s.editForm(row, safeReturn(r.URL.Query().Get("return"))), http.StatusOK)
}

func (s *server) postEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := s.entryID(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	row, ok := s.entryRow(w, r, id)
	if !ok {
		return
	}
	f, entry, msg := parseLegacyEntryForm(r, s.editForm(row, safeReturn(r.PostFormValue("return"))))
	if msg == "" {
		entry.AnimeID = id
		err := s.deps.Store.UpsertListEntry(r.Context(), entry)
		switch {
		case err == nil:
			s.done(w, r, f.Return)
			return
		case errors.Is(err, store.ErrInvalid):
			msg = invalidMessage(err)
		default:
			s.fail(w, r, err, "")
			return
		}
	}
	f.Error = msg
	s.editResponse(w, r, f, http.StatusUnprocessableEntity)
}

func (s *server) postRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.entryID(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	if err := s.deps.Store.DeleteAnime(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNoEntry) {
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such anime.", "")
			return
		}
		log.Printf("remove %d: %v", id, err)
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, safeReturn(r.PostFormValue("return")))
}
