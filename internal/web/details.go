package web

// Schema-driven item forms. The manual create form (manual.go) and "Edit
// details" (GET|POST /{plugin}/{source}/{id}/edit, this file) are generated
// from the kind's schema: one typed input per Editable field, genres and
// tags when the kind enables them as editable, and a cover upload. Both
// parse the same inputs with the same rules; user input is validated and
// REJECTED when invalid, never silently altered (beyond trimming surrounding
// whitespace, dropping blank list lines and duplicate list values).
//
// Edit details changes the item's own data only (library.UpdateDetails):
// titles, editable fields, genres, tags and the cover. The list entry
// (status, rating, progress, dates, comment) is never touched. Edits are
// local: nothing overwrites them except an explicit match of a manual item.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

const (
	// maxItemBody caps a manual create or edit request (the cover image
	// itself is capped at 10 MiB by covers.Save); maxItemMemory is the part
	// kept in memory while parsing.
	maxItemBody   = 12 << 20
	maxItemMemory = 1 << 20
	// maxTitleRunes is the longest title; maxLineRunes the longest one-line
	// text field, maxLongRunes the longest text-section field (the library's
	// limit).
	maxTitleRunes = 300
	maxLineRunes  = 1000
	maxLongRunes  = 20000
	// maxListValues is the most values of a list field; maxTitleRows the
	// most title rows of the edit form.
	maxListValues = 50
	maxTitleRows  = 50
	// fieldPrefix is the form name prefix of a field: field.<key>.
	fieldPrefix = "field."
)

// fieldInput is one typed input of a schema-generated form. Kind is the
// input shape: "text", "textarea", "number", "date", "enum", "list" or "url".
type fieldInput struct {
	Name, Label, Value, Kind string
	Min, Max, Unit           string
	Options                  []statusOpt
	Required                 bool
}

// itemForm is the schema-generated part shared by manual_form.html and
// details_form.html (item_inputs.html renders it).
type itemForm struct {
	Heading, Action, CancelURL, Submit string
	// Error is a user-facing message; FileDropped says that a chosen cover
	// file was not kept (browsers cannot refill file inputs).
	Error       string
	FileDropped bool
	Fields      []fieldInput
	// Genres and Tags are the raw texts of the term inputs, shown only when
	// the kind enables them as editable.
	ShowGenres, ShowTags   bool
	GenresLabel, TagsLabel string
	Genres, Tags           string
	CoversEnabled          bool
	// Cover is the current cover's URL (edit); RemoveCover echoes the
	// "remove the cover" checkbox.
	Cover       string
	RemoveCover bool
}

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

func termsEditable(td plugin.TermDef) bool { return td.Enabled && td.Editable }

// newItemForm builds the schema part of a form for kind k from raw values
// (keyed by field key).
func (s *server) newItemForm(k plugin.KindDef, vals map[string]string) itemForm {
	f := itemForm{CoversEnabled: s.deps.CoversDir != "",
		ShowGenres: termsEditable(k.Genres), GenresLabel: orDefault(k.Genres.Label, "Genres"),
		ShowTags: termsEditable(k.Tags), TagsLabel: orDefault(k.Tags.Label, "Tags")}
	for _, fd := range k.Fields {
		if !fd.Editable {
			continue
		}
		in := fieldInput{Name: fieldPrefix + fd.Key, Label: fd.Label, Value: vals[fd.Key], Kind: string(fd.Type), Required: fd.Required}
		switch fd.Type {
		case plugin.FieldNumber:
			if fd.Min != nil {
				in.Min = fmtNum(*fd.Min)
			}
			if fd.Max != nil {
				in.Max = fmtNum(*fd.Max)
			}
			in.Unit = fd.Unit
		case plugin.FieldEnum:
			for _, ev := range fd.EnumValues {
				in.Options = append(in.Options, statusOpt{Value: ev, Label: ev, Selected: ev == in.Value})
			}
		case plugin.FieldText:
			if isTextSectionField(k, fd.Key) {
				in.Kind = "textarea"
			}
		}
		f.Fields = append(f.Fields, in)
	}
	return f
}

// fieldFormValue formats a stored value as the form posts it back.
func fieldFormValue(fd plugin.FieldDef, v plugin.FieldValue) string {
	switch fd.Type {
	case plugin.FieldNumber:
		if v.Num != nil {
			return fmtNum(*v.Num)
		}
		return ""
	case plugin.FieldDate:
		return fmtDay(v.Date)
	case plugin.FieldList:
		return strings.Join(v.List, "\n")
	}
	return v.Text
}

// numberRe is the accepted number syntax: an optional minus, up to 15
// digits and up to 6 decimals (no exponent, no hex, no Inf/NaN).
var numberRe = regexp.MustCompile(`^-?[0-9]{1,15}(\.[0-9]{1,6})?$`)

// numberMessage explains the bounds of a number field.
func numberMessage(fd plugin.FieldDef, syntax bool) string {
	switch {
	case fd.Min != nil && fd.Max != nil:
		return fmt.Sprintf("%s must be a number from %s to %s.", fd.Label, fmtNum(*fd.Min), fmtNum(*fd.Max))
	case syntax:
		return fd.Label + " must be a number."
	case fd.Min != nil:
		return fmt.Sprintf("%s must be at least %s.", fd.Label, fmtNum(*fd.Min))
	case fd.Max != nil:
		return fmt.Sprintf("%s must be at most %s.", fd.Label, fmtNum(*fd.Max))
	}
	return fd.Label + " must be a number."
}

// isSingleLine reports whether s has no line break or tab.
func isSingleLine(s string) bool { return !strings.ContainsAny(s, "\r\n\t") }

// parseField parses one raw (trimmed, non-empty) value by the field's type.
func parseField(k plugin.KindDef, fd plugin.FieldDef, raw string) (plugin.FieldValue, string) {
	switch fd.Type {
	case plugin.FieldNumber:
		if !numberRe.MatchString(raw) {
			return plugin.FieldValue{}, numberMessage(fd, true)
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return plugin.FieldValue{}, numberMessage(fd, true)
		}
		if (fd.Min != nil && n < *fd.Min) || (fd.Max != nil && n > *fd.Max) {
			return plugin.FieldValue{}, numberMessage(fd, false)
		}
		return plugin.FieldValue{Num: &n}, ""
	case plugin.FieldDate:
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return plugin.FieldValue{}, fd.Label + " must be a date like 2024-01-31."
		}
		return plugin.FieldValue{Date: &t}, ""
	case plugin.FieldEnum:
		if !slices.Contains(fd.EnumValues, raw) {
			return plugin.FieldValue{}, "Please choose a " + lowerFirst(fd.Label) + " from the list."
		}
		return plugin.FieldValue{Text: raw}, ""
	case plugin.FieldList:
		var out []string
		seen := map[string]bool{}
		for _, line := range strings.Split(raw, "\n") {
			v := strings.TrimSpace(line) // also drops the \r of CRLF
			if v == "" || seen[v] {
				continue
			}
			if utf8.RuneCountInString(v) > maxLineRunes {
				return plugin.FieldValue{}, fmt.Sprintf("Each value of %s must be at most %d characters.", fd.Label, maxLineRunes)
			}
			seen[v] = true
			out = append(out, v)
		}
		if len(out) > maxListValues {
			return plugin.FieldValue{}, fmt.Sprintf("%s can have at most %d values.", fd.Label, maxListValues)
		}
		return plugin.FieldValue{List: out}, ""
	case plugin.FieldURL:
		u, err := url.Parse(raw)
		if err != nil || !isSingleLine(raw) || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return plugin.FieldValue{}, fd.Label + " must be an http(s) URL like https://example.com/."
		}
		return plugin.FieldValue{Text: raw}, ""
	}
	limit := maxLineRunes
	if isTextSectionField(k, fd.Key) {
		limit = maxLongRunes
	} else if !isSingleLine(raw) {
		return plugin.FieldValue{}, fd.Label + " must be a single line."
	}
	if utf8.RuneCountInString(raw) > limit {
		return plugin.FieldValue{}, fmt.Sprintf("%s must be at most %d characters.", fd.Label, limit)
	}
	return plugin.FieldValue{Text: raw}, ""
}

// parseFields reads every editable field of k (field.<key>), returning the
// raw values for the echo and the typed values. A posted field.<key> that is
// unknown or not editable is refused, a required field must have a value.
// msg is the first problem ("" when valid).
func parseFields(k plugin.KindDef, r *http.Request) (vals map[string]string, fields map[string]plugin.FieldValue, msg string) {
	vals, fields = map[string]string{}, map[string]plugin.FieldValue{}
	var posted []string
	for name := range r.PostForm {
		if key, ok := strings.CutPrefix(name, fieldPrefix); ok {
			posted = append(posted, key)
		}
	}
	sort.Strings(posted)
	for _, key := range posted {
		if fd, ok := fieldDef(k, key); !ok || !fd.Editable {
			msg = "“" + truncateRunes(key, 32) + "” is not a field of this item."
			break
		}
	}
	for _, fd := range k.Fields {
		if !fd.Editable {
			continue
		}
		raw := strings.TrimSpace(r.PostFormValue(fieldPrefix + fd.Key))
		vals[fd.Key] = raw
		if msg != "" {
			continue // echo only
		}
		if raw == "" {
			if fd.Required {
				if fd.Type == plugin.FieldEnum {
					msg = "Please choose a " + lowerFirst(fd.Label) + "."
				} else {
					msg = "Please enter " + lowerFirst(fd.Label) + "."
				}
			}
			continue
		}
		v, m := parseField(k, fd, raw)
		if m != "" {
			msg = m
			continue
		}
		if fd.Type == plugin.FieldList && len(v.List) == 0 {
			continue
		}
		fields[fd.Key] = v
	}
	return vals, fields, msg
}

// parseGenres splits the genres text at commas and line breaks.
func parseGenres(raw string) []string {
	var out []string
	for _, g := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' }) {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// tagRankRe is a rank suffix attempt after a tag's last colon.
var tagRankRe = regexp.MustCompile(`^[+-]?[0-9]+$`)

// parseTags reads one tag per line: "name", "name:rank" (a whole number 0
// to 100 after the last colon) and an optional trailing "!" for a spoiler,
// for example "Time Travel:80!". A colon not followed by a number is part of
// the name ("Re:Zero").
func parseTags(raw string) ([]plugin.Tag, string) {
	var out []plugin.Tag
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		t := plugin.Tag{}
		if rest, ok := strings.CutSuffix(line, "!"); ok {
			t.Spoiler, line = true, strings.TrimSpace(rest)
		}
		if i := strings.LastIndex(line, ":"); i >= 0 && tagRankRe.MatchString(strings.TrimSpace(line[i+1:])) {
			n, ok := parseWhole(strings.TrimSpace(line[i+1:]), 3)
			if !ok || n > 100 {
				return nil, "A tag rank must be a whole number from 0 to 100, like Time Travel:80."
			}
			rank := int(n)
			t.Rank, line = &rank, strings.TrimSpace(line[:i])
		}
		if line == "" {
			return nil, "Every tag needs a name."
		}
		t.Name = line
		out = append(out, t)
	}
	return out, ""
}

// formatTags writes tags in the syntax parseTags reads.
func formatTags(tags []plugin.Tag) string {
	lines := make([]string, 0, len(tags))
	for _, t := range tags {
		l := t.Name
		if t.Rank != nil {
			l += ":" + strconv.Itoa(*t.Rank)
		}
		if t.Spoiler {
			l += "!"
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// parseTerms reads the genres and tags inputs of k. Terms posted for a kind
// that does not enable them as editable are refused.
func parseTerms(k plugin.KindDef, r *http.Request, f *itemForm) (genres []string, tags []plugin.Tag, msg string) {
	f.Genres, f.Tags = strings.TrimSpace(r.PostFormValue("genres")), strings.TrimSpace(r.PostFormValue("tags"))
	switch {
	case f.Genres != "" && !f.ShowGenres:
		return nil, nil, "This item has no genres to edit."
	case f.Tags != "" && !f.ShowTags:
		return nil, nil, "This item has no tags to edit."
	}
	tags, msg = parseTags(f.Tags)
	return parseGenres(f.Genres), tags, msg
}

// parseItemBody limits and parses a manual create or edit body (multipart,
// or urlencoded as a fallback). On failure it has written 413 or 400 and
// returns false. The caller removes the multipart temp files (cleanupForm).
func parseItemBody(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxItemBody)
	err := r.ParseMultipartForm(maxItemMemory)
	if err == nil || errors.Is(err, http.ErrNotMultipart) {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "bad request", http.StatusBadRequest)
	}
	return false
}

func cleanupForm(r *http.Request) {
	if r.MultipartForm != nil {
		r.MultipartForm.RemoveAll()
	}
}

// coverUpload returns the uploaded cover file, if any.
func coverUpload(r *http.Request) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	if fhs := r.MultipartForm.File["cover"]; len(fhs) > 0 {
		return fhs[0]
	}
	return nil
}

// errCoverRejected marks a cover upload refused by its content; the message
// is user-facing.
type errCoverRejected struct{ msg string }

func (e errCoverRejected) Error() string { return e.msg }

// storeCover writes the uploaded cover (content-sniffed by covers.Save) and
// returns its stored base name. A refused image is an errCoverRejected.
func (s *server) storeCover(fh *multipart.FileHeader) (string, error) {
	file, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	name, err := covers.Save(s.deps.CoversDir, file)
	switch {
	case errors.Is(err, covers.ErrNotImage):
		return "", errCoverRejected{"The cover must be a JPEG, PNG, WebP or GIF image. Please choose the cover file again."}
	case errors.Is(err, covers.ErrTooLarge):
		return "", errCoverRejected{"The cover image must be smaller than 10 MiB. Please choose the cover file again."}
	}
	return name, err
}

// isLocalCover reports whether a stored cover value names a file in the
// covers directory (a plain base name, not a remote URL).
func isLocalCover(name string) bool {
	return name != "" && !strings.HasPrefix(name, "http://") && !strings.HasPrefix(name, "https://") &&
		!strings.ContainsAny(name, `/\:`) && !strings.Contains(name, "..") && !strings.HasPrefix(name, ".")
}

// cleanupCover removes a cover file that no row references any more (after
// a failed save, or the cover an edit replaced or removed). Content-hash
// names are shared, so a file another item or person still references
// stays. If the check or removal fails the orphaned file is harmless and
// only logged. Residual race: a save of the same content between the check
// and the removal loses its file (it re-downloads or is re-uploaded).
func (s *server) cleanupCover(ctx context.Context, name string) {
	if s.deps.CoversDir == "" || !isLocalCover(name) {
		return
	}
	used, err := s.deps.Library.ImageReferenced(ctx, name)
	if err != nil {
		log.Printf("cover cleanup %s: %v", name, err)
		return
	}
	if used {
		return
	}
	if err := os.Remove(filepath.Join(s.deps.CoversDir, name)); err != nil && !os.IsNotExist(err) {
		log.Printf("cover cleanup %s: %v", name, err)
	}
}

// langChoices are the title language options of a kind, sel selected.
func langChoices(langs []string, sel string) []statusOpt {
	out := make([]statusOpt, 0, len(langs))
	for _, l := range langs {
		out = append(out, statusOpt{Value: l, Label: langLabel(l), Selected: l == sel})
	}
	return out
}

// itemAction is the URL of an item action (/{plugin}/{source}/{id}/{action})
// with ?kind= when the plugin has several kinds plus the extra parameters.
func itemAction(m plugin.Manifest, source, id, kind, action string, extra url.Values) string {
	u := itemPath(m.ID, source, id) + "/" + action
	q := url.Values{}
	if len(m.Kinds) > 1 && kind != "" {
		q.Set("kind", kind)
	}
	for k, v := range extra {
		q[k] = v
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// returnToItem is the redirect target after an edit: raw when it is exactly
// the item's overview (overview, a same-site path with ?kind= only for
// plugins with several kinds) and passes safeReturn's checks, else the
// overview. Every other value (the list, other items, other plugins,
// schemes, "//", backslashes, fragments, escapes, control characters) is
// refused.
func returnToItem(raw, overview string) string {
	pid, _, _ := strings.Cut(strings.TrimPrefix(overview, "/"), "/")
	if raw != overview || safeReturn(raw, pid) != raw {
		return overview
	}
	return raw
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
	f := detailsForm{itemForm: s.newItemForm(t.kind, vals), Titles: rows, Return: t.overview}
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
	f.Genres = strings.Join(t.item.Genres, ", ")
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
	vals, fields, fmsg := parseFields(t.kind, r)
	f := s.detailsForm(t, vals, rows)
	ret := returnToItem(r.PostFormValue("return"), t.overview)
	f.Return = ret
	genres, tags, tmsg := parseTerms(t.kind, r, &f.itemForm)
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
	u := library.DetailsUpdate{Titles: titles, Fields: fields, Genres: genres, Tags: tags, CoverPath: t.item.CoverPath}
	if f.RemoveCover {
		u.CoverPath = ""
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
		stored, u.CoverPath = name, name
	}
	if err := s.deps.Library.UpdateDetails(ctx, t.item.ID, u); err != nil {
		s.cleanupCover(ctx, stored)
		if msg := s.detailsError(w, r, t, err); msg != "" {
			fail(msg)
		}
		return
	}
	if old := t.item.CoverPath; old != u.CoverPath {
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
