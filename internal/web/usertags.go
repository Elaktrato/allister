package web

// Settings > Tags: the owner's own tags (shared by every plugin) with their
// entry counts. GET /settings/tags lists them; POST /settings/tags/{id}
// renames one (onto an existing name, case-insensitively, it merges into
// that tag); POST /settings/tags/{id}/delete removes one from every entry
// and deletes it. Tags are set per entry in the entry dialog.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/allister/allister/internal/library"
)

// settingsTagsPage is the data of settings_tags.html.
type settingsTagsPage struct {
	Tags  []tagRow
	Error string
}

// tagRow is one tag of the page: its rename form's value (as stored, or as
// just typed when the rename was refused) and its entry count.
type tagRow struct {
	ID, Name, Value, Count string
	RenameURL, DeleteURL   string
	Confirm                string
}

// tagsPage builds the page; failed names the tag whose refused rename
// (typed) is shown in its input.
func (s *server) tagsPage(r *http.Request, failed int64, typed, msg string) (settingsTagsPage, error) {
	tags, err := s.deps.Library.UserTags(r.Context())
	if err != nil {
		return settingsTagsPage{}, err
	}
	p := settingsTagsPage{Error: msg}
	for _, tg := range tags {
		id := strconv.FormatInt(tg.ID, 10)
		row := tagRow{ID: id, Name: tg.Name, Value: tg.Name, RenameURL: "/settings/tags/" + id, DeleteURL: "/settings/tags/" + id + "/delete",
			Confirm: "Delete the tag “" + tg.Name + "”? It is removed from every entry."}
		if tg.ID == failed {
			row.Value = typed
		}
		row.Count = strconv.Itoa(tg.Count) + " entries"
		if tg.Count == 1 {
			row.Count = "1 entry"
		}
		p.Tags = append(p.Tags, row)
	}
	return p, nil
}

func (s *server) getTags(w http.ResponseWriter, r *http.Request) {
	p, err := s.tagsPage(r, 0, "", "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "settings_tags", p)
}

// tagID reads the {id} of a tag route; a malformed one is not found.
func (s *server) tagID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.tagNotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *server) tagNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such tag.", "")
}

func (s *server) postRenameTag(w http.ResponseWriter, r *http.Request) {
	id, ok := s.tagID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	name := r.PostFormValue("name")
	switch err := s.deps.Library.RenameUserTag(r.Context(), id, name); {
	case err == nil:
		http.Redirect(w, r, "/settings/tags", http.StatusSeeOther)
	case errors.Is(err, library.ErrNoUserTag):
		s.tagNotFound(w, r)
	case errors.Is(err, library.ErrInvalid):
		p, perr := s.tagsPage(r, id, name, invalidMessage(err))
		if perr != nil {
			s.internalError(w, r, perr)
			return
		}
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings_tags", p)
	default:
		s.internalError(w, r, err)
	}
}

func (s *server) postDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := s.tagID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	switch err := s.deps.Library.DeleteUserTag(r.Context(), id); {
	case err == nil:
		s.done(w, r, "/settings/tags")
	case errors.Is(err, library.ErrNoUserTag):
		s.tagNotFound(w, r)
	default:
		s.internalError(w, r, err)
	}
}
