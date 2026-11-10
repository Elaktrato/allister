package web

import (
	"context"
	"database/sql"
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
	"github.com/allister/allister/internal/plugins/anime/anilist"
	"github.com/allister/allister/internal/settings"
)

type animeProvider struct {
	mu                   sync.Mutex
	anime                anilist.Anime
	cast                 []anilist.CastMember
	animeErr, castErr    error
	animeCalls, castCall int
}

func (f *animeProvider) Name() anilist.Source { return anilist.AniList }
func (f *animeProvider) Search(ctx context.Context, q string) ([]anilist.SearchResult, error) {
	return nil, nil
}
func (f *animeProvider) Anime(ctx context.Context, id string) (anilist.Anime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.animeCalls++
	return f.anime, f.animeErr
}
func (f *animeProvider) Cast(ctx context.Context, id string) ([]anilist.CastMember, error) {
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
	e := &env{fp: sampleAnimeProvider(), jobs: &fakeJobs{}, set: settings.New(d),
		covers: filepath.Join(t.TempDir(), "covers"), d: d}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = &now
	if mk != nil {
		e.reg = mk(e.set)
	} else {
		e.reg = animeRegistry(t, e.set, e.fp)
	}
	e.lib = library.New(d, e.reg)
	e.srv, e.h = newServer(Deps{
		Settings: e.set, Jobs: e.jobs, SyncJobs: testSyncJobs(), CoversDir: e.covers, DataDir: filepath.Dir(e.covers),
		Registry: e.reg, Library: e.lib,
	}, func() time.Time { return *e.now })
	return e
}

// testSyncJobs is the anime entry of the production wiring of Deps.SyncJobs
// (cmd/allister wires the same generic job for every installed plugin). The
// fixture plugins get none here, so their tests can assert that nothing was
// enqueued.
func testSyncJobs() map[string]func(int64) (string, any) {
	return map[string]func(int64) (string, any){"anime": jobs.SyncJobFor("anime")}
}

func sampleAnimeProvider() *animeProvider {
	start := time.Date(2006, 10, 4, 0, 0, 0, 0, time.UTC)
	end := time.Date(2007, 6, 27, 0, 0, 0, 0, time.UTC)
	return &animeProvider{
		anime: anilist.Anime{
			SearchResult: anilist.SearchResult{
				Source: anilist.AniList, ID: "1535",
				Titles: []anilist.Title{{Lang: "ja", Text: "デスノート"}, {Lang: "ja-latn", Text: "Death Note"}, {Lang: "en", Text: "Death Note EN"}, {Lang: "de", Text: "Todesnotiz"}},
				Type:   "TV", Episodes: ip(37), Year: ip(2006), Studio: "Madhouse",
				Synopsis: "Line one.\nLine two.", CoverURL: "https://img.example/c.jpg",
			},
			StartDate: &start, EndDate: &end, SourceMaterial: "MANGA", ExternalURL: "https://anilist.co/anime/1535",
			Relations: []anilist.Relation{
				{Kind: "sequel", Source: anilist.AniList, ID: "2000", Titles: []anilist.Title{{Lang: "en", Text: "Sequel Title"}}, Type: "TV"},
				{Kind: "source", Source: anilist.AniList, ID: "30021", Titles: []anilist.Title{{Lang: "en", Text: "Manga Title"}}, Type: "MANGA"},
			},
			Recommendations: []anilist.Recommendation{
				{Source: anilist.AniList, ID: "3000", Titles: []anilist.Title{{Lang: "en", Text: "Rec Title"}}, CoverURL: "https://img.example/r.jpg"},
			},
		},
		cast: []anilist.CastMember{{
			Character: anilist.Person{Source: anilist.AniList, ID: "c1", Name: "Light Yagami", ImageURL: "https://img.example/l.jpg", URL: "https://anilist.co/character/c1"},
			Actor:     &anilist.Person{Source: anilist.AniList, ID: "a1", Name: "Mamoru Miyano", ImageURL: "https://img.example/m.jpg", URL: "https://anilist.co/staff/a1"},
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
	id, err := saveAnime(context.Background(), e.lib, e.fp.anime)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCacheBoundedAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newDetailCache(func() time.Time { return now }, 3, time.Minute)
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
