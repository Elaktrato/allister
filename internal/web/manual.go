package web

// The generic manual create form: GET|POST /{plugin}/manual/new?kind=. The
// form is generated from the kind's schema (details.go: title and title
// language, every Editable field, genres and tags, a cover upload) plus the
// list entry through the shared entry parser (parseEntryForm). Everything is
// validated before anything is written; the item and its entry are saved in
// ONE transaction (library.AddToList) with sync state done and no sync job.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/ids"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// manualForm is the data of manual_form.html; the string fields echo the
// user's input.
type manualForm struct {
	itemForm
	entryValues
	Title, TitleLang string
	Langs            []statusOpt
	Statuses         []statusOpt
	Progress         []progressInput
	// Kinds links the form of every kind (only when there are several).
	Kinds []choice
}

// manualPath is the manual form URL of kind k (with ?kind= when the plugin
// has several kinds).
func manualPath(m plugin.Manifest, k string) string {
	u := "/" + url.PathEscape(m.ID) + "/manual/new"
	if len(m.Kinds) > 1 {
		u += "?kind=" + url.QueryEscape(k)
	}
	return u
}

// manualKind resolves the plugin and kind of a manual form request (the
// first kind unless ?kind= names another); unknown kinds get the 404 page.
func (s *server) manualKind(w http.ResponseWriter, r *http.Request, pluginID string) (plugin.Manifest, plugin.KindDef, bool) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such page.", "")
		return m, plugin.KindDef{}, false
	}
	k := m.Kinds[0]
	if raw := r.URL.Query().Get("kind"); raw != "" {
		if k, ok = kindDef(m, raw); !ok {
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such page.", "")
			return m, k, false
		}
	}
	return m, k, true
}

// titleLangs are the kind's title languages (never empty).
func titleLangs(k plugin.KindDef) []string {
	if len(k.TitleLanguages) == 0 {
		return []string{"en"}
	}
	return k.TitleLanguages
}

// newManualForm builds the form of kind k: title language the preferred one
// when the kind offers it, else the kind's first; status planned.
func (s *server) newManualForm(r *http.Request, m plugin.Manifest, k plugin.KindDef, vals map[string]string, v entryValues) manualForm {
	langs := titleLangs(k)
	f := manualForm{itemForm: s.newItemForm(k, vals, nil), entryValues: v, TitleLang: langs[0]}
	if pref := s.deps.Settings.TitleLang(r.Context()); slices.Contains(langs, pref) {
		f.TitleLang = pref
	}
	f.Heading, f.Submit = "Add "+lowerFirst(k.Label)+" manually", "Add"
	f.Action, f.CancelURL = manualPath(m, k.Key), "/"+url.PathEscape(m.ID)
	if f.Status == "" {
		f.Status = string(library.Planned)
	}
	if len(m.Kinds) > 1 {
		for _, x := range m.Kinds {
			f.Kinds = append(f.Kinds, choice{Value: manualPath(m, x.Key), Label: x.Label, Selected: x.Key == k.Key})
		}
	}
	return f
}

// finish sets the parts that depend on the (possibly posted) values.
func (f *manualForm) finish(k plugin.KindDef, fields map[string]plugin.FieldValue) {
	f.Langs = langChoices(titleLangs(k), f.TitleLang)
	f.Statuses = nil
	for _, st := range library.Statuses() {
		f.Statuses = append(f.Statuses, statusOpt{Value: string(st), Label: statusLabel(k, st), Selected: string(st) == f.Status})
	}
	f.Progress = progressInputs(k, progressContext{Keys: k.Progress.Defaults, Fields: fields}, f.entryValues.Progress)
}

func (s *server) getManualNew(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, k, ok := s.manualKind(w, r, pluginID)
	if !ok {
		return
	}
	f := s.newManualForm(r, m, k, nil, entryValues{})
	f.finish(k, nil)
	s.render(w, r, "manual_form", f)
}

// postManualNew validates the title, the fields (type, bounds, enums,
// lists), the terms, the entry (parseEntryForm, then against the typed
// totals), library.ValidateItem (required fields and every storage rule)
// and the cover options BEFORE writing anything; then it stores the cover
// (content-sniffed) and saves item and entry in one transaction. A failed
// save removes the stored cover file unless another row references it.
func (s *server) postManualNew(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, k, ok := s.manualKind(w, r, pluginID)
	if !ok || !parseItemBody(w, r) {
		return
	}
	defer cleanupForm(r)
	ctx := r.Context()
	vals, fields, msg := parseFields(k, r, nil)
	ev, entry, emsg := s.parseEntryForm(k, k.Progress.Defaults, r)
	f := s.newManualForm(r, m, k, vals, ev)
	f.Title = strings.TrimSpace(r.PostFormValue("title"))
	if l := strings.TrimSpace(r.PostFormValue("title_lang")); l != "" {
		f.TitleLang = l
	}
	genres, tags, tmsg := parseTerms(k, r, &f.itemForm, nil)
	fh := coverUpload(r)
	f.FileDropped = fh != nil
	f.finish(k, fields)
	switch {
	case f.Title == "":
		msg = "Please enter a title."
	case utf8.RuneCountInString(f.Title) > maxTitleRunes:
		msg = fmt.Sprintf("Title must be at most %d characters.", maxTitleRunes)
	case !slices.Contains(titleLangs(k), f.TitleLang):
		msg = "Please choose a title language from the list."
	}
	for _, m := range []string{tmsg, emsg} {
		if msg == "" {
			msg = m
		}
	}
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: manualSource, ID: "new", Kind: k.Key,
			Titles: []plugin.Title{{Lang: f.TitleLang, Text: f.Title}}, Fields: fields},
		Genres: genres, Tags: tags,
	}
	if msg == "" {
		if err := s.deps.Library.ValidateItem(m.ID, k.Key, d); err != nil {
			msg = invalidMessage(err)
		}
	}
	if msg == "" {
		msg = s.validateAgainstItem(k, progressContext{Keys: k.Progress.Defaults, Fields: fields}, entry)
	}
	if msg == "" && fh != nil && s.deps.CoversDir == "" {
		msg = "Cover upload is not available on this server."
	}
	fail := func(msg string) {
		if f.FileDropped && !strings.Contains(msg, "choose the cover file again") {
			msg += " Please choose the cover file again, browsers cannot keep it."
		}
		f.Error = msg
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "manual_form", f)
	}
	if msg != "" {
		fail(msg)
		return
	}

	// Everything is valid: store the cover (content-checked), then the rows.
	if fh != nil {
		name, err := s.storeCover(fh)
		var rej errCoverRejected
		if errors.As(err, &rej) {
			fail(rej.msg)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		d.CoverURL = name
	}
	id, err := ids.NewManualID()
	if err != nil {
		s.cleanupCover(ctx, d.CoverURL)
		s.internalError(w, r, err)
		return
	}
	d.ID = id
	// No sync job: manual items have nothing to sync.
	if _, err := s.deps.Library.AddToList(ctx, m.ID, k.Key, d, entry, library.WithSyncState("done")); err != nil {
		s.cleanupCover(ctx, d.CoverURL)
		if errors.Is(err, library.ErrInvalid) {
			fail(invalidMessage(err))
			return
		}
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, itemURL(m, manualSource, id, k.Key), http.StatusSeeOther)
}
