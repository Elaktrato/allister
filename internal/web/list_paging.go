package web

// The list paging setting (Settings > General): the list pages load more
// rows while scrolling (the default) or split them into pages.

import (
	"context"
	"errors"
	"net/http"

	"github.com/allister/allister/internal/settings"
)

// listPaging is the effective list paging (scroll without a settings store).
func (s *server) listPaging(ctx context.Context) string {
	if s.deps.Settings == nil {
		return settings.ListPagingScroll
	}
	return s.deps.Settings.ListPaging(ctx)
}

// scrollLists reports whether the lists load more while scrolling.
func (s *server) scrollLists(ctx context.Context) bool {
	return s.listPaging(ctx) == settings.ListPagingScroll
}

// postListPaging stores the list paging (POST /settings/list-paging, field
// list_paging: scroll or pages); anything else is refused with 422.
func (s *server) postListPaging(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	v := r.PostForm.Get("list_paging")
	if !settings.ValidListPaging(v) {
		view := s.generalView(r, s.timeZoneForm(r.Context(), "", ""))
		view["ListPagingError"] = "Choose how lists load: while scrolling or in pages."
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings", view)
		return
	}
	if s.deps.Settings == nil {
		s.internalError(w, r, errors.New("no settings store"))
		return
	}
	if err := s.deps.Settings.SetListPaging(r.Context(), v); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
