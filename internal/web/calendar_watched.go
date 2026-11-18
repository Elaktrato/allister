package web

// POST /list/{id}/watched: the calendar's Mark watched button. The item is
// guarded like every entry route (itemPluginGuard); the library decides
// whether the release may be marked.

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/library"
)

// calendarReturn accepts only a same-site path (one leading "/", never "//",
// no backslash or control character, which browsers read as another host or
// a header split) and falls back to /calendar.
func calendarReturn(raw string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\#") {
		return "/calendar"
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f || c == ' ' {
			return "/calendar"
		}
	}
	return raw
}

// postWatched marks the posted release (season, number) of the item watched.
// htmx gets the refreshed calendar grid, a plain POST a 303 back to the
// calendar. A release that is not a stored, aired airing of an active entry
// answers 422 (for htmx from the grid: the grid again with the message, as
// htmx swaps no error status); an item without an entry 404; progress already at or beyond
// the release changes nothing and still answers success.
func (s *server) postWatched(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	season, err1 := strconv.Atoi(r.PostFormValue("season"))
	number, err2 := strconv.Atoi(r.PostFormValue("number"))
	if err1 != nil || err2 != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	_, err := s.deps.Library.MarkReleaseWatched(ctx, ref.ID, season, number, s.now())
	ret := calendarReturn(r.PostFormValue("return"))
	grid := isPartialRequest(r) && r.Header.Get("HX-Target") == "calendar-grid"
	notice := ""
	switch {
	case err == nil:
	case grid && errors.Is(err, library.ErrNoEntry):
		notice = "Could not mark it watched: this entry is no longer in your list."
	case grid && errors.Is(err, library.ErrInvalid):
		// htmx swaps no error status: show the message above the refreshed grid
		notice = "Could not mark it watched: " + invalidMessage(err)
	case errors.Is(err, library.ErrNoEntry):
		s.entryNotFound(w, r)
		return
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusUnprocessableEntity, "Cannot mark watched", invalidMessage(err), "")
		return
	default:
		s.internalError(w, r, err)
		return
	}
	if !grid {
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	// the grid of the period the button was on
	var q url.Values
	if u, err := url.Parse(ret); err == nil && u.Path == "/calendar" {
		q = u.Query()
	}
	v, err := s.buildCalendar(ctx, q)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	v.Notice = notice
	s.renderPartial(w, "calendar", "calendar_grid", v)
}
