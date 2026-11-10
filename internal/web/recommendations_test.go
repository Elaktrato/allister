package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/provider"
)

const recsURL = "/tv/tvdata/1/recommendations"

// htmxGet is a GET with HX-Request, as the placeholder's hx-get sends it.
func (e *env) htmxGet(target string) *httptest.ResponseRecorder {
	return e.do("GET", target, nil, true)
}

func TestOverviewRendersWithoutCallingRecommendations(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Live Show")
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: "2", Kind: "show", Titles: ttl("Some Rec")}}
	e.saveFixture(t, "tv", "show", tvShow("5", "Saved Show"))
	for _, p := range []string{"/tv/tvdata/1", "/tv/tvdata/5"} {
		rec := e.get(p)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
		b := rec.Body.String()
		mustContain(t, b, `hx-get="`+p+`/recommendations"`, `hx-trigger="load"`, `href="`+p+`/recommendations"`, "Load recommendations")
		mustNotContain(t, b, "Some Rec")
	}
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 0 {
		t.Fatalf("page render made %d Recommendations calls", n)
	}
	// the live overview makes exactly one Details call (plus People)
	if e.tv.Calls(plugintest.OpDetails) != 1 || e.tv.Calls(plugintest.OpPeople) != 1 {
		t.Fatalf("details %d people %d", e.tv.Calls(plugintest.OpDetails), e.tv.Calls(plugintest.OpPeople))
	}
	// the anime overview: one Anime request (Details), none for recommendations
	a := newEnv(t)
	if rec := a.get("/anime/anilist/1535"); rec.Code != 200 {
		t.Fatalf("anime: %d", rec.Code)
	}
	if a.fp.animeCalls != 1 {
		t.Fatalf("anime requests %d", a.fp.animeCalls)
	}
}

func TestRecommendationsPlaceholderOnlyWhenTheSourceCanSupplyThem(t *testing.T) {
	e := newFixtureEnv(t)
	// a kind without the recommendations section
	e.book.Data = bookItem("1", "Book")
	mustNotContain(t, e.get("/book/books/1").Body.String(), "/recommendations", "Load recommendations")
	// a manual item
	man := tvShow("m1", "Manual Show")
	man.Source = "manual"
	e.saveFixture(t, "tv", "show", man)
	mustNotContain(t, e.get("/tv/manual/m1").Body.String(), "/recommendations", "Load recommendations")
	// a plugin that does not provide the item's source
	f := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	f.saveFixture(t, "tv", "show", tvShow("3", "Sourceless"))
	mustNotContain(t, f.get("/tv/tvdata/3").Body.String(), "/recommendations", "Load recommendations")
}

// The recommendation card assertions of the legacy overview tests
// (TestOverviewLiveForUnsaved, TestOverviewRecommendationCards,
// TestOverviewFixtureFromDatabaseForSaved), moved to the endpoint.
func TestRecommendationsEndpointRendersCards(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Recs = []plugin.ItemSummary{
		{Source: "tvdata", ID: "r 1", Kind: "show", Titles: ttl("Rec One"), CoverURL: "https://img.example/r1.jpg"},
		{Source: "elsewhere", ID: "r2", Kind: "show", Titles: ttl("Foreign Rec")},
		{Source: "tvdata", ID: "", Kind: "show", Titles: ttl("No Id")},
		{Source: "tvdata", ID: "r3", Kind: "movie", Titles: ttl("Other Kind")},
	}
	rec := e.htmxGet(recsURL)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, ">Recommendations<", `href="/tv/tvdata/r%201"`, "Rec One", `src="https://img.example/r1.jpg"`)
	mustNotContain(t, b, "Foreign Rec", "No Id", "Other Kind", "<html", "<nav")
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "HX-Request") {
		t.Fatalf("Vary %q", v)
	}
	// without htmx (the no-JS link) it is a full page with the same cards
	full := e.get(recsURL)
	if full.Code != 200 {
		t.Fatalf("full: %d", full.Code)
	}
	mustContain(t, full.Body.String(), "<html", ">Recommendations<", `href="/tv/tvdata/r%201"`, `href="/tv/tvdata/1"`)
	// a history restore needs the full page too
	req := httptest.NewRequest("GET", recsURL, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	hr := httptest.NewRecorder()
	e.h.ServeHTTP(hr, req)
	mustContain(t, hr.Body.String(), "<html")

	// a saved item gets its recommendations live too
	e.saveFixture(t, "tv", "show", tvShow("7", "Stored Title"))
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: "2", Kind: "show", Titles: ttl("Live Rec")}}
	mustContain(t, e.htmxGet("/tv/tvdata/7/recommendations").Body.String(), "Live Rec")

	// the anime plugin (its unsaved overview's former recommendation row)
	a := newEnv(t)
	ab := a.htmxGet("/anime/anilist/1535/recommendations").Body.String()
	mustContain(t, ab, "Rec Title", `href="/anime/anilist/3000"`, "https://img.example/r.jpg")
}

func TestRecommendationsEscapeHostileValues(t *testing.T) {
	e := newFixtureEnv(t)
	const x = `<script>alert(1)</script>`
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: `"><x`, Kind: "show", Titles: ttl("Rc" + x), CoverURL: "javascript:alert(6)"}}
	for _, rec := range []*httptest.ResponseRecorder{e.htmxGet(recsURL), e.get(recsURL)} {
		b := rec.Body.String()
		if strings.Contains(b, "<script>") || strings.Contains(b, `src="javascript`) || strings.Contains(b, "javascript:alert(6)") {
			t.Fatalf("hostile markup rendered:\n%s", b)
		}
		mustContain(t, b, "Rc&lt;script&gt;alert(1)&lt;/script&gt;", `href="/tv/tvdata/%22%3E%3Cx"`)
	}
}

func TestRecommendationsEndpointCachesAndTimesOut(t *testing.T) {
	e := newFixtureEnv(t)
	e.srv.recTimeout = 50 * time.Millisecond
	e.tv.Delay = 10 * time.Second
	start := time.Now()
	rec := e.htmxGet(recsURL)
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("slow source held the response for %v", el)
	}
	if rec.Code != 200 {
		t.Fatalf("timeout: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Recommendations are unavailable right now", `hx-get="`+recsURL+`"`, `href="`+recsURL+`"`)
	// a failure is not cached: the next request asks again
	e.htmxGet(recsURL)
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 2 {
		t.Fatalf("calls after two timeouts %d", n)
	}

	e.tv.Delay = 0
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: "2", Kind: "show", Titles: ttl("Cached Rec")}}
	mustContain(t, e.htmxGet(recsURL).Body.String(), "Cached Rec")
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 3 {
		t.Fatalf("calls %d", n)
	}
	// cached values are copies: changing the source's slice changes nothing
	e.tv.Recs[0].Titles[0].Text = "Mutated"
	*e.now = e.now.Add(cacheTTL - time.Second)
	b := e.htmxGet(recsURL).Body.String()
	mustContain(t, b, "Cached Rec")
	mustNotContain(t, b, "Mutated")
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 3 {
		t.Fatalf("cached request called the source: %d", n)
	}
	// the cache is keyed by item: another id asks the source
	e.htmxGet("/tv/tvdata/9/recommendations")
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 4 {
		t.Fatalf("other id: calls %d", n)
	}
	// after the TTL the source is asked again
	*e.now = e.now.Add(time.Second)
	mustContain(t, e.htmxGet(recsURL).Body.String(), "Mutated")
	if n := e.tv.Calls(plugintest.OpRecommendations); n != 5 {
		t.Fatalf("expired: calls %d", n)
	}
	if recCacheMaxEntries <= 0 || len(e.srv.recs.m) > recCacheMaxEntries {
		t.Fatalf("recommendations cache unbounded: %d", len(e.srv.recs.m))
	}
}

func TestRecommendationsFailureIsNon500(t *testing.T) {
	for _, err := range []error{plugin.ErrUnavailable, plugin.ErrRateLimited, errors.New("boom"), context.Canceled} {
		e := newFixtureEnv(t)
		e.tv.Errs = map[string]error{plugintest.OpRecommendations: err}
		for _, rec := range []*httptest.ResponseRecorder{e.htmxGet(recsURL), e.get(recsURL)} {
			if rec.Code != 200 {
				t.Fatalf("%v: got %d", err, rec.Code)
			}
			mustContain(t, rec.Body.String(), "Recommendations are unavailable right now", "Try again")
		}
	}
	// a declared source the plugin does not provide
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	if rec := e.htmxGet(recsURL); rec.Code != 200 || !strings.Contains(rec.Body.String(), "unavailable right now") {
		t.Fatalf("nil source: %d %s", rec.Code, rec.Body.String())
	}
	// not found or no recommendations: an empty fragment, no heading noise
	for _, mod := range []func(f *plugintest.FakeSource){
		func(f *plugintest.FakeSource) {
			f.Errs = map[string]error{plugintest.OpRecommendations: plugin.ErrNotFound}
		},
		func(f *plugintest.FakeSource) { f.Recs = []plugin.ItemSummary{} },
		func(f *plugintest.FakeSource) {
			f.Recs = []plugin.ItemSummary{{Source: "elsewhere", ID: "1", Kind: "show", Titles: ttl("Foreign")}}
		},
	} {
		e := newFixtureEnv(t)
		mod(e.tv)
		rec := e.htmxGet(recsURL)
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "" {
			t.Fatalf("empty: %d %q", rec.Code, rec.Body.String())
		}
	}
	// the legacy anime provider failing: still a fragment, never a 500
	a := newEnv(t)
	a.fp.animeErr = provider.ErrUnavailable
	if rec := a.htmxGet("/anime/anilist/1535/recommendations"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "unavailable right now") {
		t.Fatalf("anime: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRecommendationsForDisabledPlugin404(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: "2", Kind: "show", Titles: ttl("Hidden Rec")}}
	if rec := e.htmxGet(recsURL); rec.Code != 200 {
		t.Fatalf("enabled: %d", rec.Code)
	}
	e.disable(t, "tv")
	rec := e.htmxGet(recsURL)
	if rec.Code != 404 {
		t.Fatalf("disabled: %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), "Hidden Rec")
	calls := e.tv.Calls(plugintest.OpRecommendations)
	for _, p := range []string{
		"/nope/tvdata/1/recommendations",    // unknown plugin
		"/book/elsewhere/1/recommendations", // undeclared source
		"/book/manual/1/recommendations",    // manual items have none
		"/book/books/1/recommendations",     // the book kind has no recommendations section
		"/book/books/1/recommendations?kind=nope",
		"/book/books/1/recommendations?kind=comic",
	} {
		if c := e.htmxGet(p).Code; c != 404 {
			t.Errorf("%s: %d", p, c)
		}
	}
	if n := e.tv.Calls(plugintest.OpRecommendations) + e.book.Calls(plugintest.OpRecommendations); n != calls {
		t.Fatalf("source called for a 404: %d", n-calls)
	}
}
