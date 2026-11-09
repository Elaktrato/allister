package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
	"github.com/allister/allister/internal/titles"
)

var (
	errNoSuchAnime = errors.New("anime not found")
	errNoProvider  = fmt.Errorf("%w: no provider configured", provider.ErrUnavailable)
)

// detail is everything the overview needs.
type detail struct {
	Anime      provider.Anime
	Saved      *store.Saved
	Entry      *store.ListEntry
	Cast       []castRow
	CastFailed bool
	Recs       []provider.Recommendation
}

type personView struct {
	Name, Image, ExtURL, LocalURL string
}

type castRow struct {
	Character personView
	Actor     *personView
	Role      string
}

type titleRow struct{ Label, Text string }

type linkCard struct {
	Title, Type, Href, Cover string
	External                 bool
}

type animeView struct {
	Source, ID     string
	URL            string
	AnimeID        int64
	Saved          bool
	PartialSync    bool
	Title          string
	Titles         []titleRow
	Type, Studio   string
	SourceMaterial string
	Episodes       *int
	Aired          string
	Cover          string
	ExternalURL    string
	Paragraphs     []string
	Related        []linkCard
	Cast           []castRow
	CastFailed     bool
	Recs           []linkCard
	Entry          *store.ListEntry
	EntryStatus    string
	Dialog         *addForm
	Match          *matchView
	// CanMatch: a manual entry that can be matched to AniList; UnmatchedMAL
	// is the MAL id of an unmatched import ("" otherwise).
	CanMatch        bool
	MatchURL        string
	UnmatchedMAL    string
	AddURL, EditURL string
}

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
		s.errorPage(w, http.StatusNotFound, "Not found", "There is no such anime.", "")
		return "", "", false
	}
	return src, id, true
}

func (s *server) errorPage(w http.ResponseWriter, status int, title, msg, retry string) {
	s.renderStatus(w, status, "error", errorView{Title: title, Message: msg, RetryURL: retry})
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
		s.errorPage(w, http.StatusNotFound, "Not found", "There is no such anime.", "")
	case errors.Is(err, provider.ErrRateLimited), errors.Is(err, provider.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		s.errorPage(w, http.StatusServiceUnavailable, "Temporarily unavailable", providerMessage(err), retry)
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func cacheKey(src provider.Source, id string) string { return string(src) + "/" + id }

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

// loadDetail builds the overview data. Saved entries come from the database;
// others live from the provider (refreshing the cache). With useCache a fresh
// cache entry replaces the provider calls.
func (s *server) loadDetail(ctx context.Context, src provider.Source, id string, useCache bool) (detail, error) {
	saved, err := s.deps.Store.FindBySource(ctx, src, id)
	if err != nil {
		return detail{}, err
	}
	if saved != nil {
		d := detail{Anime: saved.Anime, Saved: saved}
		if d.Entry, err = s.deps.Store.GetListEntry(ctx, saved.ID); err != nil {
			return detail{}, err
		}
		cast, err := s.deps.Store.Cast(ctx, saved.ID)
		if err != nil {
			return detail{}, err
		}
		for _, c := range cast {
			d.Cast = append(d.Cast, savedCastRow(c))
		}
		if src == provider.AniList { // recommendations are not stored
			if live, err := s.fetchAnime(ctx, id); err == nil {
				d.Recs = live.Recommendations
			}
		}
		return d, nil
	}
	if src != provider.AniList {
		return detail{}, errNoSuchAnime
	}
	if useCache {
		if c, ok := s.cache.get(cacheKey(src, id)); ok {
			return liveDetail(c), nil
		}
	}
	a, err := s.fetchAnime(ctx, id)
	if err != nil {
		return detail{}, err
	}
	c := cachedDetail{Anime: a}
	if c.Cast, err = s.deps.Providers[provider.AniList].Cast(ctx, id); err != nil {
		log.Printf("cast %s: %v", id, err)
		c.Cast, c.CastFailed = nil, true
	}
	s.cache.put(cacheKey(src, id), c)
	return liveDetail(c), nil
}

func liveDetail(c cachedDetail) detail {
	d := detail{Anime: c.Anime, CastFailed: c.CastFailed, Recs: c.Anime.Recommendations}
	for _, m := range c.Cast {
		row := castRow{Character: livePerson(m.Character), Role: m.Role}
		if m.Actor != nil {
			p := livePerson(*m.Actor)
			row.Actor = &p
		}
		d.Cast = append(d.Cast, row)
	}
	return d
}

func livePerson(p provider.Person) personView {
	return personView{Name: p.Name, Image: httpURL(p.ImageURL), ExtURL: httpsURL(p.URL)}
}

func savedPerson(p store.Person) personView {
	return personView{Name: p.Name, Image: covers.Src(p.ImagePath), ExtURL: httpsURL(p.ExternalURL),
		LocalURL: "/people/" + strconv.FormatInt(p.ID, 10)}
}

func savedCastRow(c store.SavedCast) castRow {
	row := castRow{Character: savedPerson(c.Character), Role: c.Role}
	if c.Actor != nil {
		p := savedPerson(*c.Actor)
		row.Actor = &p
	}
	return row
}

func (s *server) buildView(ctx context.Context, src provider.Source, id string, d detail) animeView {
	lang := s.deps.Settings.TitleLang(ctx)
	a := d.Anime
	v := animeView{
		Source: string(src), ID: id, URL: animeURL(string(src), id), AddURL: addURL(string(src), id),
		Title: titles.Resolve(a.Titles, lang), Type: a.Type, Studio: a.Studio, SourceMaterial: a.SourceMaterial,
		Episodes: a.Episodes, ExternalURL: httpsURL(a.ExternalURL), Cast: d.Cast, CastFailed: d.CastFailed,
	}
	if d.Saved != nil {
		v.Saved, v.AnimeID = true, d.Saved.ID
		v.Cover = covers.Src(d.Saved.CoverPath)
		v.PartialSync = d.Saved.SyncState != "done"
		if src == provider.Manual {
			v.CanMatch, v.MatchURL, v.UnmatchedMAL = true, matchPath(string(src), id), d.Saved.MALID
		}
	} else {
		v.Cover = httpURL(a.CoverURL)
	}
	if d.Entry != nil {
		v.Entry = d.Entry
		v.EntryStatus = statusLabels[d.Entry.Status]
		v.EditURL = "/list/" + strconv.FormatInt(d.Entry.AnimeID, 10) + "/edit"
	}
	for _, t := range a.Titles {
		v.Titles = append(v.Titles, titleRow{langLabel(t.Lang), t.Text})
	}
	for _, line := range strings.Split(a.Synopsis, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			v.Paragraphs = append(v.Paragraphs, line)
		}
	}
	start, end := fmtDay(a.StartDate), fmtDay(a.EndDate)
	switch {
	case start != "" && end != "":
		v.Aired = start + " – " + end
	case start != "":
		v.Aired = start
	case end != "":
		v.Aired = "until " + end
	}
	for _, r := range a.Relations {
		if r.Source != provider.AniList || !isASCIIDigits(r.ID, maxIDDigits) {
			continue
		}
		c := linkCard{Title: titles.Resolve(r.Titles, lang), Type: r.Type}
		if r.Kind == "source" { // manga or novel: not tracked here
			c.Href, c.External = "https://anilist.co/manga/"+r.ID, true
		} else {
			c.Href = "/anime/anilist/" + r.ID
		}
		v.Related = append(v.Related, c)
	}
	for _, r := range d.Recs {
		if r.Source != provider.AniList || !isASCIIDigits(r.ID, maxIDDigits) {
			continue
		}
		v.Recs = append(v.Recs, linkCard{Title: titles.Resolve(r.Titles, lang), Href: "/anime/anilist/" + r.ID, Cover: httpURL(r.CoverURL)})
	}
	return v
}

func fmtDay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func (s *server) getOverview(w http.ResponseWriter, r *http.Request) {
	src, id, ok := s.route(w, r)
	if !ok {
		return
	}
	s.showOverview(w, r, src, id, nil, http.StatusOK)
}

// showOverview renders the full page, with the add dialog open when dialog is set.
func (s *server) showOverview(w http.ResponseWriter, r *http.Request, src provider.Source, id string, dialog *addForm, status int) {
	d, err := s.loadDetail(r.Context(), src, id, dialog != nil)
	if err != nil {
		s.fail(w, r, err, animeURL(string(src), id))
		return
	}
	v := s.buildView(r.Context(), src, id, d)
	if dialog != nil {
		f := *dialog
		if f.Episodes == nil {
			f.Episodes = d.Anime.Episodes
		}
		f = f.withStatuses()
		v.Dialog = &f
	}
	s.renderStatus(w, status, "anime", v)
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
	if c, ok := s.cache.get(cacheKey(src, id)); ok {
		return c.Anime, nil, nil
	}
	a, err := s.fetchAnime(ctx, id)
	return a, nil, err
}

// dialogResponse writes the dialog (partial) or the full overview with the
// dialog open (no JavaScript).
func (s *server) dialogResponse(w http.ResponseWriter, r *http.Request, src provider.Source, id string, f addForm, status int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, status, "anime", "add_dialog", f.withStatuses())
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
			s.renderPartialStatus(w, http.StatusServiceUnavailable, "anime", "add_dialog", f.withStatuses())
			return
		}
		s.errorPage(w, http.StatusServiceUnavailable, "Temporarily unavailable", f.Error, animeURL(string(src), id))
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
			s.renderPartialStatus(w, http.StatusConflict, "anime", "add_dialog", f.withStatuses())
			return
		}
	}
	s.renderPartial(w, "anime", "add_dialog", f.withStatuses())
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
	if c, ok := s.cache.get(cacheKey(src, id)); ok {
		return c.Anime.Episodes
	}
	return nil
}

func (s *server) enqueueSync(ctx context.Context, animeID int64) {
	if s.deps.Jobs == nil {
		log.Printf("enqueue sync for anime %d: no job queue", animeID)
		return
	}
	if err := s.deps.Jobs.Enqueue(ctx, jobs.KindSyncAnime, jobs.SyncPayload{AnimeID: animeID}); err != nil {
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
