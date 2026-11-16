package web

// Custom lists: named, cross-plugin collections of entries. GET /lists is
// the overview with the "New list" form (POST /lists); GET /lists/{id} is a
// list page with its entries as the list's cards (each with its own
// plugin's status labels and +1), sorted by ?sort=position|title|added|status
// (position only while the list is in manual order). POST /lists/{id}
// renames (name) or switches the order (order=manual|sorted), POST
// /lists/{id}/delete deletes the list (the entries stay). Entries join with
// POST /lists/{id}/items (item, from GET /lists/{id}/search?q=, a search of
// the whole library), leave with POST /lists/{id}/items/{item}/delete and
// move with POST /lists/{id}/items/{item}/move (dir=up|down). An add from a
// plugin's search carries ?list={id} like ?series= (addCarry) and joins the
// new entry to the list. The item page's picker posts every list at once to
// the entry route POST /list/{id}/lists. "lists" is a reserved plugin id, so
// these routes cannot clash with a plugin's.

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// maxListResults is the most library search results a list page offers.
const maxListResults = 20

// listSorts are the ?sort= values of a list page, in tab order.
var listSorts = []string{library.ListSortPosition, library.ListSortTitle, library.ListSortAdded, library.ListSortStatus}

var listSortLabels = map[string]string{
	library.ListSortPosition: "Manual order", library.ListSortTitle: "Title",
	library.ListSortAdded: "Date added", library.ListSortStatus: "Status",
}

func customListURL(id int64) string { return "/lists/" + strconv.FormatInt(id, 10) }

func (s *server) mountLists(mux *http.ServeMux) {
	mux.HandleFunc("GET /lists", s.getLists)
	mux.HandleFunc("POST /lists", s.postNewList)
	h := func(f func(http.ResponseWriter, *http.Request, library.List)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { s.withList(w, r, f) }
	}
	mux.HandleFunc("GET /lists/{id}", h(s.getListPage))
	mux.HandleFunc("POST /lists/{id}", h(s.postList))
	mux.HandleFunc("POST /lists/{id}/delete", h(s.postListDelete))
	mux.HandleFunc("POST /lists/{id}/items", h(s.postListAdd))
	mux.HandleFunc("POST /lists/{id}/items/{item}/delete", h(s.postListRemove))
	mux.HandleFunc("POST /lists/{id}/items/{item}/move", h(s.postListMove))
	mux.HandleFunc("GET /lists/{id}/search", h(s.getListSearch))
}

// withList resolves the {id} of a list route; a malformed or unknown one
// gets the 404 page.
func (s *server) withList(w http.ResponseWriter, r *http.Request, f func(http.ResponseWriter, *http.Request, library.List)) {
	id, ok := formItemID(r.PathValue("id"))
	if !ok {
		s.listNotFound(w, r)
		return
	}
	ls, err := s.deps.Library.CustomList(r.Context(), id)
	switch {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		f(w, r, ls)
	}
}

func (s *server) listNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
}

// listProgress is a list's progress line.
func listProgress(ls library.List) string {
	return strconv.Itoa(ls.Done) + " of " + strconv.Itoa(ls.Items) + " done"
}

// listsPage is the data of lists.html; Name and Error are the create form's
// typed value and refusal.
type listsPage struct {
	Lists       []listSummary
	Name, Error string
}

type listSummary struct{ Name, Href, Progress string }

func (s *server) renderLists(w http.ResponseWriter, r *http.Request, status int, name, msg string) {
	ls, err := s.deps.Library.Lists(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	p := listsPage{Name: name, Error: msg}
	for _, x := range ls {
		p.Lists = append(p.Lists, listSummary{Name: x.Name, Href: customListURL(x.ID), Progress: listProgress(x)})
	}
	s.renderStatus(w, r, status, "lists", p)
}

func (s *server) getLists(w http.ResponseWriter, r *http.Request) {
	s.renderLists(w, r, http.StatusOK, "", "")
}

// listNameRefusal maps a refused list name to its status and message; ok is
// false for any other error.
func listNameRefusal(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, library.ErrListExists):
		return http.StatusConflict, "A list with this name already exists.", true
	case errors.Is(err, library.ErrInvalid):
		return http.StatusUnprocessableEntity, invalidMessage(err), true
	}
	return 0, "", false
}

func (s *server) postNewList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	name := r.PostFormValue("name")
	ls, err := s.deps.Library.CreateList(r.Context(), name)
	if err == nil {
		http.Redirect(w, r, customListURL(ls.ID), http.StatusSeeOther)
		return
	}
	if status, msg, ok := listNameRefusal(err); ok {
		s.renderLists(w, r, status, name, msg)
		return
	}
	s.internalError(w, r, err)
}

// listPageView is the data of list_page.html.
type listPageView struct {
	Name, NameValue, Progress string
	Manual                    bool
	Sorts                     []listSortTab
	Cards                     []listCard
	BaseURL, DeleteURL        string
	SearchURL, AddURL         string
	Searches                  []homeLink
	Query                     string
	Searched                  bool
	Results                   []listResult
	Error                     string
}

type listSortTab struct {
	Label, Href string
	Active      bool
}

// listCard is a card of the list page with its list controls.
type listCard struct {
	rowView
	Movable, CanMoveUp, CanMoveDown bool
	MoveURL, RemoveURL              string
}

// listResult is one entry found by the list page's library search.
type listResult struct {
	ID, Title, Cover, Label string
	InList                  bool
}

// titleLangs maps every enabled plugin to its title language.
func (s *server) titleLangs(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, m := range s.enabledManifests() {
		out[m.ID] = s.titleLang(r.Context(), m)
	}
	return out
}

// listSort is the sort a list page shows: ?sort= when it is offered (position
// only in manual order), else the default; explicit reports a valid ?sort=.
func listSort(r *http.Request, ls library.List) (sort string, explicit bool) {
	q := r.URL.Query().Get("sort")
	if slices.Contains(listSorts, q) && (q != library.ListSortPosition || ls.ManualOrder) {
		return q, true
	}
	if ls.ManualOrder {
		return library.ListSortPosition, false
	}
	return library.ListSortTitle, false
}

// listReturn reports whether raw is a list page as a return target: exactly
// /lists/{digits}, optionally with ?sort= one of listSorts.
func listReturn(raw string) bool {
	path, query, hasQuery := strings.Cut(raw, "?")
	rest, ok := strings.CutPrefix(path, "/lists/")
	if !ok || !isASCIIDigits(rest, maxIDDigits) {
		return false
	}
	if !hasQuery {
		return true
	}
	sort, ok := strings.CutPrefix(query, "sort=")
	return ok && slices.Contains(listSorts, sort)
}

func (s *server) buildListPage(r *http.Request, ls library.List) (listPageView, []library.Row, error) {
	ctx := r.Context()
	sort, explicit := listSort(r, ls)
	rows, err := s.deps.Library.ListRows(ctx, ls.ID, sort, s.titleLangs(r))
	if err != nil {
		return listPageView{}, nil, err
	}
	base := customListURL(ls.ID)
	v := listPageView{Name: ls.Name, NameValue: ls.Name, Progress: listProgress(ls), Manual: ls.ManualOrder,
		BaseURL: base, DeleteURL: base + "/delete", SearchURL: base + "/search", AddURL: base + "/items"}
	for _, key := range listSorts {
		if key == library.ListSortPosition && !ls.ManualOrder {
			continue
		}
		v.Sorts = append(v.Sorts, listSortTab{Label: listSortLabels[key], Href: base + "?sort=" + key, Active: key == sort})
	}
	ret := base
	if explicit {
		ret += "?sort=" + sort
	}
	var entries []homeEntry
	var cards []rowView
	var ids []int64
	for _, row := range rows {
		m, ok := s.deps.Registry.Manifest(row.Item.PluginID)
		if !ok {
			continue
		}
		k, ok := kindDef(m, row.Item.Kind)
		if !ok {
			continue
		}
		rv := s.rowView(m, row, ret, nil)
		rv.BadgeIcon, rv.BadgeLabel = badge(m, k)
		entries, cards, ids = append(entries, homeEntry{m: m, k: k, row: row}), append(cards, rv), append(ids, row.Item.ID)
	}
	if err := s.markHomeSeries(ctx, cards, entries); err != nil {
		return listPageView{}, nil, err
	}
	movable := sort == library.ListSortPosition
	for i, rv := range cards {
		id := strconv.FormatInt(ids[i], 10)
		v.Cards = append(v.Cards, listCard{rowView: rv, Movable: movable, CanMoveUp: movable && i > 0, CanMoveDown: movable && i < len(cards)-1,
			MoveURL: base + "/items/" + id + "/move", RemoveURL: base + "/items/" + id + "/delete"})
	}
	for _, m := range s.enabledManifests() {
		for _, k := range m.Kinds {
			if link, ok := listSearchLink(m, k, ls.ID); ok {
				v.Searches = append(v.Searches, link)
			}
		}
	}
	return v, rows, nil
}

// listSearchLink is the plugin search of kind k that adds into the list
// (false when no source serves the kind), labelled by its sources.
func listSearchLink(m plugin.Manifest, k plugin.KindDef, listID int64) (homeLink, bool) {
	srcs := sourcesForKind(m, k.Key)
	if len(srcs) == 0 {
		return homeLink{}, false
	}
	names := make([]string, 0, len(srcs))
	for _, si := range srcs {
		names = append(names, si.Name)
	}
	u := "/" + url.PathEscape(m.ID) + "/search"
	label := "Search " + strings.Join(names, " / ")
	if len(m.Kinds) > 1 {
		u += "?kind=" + url.QueryEscape(k.Key)
		label += " (" + lowerFirst(k.LabelPlural) + ")"
	}
	return homeLink{Label: label, Href: addCarry{list: listID}.on(u)}, true
}

func (s *server) getListPage(w http.ResponseWriter, r *http.Request, ls library.List) {
	v, _, err := s.buildListPage(r, ls)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "list_page", v)
}

// postList renames the list (name) or switches its order (order).
func (s *server) postList(w http.ResponseWriter, r *http.Request, ls library.List) {
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	var err error
	if _, rename := r.PostForm["name"]; rename {
		err = s.deps.Library.RenameList(ctx, ls.ID, r.PostFormValue("name"))
	} else {
		switch r.PostFormValue("order") {
		case "manual":
			err = s.deps.Library.SetListOrder(ctx, ls.ID, true)
		case "sorted":
			err = s.deps.Library.SetListOrder(ctx, ls.ID, false)
		default:
			s.errorPage(w, r, http.StatusUnprocessableEntity, "List", "Choose manual or sorted order.", "")
			return
		}
	}
	switch {
	case err == nil:
		http.Redirect(w, r, customListURL(ls.ID), http.StatusSeeOther)
		return
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
		return
	}
	status, msg, ok := listNameRefusal(err)
	if !ok {
		s.internalError(w, r, err)
		return
	}
	v, _, berr := s.buildListPage(r, ls)
	if berr != nil {
		s.internalError(w, r, berr)
		return
	}
	v.NameValue, v.Error = r.PostFormValue("name"), msg
	s.renderStatus(w, r, status, "list_page", v)
}

func (s *server) postListDelete(w http.ResponseWriter, r *http.Request, ls library.List) {
	if !parseForm(w, r) {
		return
	}
	switch err := s.deps.Library.DeleteList(r.Context(), ls.ID); {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, "/lists", http.StatusSeeOther)
	}
}

// visibleItem parses an item id and reports whether it is an entry of an
// installed, enabled plugin.
func (s *server) visibleItem(r *http.Request, raw string) (int64, bool, error) {
	id, ok := formItemID(raw)
	if !ok {
		return 0, false, nil
	}
	pluginID, err := s.deps.Library.ItemPluginID(r.Context(), id)
	if errors.Is(err, library.ErrNoEntry) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, installed := s.deps.Registry.Manifest(pluginID)
	return id, installed && s.deps.Registry.IsEnabled(pluginID), nil
}

func (s *server) postListAdd(w http.ResponseWriter, r *http.Request, ls library.List) {
	if !parseForm(w, r) {
		return
	}
	id, ok, err := s.visibleItem(r, r.PostFormValue("item"))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
		return
	}
	switch err := s.deps.Library.AddToCustomList(r.Context(), ls.ID, id); {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
	case errors.Is(err, library.ErrNoEntry):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, customListURL(ls.ID), http.StatusSeeOther)
	}
}

func (s *server) postListRemove(w http.ResponseWriter, r *http.Request, ls library.List) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PathValue("item"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in this list.", "")
		return
	}
	switch err := s.deps.Library.RemoveFromCustomList(r.Context(), ls.ID, id); {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, customListURL(ls.ID), http.StatusSeeOther)
	}
}

func (s *server) postListMove(w http.ResponseWriter, r *http.Request, ls library.List) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PathValue("item"))
	delta := moveDelta(r.PostFormValue("dir"))
	if !ok || delta == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in this list.", "")
		return
	}
	switch err := s.deps.Library.MoveInList(r.Context(), ls.ID, id, delta); {
	case errors.Is(err, library.ErrNoList):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry in this list.", "")
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusUnprocessableEntity, "List", "Switch the list to manual order to move its entries.", "")
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, customListURL(ls.ID), http.StatusSeeOther)
	}
}

// moveDelta is the move of a dir=up|down form value (0 for anything else).
func moveDelta(dir string) int {
	switch dir {
	case "up":
		return -1
	case "down":
		return 1
	}
	return 0
}

// getListSearch searches the whole library for entries to add: the results
// for htmx, else the list page with them.
func (s *server) getListSearch(w http.ResponseWriter, r *http.Request, ls library.List) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	found, err := s.deps.Library.SearchLibrary(r.Context(), q, s.titleLangs(r), maxListResults)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v, rows, err := s.buildListPage(r, ls)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	members := map[int64]bool{}
	for _, row := range rows {
		members[row.Item.ID] = true
	}
	v.Query, v.Searched = q, q != ""
	for _, row := range found {
		m, _ := s.deps.Registry.Manifest(row.Item.PluginID)
		k, ok := kindDef(m, row.Item.Kind)
		if !ok {
			continue
		}
		title := row.Title
		if title == "" {
			title = "(untitled)"
		}
		v.Results = append(v.Results, listResult{ID: strconv.FormatInt(row.Item.ID, 10), Title: title,
			Cover: covers.Src(row.Item.CoverPath), Label: k.Label, InList: members[row.Item.ID]})
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "list_page", "list_search_results", v)
		return
	}
	s.render(w, r, "list_page", v)
}

// listPickerView is the item page's "Lists" form: every list with a
// checkbox, checked for the lists the entry is in.
type listPickerView struct {
	Action, Return string
	Lists          []listChoice
}

type listChoice struct {
	ID, Name string
	Member   bool
}

// listPicker builds the picker of a listed item; return is its overview.
func (s *server) listPicker(r *http.Request, itemID int64, ret string) (*listPickerView, error) {
	ctx := r.Context()
	all, err := s.deps.Library.Lists(ctx)
	if err != nil {
		return nil, err
	}
	of, err := s.deps.Library.ListsOfItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	p := &listPickerView{Action: "/list/" + strconv.FormatInt(itemID, 10) + "/lists", Return: ret}
	for _, x := range all {
		member := slices.ContainsFunc(of, func(o library.List) bool { return o.ID == x.ID })
		p.Lists = append(p.Lists, listChoice{ID: strconv.FormatInt(x.ID, 10), Name: x.Name, Member: member})
	}
	return p, nil
}

// postItemLists saves the picker: the entry is in exactly the posted lists.
func (s *server) postItemLists(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	var ids []int64
	for _, raw := range r.PostForm["list"] {
		id, ok := formItemID(raw)
		if !ok {
			s.listNotFound(w, r)
			return
		}
		ids = append(ids, id)
	}
	switch err := s.deps.Library.SetListsOfItem(r.Context(), ref.ID, ids); {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
	case errors.Is(err, library.ErrNoEntry):
		s.entryNotFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, safeReturn(r.PostFormValue("return"), ref.PluginID), http.StatusSeeOther)
	}
}

// addCarry is where an add from search returns to, carried as ?series=
// (a series uid) and ?list= (a custom list id) from the search to the
// overview, the add dialog and the manual form.
type addCarry struct {
	series string
	list   int64
}

// carriedAdd reads the carried parameters of r (a malformed one is dropped).
func carriedAdd(r *http.Request) addCarry {
	return addCarry{series: seriesParam(r), list: listParam(r)}
}

// on adds the carried parameters to u.
func (c addCarry) on(u string) string {
	u = withSeriesParam(u, c.series)
	if c.list <= 0 {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "list=" + strconv.FormatInt(c.list, 10)
}

// listParam is the ?list= id a search, an overview or an add carries (0 when
// absent or malformed).
func listParam(r *http.Request) int64 {
	id, _ := formItemID(r.URL.Query().Get("list"))
	return id
}

// targetList resolves the custom list an add request targets, before
// anything is saved: none is (nil, true); a malformed or unknown id is a 404
// (false: the page is written).
func (s *server) targetList(w http.ResponseWriter, r *http.Request) (*library.List, bool) {
	raw := r.URL.Query().Get("list")
	if raw == "" {
		return nil, true
	}
	id, ok := formItemID(raw)
	if !ok {
		s.listNotFound(w, r)
		return nil, false
	}
	ls, err := s.deps.Library.CustomList(r.Context(), id)
	switch {
	case errors.Is(err, library.ErrNoList):
		s.listNotFound(w, r)
		return nil, false
	case err != nil:
		s.internalError(w, r, err)
		return nil, false
	}
	return &ls, true
}

// noticeListAttach is the ?notice= value of an overview reached after an add
// whose entry could not be put into its custom list.
const noticeListAttach = "list"

// joinAfterAdd puts the entry an add just created into the custom list the
// add targeted. The list may have gone since it was checked; the entry is
// saved by then, so that answers the overview with a notice. It returns
// false when it has written the answer.
func (s *server) joinAfterAdd(w http.ResponseWriter, r *http.Request, ls *library.List, item int64, overview string) bool {
	switch err := s.deps.Library.AddToCustomList(r.Context(), ls.ID, item); {
	case err == nil:
		return true
	case errors.Is(err, library.ErrNoList), errors.Is(err, library.ErrNoEntry):
		s.redirectAfterAdd(w, r, noticeURL(overview, noticeListAttach))
	default:
		s.internalError(w, r, err)
	}
	return false
}

// joinExisting puts an entry that is already in the library into the custom
// list (and the series, when the add targets one too).
func (s *server) joinExisting(w http.ResponseWriter, r *http.Request, t *addTarget, ser *library.Series, ls *library.List) {
	if ls != nil {
		switch err := s.deps.Library.AddToCustomList(r.Context(), ls.ID, t.saved.ID); {
		case errors.Is(err, library.ErrNoList):
			s.listNotFound(w, r)
			return
		case errors.Is(err, library.ErrNoEntry):
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
	}
	if ser != nil {
		s.attachToSeries(w, r, t, ser)
		return
	}
	s.redirectAfterAdd(w, r, customListURL(ls.ID))
}
