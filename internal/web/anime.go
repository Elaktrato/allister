package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/plugins/anime/animemap"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

var (
	errNoSuchAnime = errors.New("anime not found")
	errNoProvider  = fmt.Errorf("%w: no provider configured", provider.ErrUnavailable)
)

type errorView struct {
	Title, Message, RetryURL string
}

func animeURL(source, id string) string {
	return "/anime/" + url.PathEscape(source) + "/" + url.PathEscape(id)
}
func addURL(source, id string) string { return animeURL(source, id) + "/add" }

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

// route validates the path wildcards; it writes a 404 page and returns false
// for an unknown source or malformed id.
func (s *server) route(w http.ResponseWriter, r *http.Request) (provider.Source, string, bool) {
	src, id := provider.Source(r.PathValue("source")), r.PathValue("id")
	switch {
	case src == provider.AniList && isASCIIDigits(id, maxIDDigits):
	case src == provider.Manual && id != "" && len(id) <= 64:
	default:
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such anime.", "")
		return "", "", false
	}
	return src, id, true
}

func (s *server) errorPage(w http.ResponseWriter, r *http.Request, status int, title, msg, retry string) {
	s.renderStatus(w, r, status, "error", errorView{Title: title, Message: msg, RetryURL: retry})
}

func providerMessage(err error) string {
	if errors.Is(err, provider.ErrRateLimited) {
		return "AniList is rate limiting requests, please wait a moment and try again."
	}
	return "AniList is unavailable right now, please try again."
}

// fail writes the error page for a load error.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error, retry string) {
	switch {
	case errors.Is(err, errNoSuchAnime), errors.Is(err, provider.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such anime.", "")
	case errors.Is(err, provider.ErrRateLimited), errors.Is(err, provider.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", providerMessage(err), retry)
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *server) fetchAnime(ctx context.Context, id string) (provider.Anime, error) {
	p := s.deps.Providers[provider.AniList]
	if p == nil {
		return provider.Anime{}, errNoProvider
	}
	a, err := p.Anime(ctx, id)
	if err != nil {
		return provider.Anime{}, err
	}
	a.Source, a.ID = provider.AniList, id
	return a, nil
}

// showOverview renders the generic item page of the anime, with the add
// dialog open when dialog is set (served from a fresh cache entry then).
func (s *server) showOverview(w http.ResponseWriter, r *http.Request, src provider.Source, id string, dialog *addForm, status int) {
	s.showItem(w, r, itemRef{Plugin: animePluginID, Source: string(src), ID: id}, itemExtras{Dialog: dialog, UseCache: dialog != nil}, status)
}

// animeLiveKey is the live cache key of an AniList anime.
func animeLiveKey(src provider.Source, id string) string {
	return liveKey(animePluginID, string(src), anime.KindAnime, id)
}

// cachedAnime returns the fresh cached overview data of the anime in the
// legacy shape.
func (s *server) cachedAnime(src provider.Source, id string) (provider.Anime, bool) {
	c, ok := s.cache.get(animeLiveKey(src, id))
	if !ok {
		return provider.Anime{}, false
	}
	a := animemap.LegacyAnime(c.Data)
	a.Source, a.ID = src, id
	return a, true
}

// resolveAnime returns the anime for the add flow: saved row, fresh cache
// entry, or one provider call (not cached).
func (s *server) resolveAnime(ctx context.Context, src provider.Source, id string) (provider.Anime, *store.Saved, error) {
	saved, err := s.deps.Store.FindBySource(ctx, src, id)
	if err != nil {
		return provider.Anime{}, nil, err
	}
	if saved != nil {
		return saved.Anime, saved, nil
	}
	if src != provider.AniList {
		return provider.Anime{}, nil, errNoSuchAnime
	}
	if a, ok := s.cachedAnime(src, id); ok {
		return a, nil, nil
	}
	a, err := s.fetchAnime(ctx, id)
	return a, nil, err
}

// dialogResponse writes the dialog (partial) or the full overview with the
// dialog open (no JavaScript).
func (s *server) dialogResponse(w http.ResponseWriter, r *http.Request, src provider.Source, id string, f addForm, status int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "item", "add_dialog", f.withStatuses())
		return
	}
	s.showOverview(w, r, src, id, &f, status)
}

// resolveFailure handles a resolveAnime error in the add flow: provider
// outages re-render the dialog (keeping the form), the rest use error pages.
func (s *server) resolveFailure(w http.ResponseWriter, r *http.Request, src provider.Source, id string, f addForm, err error) {
	if errors.Is(err, provider.ErrRateLimited) || errors.Is(err, provider.ErrUnavailable) {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		f.Error = providerMessage(err)
		if isPartialRequest(r) {
			s.renderPartialStatus(w, http.StatusServiceUnavailable, "item", "add_dialog", f.withStatuses())
			return
		}
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", f.Error, animeURL(string(src), id))
		return
	}
	s.fail(w, r, err, animeURL(string(src), id))
}

func (s *server) getAdd(w http.ResponseWriter, r *http.Request) {
	src, id, ok := s.route(w, r)
	if !ok {
		return
	}
	f := newAddForm(string(src), id)
	if !isPartialRequest(r) {
		s.showOverview(w, r, src, id, &f, http.StatusOK)
		return
	}
	a, saved, err := s.resolveAnime(r.Context(), src, id)
	if err != nil {
		s.resolveFailure(w, r, src, id, f, err)
		return
	}
	f.Episodes = a.Episodes
	if saved != nil {
		if en, err := s.deps.Store.GetListEntry(r.Context(), saved.ID); err != nil {
			s.fail(w, r, err, "")
			return
		} else if en != nil {
			f.Error, f.ExistingURL = "This anime is already in your list.", animeURL(string(src), id)
			s.renderPartialStatus(w, http.StatusConflict, "item", "add_dialog", f.withStatuses())
			return
		}
	}
	s.renderPartial(w, "item", "add_dialog", f.withStatuses())
}

func (s *server) postAdd(w http.ResponseWriter, r *http.Request) {
	src, id, ok := s.route(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	f, entry, msg := parseEntryForm(r, newAddForm(string(src), id))
	// Show the known episode total (no provider call) on validation errors.
	if msg != "" {
		f.Episodes, f.Error = s.peekEpisodes(ctx, src, id), msg
		s.dialogResponse(w, r, src, id, f, http.StatusUnprocessableEntity)
		return
	}
	a, saved, err := s.resolveAnime(ctx, src, id)
	if err != nil {
		s.resolveFailure(w, r, src, id, f, err)
		return
	}
	f.Episodes = a.Episodes
	if saved == nil {
		animeID, err := s.deps.Store.AddToList(ctx, a, entry)
		switch {
		case err == nil:
			s.enqueueSync(ctx, animeID)
			s.addDone(w, r)
			return
		case errors.Is(err, store.ErrInvalid):
			f.Error = invalidMessage(err)
			s.dialogResponse(w, r, src, id, f, http.StatusUnprocessableEntity)
			return
		case errors.Is(err, store.ErrAlreadySaved): // saved concurrently
			if saved, err = s.deps.Store.FindBySource(ctx, src, id); err != nil || saved == nil {
				s.fail(w, r, errors.Join(errors.New("add: re-read after duplicate"), err), "")
				return
			}
		default:
			s.fail(w, r, err, "")
			return
		}
	}
	s.addToExisting(w, r, src, id, saved, entry, f)
}

// addToExisting gives an already saved anime its list entry (no re-save, no job).
func (s *server) addToExisting(w http.ResponseWriter, r *http.Request, src provider.Source, id string, saved *store.Saved, entry store.ListEntry, f addForm) {
	ctx := r.Context()
	existing, err := s.deps.Store.GetListEntry(ctx, saved.ID)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	if existing != nil {
		f.Error, f.ExistingURL = "This anime is already in your list.", animeURL(string(src), id)
		s.dialogResponse(w, r, src, id, f, http.StatusConflict)
		return
	}
	entry.AnimeID = saved.ID
	if err := s.deps.Store.UpsertListEntry(ctx, entry); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			f.Episodes, f.Error = saved.Episodes, invalidMessage(err)
			s.dialogResponse(w, r, src, id, f, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, r, err, "")
		return
	}
	s.addDone(w, r)
}

func (s *server) peekEpisodes(ctx context.Context, src provider.Source, id string) *int {
	if sv, err := s.deps.Store.FindBySource(ctx, src, id); err == nil && sv != nil {
		return sv.Episodes
	}
	if a, ok := s.cachedAnime(src, id); ok {
		return a.Episodes
	}
	return nil
}

func (s *server) enqueueSync(ctx context.Context, animeID int64) {
	if s.deps.Jobs == nil {
		log.Printf("enqueue sync for anime %d: no job queue", animeID)
		return
	}
	if err := s.deps.Jobs.Enqueue(ctx, jobs.KindSyncAnime, jobs.SyncPayload{AnimeID: animeID, PluginID: animePluginID}); err != nil {
		log.Printf("enqueue sync for anime %d: %v", animeID, err)
	}
}

// addDone redirects to the list: htmx would swap a 303 target into the
// dialog, so it gets HX-Redirect with 204.
func (s *server) addDone(w http.ResponseWriter, r *http.Request) {
	if isPartialRequest(r) {
		w.Header().Set("HX-Redirect", "/anime")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/anime", http.StatusSeeOther)
}
