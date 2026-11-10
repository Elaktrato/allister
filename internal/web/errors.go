package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

type errorView struct {
	Title, Message, RetryURL string
}

// maxIDDigits is the longest numeric id accepted in a URL or form field.
const maxIDDigits = 12

// isASCIIDigits reports whether s is a non-empty run of ASCII digits no longer
// than maxLen; maxLen <= 0 means any length.
func isASCIIDigits(s string, maxLen int) bool {
	if s == "" || (maxLen > 0 && len(s) > maxLen) {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (s *server) errorPage(w http.ResponseWriter, r *http.Request, status int, title, msg, retry string) {
	s.renderStatus(w, r, status, "error", errorView{Title: title, Message: msg, RetryURL: retry})
}

// unavailableMessage words a temporary source failure for the error page.
func unavailableMessage(err error) string {
	if errors.Is(err, plugin.ErrRateLimited) {
		return "The source is rate limiting requests, please wait a moment and try again."
	}
	return "The source is unavailable right now, please try again."
}

// fail writes the error page for a load error.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error, retry string) {
	switch {
	case errors.Is(err, plugin.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", unavailableMessage(err), retry)
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// unmatchedHint explains an imported manual item that its importer's
// external id did not resolve ("" when the item carries no such id).
//
// TEMPORARY: the anime plugin keeps the legacy sentence (asserted by the
// match tests) until the importer becomes generic (Task 11); other plugins
// get a neutral one built from the importer's ExternalIDType.
func unmatchedHint(m plugin.Manifest, it *library.Item) string {
	for _, imp := range m.Importers {
		v := it.ExternalIDs[imp.ExternalIDType]
		if imp.ExternalIDType == "" || v == "" {
			continue
		}
		if m.ID == animePluginID {
			return "Imported from Yamtrack; AniList had no match for MyAnimeList #" + v + "."
		}
		return "Imported without a match; " + imp.ExternalIDType + " " + v
	}
	return ""
}
