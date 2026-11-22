package web

// The series' own data: GET|POST /{plugin}/series/{uid}/edit (a dialog like
// the entry edit, a full page without htmx), the link and parent pickers and
// the spin-off toggle of a part. The form shows the series' OWN values (never
// the resolved ones): clearing a field clears the own value so the linked or
// derived value shows again. The plugin's series fields reuse the entry
// details field rendering (itemform.go).

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// pickItem is one choice of the link or parent picker.
type pickItem struct {
	ID, Title, Cover string
}

// seriesPickView is the result list of a picker: radio inputs called Name.
type seriesPickView struct {
	Name  string
	Items []pickItem
	Note  string
	// Current is the choice kept checked in every result list, so a search
	// never loses it
	Current *pickItem
}

// seriesDialog is the data of series_dialog.
type seriesDialog struct {
	itemForm                                           // heading, action, cancel, submit, error, series fields and cover
	Title, Description, StartedOn, EndedOn, PartsTotal string
	CoverURL                                           string
	Finished                                           bool
	// the link picker (ShowLink: the kind declares a link) and the parent picker
	ShowLink             bool
	LinkLabel            string
	LinkedID             int64
	LinkURL, ParentURL   string
	LinkPick, ParentPick seriesPickView
	ParentID             int64
	ParentLocked         string
}

// seriesFieldKind is the kind whose editable Fields are the series fields of
// k, so the entry details field rendering and parsing apply unchanged.
func seriesFieldKind(k plugin.KindDef) plugin.KindDef {
	fs := slices.Clone(k.SeriesFields)
	for i := range fs {
		fs[i].Editable, fs[i].Required = true, false
	}
	return plugin.KindDef{Key: k.Key, Fields: fs}
}

// linkedTitle is the display title of the linked item (0: "").
func (s *server) linkedTitle(r *http.Request, m plugin.Manifest, id int64) string {
	if id == 0 {
		return ""
	}
	it, err := s.deps.Library.Item(r.Context(), id)
	if err != nil || it == nil {
		return "(unknown entry)"
	}
	var fallback []string
	if k, ok := kindDef(m, it.Kind); ok {
		fallback = k.TitleFallback
	}
	if t := library.ResolveTitle(it.Titles, s.titleLang(r.Context(), m), fallback); t != "" {
		return t
	}
	return "(untitled)"
}

// newSeriesDialog is the dialog of ser filled with its own values.
func (s *server) newSeriesDialog(r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series) (seriesDialog, error) {
	base := seriesURL(m.ID, ser.UID)
	vals := map[string]string{}
	sk := seriesFieldKind(k)
	for _, fd := range sk.Fields {
		vals[fd.Key] = fieldFormValue(fd, ser.Fields[fd.Key])
	}
	d := seriesDialog{itemForm: s.newItemForm(sk, vals, &library.Item{Fields: ser.Fields}),
		Title: ser.Title, Description: ser.Description, StartedOn: fmtDay(ser.StartedOn), EndedOn: fmtDay(ser.EndedOn),
		Finished: ser.Finished, LinkedID: ser.LinkedItemID, ParentID: ser.ParentID,
		LinkURL: base + "/link-candidates", ParentURL: base + "/parent-candidates"}
	d.Heading, d.Submit, d.CancelURL, d.Action = "Edit series details", "Save", base, base+"/edit"
	if ser.PartsTotal > 0 {
		d.PartsTotal = strconv.Itoa(ser.PartsTotal)
	}
	if !isLocalCover(ser.CoverPath) {
		d.CoverURL = ser.CoverPath
	} else {
		d.Cover = covers.Src(ser.CoverPath)
	}
	if k.SeriesLink != nil {
		d.ShowLink = true
		if lk, ok := kindDef(m, k.SeriesLink.Kind); ok {
			d.LinkLabel = lk.Label
		}
	}
	return d, nil
}

// fillPicks loads the pickers of the dialog: the first results and the
// current choice (the dialog's LinkedID and ParentID) pinned in each.
func (s *server) fillPicks(r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series, d *seriesDialog) error {
	var err error
	if d.ShowLink {
		if d.LinkPick, err = s.linkPick(r, m, k, ser, "", d.LinkedID); err != nil {
			return err
		}
	}
	if d.ParentPick, err = s.parentPick(r, m, ser, "", d.ParentID); err != nil {
		return err
	}
	if d.ParentPick.Note == noteHasSpinOffs {
		d.ParentLocked = d.ParentPick.Note
	}
	return nil
}

const noteHasSpinOffs = "This series has spin-offs, so it cannot be a spin-off itself."

// linkPick lists the entries of the link kind of this plugin that match text,
// with the entry current pinned first.
func (s *server) linkPick(r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series, text string, current int64) (seriesPickView, error) {
	v := seriesPickView{Name: "linked"}
	if k.SeriesLink == nil {
		return v, nil
	}
	if current != 0 {
		v.Current = &pickItem{ID: strconv.FormatInt(current, 10), Title: s.linkedTitle(r, m, current)}
	}
	ctx := r.Context()
	rows, err := s.deps.Library.Rows(ctx, library.Query{PluginID: m.ID, Kind: k.SeriesLink.Kind, Text: text, Sort: library.Sort{Kind: library.SortTitle}}, s.titleLang(ctx, m), maxCandidates+1, 0)
	if err != nil {
		return v, err
	}
	for _, row := range rows {
		if row.Item.ID == current || len(v.Items) >= maxCandidates {
			continue
		}
		title := row.Title
		if title == "" {
			title = "(untitled)"
		}
		v.Items = append(v.Items, pickItem{ID: strconv.FormatInt(row.Item.ID, 10), Title: title, Cover: covers.Src(row.Item.CoverPath)})
	}
	return v, nil
}

// parentPick lists the series of the plugin that can be the parent of ser:
// not itself, no spin-off itself; none when ser has spin-offs. The series
// current is pinned first.
func (s *server) parentPick(r *http.Request, m plugin.Manifest, ser *library.Series, text string, current int64) (seriesPickView, error) {
	v := seriesPickView{Name: "parent"}
	ctx := r.Context()
	kids, err := s.deps.Library.SpinOffsOf(ctx, ser.ID)
	if err != nil {
		return v, err
	}
	if len(kids) > 0 {
		v.Note = noteHasSpinOffs
		return v, nil
	}
	if current != 0 {
		p, err := s.deps.Library.SeriesByID(ctx, current)
		switch {
		case err == nil:
			v.Current = &pickItem{ID: strconv.FormatInt(p.ID, 10), Title: p.Title}
		case !errors.Is(err, library.ErrNoSeries):
			return v, err
		}
	}
	rows, err := s.deps.Library.ParentCandidates(ctx, m.ID, text, maxCandidates, ser.ID, current)
	if err != nil {
		return v, err
	}
	for _, c := range rows {
		v.Items = append(v.Items, pickItem{ID: strconv.FormatInt(c.ID, 10), Title: c.Title, Cover: covers.Src(c.CoverPath)})
	}
	return v, nil
}

// seriesDialogResponse writes the dialog (htmx) or the series page with the
// dialog open (no JavaScript).
func (s *server) seriesDialogResponse(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series, d seriesDialog, code int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, code, "series", "series_dialog", d)
		return
	}
	v, err := s.buildSeries(r, m, ser)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v.Dialog = &d
	s.renderStatus(w, r, code, "series", v)
}

func (s *server) getSeriesEdit(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	k, _ := kindDef(m, ser.Kind)
	d, err := s.newSeriesDialog(r, m, k, ser)
	if err == nil {
		err = s.fillPicks(r, m, k, ser, &d)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.seriesDialogResponse(w, r, m, ser, d, http.StatusOK)
}

func (s *server) getSeriesLinkCandidates(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	k, _ := kindDef(m, ser.Kind)
	v, err := s.linkPick(r, m, k, ser, strings.TrimSpace(r.URL.Query().Get("q")), ser.LinkedItemID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.renderPartial(w, "series", "series_pick", v)
}

func (s *server) getSeriesParentCandidates(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	v, err := s.parentPick(r, m, ser, strings.TrimSpace(r.URL.Query().Get("q")), ser.ParentID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.renderPartial(w, "series", "series_pick", v)
}

// pickID reads a picker's posted id: an absent value keeps current, "" or "0"
// clears it.
func pickID(r *http.Request, name, label string, current int64) (int64, string) {
	vs, ok := r.PostForm[name]
	if !ok || len(vs) == 0 {
		return current, ""
	}
	raw := strings.TrimSpace(vs[0])
	if raw == "" || raw == "0" {
		return 0, ""
	}
	id, ok := formItemID(raw)
	if !ok {
		return 0, "Please choose the " + label + " from the list."
	}
	return id, ""
}

func (s *server) postSeriesEdit(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseItemBody(w, r) {
		return
	}
	defer cleanupForm(r)
	ctx := r.Context()
	k, _ := kindDef(m, ser.Kind)
	d, err := s.newSeriesDialog(r, m, k, ser)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	first := func(msgs ...string) string {
		for _, m := range msgs {
			if m != "" {
				return m
			}
		}
		return ""
	}
	det := library.SeriesDetails{Title: r.PostFormValue("title"), Description: r.PostFormValue("description")}
	d.Title, d.Description = det.Title, det.Description
	d.StartedOn, d.EndedOn = r.PostFormValue("started_on"), r.PostFormValue("ended_on")
	d.PartsTotal = strings.TrimSpace(r.PostFormValue("parts_total"))
	det.Finished = r.PostFormValue("finished") != ""
	d.Finished = det.Finished
	var m1, m2, m3, m4 string
	det.StartedOn, m1 = parseDay(strings.TrimSpace(d.StartedOn), "Started")
	det.EndedOn, m2 = parseDay(strings.TrimSpace(d.EndedOn), "Ended")
	if d.PartsTotal != "" {
		n, err := strconv.Atoi(d.PartsTotal)
		if err != nil || !isASCIIDigits(d.PartsTotal, 7) {
			m3 = "Expected parts must be a whole number."
		}
		det.PartsTotal = n
	}
	sk := seriesFieldKind(k)
	var vals map[string]string
	vals, det.Fields, m4 = parseFields(sk, r, &library.Item{Fields: ser.Fields})
	d.itemForm = s.newItemForm(sk, vals, &library.Item{Fields: ser.Fields})
	d.Heading, d.Submit, d.CancelURL, d.Action = "Edit series details", "Save", seriesURL(m.ID, ser.UID), seriesURL(m.ID, ser.UID)+"/edit"
	var m5, m6 string
	det.LinkedItemID, m5 = pickID(r, "linked", "linked entry", ser.LinkedItemID)
	det.ParentID, m6 = pickID(r, "parent", "parent series", ser.ParentID)
	d.LinkedID, d.ParentID = det.LinkedItemID, det.ParentID

	// the cover: an upload, else a URL, else removal, else unchanged
	det.CoverPath = ser.CoverPath
	fh := coverUpload(r)
	d.FileDropped = fh != nil
	_, urlPosted := r.PostForm["cover_url"]
	coverURL := strings.TrimSpace(r.PostFormValue("cover_url"))
	d.CoverURL = coverURL
	d.RemoveCover = r.PostFormValue("remove_cover") != ""
	if isLocalCover(ser.CoverPath) {
		d.Cover = covers.Src(ser.CoverPath)
	}
	var m7 string
	switch {
	case fh != nil:
		if s.deps.CoversDir == "" {
			m7 = "Cover upload is not available on this server."
		}
	case coverURL != "":
		if !strings.HasPrefix(coverURL, "https://") {
			m7 = "Cover URL must be an https URL like https://example.com/cover.jpg."
		}
		det.CoverPath = coverURL
	case d.RemoveCover, urlPosted && !isLocalCover(ser.CoverPath):
		det.CoverPath = ""
	}
	if msg := first(m1, m2, m3, m4, m5, m6, m7); msg != "" {
		s.seriesFail(w, r, m, k, ser, d, msg)
		return
	}
	stored := ""
	if fh != nil {
		name, err := s.storeCover(fh)
		var rej errCoverRejected
		if errors.As(err, &rej) {
			s.seriesFail(w, r, m, k, ser, d, rej.msg)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		stored, det.CoverPath = name, name
	}
	switch err := s.deps.Library.UpdateSeriesDetails(ctx, ser.ID, k, det); {
	case errors.Is(err, library.ErrInvalid):
		s.cleanupCover(ctx, stored)
		s.seriesFail(w, r, m, k, ser, d, seriesInvalidMessage(err))
		return
	case errors.Is(err, library.ErrNoSeries):
		s.cleanupCover(ctx, stored)
		s.seriesNotFound(w, r)
		return
	case err != nil:
		s.cleanupCover(ctx, stored)
		s.internalError(w, r, err)
		return
	}
	if old := ser.CoverPath; old != det.CoverPath {
		s.cleanupCover(ctx, old)
	}
	s.done(w, r, seriesURL(m.ID, ser.UID))
}

// seriesFail re-renders the dialog with the message (422).
func (s *server) seriesFail(w http.ResponseWriter, r *http.Request, m plugin.Manifest, k plugin.KindDef, ser *library.Series, d seriesDialog, msg string) {
	if err := s.fillPicks(r, m, k, ser, &d); err != nil {
		s.internalError(w, r, err)
		return
	}
	if d.FileDropped && !strings.Contains(msg, "choose the cover file again") {
		msg += " Please choose the cover file again, browsers cannot keep it."
	}
	d.Error = msg
	s.seriesDialogResponse(w, r, m, ser, d, http.StatusUnprocessableEntity)
}

// postSeriesSpinOff flips a part between the main parts and the spin-offs.
func (s *server) postSeriesSpinOff(w http.ResponseWriter, r *http.Request, m plugin.Manifest, ser *library.Series) {
	if !parseForm(w, r) {
		return
	}
	id, ok := formItemID(r.PathValue("item"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	}
	var spin bool
	switch r.PostFormValue("spin_off") {
	case "1":
		spin = true
	case "0":
	default:
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Series", "Choose whether the part is a spin-off.", "")
		return
	}
	switch err := s.deps.Library.SetPartSpinOff(r.Context(), ser.ID, id, spin); {
	case errors.Is(err, library.ErrNoSeries):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such part.", "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, seriesURL(m.ID, ser.UID), http.StatusSeeOther)
}

// seriesInvalidMessage is a library message with the column names replaced by
// the labels of the form.
func seriesInvalidMessage(err error) string {
	msg := strings.NewReplacer("parts_total", "expected parts", "started_on", "started", "ended_on", "ended").Replace(err.Error())
	return invalidMessage(errors.New(msg))
}
