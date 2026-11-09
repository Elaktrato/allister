package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
	"github.com/allister/allister/internal/titles"
)

// matchView is the data of the match dialog (match_dialog.html).
type matchView struct {
	// Action posts a match; SearchURL serves the results partial; CancelURL
	// is the overview (cancel link without JavaScript).
	Action, SearchURL, CancelURL string
	// Title is the entry's resolved title, MALID its MAL id ("" if none).
	Title, MALID string
	// Anilist echoes the URL/number field.
	Anilist string
	// Error is a user-facing message; ExistingURL links the entry that
	// already holds the chosen AniList anime.
	Error, ExistingURL string
	Search             searchView
}

func matchPath(source, id string) string { return animeURL(source, id) + "/match" }

// matchTarget loads the saved manual entry the request is about; it writes a
// 404 page and returns nil when there is none (unknown, not saved, or not manual).
func (s *server) matchTarget(w http.ResponseWriter, r *http.Request) (provider.Source, string, *store.Saved) {
	src, id, ok := s.route(w, r)
	if !ok {
		return "", "", nil
	}
	var saved *store.Saved
	if src == provider.Manual {
		var err error
		if saved, err = s.deps.Store.FindBySource(r.Context(), src, id); err != nil {
			s.fail(w, r, err, "")
			return "", "", nil
		}
	}
	if saved == nil {
		s.errorPage(w, http.StatusNotFound, "Not found", "There is no such manual entry to match.", "")
		return "", "", nil
	}
	return src, id, saved
}

func (s *server) newMatchView(r *http.Request, src provider.Source, id string, saved *store.Saved) matchView {
	return matchView{
		Action: matchPath(string(src), id), SearchURL: matchPath(string(src), id) + "/search",
		CancelURL: animeURL(string(src), id),
		Title:     titles.Resolve(saved.Titles, s.deps.Settings.TitleLang(r.Context())),
		MALID:     saved.MALID,
	}
}

// matchResponse writes the dialog (htmx) or the overview page with the dialog
// open (no JavaScript).
func (s *server) matchResponse(w http.ResponseWriter, r *http.Request, src provider.Source, id string, v matchView, status int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "anime", "match_dialog", v)
		return
	}
	d, err := s.loadDetail(r.Context(), src, id, false)
	if err != nil {
		s.fail(w, r, err, animeURL(string(src), id))
		return
	}
	av := s.buildView(r.Context(), src, id, d)
	av.Match = &v
	s.renderStatus(w, status, "anime", av)
}

func (s *server) getMatch(w http.ResponseWriter, r *http.Request) {
	src, id, saved := s.matchTarget(w, r)
	if saved == nil {
		return
	}
	s.matchResponse(w, r, src, id, s.newMatchView(r, src, id, saved), http.StatusOK)
}

func (s *server) getMatchSearch(w http.ResponseWriter, r *http.Request) {
	src, id, saved := s.matchTarget(w, r)
	if saved == nil {
		return
	}
	v := s.newMatchView(r, src, id, saved)
	q := cleanQuery(r.URL.Query().Get("q"))
	v.Search.Query = q
	if q != "" { // an empty query makes no AniList request
		s.runSearch(r, q, &v.Search)
		for i := range v.Search.Results {
			v.Search.Results[i].CoverURL = httpURL(v.Search.Results[i].CoverURL)
		}
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "anime", "match_results", v)
		return
	}
	s.matchResponse(w, r, src, id, v, http.StatusOK)
}

// plainAnimeID accepts a search pick: 1 to 9 digits, no leading zero.
func plainAnimeID(s string) bool {
	return len(s) <= 9 && s != "" && s[0] != '0' && isDigits(s)
}

func (s *server) parseAniListInput(raw string) (string, bool) {
	if s.deps.ParseAnimeURL == nil {
		return raw, plainAnimeID(raw)
	}
	return s.deps.ParseAnimeURL(raw)
}

func (s *server) postMatch(w http.ResponseWriter, r *http.Request) {
	src, id, saved := s.matchTarget(w, r)
	if saved == nil {
		return
	}
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	v := s.newMatchView(r, src, id, saved)
	pick := strings.TrimSpace(r.PostFormValue("pick"))
	raw := strings.TrimSpace(r.PostFormValue("anilist"))
	v.Anilist = raw
	fail := func(status int, msg string) {
		v.Error = msg
		s.matchResponse(w, r, src, id, v, status)
	}

	// 1. Reduce the input to an AniList id (pick wins over the URL field).
	var aid string
	switch {
	case pick != "":
		if !plainAnimeID(pick) {
			fail(http.StatusUnprocessableEntity, "That search result is not a valid AniList id.")
			return
		}
		aid = pick
	case raw == "":
		fail(http.StatusUnprocessableEntity, "Paste an AniList URL or number, or pick a search result.")
		return
	default:
		var ok bool
		if aid, ok = s.parseAniListInput(raw); !ok {
			fail(http.StatusUnprocessableEntity, "That does not look like an AniList anime URL or id. Example: https://anilist.co/anime/154587")
			return
		}
	}

	// 2. Fetch the details first: a provider failure never touches the database.
	a, err := s.fetchAnime(ctx, aid)
	switch {
	case err == nil:
	case errors.Is(err, provider.ErrNotFound):
		fail(http.StatusUnprocessableEntity, "AniList has no anime with that id.")
		return
	case errors.Is(err, provider.ErrRateLimited), errors.Is(err, provider.ErrUnavailable):
		log.Printf("match %s: fetch %s: %v", id, aid, err)
		fail(http.StatusServiceUnavailable, providerMessage(err)+" Nothing was changed.")
		return
	default:
		s.fail(w, r, err, "")
		return
	}

	// 3. Convert in one transaction.
	err = s.deps.Store.ConvertToSource(ctx, saved.ID, a)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrAlreadySaved):
		msg := "That anime is already in your library, so nothing was changed."
		var ase *store.AlreadySavedError
		if errors.As(err, &ase) {
			if ex, gerr := s.deps.Store.GetByID(ctx, ase.ExistingID); gerr == nil && ex != nil {
				v.ExistingURL = animeURL(string(ex.Source), ex.Anime.ID)
			}
		}
		fail(http.StatusUnprocessableEntity, msg)
		return
	case errors.Is(err, store.ErrInvalid):
		fail(http.StatusUnprocessableEntity, invalidMessage(err))
		return
	case errors.Is(err, store.ErrNotFound): // deleted meanwhile
		s.errorPage(w, http.StatusNotFound, "Not found", "There is no such manual entry to match.", "")
		return
	default:
		s.fail(w, r, err, "")
		return
	}

	s.enqueueSync(ctx, saved.ID)
	dest := "/anime/anilist/" + url.PathEscape(a.ID)
	if isPartialRequest(r) {
		w.Header().Set("HX-Redirect", dest)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
