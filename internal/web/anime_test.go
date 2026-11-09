package web

import (
	"context"
	"database/sql"
	"errors"
	"github.com/allister/allister/internal/plugin"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
)

type animeProvider struct {
	mu                   sync.Mutex
	anime                provider.Anime
	cast                 []provider.CastMember
	animeErr, castErr    error
	animeCalls, castCall int
}

func (f *animeProvider) Name() provider.Source { return provider.AniList }
func (f *animeProvider) Search(ctx context.Context, q string) ([]provider.SearchResult, error) {
	return nil, nil
}
func (f *animeProvider) Anime(ctx context.Context, id string) (provider.Anime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.animeCalls++
	return f.anime, f.animeErr
}
func (f *animeProvider) Cast(ctx context.Context, id string) ([]provider.CastMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.castCall++
	return f.cast, f.castErr
}

type enqueued struct {
	Kind    string
	Payload any
}

type fakeJobs struct {
	mu   sync.Mutex
	got  []enqueued
	fail error
}

func (f *fakeJobs) Enqueue(ctx context.Context, kind string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, enqueued{kind, payload})
	return f.fail
}

type env struct {
	h    http.Handler
	fp   *animeProvider
	jobs *fakeJobs
	st   *store.Store
	set  *settings.Store
	now  *time.Time
	// covers is the temp covers dir configured as Deps.CoversDir.
	covers string
	// d is the database, for tests that must seed values the store refuses.
	d *sql.DB
	// srv is the server behind h (route tables, registry).
	srv *server
	// reg is the plugin registry given to the server.
	reg *plugin.Registry
	// lib is the server's library (for seeding items of any plugin).
	lib *library.Library
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith is newEnv with a custom plugin registry (built over the env's
// settings store). A nil mk gets the default registry holding the real anime plugin.
func newEnvWith(t *testing.T, mk func(es plugin.EnabledStore) *plugin.Registry) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &env{fp: sampleAnimeProvider(), jobs: &fakeJobs{}, st: store.New(d), set: settings.New(d),
		covers: filepath.Join(t.TempDir(), "covers"), d: d}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = &now
	if mk != nil {
		e.reg = mk(e.set)
	}
	e.srv, e.h = newServer(Deps{
		Settings: e.set, Store: e.st, Jobs: e.jobs, CoversDir: e.covers, Registry: e.reg,
		Providers: map[provider.Source]provider.Provider{provider.AniList: e.fp},
	}, func() time.Time { return *e.now })
	e.reg = e.srv.deps.Registry
	e.lib = e.srv.deps.Library
	return e
}

func sampleAnimeProvider() *animeProvider {
	start := time.Date(2006, 10, 4, 0, 0, 0, 0, time.UTC)
	end := time.Date(2007, 6, 27, 0, 0, 0, 0, time.UTC)
	return &animeProvider{
		anime: provider.Anime{
			SearchResult: provider.SearchResult{
				Source: provider.AniList, ID: "1535",
				Titles: []provider.Title{{Lang: "ja", Text: "デスノート"}, {Lang: "ja-latn", Text: "Death Note"}, {Lang: "en", Text: "Death Note EN"}, {Lang: "de", Text: "Todesnotiz"}},
				Type:   "TV", Episodes: ip(37), Year: ip(2006), Studio: "Madhouse",
				Synopsis: "Line one.\nLine two.", CoverURL: "https://img.example/c.jpg",
			},
			StartDate: &start, EndDate: &end, SourceMaterial: "MANGA", ExternalURL: "https://anilist.co/anime/1535",
			Relations: []provider.Relation{
				{Kind: "sequel", Source: provider.AniList, ID: "2000", Titles: []provider.Title{{Lang: "en", Text: "Sequel Title"}}, Type: "TV"},
				{Kind: "source", Source: provider.AniList, ID: "30021", Titles: []provider.Title{{Lang: "en", Text: "Manga Title"}}, Type: "MANGA"},
			},
			Recommendations: []provider.Recommendation{
				{Source: provider.AniList, ID: "3000", Titles: []provider.Title{{Lang: "en", Text: "Rec Title"}}, CoverURL: "https://img.example/r.jpg"},
			},
		},
		cast: []provider.CastMember{{
			Character: provider.Person{Source: provider.AniList, ID: "c1", Name: "Light Yagami", ImageURL: "https://img.example/l.jpg", URL: "https://anilist.co/character/c1"},
			Actor:     &provider.Person{Source: provider.AniList, ID: "a1", Name: "Mamoru Miyano", ImageURL: "https://img.example/m.jpg", URL: "https://anilist.co/staff/a1"},
			Role:      "MAIN",
		}},
	}
}

func (e *env) do(method, target string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(target string) *httptest.ResponseRecorder { return e.do("GET", target, nil, false) }
func (e *env) post(form url.Values, htmx bool) *httptest.ResponseRecorder {
	return e.do("POST", "/anime/anilist/1535/add", form, htmx)
}

func okForm() url.Values {
	return url.Values{"status": {"watching"}, "rating": {"8"}, "progress": {"3"}, "started_on": {"2024-01-02"}, "finished_on": {""}, "comment": {"nice"}}
}

func mustContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

func mustNotContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}

func (e *env) saved(t *testing.T) int64 {
	t.Helper()
	id, err := e.st.SaveAnime(context.Background(), e.fp.anime)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddDialogPartialVsFull(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/anime/anilist/1535/add", nil, true)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, `<form`, `name="status"`, `value="plan_to_watch" selected`, `type="date"`, `name="started_on"`, `name="finished_on"`,
		`type="number" min="0" max="10" step="1"`, `placeholder="0–10"`, `max="37"`, "/ 37", "Cancel", `hx-post="/anime/anilist/1535/add"`)
	mustNotContain(t, b, "<html", "Light Yagami")
	if rec.Header().Get("Vary") == "" {
		t.Fatal("no Vary")
	}
	full := e.get("/anime/anilist/1535/add")
	if full.Code != 200 {
		t.Fatalf("got %d", full.Code)
	}
	mustContain(t, full.Body.String(), "<html", "Light Yagami", `name="status"`, `action="/anime/anilist/1535/add"`)
}

func TestAddCreatesEntryAndJob(t *testing.T) {
	e := newEnv(t)
	rec := e.post(okForm(), true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
	ctx := context.Background()
	sv, _ := e.st.FindBySource(ctx, provider.AniList, "1535")
	if sv == nil {
		t.Fatal("not saved")
	}
	en, _ := e.st.GetListEntry(ctx, sv.ID)
	if en == nil || en.Status != store.Watching || en.Rating == nil || *en.Rating != 8 || en.Progress != 3 ||
		en.StartedOn == nil || en.StartedOn.Format("2006-01-02") != "2024-01-02" || en.FinishedOn != nil || en.Comment != "nice" {
		t.Fatalf("%+v", en)
	}
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != jobs.KindSyncAnime || e.jobs.got[0].Payload != (jobs.SyncPayload{AnimeID: sv.ID, PluginID: "anime"}) {
		t.Fatalf("jobs %+v", e.jobs.got)
	}
}

func TestAddPlainPostRedirects303(t *testing.T) {
	e := newEnv(t)
	rec := e.post(okForm(), false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
}

func TestAddRatingBlankAndZero(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want *int
	}{{"", nil}, {"0", ip(0)}} {
		e := newEnv(t)
		f := okForm()
		f.Set("rating", tc.in)
		if rec := e.post(f, true); rec.Code != 204 {
			t.Fatalf("got %d", rec.Code)
		}
		sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535")
		en, _ := e.st.GetListEntry(context.Background(), sv.ID)
		if (tc.want == nil) != (en.Rating == nil) || (tc.want != nil && *tc.want != *en.Rating) {
			t.Fatalf("%q -> %v", tc.in, en.Rating)
		}
	}
}

func TestAddRejectsInvalidRating(t *testing.T) {
	for _, bad := range []string{"11", "abc", "-1", "5.5"} {
		e := newEnv(t)
		f := okForm()
		f.Set("rating", bad)
		rec := e.post(f, true)
		if rec.Code != 422 {
			t.Fatalf("%q: got %d", bad, rec.Code)
		}
		mustContain(t, rec.Body.String(), "Rating", `value="`+bad+`"`, `value="nice"`[:0]+"nice")
		assertNothingSaved(t, e)
	}
}

func assertNothingSaved(t *testing.T, e *env) {
	t.Helper()
	if sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535"); sv != nil {
		t.Fatal("anime saved")
	}
	if len(e.jobs.got) != 0 {
		t.Fatal("job enqueued")
	}
}

func TestAddOtherValidationErrors(t *testing.T) {
	cases := map[string]func(url.Values){
		"progress over total": func(f url.Values) { f.Set("progress", "38") },
		"progress negative":   func(f url.Values) { f.Set("progress", "-1") },
		"progress text":       func(f url.Values) { f.Set("progress", "x") },
		"finish before start": func(f url.Values) { f.Set("started_on", "2024-02-01"); f.Set("finished_on", "2024-01-01") },
		"bad date":            func(f url.Values) { f.Set("started_on", "01/02/2024") },
		"unknown status":      func(f url.Values) { f.Set("status", "nope") },
		"missing status":      func(f url.Values) { f.Del("status") },
	}
	for name, mod := range cases {
		e := newEnv(t)
		f := okForm()
		mod(f)
		rec := e.post(f, true)
		if rec.Code != 422 {
			t.Errorf("%s: got %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), "<form")
		assertNothingSaved(t, e)
	}
}

func TestAddBlankProgressAndDatesAndCommentCap(t *testing.T) {
	e := newEnv(t)
	f := url.Values{"status": {"plan_to_watch"}, "progress": {""}, "comment": {strings.Repeat("あ", 2500)}}
	if rec := e.post(f, true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535")
	en, _ := e.st.GetListEntry(context.Background(), sv.ID)
	if en.Progress != 0 || en.StartedOn != nil || len([]rune(en.Comment)) != 2000 {
		t.Fatalf("%+v", en)
	}
}

func TestAddDuplicateIsBlocked(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(okForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	rec := e.post(okForm(), true)
	if rec.Code != 409 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "already in your list", `href="/anime/anilist/1535"`)
	if len(e.jobs.got) != 1 {
		t.Fatalf("jobs %d", len(e.jobs.got))
	}
}

func TestAddSavedWithoutEntryGetsEntryNoJob(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	rec := e.post(okForm(), true)
	if rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	en, _ := e.st.GetListEntry(context.Background(), id)
	if en == nil || en.Status != store.Watching {
		t.Fatalf("%+v", en)
	}
	if len(e.jobs.got) != 0 {
		t.Fatal("job enqueued for existing anime")
	}
	if e.fp.animeCalls != 0 {
		t.Fatal("provider called")
	}
}

func TestAddSavedWithoutEntryValidatesAgainstStoredEpisodes(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	f := okForm()
	f.Set("progress", "99")
	if rec := e.post(f, true); rec.Code != 422 {
		t.Fatalf("got %d", rec.Code)
	}
	if en, _ := e.st.GetListEntry(context.Background(), id); en != nil {
		t.Fatal("entry created")
	}
}

func TestAddWithProviderDownUsesAlreadyFetchedDetails(t *testing.T) {
	e := newEnv(t)
	if rec := e.get("/anime/anilist/1535"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	e.fp.animeErr = provider.ErrUnavailable
	before := e.fp.animeCalls
	rec := e.post(okForm(), true)
	if rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	if e.fp.animeCalls != before {
		t.Fatal("provider was called again")
	}
	if sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535"); sv == nil {
		t.Fatal("not saved")
	}
}

func TestAddCacheExpiresAfterTTL(t *testing.T) {
	e := newEnv(t)
	e.get("/anime/anilist/1535")
	*e.now = e.now.Add(11 * time.Minute)
	e.fp.animeErr = provider.ErrUnavailable
	rec := e.post(okForm(), true)
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<form", `value="nice"`[:0]+"nice", "AniList")
	assertNothingSaved(t, e)
	// within the TTL it is served from cache
	e.fp.animeErr = nil
	e.get("/anime/anilist/1535")
	*e.now = e.now.Add(9 * time.Minute)
	e.fp.animeErr = provider.ErrUnavailable
	if rec := e.post(okForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddCacheMissFetchesOnceAndNotFoundIs404(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(okForm(), true); rec.Code != 204 || e.fp.animeCalls != 1 {
		t.Fatalf("got %d calls %d", rec.Code, e.fp.animeCalls)
	}
	e = newEnv(t)
	e.fp.animeErr = provider.ErrNotFound
	if rec := e.post(okForm(), true); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddEnqueueFailureStillSucceeds(t *testing.T) {
	e := newEnv(t)
	e.jobs.fail = errors.New("boom")
	if rec := e.post(okForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddManualNotSavedIs404(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/anime/manual/5/add", okForm(), true); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestCacheBoundedAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newAnimeCache(func() time.Time { return now }, 3, time.Minute)
	for _, id := range []string{"1", "2", "3", "4"} {
		c.put("anilist/"+id, cachedDetail{})
		now = now.Add(time.Second)
	}
	if _, ok := c.get("anilist/1"); ok {
		t.Fatal("oldest not evicted")
	}
	if _, ok := c.get("anilist/4"); !ok {
		t.Fatal("missing")
	}
	now = now.Add(time.Minute)
	if _, ok := c.get("anilist/4"); ok {
		t.Fatal("not expired")
	}
}
