package web

import (
	"context"
	"errors"
	"fmt"
	"github.com/allister/allister/internal/library"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/plugins/anime"
)

// withPlugins builds a registry with the real anime plugin followed by extra.
func withPlugins(extra ...plugin.Plugin) func(plugin.EnabledStore) *plugin.Registry {
	return func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(anime.New(anime.Options{}))
		for _, p := range extra {
			r.Register(p) // refusals show up in Problems
		}
		return r
	}
}

// dump returns every table's rows, so a test can assert nothing was mutated.
func dump(t *testing.T, e *env) string {
	t.Helper()
	rows, err := e.d.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	var b strings.Builder
	for _, tb := range tables {
		r, err := e.d.Query(`SELECT * FROM ` + tb)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := r.Columns()
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			r.Scan(ptrs...)
			fmt.Fprintf(&b, "%s %v\n", tb, vals)
		}
		r.Close()
	}
	return b.String()
}

// concrete turns a recorded pattern into a method and URL.
func concrete(pattern string, id int64) (method, target string) {
	method, path, _ := strings.Cut(pattern, " ")
	path = strings.NewReplacer("{source}", "anilist", "{id}", fmt.Sprint(id)).Replace(path)
	return method, path
}

func (e *env) disable(t *testing.T, id string) {
	t.Helper()
	if err := e.reg.SetEnabled(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledPluginRoutes404(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1535", nil, library.Active, 1)
	if len(e.srv.pluginRoutes[animePluginID]) < 10 || len(e.srv.coreGuarded) < 4 {
		t.Fatalf("route tables too small: %v %v", e.srv.pluginRoutes, e.srv.coreGuarded)
	}
	e.disable(t, "anime")
	before := dump(t, e)
	for _, table := range [][]string{e.srv.pluginRoutes[animePluginID], e.srv.coreGuarded} {
		for _, pat := range table {
			method, target := concrete(pat, id)
			form := url.Values{"status": {"watching"}, "progress": {"2"}, "title": {"x"}, "q": {"x"}}
			var rec *httptest.ResponseRecorder
			if method == "GET" {
				rec = e.do(method, target, nil, false)
			} else {
				rec = e.do(method, target, form, true)
			}
			if rec.Code != 404 {
				t.Errorf("%s -> %d, want 404", pat, rec.Code)
			}
		}
	}
	if len(e.jobs.got) != 0 {
		t.Errorf("jobs enqueued: %+v", e.jobs.got)
	}
	if after := dump(t, e); after != before {
		t.Errorf("database changed while plugin disabled:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEveryAnimeRouteIsGuardedByTheRouteTable(t *testing.T) {
	e := newEnv(t)
	// the table itself: every route registered by the set carries the /anime prefix
	var rec recordRoutes
	e.srv.coreRoutes(animePluginID)(&rec) // the anime route set is exactly the core one
	if len(rec.patterns) == 0 || len(rec.patterns) != len(e.srv.pluginRoutes[animePluginID]) {
		t.Fatalf("recorded %v vs mounted %v", rec.patterns, e.srv.pluginRoutes[animePluginID])
	}
	for _, full := range e.srv.pluginRoutes[animePluginID] {
		_, path, _ := strings.Cut(full, " ")
		if !underPrefix(path, "/anime") {
			t.Errorf("%s escapes the plugin prefix", full)
		}
	}
	// enabled: the guard lets requests through (not the disabled 404 message)
	for _, full := range e.srv.pluginRoutes[animePluginID] {
		method, target := concrete(full, 1535)
		if method != "GET" {
			continue
		}
		body := e.do(method, target, nil, false).Body.String()
		if strings.Contains(body, "its plugin is disabled") {
			t.Errorf("%s guarded while enabled", full)
		}
	}
}

type recordRoutes struct{ patterns []string }

func (r *recordRoutes) Handle(p string, _ http.HandlerFunc) { r.patterns = append(r.patterns, p) }

func TestEnablingRestoresRoutes(t *testing.T) {
	e := newEnv(t)
	e.disable(t, "anime")
	if c := e.get("/anime?view=table").Code; c != 404 {
		t.Fatalf("disabled: %d", c)
	}
	if rec := e.do("POST", "/settings/plugins/anime", url.Values{"enabled": {"1"}}, false); rec.Code != 303 {
		t.Fatalf("toggle: %d", rec.Code)
	}
	if c := e.get("/anime?view=table").Code; c != 200 {
		t.Fatalf("enabled: %d", c)
	}
}

func TestDisabledPluginDoesNotSwallowCoreRoutes(t *testing.T) {
	e := newEnv(t)
	e.disable(t, "anime")
	for _, p := range []string{"/settings", "/healthz", "/static/htmx.min.js"} {
		if c := e.get(p).Code; c != 200 {
			t.Errorf("%s -> %d", p, c)
		}
	}
}

func TestNavShowsOnlyEnabledPlugins(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin(), plugintest.TVPlugin()))
	body := e.get("/settings").Body.String()
	nav := body[:strings.Index(body, "</header>")]
	mustContain(t, nav, `href="/anime"`, `href="/book"`, `href="/tv"`, ">Books<", ">Shows<", `href="/settings"`)
	e.disable(t, "book")
	body = e.get("/settings").Body.String()
	nav = body[:strings.Index(body, "</header>")]
	mustContain(t, nav, `href="/anime"`, `href="/tv"`)
	mustNotContain(t, nav, `href="/book"`, ">Books<")
}

func TestNavMarksActiveItem(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	body := e.get("/settings").Body.String()
	nav := body[:strings.Index(body, "</header>")]
	if strings.Count(nav, `aria-current="page"`) != 1 {
		t.Fatalf("nav: %s", nav)
	}
	if i := strings.Index(nav, `aria-current`); !strings.Contains(nav[strings.LastIndex(nav[:i], "<a "):i], `href="/settings"`) {
		t.Fatalf("settings not active: %s", nav)
	}
	for _, p := range []string{"/anime", "/anime/search"} {
		body := e.get(p).Body.String()
		nav := body[:strings.Index(body, "</header>")]
		i := strings.Index(nav, `aria-current`)
		if strings.Count(nav, "aria-current") != 1 || !strings.Contains(nav[strings.LastIndex(nav[:i], "<a "):i], `href="/anime"`) {
			t.Errorf("%s: wrong active item: %s", p, nav)
		}
	}
	// "/animefoo" style prefixes must not match
	if underPrefix("/animefoo", "/anime") {
		t.Fatal("prefix match too loose")
	}
}

func TestSettingsTogglePersistsAndIsCSRFProtected(t *testing.T) {
	e := newEnv(t)
	post := func(hdr map[string]string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/settings/plugins/anime", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(map[string]string{"Sec-Fetch-Site": "cross-site"}, url.Values{"enabled": {"0"}}); rec.Code != 403 || !e.reg.IsEnabled("anime") {
		t.Fatalf("cross-site: %d enabled=%v", rec.Code, e.reg.IsEnabled("anime"))
	}
	if rec := post(map[string]string{"Origin": "https://evil.example"}, url.Values{"enabled": {"0"}}); rec.Code != 403 || !e.reg.IsEnabled("anime") {
		t.Fatalf("evil origin: %d", rec.Code)
	}
	rec := post(map[string]string{"Sec-Fetch-Site": "same-origin"}, url.Values{"enabled": {"0"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings/plugins" || e.reg.IsEnabled("anime") {
		t.Fatalf("same-origin: %d %q enabled=%v", rec.Code, rec.Header().Get("Location"), e.reg.IsEnabled("anime"))
	}
	if v, ok, _ := e.set.Get(context.Background(), "plugin.anime.enabled"); !ok || v != "0" {
		t.Fatalf("stored %q %v", v, ok)
	}
	if rec := post(nil, url.Values{"enabled": {"1"}}); rec.Code != 303 || !e.reg.IsEnabled("anime") {
		t.Fatalf("re-enable: %d", rec.Code)
	}
	// GET of the same path is the plugin's settings page now: read-only, it
	// never toggles
	if rec := e.do("GET", "/settings/plugins/anime", nil, false); rec.Code != 200 || !e.reg.IsEnabled("anime") {
		t.Fatalf("GET toggle: %d enabled=%v", rec.Code, e.reg.IsEnabled("anime"))
	}
}

func TestToggleUnknownPluginAndBadValue(t *testing.T) {
	e := newEnv(t)
	before := dump(t, e)
	rec := e.do("POST", "/settings/plugins/nope%3Cscript%3E", url.Values{"enabled": {"1"}}, false)
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
	}
	for _, v := range []string{"", "yes", "2", "true"} {
		if rec := e.do("POST", "/settings/plugins/anime", url.Values{"enabled": {v}}, false); rec.Code != 400 {
			t.Errorf("enabled=%q -> %d", v, rec.Code)
		}
	}
	if rec := e.do("POST", "/settings/plugins/anime", url.Values{}, false); rec.Code != 400 {
		t.Errorf("missing -> %d", rec.Code)
	}
	if dump(t, e) != before {
		t.Fatal("settings changed")
	}
}

func TestSettingsListsPlugins(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	body := e.get("/settings/plugins").Body.String()
	mustContain(t, body, "Plugins", "Anime", "Books", "AniList", "Fake Books", "Books CSV", "JSON",
		`action="/settings/plugins/anime"`, `action="/settings/plugins/book"`, `name="enabled" value="0"`,
		`href="/anime"`, "keeps all its data")
	e.disable(t, "book")
	body = e.get("/settings/plugins").Body.String()
	mustContain(t, body, `action="/settings/plugins/book"`, `name="enabled" value="1"`, ">Enable<")
	// the disabled plugin row shows no list link; fixtures have no routes either way
	i := strings.Index(body, `data-plugin="book"`)
	row := body[i : i+strings.Index(body[i:], "</li>")]
	mustNotContain(t, row, `href="/book"`)
}

func TestSettingsListsRefusedPluginsWithReason(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin(plugintest.WithID("list")), plugintest.TVPlugin(plugintest.WithID("Bad ID"))))
	body := e.get("/settings/plugins").Body.String()
	mustContain(t, body, "Refused plugins", "reserved by the core")
	if !strings.Contains(body, `&#34;list&#34;`) && !strings.Contains(body, "&quot;list&quot;") {
		t.Errorf("refused id missing: %s", body)
	}
	if strings.Contains(body, `data-plugin="list"`) {
		t.Error("refused plugin listed as installed")
	}
}

func TestSettingsEscapesHostileManifestText(t *testing.T) {
	evil := `<script>alert(1)</script>`
	p := plugintest.BookPlugin()
	p.M.Name = evil
	p.M.Kinds[0].Label = evil
	p.M.Kinds[0].LabelPlural = evil
	p.M.Sources[0].Name = evil
	p.M.Importers[0].Name = evil
	e := newEnvWith(t, withPlugins(p))
	if !e.reg.IsEnabled("book") {
		t.Fatalf("fixture refused: %v", e.reg.Problems())
	}
	for _, path := range []string{"/settings", "/anime"} {
		body := e.get(path).Body.String()
		if strings.Contains(body, "<script>alert") {
			t.Errorf("%s: unescaped manifest text", path)
		}
		if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("%s: escaped text missing", path)
		}
	}
}

func TestFixturePluginsGetOnlyCoreRoutes(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	var rec recordRoutes
	e.srv.coreRoutes("book")(&rec)
	got := e.srv.pluginRoutes["book"]
	if len(rec.patterns) == 0 || len(got) != len(rec.patterns) {
		t.Fatalf("fixture routes %v, core routes %v", got, rec.patterns)
	}
	for i, full := range got {
		method, path, _ := strings.Cut(rec.patterns[i], " ")
		if path == "/" { // "/" is the prefix itself
			path = ""
		}
		if full != method+" /book"+path {
			t.Errorf("route %q is not the core route %q under /book", full, rec.patterns[i])
		}
	}
	// the generic list page is a core route
	if c := e.get("/book?view=table").Code; c != 200 {
		t.Fatalf("/book: %d", c)
	}
}

func TestRootRedirectsToFirstEnabledOrSettings(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	loc := func() string {
		rec := e.get("/")
		if rec.Code != 303 {
			t.Fatalf("code %d", rec.Code)
		}
		return rec.Header().Get("Location")
	}
	if l := loc(); l != "/anime" {
		t.Fatalf("got %q", l)
	}
	e.disable(t, "anime")
	if l := loc(); l != "/book" {
		t.Fatalf("got %q", l)
	}
	e.disable(t, "book")
	if l := loc(); l != "/settings/plugins" {
		t.Fatalf("got %q", l)
	}
}

func TestReservedIDsNeverReachServeMux(t *testing.T) {
	for _, id := range []string{"list", "people", "settings", "covers", "static", "healthz", "api"} {
		reg := plugin.NewRegistry(plugintest.NewMemEnabled())
		if err := reg.Register(plugintest.BookPlugin(plugintest.WithID(id))); err == nil {
			t.Fatalf("%s accepted", id)
		}
		e := newEnvWith(t, func(plugin.EnabledStore) *plugin.Registry { return reg })
		if len(reg.Manifests()) != 0 {
			t.Fatal("registered")
		}
		if c := e.get("/healthz").Code; c != 200 {
			t.Fatalf("server broken for %s: %d", id, c)
		}
		if c := e.get("/settings").Code; c != 200 {
			t.Fatalf("settings %d", c)
		}
	}
}

func TestAllExistingAnimeURLsStillWork(t *testing.T) {
	e := newEnv(t)
	e.saved(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	man := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "man1", Titles: []anilist.Title{{Lang: "en", Text: "M"}}}}
	if _, err := addToList(context.Background(), e.lib, man, listEntry{Status: library.Active}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/", 303},
		{"GET", "/anime", 200},
		{"GET", "/anime?status=watching", 200},
		{"GET", "/anime/search", 200},
		{"GET", "/anime/search?q=death", 200},
		{"GET", "/anime/manual/new", 200},
		{"GET", "/anime/anilist/1535", 200},
		{"GET", "/anime/anilist/1535/add", 200},
		{"GET", "/anime/manual/man1/match", 200},
		{"GET", "/anime/manual/man1/match/search?q=x", 200},
		{"GET", fmt.Sprintf("/list/%d/edit", id), 200},
		{"GET", "/settings", 200},
		{"GET", "/healthz", 200},
		{"GET", "/anime/anilist/abc", 404},
	} {
		if rec := e.do(tc.method, tc.path, nil, false); rec.Code != tc.want {
			t.Errorf("%s %s -> %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
	if rec := e.do("POST", fmt.Sprintf("/list/%d/increment", id), url.Values{}, true); rec.Code != 200 {
		t.Errorf("increment -> %d", rec.Code)
	}
}

type failingEnabled struct{}

func (failingEnabled) IsEnabled(context.Context, string, bool) (bool, error) {
	return false, errors.New("db down")
}
func (failingEnabled) SetEnabled(context.Context, string, bool) error { return errors.New("db down") }

func TestStoreErrorFallsBackToDefault(t *testing.T) {
	on, off := plugintest.BookPlugin(), plugintest.TVPlugin(plugintest.WithDefaultEnabled(false))
	e := newEnvWith(t, func(plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(failingEnabled{})
		r.Register(anime.New(anime.Options{}))
		r.Register(on)
		r.Register(off)
		return r
	})
	if c := e.get("/anime?view=table").Code; c != 200 {
		t.Fatalf("anime (default on): %d", c)
	}
	body := e.get("/settings").Body.String()
	nav := body[:strings.Index(body, "</header>")]
	mustContain(t, nav, `href="/book"`)
	mustNotContain(t, nav, `href="/tv"`)
	// toggling reports a server error, not a panic
	if rec := e.do("POST", "/settings/plugins/anime", url.Values{"enabled": {"0"}}, false); rec.Code != 500 {
		t.Fatalf("toggle with failing store: %d", rec.Code)
	}
}

func TestEnqueuedJobsCarryPluginID(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(okAddForm(), true); rec.Code != 204 {
		t.Fatalf("add: %d", rec.Code)
	}
	if len(e.jobs.got) != 1 || e.jobs.got[0].Payload.(jobs.SyncPayload).PluginID != "anime" {
		t.Fatalf("add payload: %+v", e.jobs.got)
	}
	m := newMatchEnv(t)
	m.unmatched(t)
	if rec := m.do("POST", matchURL, url.Values{"source_ref": {"1535"}}, true); rec.Code >= 400 {
		t.Fatalf("match: %d %s", rec.Code, rec.Body.String())
	}
	if len(m.jobs.got) != 1 || m.jobs.got[0].Payload.(jobs.SyncPayload).PluginID != "anime" {
		t.Fatalf("match payload: %+v", m.jobs.got)
	}
}
