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
	reg.Handle("GET /series/{uid}/edit", h(s.getSeriesEdit))
	reg.Handle("POST /series/{uid}/edit", h(s.postSeriesEdit))
	reg.Handle("POST /series/{uid}/parts/{item}/spinoff", h(s.postSeriesSpinOff))
	reg.Handle("GET /series/{uid}/link-candidates", h(s.getSeriesLinkCandidates))
	reg.Handle("GET /series/{uid}/parent-candidates", h(s.getSeriesParentCandidates))
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
	// Pos is the 1-based position within the part's block; SpinOff says which
	MoveURL     string
	RemoveURL   string
	Pos         int
	SpinOff     bool
	SpinOffURL  string
	SpinOffNext string // the spin_off value the toggle posts: "1" or "0"
}

type candidateView struct {
	ID, Title, Cover string
}

// spinOffSeriesView is a spin-off series in the Spin-offs block.
type spinOffSeriesView struct {
	Title, Href, Cover, Status, StatusKey string
	Done, Total                           int
}

type seriesView struct {
	Title, Status, StatusKey string
	Done, Total              int
	// Parts are the main parts, SpinOffParts the spin-off parts in order
	Parts, SpinOffParts      []seriesPartView
	SpinOffSeries            []spinOffSeriesView
	Cover, Description       string
	Info                     []infoRow
	ParentTitle, ParentHref  string
	EditURL                  string
	Dialog                   *seriesDialog
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

// seriesInfo is the info block of the header: the dates, finished marker and
// expected parts, then the kind's series fields in their declared order.
func seriesInfo(k plugin.KindDef, res library.ResolvedSeries) []infoRow {
	var out []infoRow
	switch {
	case res.StartedOn != nil && res.EndedOn != nil:
		out = append(out, infoRow{Label: "Started - ended", Text: fmtDay(res.StartedOn) + " - " + fmtDay(res.EndedOn)})
	case res.StartedOn != nil:
		out = append(out, infoRow{Label: "Started", Text: fmtDay(res.StartedOn)})
	case res.EndedOn != nil:
		out = append(out, infoRow{Label: "Ended", Text: fmtDay(res.EndedOn)})
	}
	if res.Finished {
		out = append(out, infoRow{Label: "Finished", Text: "Yes"})
	}
	if res.ExpectedParts > 0 {
		out = append(out, infoRow{Label: "Expected parts", Text: strconv.Itoa(res.ExpectedParts)})
	}
	for _, fd := range k.SeriesFields {
		v, ok := res.Fields[fd.Key]
		if !ok || !hasValue(v) {
			continue
		}
		text, href := fieldText(fd, v)
		if text != "" {
			out = append(out, infoRow{Label: fd.Label, Text: text, Href: href})
		}
	}
	return out
}

func (s *server) buildSeries(r *http.Request, m plugin.Manifest, ser *library.Series) (seriesView, error) {
	ctx := r.Context()
	lang := s.titleLang(ctx, m)
	k, _ := kindDef(m, ser.Kind)
	res, err := s.deps.Library.LoadResolvedSeries(ctx, ser, lang)
	if err != nil {
		return seriesView{}, err
	}
	order, err := s.deps.Library.SeriesPartsOrder(ctx, ser.ID)
	if err != nil {
		return seriesView{}, err
	}
	rows, err := s.deps.Library.SeriesParts(ctx, ser.ID, lang)
	if err != nil {
		return seriesView{}, err
	}
	spin := map[int64]bool{}
	for _, p := range order {
		spin[p.ItemID] = p.SpinOff
	}
	base := seriesURL(m.ID, ser.UID)
	v := seriesView{Title: ser.Title, Total: res.PartsTotal, Done: res.Completed, KindLabel: lowerFirst(k.Label),
		Cover: covers.Src(res.Cover), Description: res.Description, Info: seriesInfo(k, res), EditURL: base + "/edit",
		RenameURL: base + "/rename", AddURL: base + "/parts", DeleteURL: base + "/delete",
		CandidatesURL: base + "/candidates", ListURL: seriesTabURL(m.ID, "", "", "", "", 1)}
	v.SearchURL, v.SearchLabel = seriesSearchLink(m, ser)
	v.DeleteNote = "Delete the series and remove its " + strconv.Itoa(len(rows)) + " " + entriesWord(len(rows)) + " from your list"
	var mainRows, spinRows []library.Row
	for _, row := range rows {
		if spin[row.Item.ID] {
			spinRows = append(spinRows, row)
		} else {
			mainRows = append(mainRows, row)
		}
	}
	part := func(block []library.Row, i int, isSpin bool) seriesPartView {
		row := block[i]
		id := strconv.FormatInt(row.Item.ID, 10)
		next := "1"
		if isSpin {
			next = "0"
		}
		return seriesPartView{rowView: s.rowView(m, row, base, nil),
			CanMoveUp: i > 0, CanMoveDown: i < len(block)-1, Pos: i + 1, SpinOff: isSpin,
			MoveURL: base + "/parts/" + id + "/move", RemoveURL: base + "/parts/" + id + "/remove",
			SpinOffURL: base + "/parts/" + id + "/spinoff", SpinOffNext: next}
	}
	for i := range mainRows {
		v.Parts = append(v.Parts, part(mainRows, i, false))
	}
	for i := range spinRows {
		v.SpinOffParts = append(v.SpinOffParts, part(spinRows, i, true))
	}
	v.Status, v.StatusKey = statusLabel(k, res.Status), string(res.Status)
	if ser.ParentID != 0 {
		if p, err := s.deps.Library.SeriesByID(ctx, ser.ParentID); err == nil && p != nil {
			v.ParentTitle, v.ParentHref = p.Title, seriesURL(m.ID, p.UID)
		} else if err != nil && !errors.Is(err, library.ErrNoSeries) {
			return v, err
		}
	}
	kids, err := s.deps.Library.SpinOffsOf(ctx, ser.ID)
	if err != nil {
		return v, err
	}
	for i := range kids {
		kk, _ := kindDef(m, kids[i].Kind)
		cr, err := s.deps.Library.LoadResolvedSeries(ctx, &kids[i], lang)
		if err != nil {
			return v, err
		}
		v.SpinOffSeries = append(v.SpinOffSeries, spinOffSeriesView{Title: kids[i].Title, Href: seriesURL(m.ID, kids[i].UID),
			Cover: covers.Src(cr.Cover), Status: statusLabel(kk, cr.Status), StatusKey: string(cr.Status), Done: cr.Completed, Total: cr.PartsTotal})
	}
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
	order, err := s.deps.Library.SeriesPartsOrder(ctx, ser.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	pos := map[int64]library.SeriesPart{}
	for _, p := range order {
		pos[p.ItemID] = p
	}
	var v struct{ Rows []rowView }
	for _, row := range rows {
		rv := s.rowView(m, row, ret, nil)
		rv.SeriesPos, rv.SeriesSpinOff = pos[row.Item.ID].Position, pos[row.Item.ID].SpinOff
		v.Rows = append(v.Rows, rv)
	}
	s.renderPartial(w, "list", "series_parts", v)
}
