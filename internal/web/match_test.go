package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

// matchProvider is the sample provider with a counted search.
type matchProvider struct {
	*animeProvider
	mu        sync.Mutex
	searches  []string
	searchErr error
	results   []provider.SearchResult
}

func (m *matchProvider) Search(ctx context.Context, q string) ([]provider.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searches = append(m.searches, q)
	return m.results, m.searchErr
}

// localParse is a tiny stand-in for anilist.ParseAnimeURL (the web package must not import it).
func localParse(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if isASCIIDigits(s, maxIDDigits) {
		return s, true
	}
	for _, p := range []string{"https://anilist.co/anime/", "anilist.co/anime/"} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			id, _, _ := strings.Cut(rest, "/")
			if isASCIIDigits(id, maxIDDigits) {
				return id, true
			}
		}
	}
	return "", false
}

type matchEnv struct {
	*env
	mp *matchProvider
}

func newMatchEnv(t *testing.T) *matchEnv {
	t.Helper()
	e := newEnv(t)
	mp := &matchProvider{animeProvider: e.fp, results: []provider.SearchResult{e.fp.anime.SearchResult}}
	_, e.h = newServer(Deps{
		Settings: e.set, Store: e.st, Jobs: e.jobs, SyncJobs: testSyncJobs(), CoversDir: e.covers, ParseAnimeURL: localParse,
		Providers: map[provider.Source]provider.Provider{provider.AniList: mp},
	}, func() time.Time { return *e.now })
	return &matchEnv{env: e, mp: mp}
}

// unmatched saves an import-like manual entry with list data and returns its anime id.
func (e *matchEnv) unmatched(t *testing.T) int64 {
	t.Helper()
	m := provider.Anime{SearchResult: provider.SearchResult{Source: provider.Manual, ID: "man1", Type: "TV",
		Titles: []provider.Title{{Lang: "en", Text: "Lost Show <b>"}}, CoverURL: "oldcover.jpg"}, MALID: "5114"}
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	rating := 0
	id, err := e.st.AddToList(context.Background(), m,
		store.ListEntry{Status: store.Completed, Rating: &rating, Progress: 3, StartedOn: &start, Comment: "c"},
		store.WithSyncState("pending"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *matchEnv) snapshot(t *testing.T, id int64) (*store.Saved, *store.ListEntry) {
	t.Helper()
	sv, err := e.st.GetByID(context.Background(), id)
	if err != nil || sv == nil {
		t.Fatalf("get: %v %v", sv, err)
	}
	en, _ := e.st.GetListEntry(context.Background(), id)
	return sv, en
}

const matchURL = "/anime/manual/man1/match"

func TestMatchDialogOnlyForManual(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	e.saved(t) // anilist 1535
	rec := e.do("GET", matchURL, nil, true)
	if rec.Code != 200 {
		t.Fatalf("manual: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `name="anilist"`, `name="q"`, `hx-get="/anime/manual/man1/match/search"`,
		`keyup changed delay:400ms, search`, "MyAnimeList #5114", "Lost Show &lt;b&gt;", `id="match-results"`)
	mustNotContain(t, rec.Body.String(), "<html", "<b>")
	if rec.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("vary %q", rec.Header().Get("Vary"))
	}
	if c := e.get("/anime/anilist/1535/match").Code; c != 404 {
		t.Fatalf("anilist entry: %d", c)
	}
	if c := e.do("GET", "/anime/anilist/1535/match", nil, true).Code; c != 404 {
		t.Fatalf("anilist entry htmx: %d", c)
	}
	if c := e.get("/anime/manual/nope/match").Code; c != 404 {
		t.Fatalf("unknown: %d", c)
	}
	if c := e.get("/anime/anilist/999/match").Code; c != 404 {
		t.Fatalf("unsaved anilist: %d", c)
	}
	if c := e.get("/anime/mal/5/match").Code; c != 404 {
		t.Fatalf("bad source: %d", c)
	}
	if c := e.do("POST", "/anime/anilist/1535/match", url.Values{"pick": {"1535"}}, true).Code; c != 404 {
		t.Fatalf("post anilist entry: %d", c)
	}
}

func TestMatchDialogWithoutJSIsFullPage(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	rec := e.get(matchURL)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<html", `name="anilist"`, "Lost Show")
}

func TestOverviewOffersMatchForManualOnly(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	e.saved(t)
	b := e.get("/anime/manual/man1").Body.String()
	mustContain(t, b, "Match to AniList", `hx-get="/anime/manual/man1/match"`,
		"Imported from Yamtrack; AniList had no match for MyAnimeList #5114")
	b = e.get("/anime/anilist/1535").Body.String()
	mustNotContain(t, b, "Match to AniList", "Imported from Yamtrack")
}

func TestMatchByURLConverts(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	_, enBefore := e.snapshot(t, id)
	rec := e.do("POST", matchURL, url.Values{"anilist": {"https://anilist.co/anime/1535/Death-Note"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime/anilist/1535" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	sv, en := e.snapshot(t, id)
	if sv.Source != provider.AniList || sv.Anime.ID != "1535" || !reflect.DeepEqual(en, enBefore) {
		t.Fatalf("%+v %+v", sv, en)
	}
	if sv.MALID != "5114" {
		t.Fatalf("mal id %q", sv.MALID)
	}
	mustContain(t, e.get("/anime/anilist/1535").Body.String(), "デスノート")
	if c := e.get("/anime/manual/man1").Code; c != 404 {
		t.Fatalf("old url: %d", c)
	}
}

func TestMatchByPickConverts(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	rec := e.do("POST", matchURL, url.Values{"pick": {"1535"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime/anilist/1535" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if sv, _ := e.snapshot(t, id); sv.Source != provider.AniList {
		t.Fatalf("%+v", sv)
	}
}

func TestMatchPickWinsOverURL(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	rec := e.do("POST", matchURL, url.Values{"pick": {"1535"}, "anilist": {"not a url"}}, true)
	if rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if sv, _ := e.snapshot(t, id); sv.Anime.ID != "1535" {
		t.Fatalf("%+v", sv)
	}
}

func TestMatchRejectsBadInput(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	before, _ := e.snapshot(t, id)
	for name, form := range map[string]url.Values{
		"neither":     {},
		"blank":       {"anilist": {"  "}},
		"bad url":     {"anilist": {"https://example.com/anime/5"}},
		"bad pick":    {"pick": {"12abc"}},
		"manga":       {"anilist": {"https://anilist.co/manga/5"}},
		"pick as url": {"pick": {"https://anilist.co/anime/5"}},
	} {
		rec := e.do("POST", matchURL, form, true)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), `name="anilist"`)
	}
	if e.fp.animeCalls != 0 {
		t.Fatalf("provider called %d times for invalid input", e.fp.animeCalls)
	}
	after, _ := e.snapshot(t, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("changed")
	}
}

func TestMatchUnknownIDShowsError(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	before, enB := e.snapshot(t, id)
	e.fp.animeErr = provider.ErrNotFound
	rec := e.do("POST", matchURL, url.Values{"anilist": {"999999"}}, true)
	if rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "AniList has no anime with that id", `value="999999"`)
	after, enA := e.snapshot(t, id)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(enB, enA) || len(e.jobs.got) != 0 {
		t.Fatal("changed")
	}
}

func TestMatchProviderDownChangesNothing(t *testing.T) {
	for name, perr := range map[string]error{"unavailable": provider.ErrUnavailable, "rate limited": provider.ErrRateLimited} {
		t.Run(name, func(t *testing.T) {
			e := newMatchEnv(t)
			id := e.unmatched(t)
			before, enB := e.snapshot(t, id)
			e.fp.animeErr = perr
			rec := e.do("POST", matchURL, url.Values{"anilist": {"1535"}}, true)
			if rec.Code != 503 {
				t.Fatalf("%d", rec.Code)
			}
			mustContain(t, rec.Body.String(), "try again", "Nothing was changed")
			after, enA := e.snapshot(t, id)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(enB, enA) || len(e.jobs.got) != 0 {
				t.Fatal("changed")
			}
			// no JS: still a readable page with the status
			rec = e.do("POST", matchURL, url.Values{"anilist": {"1535"}}, false)
			if rec.Code != 503 {
				t.Fatalf("nojs %d", rec.Code)
			}
			mustContain(t, rec.Body.String(), "<html", "Nothing was changed")
		})
	}
}

func TestMatchSwappableStatuses(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	b := e.do("GET", "/anime/manual/man1", nil, false).Body.String()
	mustContain(t, b, "s===422", "s===503")
}

func TestMatchEnqueuesSync(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	e.do("POST", matchURL, url.Values{"pick": {"1535"}}, true)
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != "sync_anime" {
		t.Fatalf("%+v", e.jobs.got)
	}
	if p := e.jobs.got[0].Payload; !strings.Contains(strings.ReplaceAll(toJSON(p), " ", ""), `"anime_id":`+itoa(id)) {
		t.Fatalf("payload %v", p)
	}
	// enqueue failure is logged, conversion stays
	e2 := newMatchEnv(t)
	id2 := e2.unmatched(t)
	e2.jobs.fail = errors.New("queue down")
	if rec := e2.do("POST", matchURL, url.Values{"pick": {"1535"}}, true); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if sv, _ := e2.snapshot(t, id2); sv.Source != provider.AniList {
		t.Fatal("not converted")
	}
}

func TestMatchRefusesDuplicateWithLink(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	e.saved(t) // anilist 1535 already saved
	before, _ := e.snapshot(t, id)
	rec := e.do("POST", matchURL, url.Values{"anilist": {"1535"}}, true)
	if rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "already", `href="/anime/anilist/1535"`)
	after, _ := e.snapshot(t, id)
	if !reflect.DeepEqual(before, after) || len(e.jobs.got) != 0 {
		t.Fatal("changed")
	}
}

func TestMatchSearchPartial(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	for _, q := range []string{"", "   "} {
		rec := e.do("GET", matchURL+"/search?q="+url.QueryEscape(q), nil, true)
		if rec.Code != 200 {
			t.Fatalf("%d", rec.Code)
		}
	}
	if len(e.mp.searches) != 0 {
		t.Fatalf("searched for empty query: %v", e.mp.searches)
	}
	rec := e.do("GET", matchURL+"/search?q=death", nil, true)
	b := rec.Body.String()
	if rec.Code != 200 || len(e.mp.searches) != 1 {
		t.Fatalf("%d %v", rec.Code, e.mp.searches)
	}
	mustContain(t, b, "デスノート", "TV", "37 eps", "2006", `name="pick" value="1535"`, `action="/anime/manual/man1/match"`, "https://img.example/c.jpg")
	mustNotContain(t, b, "<html")
	// no JS: full page with the dialog open
	b = e.get(matchURL + "/search?q=death").Body.String()
	mustContain(t, b, "<html", `name="pick" value="1535"`, `name="anilist"`)
	// long query is capped
	e.do("GET", matchURL+"/search?q="+strings.Repeat("é", 500), nil, true)
	if got := e.mp.searches[len(e.mp.searches)-1]; len([]rune(got)) != 200 {
		t.Fatalf("query runes %d", len([]rune(got)))
	}
	// not for anilist entries
	if c := e.do("GET", "/anime/anilist/1535/match/search?q=x", nil, true).Code; c != 404 {
		t.Fatalf("%d", c)
	}
}

func TestMatchSearchErrors(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	e.mp.searchErr = provider.ErrRateLimited
	b := e.do("GET", matchURL+"/search?q=x", nil, true).Body.String()
	mustContain(t, b, "rate limiting")
	e.mp.searchErr = provider.ErrUnavailable
	b = e.do("GET", matchURL+"/search?q=x", nil, true).Body.String()
	mustContain(t, b, "unavailable")
	e.mp.searchErr, e.mp.results = nil, nil
	b = e.do("GET", matchURL+"/search?q=zzz", nil, true).Body.String()
	mustContain(t, b, "No results")
}

func TestMatchKeepsOtherEntriesAndPeople(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	other := e.saved(t) // anilist 1535 (different from the match target below)
	e.withCast(t, other, e.fp.cast...)
	actor := e.personPath(t, "a1")
	e.fp.anime.ID = "1600"
	e.fp.anime.ExternalURL = "https://anilist.co/anime/1600"
	if rec := e.do("POST", matchURL, url.Values{"pick": {"1600"}}, true); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if e.personPath(t, "a1") != actor {
		t.Fatal("person changed")
	}
	if sv, _ := e.st.GetByID(context.Background(), other); sv.Anime.ID != "1535" {
		t.Fatal("other entry touched")
	}
	_ = id
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestMatchSearchWithoutAnimePlugin404(t *testing.T) {
	// the anime routes are only mounted with the anime plugin; the handler
	// still refuses to run without its manifest
	e := newEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(plugintest.BookPlugin())
		return r
	})
	man := provider.Anime{SearchResult: provider.SearchResult{Source: provider.Manual, ID: "man1", Titles: []provider.Title{{Lang: "en", Text: "M"}}}}
	if _, err := e.st.SaveAnime(context.Background(), man, store.WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/anime/manual/man1/match/search?q=x", nil)
	req.SetPathValue("source", "manual")
	req.SetPathValue("id", "man1")
	rec := httptest.NewRecorder()
	e.srv.getMatchSearch(rec, req)
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}
