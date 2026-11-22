package web

// The series page: a hand-built, ordered group of list entries of one plugin
// and kind. GET /{plugin}/series/{uid} shows the parts with the derived
// progress and status; the POST routes rename, add, reorder and remove parts
// and delete the series. All of them sit in the plugin's route set, so the
// plugin's enabled guard applies; "series" is a reserved source id, so these
// routes cannot clash with a source.

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// maxCandidates is the most entries "From your list" offers at once.
const maxCandidates = 20

// seriesURL is the page of the series uid of a plugin.
func seriesURL(pluginID, uid string) string {
	return "/" + url.PathEscape(pluginID) + "/series/" + url.PathEscape(uid)
}

func (s *server) seriesRoutes(reg routeRegistrar, pluginID string) {
	h := func(f func(http.ResponseWriter, *http.Request, plugin.Manifest, *library.Series)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { s.withSeries(w, r, pluginID, f) }
	}
	reg.Handle("GET /series/{uid}", h(s.getSeries))
	reg.Handle("POST /series/{uid}/rename", h(s.postSeriesRename))
	reg.Handle("POST /series/{uid}/parts", h(s.postSeriesAddPart))
	reg.Handle("POST /series/{uid}/parts/{item}/move", h(s.postSeriesMove))
	reg.Handle("POST /series/{uid}/parts/{item}/remove", h(s.postSeriesRemove))
	reg.Handle("POST /series/{uid}/delete", h(s.postSeriesDelete))
	reg.Handle("GET /series/{uid}/parts", h(s.getSeriesParts))
	reg.Handle("GET /series/{uid}/candidates", h(s.getSeriesCandidates))
}

// withSeries resolves the plugin and the series of the request; an unknown
// one gets the 404 page.
func (s *server) withSeries(w http.ResponseWriter, r *http.Request, pluginID string, f func(http.ResponseWriter, *http.Request, plugin.Manifest, *library.Series)) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	uid := r.PathValue("uid")
	if !ok || !validPathID(uid) {
		s.seriesNotFound(w, r)
		return
	}
	ser, err := s.deps.Library.SeriesByUID(r.Context(), pluginID, uid)
	switch {
	case errors.Is(err, library.ErrNoSeries):
		s.seriesNotFound(w, r)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	f(w, r, m, ser)
}

func (s *server) seriesNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such series.", "")
}

type seriesPartView struct {
	rowView
	CanMoveUp   bool
	CanMoveDown bool
	MoveURL     string
	RemoveURL   string
}

type candidateView struct {
	ID, Title, Cover string
}

type seriesView struct {
	Title, Status, StatusKey string
	Done, Total              int
	Parts                    []seriesPartView
	RenameURL, AddURL        string
	DeleteURL, CandidatesURL string
	Candidates               []candidateView
	KindLabel                string
	ListURL                  string
	// SearchURL and SearchLabel: add a part by searching the kind's sources
	SearchURL, SearchLabel string
	// DeleteNote names the entries a delete with "remove" takes along
	DeleteNote string
}

func (s *server) buildSeries(r *http.Request, m plugin.Manifest, ser *library.Series) (seriesView, error) {
	ctx := r.Context()
	k, _ := kindDef(m, ser.Kind)
	rows, err := s.deps.Library.SeriesParts(ctx, ser.ID, s.titleLang(ctx, m))
	if err != nil {
		return seriesView{}, err
	}
	base := seriesURL(m.ID, ser.UID)
	v := seriesView{Title: ser.Title, Total: len(rows), KindLabel: lowerFirst(k.Label),
		RenameURL: base + "/rename", AddURL: base + "/parts", DeleteURL: base + "/delete",
		CandidatesURL: base + "/candidates", ListURL: seriesTabURL(m.ID, "", "", "", "", 1)}
	v.SearchURL, v.SearchLabel = seriesSearchLink(m, ser)
	v.DeleteNote = "Delete the series and remove its " + strconv.Itoa(len(rows)) + " " + entriesWord(len(rows)) + " from your list"
	sts := make([]library.Status, 0, len(rows))
	for i, row := range rows {
		sts = append(sts, row.Entry.Status)
		if row.Entry.Status == library.Completed {
			v.Done++
		}
		id := strconv.FormatInt(row.Item.ID, 10)
		v.Parts = append(v.Parts, seriesPartView{rowView: s.rowView(m, row, base, nil),
			CanMoveUp: i > 0, CanMoveDown: i < len(rows)-1,
			MoveURL: base + "/parts/" + id + "/move", RemoveURL: base + "/parts/" + id + "/remove"})
	}
	st := library.DeriveSeriesStatus(sts, 0)
	v.Status, v.StatusKey = statusLabel(k, st), string(st)
	v.Candidates, err = s.seriesCandidates(r, m, k, ser, "")
	return v, err
}

// seriesSearchLink is the search page that adds into ser ("" when no source
// serves the series' kind) and its label naming the sources.
func seriesSearchLink(m plugin.Manifest, ser *library.Series) (href, label string) {
	srcs := sourcesForKind(m, ser.Kind)
	if len(srcs) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(srcs))
	for _, si := range srcs {
		names = append(names, si.Name)
	}
	u := "/" + url.PathEscape(m.ID) + "/search"
	if len(m.Kinds) > 1 {
		u += "?kind=" + url.QueryEscape(ser.Kind)
	}
	return withSeriesParam(u, ser.UID), "Search " + strings.Join(names, " / ")
}

func entriesWord(n int) string {
	if n == 1 {
		return "entry"
	}
	return "entries"
}

func (s *server) getSeries(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	v, err := s.buildSeries(r, m, ser)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "series", v)
}

func (s *server) postSeriesRename(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	err := s.deps.Library.RenameSeries(r.Context(), ser.ID, r.PostFormValue("title"))
	switch {
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Series", invalidMessage(err), "")
		return
	case errors.Is(err, library.ErrNoSeries):
		s.seriesNotFound(w, r)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
}

func (s *server) postSeriesAddPart(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PostFormValue("item"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
		return
	}
	if !s.addPart(w, r, m, ser, id) {
		return
	}
	http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
}

// partPositionField is the field that orders the parts of ser (the kind's
// series hint), "" for none.
func partPositionField(m plugin.Manifest, ser *library.Series) string {
	if k, ok := kindDef(m, ser.Kind); ok && k.SeriesHint != nil {
		return k.SeriesHint.PositionField
	}
	return ""
}

// addPart appends the entry of item as the series' last part (ordered by the
// kind's hint position field when it has one). On failure it writes the error
// page and returns false.
func (s *server) addPart(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series, item int64) bool {
	err := s.deps.Library.AddPart(r.Context(), ser.ID, item, partPositionField(m, ser))
	switch {
	case err == nil:
		return true
	case errors.Is(err, library.ErrAlreadyInSeries):
		s.errorPage(w, r, http.StatusConflict, "Series", "This entry is already in a series.", "")
	case errors.Is(err, library.ErrNoEntry), errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
	case errors.Is(err, library.ErrNoSeries):
		s.seriesNotFound(w, r)
	default:
		s.internalError(w, r, err)
	}
	return false
}

// seriesParam is the ?series= uid a search, an overview or an add carries
// ("" when absent or malformed).
func seriesParam(r *http.Request) string {
	if uid := r.URL.Query().Get("series"); validPathID(uid) {
		return uid
	}
	return ""
}

// withSeriesParam adds ?series=uid (or &series=) to u; u is returned as is
// for an empty uid.
func withSeriesParam(u, uid string) string {
	if uid == "" {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + url.Values{"series": {uid}}.Encode()
}

// addSeries resolves the series an add request targets, before anything is
// saved: none is (nil, true); a malformed or unknown uid is a 404, a series of
// another kind than the item a 422 (false: the page is written).
func (s *server) addSeries(w http.ResponseWriter, r *http.Request, t *addTarget) (*library.Series, bool) {
	return s.targetSeries(w, r, t.man.ID, t.kind)
}

// targetSeries is addSeries for any request that adds an entry of kind k to
// plugin pluginID (the add dialog, the manual form).
func (s *server) targetSeries(w http.ResponseWriter, r *http.Request, pluginID string, k plugin.KindDef) (*library.Series, bool) {
	raw := r.URL.Query().Get("series")
	if raw == "" {
		return nil, true
	}
	if !validPathID(raw) {
		s.seriesNotFound(w, r)
		return nil, false
	}
	ser, err := s.deps.Library.SeriesByUID(r.Context(), pluginID, raw)
	switch {
	case errors.Is(err, library.ErrNoSeries):
		s.seriesNotFound(w, r)
		return nil, false
	case err != nil:
		s.internalError(w, r, err)
		return nil, false
	}
	if ser.Kind != k.Key {
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Series",
			"This series holds "+lowerFirst(k.LabelPlural)+" of another kind; it cannot take this entry.", "")
		return nil, false
	}
	return ser, true
}

// attachToSeries puts an entry that is already in the list into the series.
func (s *server) attachToSeries(w http.ResponseWriter, r *http.Request, t *addTarget, ser *library.Series) {
	if s.addPart(w, r, t.man, ser, t.saved.ID) {
		s.redirectAfterAdd(w, r, seriesURL(t.man.ID, ser.UID))
	}
}

// addFinished ends a successful add of item: joined to the custom list when
// the add targeted one, then to the series when it targeted one (attached
// now, after the add has its final item), else to the custom list, else to
// the plugin's list.
func (s *server) addFinished(w http.ResponseWriter, r *http.Request, t *addTarget, item int64, ser *library.Series, lst *library.List) {
	if lst != nil {
		if !s.joinAfterAdd(w, r, lst, item, t.overviewURL()) {
			return
		}
		if ser == nil {
			s.redirectAfterAdd(w, r, customListURL(lst.ID))
			return
		}
	}
	if ser == nil {
		s.redirectAfterAdd(w, r, "/"+url.PathEscape(t.man.ID))
		return
	}
	s.attachAfterAdd(w, r, t.man, ser, item, t.overviewURL())
}

// overviewURL is the overview of the target as it is now (saved, else the
// live item).
func (t *addTarget) overviewURL() string {
	if t.saved != nil {
		return itemURL(t.man, t.saved.Source, t.saved.SourceID, t.saved.Kind)
	}
	return itemURL(t.man, t.data.Source, t.data.ID, t.kind.Key)
}

// attachAfterAdd puts the entry that an add just created into the series the
// add targeted and goes to the series page. The series may have changed since
// it was checked (deleted, or the item taken into another series); the entry
// is in the list by then, so that is not an error page: the answer is the
// item's overview with a notice that it could not be put into the series.
func (s *server) attachAfterAdd(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series, item int64, overview string) {
	err := s.deps.Library.AddPart(r.Context(), ser.ID, item, partPositionField(m, ser))
	switch {
	case err == nil:
		s.redirectAfterAdd(w, r, seriesURL(m.ID, ser.UID))
	case errors.Is(err, library.ErrAlreadyInSeries), errors.Is(err, library.ErrNoSeries),
		errors.Is(err, library.ErrNoEntry), errors.Is(err, library.ErrInvalid):
		s.redirectAfterAdd(w, r, noticeURL(overview, noticeSeriesAttach))
	default:
		s.internalError(w, r, err)
	}
}

// postMakeSeries is "Make this a series": a new series titled by the form,
// with the saved entry as its first part.
func (s *server) postMakeSeries(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ref, ok := s.addRef(w, r, pluginID)
	if !ok || !parseForm(w, r) {
		return
	}
	it, _, err := s.findSaved(r.Context(), m, ref)
	switch {
	case errors.Is(err, errAmbiguousKind):
		s.itemFail(w, r, m, ref.Source, err, "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	case it == nil:
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
		return
	}
	ser, err := s.deps.Library.CreateSeries(r.Context(), it.ID, r.PostFormValue("title"))
	switch {
	case errors.Is(err, library.ErrNoEntry):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
	case errors.Is(err, library.ErrAlreadyInSeries):
		s.errorPage(w, r, http.StatusConflict, "Series", "This entry is already in a series.", "")
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Series", invalidMessage(err), "")
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
	}
}

// formItemID parses an item id from a path or form value.
func formItemID(raw string) (int64, bool) {
	if !isASCIIDigits(raw, maxIDDigits) {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

func (s *server) postSeriesMove(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PathValue("item"))
	delta := moveDelta(r.PostFormValue("dir"))
	if !ok || delta == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	}
	switch err := s.deps.Library.MovePart(r.Context(), ser.ID, id, delta); {
	case errors.Is(err, library.ErrNoSeries):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
}

func (s *server) postSeriesRemove(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PathValue("item"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	}
	switch err := s.deps.Library.RemovePart(r.Context(), ser.ID, id); {
	case errors.Is(err, library.ErrNoSeries):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	// the last part takes the series with it
	_, err := s.deps.Library.SeriesByUID(r.Context(), m.ID, ser.UID)
	switch {
	case errors.Is(err, library.ErrNoSeries):
		http.Redirect(w, r, seriesTabURL(m.ID, "", "", "", "", 1), http.StatusSeeOther)
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
	}
}

func (s *server) postSeriesDelete(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	var remove bool
	switch r.PostFormValue("entries") {
	case "keep":
	case "remove":
		remove = true
	default:
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Series", "Choose whether to keep or remove the entries.", "")
		return
	}
	switch err := s.deps.Library.DeleteSeries(r.Context(), ser.ID, remove); {
	case errors.Is(err, library.ErrNoSeries):
		s.seriesNotFound(w, r)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, seriesTabURL(m.ID, "", "", "", "", 1), http.StatusSeeOther)
}

// seriesCandidates lists up to maxCandidates entries of the series' plugin and
// kind that are in no series and match text; entries whose hint title field
// equals the series title (trimmed, case-insensitive) come first.
func (s *server) seriesCandidates(r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series, text string) ([]candidateView, error) {
	ctx := r.Context()
	hint := ""
	if k.SeriesHint != nil {
		hint = k.SeriesHint.TitleField
	}
	rows, err := s.deps.Library.SeriesCandidates(ctx, m.ID, ser.Kind, text, hint, ser.Title, s.titleLang(ctx, m), maxCandidates)
	if err != nil {
		return nil, err
	}
	out := make([]candidateView, 0, len(rows))
	for _, row := range rows {
		title := row.Title
		if title == "" {
			title = "(untitled)"
		}
		out = append(out, candidateView{ID: strconv.FormatInt(row.Item.ID, 10), Title: title, Cover: covers.Src(row.Item.CoverPath)})
	}
	return out, nil
}

func (s *server) getSeriesCandidates(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	k, _ := kindDef(m, ser.Kind)
	cs, err := s.seriesCandidates(r, m, k, ser, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.renderPartial(w, "series", "series_candidates", seriesView{AddURL: seriesURL(m.ID, ser.UID) + "/parts", Candidates: cs})
}

// getSeriesParts answers the Series tab's expand control: the parts as
// table rows that fit the tab's head, in position order.
func (s *server) getSeriesParts(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	ctx := r.Context()
	rows, err := s.deps.Library.SeriesParts(ctx, ser.ID, s.titleLang(ctx, m))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ret := seriesTabURL(m.ID, "", "", "", "", 1)
	var v struct{ Rows []rowView }
	for i, row := range rows {
		rv := s.rowView(m, row, ret, nil)
		rv.SeriesPos = i + 1
		v.Rows = append(v.Rows, rv)
	}
	s.renderPartial(w, "list", "series_parts", v)
}
