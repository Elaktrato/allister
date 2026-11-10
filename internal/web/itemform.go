package web

// Schema-driven item forms, shared by the manual create form (manual.go)
// and "Edit details" (details.go): one typed input per Editable field of the
// kind, genres and tags when the kind enables them as editable, and a cover
// upload. User input is validated and REJECTED when invalid, never silently
// altered (beyond trimming surrounding whitespace, dropping blank lines and
// duplicate list values). In an edit, a value the user did not change (the
// posted text equals what the form rendered for the stored value) is kept
// verbatim and skips the form-only syntax and limits.

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

func termsEditable(td plugin.TermDef) bool { return td.Enabled && td.Editable }

// hasValue reports whether a stored field value is set.
func hasValue(v plugin.FieldValue) bool {
	return strings.TrimSpace(v.Text) != "" || v.Num != nil || v.Date != nil || len(v.List) > 0
}

// requiredFor reports whether a required field must have a value: always
// when creating (it == nil); in an edit only when the item has one stored
// (a source may lack a required field, which must not block other edits).
func requiredFor(fd plugin.FieldDef, it *library.Item) bool {
	return fd.Required && (it == nil || hasValue(it.Fields[fd.Key]))
}

// normalizeNewlines turns the CRLF line breaks browsers submit into LF.
func normalizeNewlines(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// newItemForm builds the schema part of a form for kind k from raw values
// (keyed by field key); it is the item an edit starts from (nil when
// creating). A text field whose value has a line break is a textarea (an
// input would drop it), and a stored enum value outside the kind's values is
// offered too, so an untouched value posts back unchanged.
func (s *server) newItemForm(k plugin.KindDef, vals map[string]string, it *library.Item) itemForm {
	f := itemForm{CoversEnabled: s.deps.CoversDir != "",
		ShowGenres: termsEditable(k.Genres), GenresLabel: orDefault(k.Genres.Label, "Genres"),
		ShowTags: termsEditable(k.Tags), TagsLabel: orDefault(k.Tags.Label, "Tags")}
	for _, fd := range k.Fields {
		if !fd.Editable {
			continue
		}
		in := fieldInput{Name: fieldPrefix + fd.Key, Label: fd.Label, Value: vals[fd.Key], Kind: string(fd.Type), Required: requiredFor(fd, it)}
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
			if in.Value != "" && !slices.Contains(fd.EnumValues, in.Value) {
				in.Options = append(in.Options, statusOpt{Value: in.Value, Label: in.Value, Selected: true})
			}
			for _, ev := range fd.EnumValues {
				in.Options = append(in.Options, statusOpt{Value: ev, Label: ev, Selected: ev == in.Value})
			}
		case plugin.FieldText:
			if isTextSectionField(k, fd.Key) || strings.ContainsAny(in.Value, "\r\n") {
				in.Kind = "textarea"
			}
		}
		f.Fields = append(f.Fields, in)
	}
	return f
}

// fieldFormValue renders a stored value for its input. It is THE rendering:
// the edit form shows it and the POST compares the posted text with it to
// tell an untouched value (kept verbatim) from a changed one.
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
// raw values for the echo and the typed values. it is the item an edit
// starts from (nil when creating): a posted value equal to the rendering of
// its stored value (fieldFormValue, CRLF normalised) keeps the stored value
// verbatim without the form-only syntax and limits; every other value is
// parsed as user input. A posted field.<key> that is unknown or not
// editable is refused; a required field must have a value (requiredFor).
// msg is the first problem ("" when valid).
func parseFields(k plugin.KindDef, r *http.Request, it *library.Item) (vals map[string]string, fields map[string]plugin.FieldValue, msg string) {
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
		raw := normalizeNewlines(r.PostFormValue(fieldPrefix + fd.Key))
		vals[fd.Key] = raw
		if msg != "" {
			continue // echo only
		}
		if it != nil {
			if sv, ok := it.Fields[fd.Key]; ok && hasValue(sv) && raw == fieldFormValue(fd, sv) {
				fields[fd.Key] = sv // untouched: kept verbatim
				continue
			}
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			if requiredFor(fd, it) {
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

// parseGenres reads one genre per line (a genre may contain commas).
func parseGenres(raw string) []string {
	var out []string
	for _, g := range strings.Split(raw, "\n") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// formatGenres renders genres for their textarea, one per line.
func formatGenres(genres []string) string { return strings.Join(genres, "\n") }

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

// formatTags renders tags for their textarea in the syntax parseTags reads.
// A stored name that itself ends in "!" or ":<number>" would read back
// differently, which is why an untouched tags text keeps the stored tags
// verbatim (parseTerms).
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

// sameText reports whether a posted textarea text equals the rendering of
// the stored value (surrounding whitespace aside).
func sameText(posted, rendered string) bool {
	return posted == rendered || strings.TrimSpace(posted) == rendered
}

// parseTerms reads the genres and tags inputs of k. Terms posted for a kind
// that does not enable them as editable are refused. In an edit (it set) a
// text equal to the rendering of the stored terms keeps them verbatim; an
// edited text follows the documented syntax.
func parseTerms(k plugin.KindDef, r *http.Request, f *itemForm, it *library.Item) (genres []string, tags []plugin.Tag, msg string) {
	f.Genres, f.Tags = normalizeNewlines(r.PostFormValue("genres")), normalizeNewlines(r.PostFormValue("tags"))
	switch {
	case strings.TrimSpace(f.Genres) != "" && !f.ShowGenres:
		return nil, nil, "This item has no genres to edit."
	case strings.TrimSpace(f.Tags) != "" && !f.ShowTags:
		return nil, nil, "This item has no tags to edit."
	}
	if it != nil && f.ShowGenres && sameText(f.Genres, formatGenres(it.Genres)) {
		genres = slices.Clone(it.Genres)
	} else {
		genres = parseGenres(f.Genres)
	}
	if it != nil && f.ShowTags && sameText(f.Tags, formatTags(it.Tags)) {
		return genres, slices.Clone(it.Tags), ""
	}
	tags, msg = parseTags(f.Tags)
	return genres, tags, msg
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
