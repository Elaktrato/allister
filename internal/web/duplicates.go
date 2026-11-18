package web

// The possible duplicates page: GET /{plugin}/duplicates lists the pairs
// library.Duplicates finds (a shared external id or the same title and
// year); "Not a duplicate" (POST /{plugin}/duplicates/dismiss) hides a pair
// for good and "Compare & merge" links to the merge page.

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

// duplicateSide is one item of a pair.
type duplicateSide struct {
	Title, Href, Cover, Source, Status string
	manual                             bool
}

// duplicateRow is one pair with the reason text and its two actions.
type duplicateRow struct {
	A, B      duplicateSide
	AID, BID  string
	Reason    string
	MergeURL  string
	DismissTo string
}

// duplicatesView is the page's data.
type duplicatesView struct {
	Heading, ListURL string
	Rows             []duplicateRow
}

// duplicatesURL is the plugin's duplicates page.
func duplicatesURL(pluginID string) string {
	return "/" + url.PathEscape(pluginID) + "/duplicates"
}

// duplicateReason is the text of a pair's reason: the plugin's own name of
// the external id type (an importer's label) or the type in capitals, and
// "same title and year" for a kind with a year field.
func duplicateReason(m plugin.Manifest, k plugin.KindDef, reason string) string {
	typ, ok := strings.CutPrefix(reason, "id:")
	if !ok {
		if _, has := fieldDef(k, "year"); has {
			return "same title and year"
		}
		return "same title"
	}
	for _, im := range m.Importers {
		if im.ExternalIDType == typ && im.ExternalIDLabel != "" {
			return "same " + im.ExternalIDLabel
		}
	}
	return "same " + strings.ToUpper(typ)
}

// errItemGone is duplicateSideOf's answer for an item deleted since the
// pairs were read.
var errItemGone = errors.New("item gone")

// duplicateSideOf builds one item of a pair (errItemGone for an item that
// no longer exists).
func (s *server) duplicateSideOf(r *http.Request, m plugin.Manifest, id int64) (duplicateSide, plugin.KindDef, error) {
	it, err := s.deps.Library.Item(r.Context(), id)
	if err != nil {
		return duplicateSide{}, plugin.KindDef{}, err
	}
	if it == nil {
		return duplicateSide{}, plugin.KindDef{}, errItemGone
	}
	k, ok := kindDef(m, it.Kind)
	if !ok {
		k = m.Kinds[0]
	}
	e, err := s.deps.Library.Entry(r.Context(), id)
	if err != nil {
		return duplicateSide{}, k, err
	}
	title := library.ResolveTitle(it.Titles, s.titleLang(r.Context(), m), titleFallback(m, k, it.Kind))
	if title == "" {
		title = "(untitled)"
	}
	sd := duplicateSide{Title: title, Href: itemURL(m, it.Source, it.SourceID, it.Kind), Cover: covers.Src(it.CoverPath),
		Source: "Added by hand", Status: "not in your library", manual: it.Source == manualSource}
	if it.Source != manualSource {
		sd.Source = sourceName(m, it.Source)
	}
	if e != nil {
		sd.Status = statusLabel(k, e.Status)
	}
	return sd, k, nil
}

// duplicateCount is the number of pairs the plugin's duplicates page shows.
func (s *server) duplicateCount(r *http.Request, pluginID string) (int, error) {
	pairs, err := s.deps.Library.Duplicates(r.Context(), pluginID)
	return len(pairs), err
}

func (s *server) getDuplicates(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	pairs, err := s.deps.Library.Duplicates(r.Context(), pluginID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows, err := s.duplicateRows(r, m, pairs)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "duplicates", duplicatesView{Heading: "Possible duplicates", ListURL: "/" + url.PathEscape(pluginID), Rows: rows})
}

// duplicateRows builds the rows of the pairs; a pair whose item was
// deleted meanwhile is left out.
func (s *server) duplicateRows(r *http.Request, m plugin.Manifest, pairs []library.DuplicatePair) ([]duplicateRow, error) {
	var rows []duplicateRow
	for _, p := range pairs {
		a, k, err := s.duplicateSideOf(r, m, p.A)
		if errors.Is(err, errItemGone) {
			continue
		}
		if err != nil {
			return nil, err
		}
		b, _, err := s.duplicateSideOf(r, m, p.B)
		if errors.Is(err, errItemGone) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ida, idb := strconv.FormatInt(p.A, 10), strconv.FormatInt(p.B, 10)
		rows = append(rows, duplicateRow{A: a, B: b, AID: ida, BID: idb, Reason: duplicateReason(m, k, p.Reason),
			MergeURL: mergeURL(m.ID, p.A, p.B, a.manual, b.manual)})
	}
	return rows, nil
}

func (s *server) postDuplicateDismiss(w http.ResponseWriter, r *http.Request, pluginID string) {
	if _, ok := s.deps.Registry.Manifest(pluginID); !ok || !parseForm(w, r) {
		if !ok {
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		}
		return
	}
	var ids [2]int64
	for i, name := range []string{"a", "b"} {
		id, err := strconv.ParseInt(r.PostFormValue(name), 10, 64)
		if err != nil || id <= 0 {
			s.errorPage(w, r, http.StatusUnprocessableEntity, "Possible duplicates", "Both items are needed.", duplicatesURL(pluginID))
			return
		}
		owner, err := s.deps.Library.ItemPluginID(r.Context(), id)
		if err != nil || owner != pluginID {
			s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
			return
		}
		ids[i] = id
	}
	switch err := s.deps.Library.DismissDuplicate(r.Context(), ids[0], ids[1]); {
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Possible duplicates", invalidMessage(err), duplicatesURL(pluginID))
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, duplicatesURL(pluginID), http.StatusSeeOther)
	}
}
