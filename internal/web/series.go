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

// seriesListURL is the plugin's list on its series tab.
func seriesListURL(pluginID string) string {
	return "/" + url.PathEscape(pluginID) + "/?tab=series"
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
	ID         string
	CanMoveUp  bool
	CanMoveDwn bool
	MoveURL    string
	RemoveURL  string
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
		CandidatesURL: base + "/candidates", ListURL: seriesListURL(m.ID)}
	sts := make([]library.Status, 0, len(rows))
	for i, row := range rows {
		sts = append(sts, row.Entry.Status)
		if row.Entry.Status == library.Completed {
			v.Done++
		}
		id := strconv.FormatInt(row.Item.ID, 10)
		v.Parts = append(v.Parts, seriesPartView{rowView: s.rowView(m, row, base, nil), ID: id,
			CanMoveUp: i > 0, CanMoveDwn: i < len(rows)-1,
			MoveURL: base + "/parts/" + id + "/move", RemoveURL: base + "/parts/" + id + "/remove"})
	}
	st := library.DeriveSeriesStatus(sts)
	v.Status, v.StatusKey = statusLabel(k, st), string(st)
	v.Candidates, err = s.seriesCandidates(r, m, k, ser, "")
	return v, err
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
	field := ""
	if k, ok := kindDef(m, ser.Kind); ok && k.SeriesHint != nil {
		field = k.SeriesHint.PositionField
	}
	err := s.deps.Library.AddPart(r.Context(), ser.ID, id, field)
	switch {
	case errors.Is(err, library.ErrAlreadyInSeries):
		s.errorPage(w, r, http.StatusConflict, "Series", "This entry is already in a series.", "")
		return
	case errors.Is(err, library.ErrNoEntry), errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such entry.", "")
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
	delta := 0
	switch r.PostFormValue("dir") {
	case "up":
		delta = -1
	case "down":
		delta = 1
	}
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
		http.Redirect(w, r, seriesListURL(m.ID), http.StatusSeeOther)
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
	http.Redirect(w, r, seriesListURL(m.ID), http.StatusSeeOther)
}

// seriesCandidates lists up to maxCandidates entries of the series' plugin and
// kind that are in no series and match text; entries whose hint title field
// equals the series title (trimmed, case-insensitive) come first.
func (s *server) seriesCandidates(r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series, text string) ([]candidateView, error) {
	ctx := r.Context()
	lang := s.titleLang(ctx, m)
	q := library.Query{PluginID: m.ID, Kind: ser.Kind, Text: text, Sort: library.Sort{Kind: library.SortTitle}, PageSize: 200}
	want := strings.ToLower(strings.TrimSpace(ser.Title))
	members, err := s.deps.Library.SeriesMemberIDs(ctx, m.ID, ser.Kind)
	if err != nil {
		return nil, err
	}
	var hinted, others []candidateView
	for page := 1; ; page++ {
		q.Page = page
		pg, err := s.deps.Library.List(ctx, q, lang)
		if err != nil {
			return nil, err
		}
		for _, row := range pg.Rows {
			if members[row.Item.ID] {
				continue
			}
			title := row.Title
			if title == "" {
				title = "(untitled)"
			}
			c := candidateView{ID: strconv.FormatInt(row.Item.ID, 10), Title: title, Cover: covers.Src(row.Item.CoverPath)}
			if h := k.SeriesHint; h != nil && want != "" &&
				strings.ToLower(strings.TrimSpace(row.Item.Fields[h.TitleField].Text)) == want {
				hinted = append(hinted, c)
			} else {
				others = append(others, c)
			}
		}
		if page >= pg.Pages {
			break
		}
	}
	return append(hinted, others...)[:min(maxCandidates, len(hinted)+len(others))], nil
}

func (s *server) getSeriesCandidates(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	k, _ := kindDef(m, ser.Kind)
	cs, err := s.seriesCandidates(r, m, k, ser, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Vary", "HX-Request")
	s.renderPartial(w, "series", "series_candidates", seriesView{AddURL: seriesURL(m.ID, ser.UID) + "/parts", Candidates: cs})
}
