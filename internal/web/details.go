package web

// "Edit details": GET|POST /{plugin}/{source}/{id}/edit for every saved item
// (manual, unmatched, matched, sourced). It changes the item's own data only
// (library.UpdateDetails): titles, editable fields, genres, tags and the
// cover. The list entry (status, rating, progress, dates, comment) is never
// touched. Edits are local: nothing overwrites them except an explicit match
// of a manual item. The form is built by itemform.go.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// titleInput is one title row of the edit form: a fixed language (hidden
// input) or, with Choose, the free row with a language select.
type titleInput struct {
	Lang, Label, Text string
	Choose            bool
	Langs             []statusOpt
}

// detailsForm is the data of details_form.html.
type detailsForm struct {
	itemForm
	Titles []titleInput
	Return string
}

// detailsTarget is the saved item of an edit request.
type detailsTarget struct {
	man      plugin.Manifest
	kind     plugin.KindDef
	ref      itemRef // Kind resolved
	item     *library.Item
	overview string
}

// loadDetails resolves the saved item of the request (database only); it
// writes the 404 page for malformed, unknown and unsaved items.
func (s *server) loadDetails(w http.ResponseWriter, r *http.Request, pluginID string) (*detailsTarget, bool) {
	m, ref, ok := s.addRef(w, r, pluginID)
	if !ok {
		return nil, false
	}
	it, k, err := s.findSaved(r.Context(), m, ref)
	if err == nil && it == nil {
		err = errNoSuchItem
	}
	if err != nil {
		s.itemFail(w, r, m, ref.Source, err, "")
		return nil, false
	}
	ref.Kind = k.Key
	return &detailsTarget{man: m, kind: k, ref: ref, item: it, overview: itemURL(m, ref.Source, ref.ID, k.Key)}, true
}

// form builds the edit form of t from raw field values and title rows.
func (s *server) detailsForm(t *detailsTarget, vals map[string]string, rows []titleInput) detailsForm {
	f := detailsForm{itemForm: s.newItemForm(t.kind, vals, t.item), Titles: rows, Return: t.overview}
	f.Heading, f.Submit, f.CancelURL = "Edit details", "Save", t.overview
	f.Action = itemAction(t.man, t.ref.Source, t.ref.ID, t.ref.Kind, "edit", nil)
	f.Cover = covers.Src(t.item.CoverPath)
	f.Titles = append(f.Titles, titleInput{Label: "Add a title", Choose: true, Langs: langChoices(t.kind.TitleLanguages, "")})
	return f
}

// storedRows are the edit form's title rows of the stored item: one per
// stored title, then an empty one per kind language without a title.
func storedRows(k plugin.KindDef, it *library.Item) []titleInput {
	var rows []titleInput
	have := map[string]bool{}
	for _, tl := range it.Titles {
		rows = append(rows, titleInput{Lang: tl.Lang, Label: langLabel(tl.Lang), Text: tl.Text})
		have[tl.Lang] = true
	}
	for _, l := range k.TitleLanguages {
		if !have[l] {
			rows = append(rows, titleInput{Lang: l, Label: langLabel(l)})
		}
	}
	return rows
}

func (s *server) getDetails(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.loadDetails(w, r, pluginID)
	if !ok {
		return
	}
	vals := map[string]string{}
	for _, fd := range t.kind.Fields {
		if fd.Editable {
			vals[fd.Key] = fieldFormValue(fd, t.item.Fields[fd.Key])
		}
	}
	f := s.detailsForm(t, vals, storedRows(t.kind, t.item))
	f.Genres = formatGenres(t.item.Genres)
	f.Tags = formatTags(t.item.Tags)
	s.render(w, r, "details_form", f)
}

// parseTitleRows reads the posted title rows (parallel title_lang and title
// values). Languages must be the kind's or ones the item already has; empty
// rows remove that title; at least one title must remain.
func parseTitleRows(k plugin.KindDef, it *library.Item, r *http.Request) ([]titleInput, []plugin.Title, string) {
	langs, texts := r.PostForm["title_lang"], r.PostForm["title"]
	n := min(len(langs), len(texts), maxTitleRows)
	rows := make([]titleInput, 0, n)
	for i := 0; i < n; i++ {
		l := strings.TrimSpace(langs[i])
		rows = append(rows, titleInput{Lang: l, Label: langLabel(l), Text: strings.TrimSpace(texts[i])})
	}
	switch {
	case len(langs) != len(texts):
		return rows, nil, "The title rows are incomplete, please reload the page."
	case len(langs) > maxTitleRows:
		return rows, nil, fmt.Sprintf("An item can have at most %d titles.", maxTitleRows)
	}
	allowed := map[string]bool{}
	for _, l := range k.TitleLanguages {
		allowed[l] = true
	}
	for _, tl := range it.Titles {
		allowed[tl.Lang] = true
	}
	var out []plugin.Title
	for _, row := range rows {
		if row.Text == "" {
			continue
		}
		if !allowed[row.Lang] {
			return rows, nil, "Please choose a title language from the list."
		}
		if utf8.RuneCountInString(row.Text) > maxTitleRunes {
			return rows, nil, fmt.Sprintf("Title must be at most %d characters.", maxTitleRunes)
		}
		out = append(out, plugin.Title{Lang: row.Lang, Text: row.Text})
	}
	if len(out) == 0 {
		return rows, nil, "Please keep at least one title."
	}
	return rows, out, ""
}

// postDetails validates the whole form (titles, fields, terms, cover
// options, then library.ValidateDetails) before anything is written; only
// then is an uploaded cover stored and library.UpdateDetails called. A
// failed save removes the just-stored file; a replaced or removed cover file
// is deleted when nothing references it any more. Success redirects to the
// item's overview (returnToItem).
func (s *server) postDetails(w http.ResponseWriter, r *http.Request, pluginID string) {
	t, ok := s.loadDetails(w, r, pluginID)
	if !ok || !parseItemBody(w, r) {
		return
	}
	defer cleanupForm(r)
	ctx := r.Context()
	rows, titles, msg := parseTitleRows(t.kind, t.item, r)
	vals, fields, fmsg := parseFields(t.kind, r, t.item)
	f := s.detailsForm(t, vals, rows)
	ret := returnToItem(r.PostFormValue("return"), t.overview)
	f.Return = ret
	genres, tags, tmsg := parseTerms(t.kind, r, &f.itemForm, t.item)
	f.RemoveCover = r.PostFormValue("remove_cover") != ""
	fh := coverUpload(r)
	f.FileDropped = fh != nil
	for _, m := range []string{fmsg, tmsg} {
		if msg == "" {
			msg = m
		}
	}
	if msg == "" && fh != nil && s.deps.CoversDir == "" {
		msg = "Cover upload is not available on this server."
	}
	// the cover is only written when the user uploads or removes one, so a
	// value read now is never written back stale
	u := library.DetailsUpdate{Titles: titles, Fields: fields, Genres: genres, Tags: tags}
	if f.RemoveCover {
		u.CoverPath = new(string)
	}
	if msg == "" {
		if err := s.deps.Library.ValidateDetails(ctx, t.item.ID, u); err != nil {
			msg = s.detailsError(w, r, t, err)
			if msg == "" {
				return
			}
		}
	}
	fail := func(msg string) {
		if f.FileDropped && !strings.Contains(msg, "choose the cover file again") {
			msg += " Please choose the cover file again, browsers cannot keep it."
		}
		f.Error = msg
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "details_form", f)
	}
	if msg != "" {
		fail(msg)
		return
	}
	stored := ""
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
		stored, u.CoverPath = name, &name
	}
	if err := s.deps.Library.UpdateDetails(ctx, t.item.ID, u); err != nil {
		s.cleanupCover(ctx, stored)
		if msg := s.detailsError(w, r, t, err); msg != "" {
			fail(msg)
		}
		return
	}
	if old := t.item.CoverPath; u.CoverPath != nil && old != *u.CoverPath {
		s.cleanupCover(ctx, old)
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

// detailsError maps a library error of an edit: ErrInvalid is a message for
// the form; a vanished item gets the 404 page and anything else 500 (both
// written here, "" returned).
func (s *server) detailsError(w http.ResponseWriter, r *http.Request, t *detailsTarget, err error) string {
	switch {
	case errors.Is(err, library.ErrInvalid):
		return invalidMessage(err)
	case errors.Is(err, library.ErrNoEntry):
		s.itemFail(w, r, t.man, t.ref.Source, errNoSuchItem, "")
	default:
		s.internalError(w, r, err)
	}
	return ""
}
