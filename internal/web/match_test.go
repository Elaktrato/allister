package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
)

// matchProvider is the sample provider with a counted search.
type matchProvider struct {
	*animeProvider
	mu        sync.Mutex
	searches  []string
	searchErr error
	results   []anilist.SearchResult
}

func (m *matchProvider) Search(ctx context.Context, typ, q string) ([]anilist.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searches = append(m.searches, q)
	return m.results, m.searchErr
}

type matchEnv struct {
	*env
	mp *matchProvider
}

// newMatchEnv is newEnv with the counting search provider. ADAPTED: no
// Deps.ParseAnimeURL any more, the anime manifest's URL pattern is used.
func newMatchEnv(t *testing.T) *matchEnv {
	t.Helper()
	e := newEnv(t)
	mp := &matchProvider{animeProvider: e.fp, results: []anilist.SearchResult{e.fp.anime.SearchResult}}
	reg := animeRegistry(t, e.set, mp)
	e.srv, e.h = newServer(Deps{
		Settings: e.set, Jobs: e.jobs, SyncJobs: testSyncJobs(), CoversDir: e.covers,
		Registry: reg, Library: library.New(e.d, reg),
	}, func() time.Time { return *e.now })
	e.reg, e.lib = e.srv.deps.Registry, e.srv.deps.Library
	return &matchEnv{env: e, mp: mp}
}

// unmatched saves an import-like manual anime "man1" with list data and
// returns its item id. ADAPTED: seeded through the library.
func (e *matchEnv) unmatched(t *testing.T) int64 {
	t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "man1", Kind: "anime",
		Titles: []plugin.Title{{Lang: "en", Text: "Lost Show <b>"}}, Fields: map[string]plugin.FieldValue{"type": {Text: "TV"}},
		CoverURL: "oldcover.jpg"}, ExternalIDs: map[string]string{"mal": "5114"}}
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	return e.addFixture(t, "anime", "anime", d, library.Entry{Status: library.Completed, Rating: ip(0), StartedOn: &start,
		Comment: "c", Progress: map[string]float64{"episode": 3}}, library.WithSyncState("pending"))
}

// snapshot returns the item and its entry. ADAPTED: library types.
func (e *env) snapshot(t *testing.T, id int64) (*library.Item, *library.Entry) {
	t.Helper()
	it, err := e.lib.Item(context.Background(), id)
	if err != nil || it == nil {
		t.Fatalf("get: %v %v", it, err)
	}
	en, _ := e.lib.Entry(context.Background(), id)
	return it, en
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
	// ADAPTED: the reference field is source_ref (the external id label comes
	// from the importer's ExternalIDLabel)
	mustContain(t, rec.Body.String(), `name="source_ref"`, `name="q"`, `hx-get="/anime/manual/man1/match/search"`,
		`keyup changed delay:400ms, search`, "MyAnimeList #5114", "Lost Show &lt;b&gt;", `id="match-results"`, "Match to AniList",
		`placeholder="https://anilist.co/anime/123"`)
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
	if c := e.get(matchURL + "?source=nope").Code; c != 404 {
		t.Fatalf("unknown source: %d", c)
	}
}

func TestMatchDialogWithoutJSIsFullPage(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
	rec := e.get(matchURL)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<html", `name="source_ref"`, "Lost Show")
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
	rec := e.do("POST", matchURL, url.Values{"source_ref": {"https://anilist.co/anime/1535/Death-Note"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime/anilist/1535" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	it, en := e.snapshot(t, id)
	if it.Source != "anilist" || it.SourceID != "1535" || it.SyncState != "pending" || !reflect.DeepEqual(en, enBefore) {
		t.Fatalf("%+v %+v", it, en)
	}
	if it.ExternalIDs["mal"] != "5114" {
		t.Fatalf("mal id %q", it.ExternalIDs["mal"])
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
	if it, _ := e.snapshot(t, id); it.Source != "anilist" {
		t.Fatalf("%+v", it)
	}
}

func TestMatchPickWinsOverURL(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	rec := e.do("POST", matchURL, url.Values{"pick": {"1535"}, "source_ref": {"not a url"}}, true)
	if rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if it, _ := e.snapshot(t, id); it.SourceID != "1535" {
		t.Fatalf("%+v", it)
	}
}

func TestMatchBareNumberAndLeadingZeros(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	if rec := e.do("POST", matchURL, url.Values{"source_ref": {" 001535 "}}, true); rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime/anilist/1535" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if it, _ := e.snapshot(t, id); it.SourceID != "1535" {
		t.Fatalf("%+v", it)
	}
}

func TestMatchRejectsBadInput(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	before, _ := e.snapshot(t, id)
	for name, form := range map[string]url.Values{
		"neither":      {},
		"blank":        {"source_ref": {"  "}},
		"bad url":      {"source_ref": {"https://example.com/anime/5"}},
		"look-alike":   {"source_ref": {"https://anilist.co.evil.com/anime/5"}},
		"userinfo":     {"source_ref": {"https://anilist.co@evil.com/anime/5"}},
		"bad pick":     {"pick": {"12abc"}}, // a valid path id, refused by the source
		"control pick": {"pick": {"1\x002"}},
		"zero":         {"source_ref": {"0"}},
		"manga":        {"source_ref": {"https://anilist.co/manga/5"}},
		"pick as url":  {"pick": {"https://anilist.co/anime/5"}},
		"bad source":   {"pick": {"5"}, "source": {"mal"}},
	} {
		rec := e.do("POST", matchURL, form, true)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), `name="source_ref"`)
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
	e.fp.animeErr = anilist.ErrNotFound
	rec := e.do("POST", matchURL, url.Values{"source_ref": {"999999"}}, true)
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
	for name, perr := range map[string]error{"unavailable": anilist.ErrUnavailable, "rate limited": anilist.ErrRateLimited} {
		t.Run(name, func(t *testing.T) {
			e := newMatchEnv(t)
			id := e.unmatched(t)
			before, enB := e.snapshot(t, id)
			e.fp.animeErr = perr
			rec := e.do("POST", matchURL, url.Values{"source_ref": {"1535"}}, true)
			if rec.Code != 503 {
				t.Fatalf("%d", rec.Code)
			}
			mustContain(t, rec.Body.String(), "try again", "Nothing was changed")
			after, enA := e.snapshot(t, id)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(enB, enA) || len(e.jobs.got) != 0 {
				t.Fatal("changed")
			}
			// no JS: still a readable page with the status
			rec = e.do("POST", matchURL, url.Values{"source_ref": {"1535"}}, false)
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
	swaps := slotStatuses(t, b)
	for _, code := range []int{422, 503} {
		if !swaps[code] {
			t.Errorf("match page's dialog slot does not swap %d", code)
		}
	}
}

func TestMatchEnqueuesSync(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	e.do("POST", matchURL, url.Values{"pick": {"1535"}}, true)
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != "sync_item" {
		t.Fatalf("%+v", e.jobs.got)
	}
	p := strings.ReplaceAll(toJSON(e.jobs.got[0].Payload), " ", "")
	if !strings.Contains(p, `"item_id":`+itoa(id)) || !strings.Contains(p, `"plugin_id":"anime"`) {
		t.Fatalf("payload %v", p)
	}
	// enqueue failure is logged, conversion stays
	e2 := newMatchEnv(t)
	id2 := e2.unmatched(t)
	e2.jobs.fail = errors.New("queue down")
	if rec := e2.do("POST", matchURL, url.Values{"pick": {"1535"}}, true); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if it, _ := e2.snapshot(t, id2); it.Source != "anilist" {
		t.Fatal("not converted")
	}
}

func TestMatchRefusesDuplicateWithLink(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	e.saved(t) // anilist 1535 already saved
	before, _ := e.snapshot(t, id)
	rec := e.do("POST", matchURL, url.Values{"source_ref": {"1535"}}, true)
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
	mustContain(t, b, "<html", `name="pick" value="1535"`, `name="source_ref"`)
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
	e.mp.searchErr = anilist.ErrRateLimited
	b := e.do("GET", matchURL+"/search?q=x", nil, true).Body.String()
	mustContain(t, b, "rate limiting")
	e.mp.searchErr = anilist.ErrUnavailable
	b = e.do("GET", matchURL+"/search?q=x", nil, true).Body.String()
	mustContain(t, b, "unavailable")
	e.mp.searchErr, e.mp.results = nil, nil
	b = e.do("GET", matchURL+"/search?q=zzz", nil, true).Body.String()
	mustContain(t, b, "No results")
}

func TestMatchKeepsOtherEntriesAndPeople(t *testing.T) {
	e := newMatchEnv(t)
	e.unmatched(t)
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
	if it, _ := e.snapshot(t, other); it.SourceID != "1535" {
		t.Fatal("other entry touched")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// REPLACES TestMatchSearchWithoutAnimePlugin404: a plugin without sources
// offers no match (dialog, search and POST answer 404).
func TestMatchNeedsASource(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Sources, book.Sources, book.M.Importers, book.Importers = nil, nil, nil, nil
	})
	e.saveFixture(t, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "m1", Titles: ttl("Mine")}},
		library.WithSyncState("done"))
	mustNotContain(t, e.get("/book/manual/m1").Body.String(), "Match to")
	for _, c := range []int{e.get("/book/manual/m1/match").Code, e.get("/book/manual/m1/match/search?q=x").Code,
		e.do("POST", "/book/manual/m1/match", url.Values{"pick": {"1"}}, true).Code} {
		if c != 404 {
			t.Fatalf("%d", c)
		}
	}
}

// tvManual saves a manual tv show "m1" in the list at episode 30.
func tvManual(t *testing.T, e *env) int64 {
	t.Helper()
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "m1", Kind: "show", Titles: ttl("My Show"),
		Fields: map[string]plugin.FieldValue{"type": {Text: "series"}}}}
	return e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active, Comment: "mine",
		Progress: map[string]float64{"episode": 30}}, library.WithSyncState("done"))
}

// REPLACES TestMatchUsesSourceLookup: matching asks the source's Search and
// Details (never Lookup) and converts the item in place without a sync job
// for a plugin that has none.
func TestMatchUsesSourceDetailsAndSearch(t *testing.T) {
	e := newFixtureEnv(t)
	id := tvManual(t, e.env)
	e.tv.Summaries = []plugin.ItemSummary{{Source: "tvdata", ID: "77", Kind: "show", Titles: ttl("Found Show"),
		Fields: map[string]plugin.FieldValue{"year": {Num: num(2001)}}}}
	e.tv.Data = tvShow("77", "Found Show")
	b := e.do("GET", "/tv/manual/m1/match", nil, true).Body.String()
	mustContain(t, b, "Match to Fake TV", "My Show", `placeholder="https://tv.example/show/123"`)
	mustNotContain(t, b, "anime", "Anime", "AniList")
	b = e.do("GET", "/tv/manual/m1/match/search?q=found", nil, true).Body.String()
	mustContain(t, b, "Found Show", "2001", `name="pick" value="77"`)
	if e.tv.Calls(plugintest.OpSearch) != 1 {
		t.Fatalf("searches %d", e.tv.Calls(plugintest.OpSearch))
	}
	rec := e.do("POST", "/tv/manual/m1/match", url.Values{"source_ref": {"https://www.tv.example/show/77/found-show"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/77" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if e.tv.Calls(plugintest.OpDetails) != 1 || e.tv.Calls(plugintest.OpLookup) != 0 {
		t.Fatalf("details %d lookup %d", e.tv.Calls(plugintest.OpDetails), e.tv.Calls(plugintest.OpLookup))
	}
	it, en := e.snapshot(t, id)
	if it.Source != "tvdata" || it.SourceID != "77" || it.Titles[0].Text != "Found Show" || en.Comment != "mine" {
		t.Fatalf("%+v %+v", it, en)
	}
	if len(e.jobs.got) != 0 {
		t.Fatalf("job for a fixture: %+v", e.jobs.got)
	}
}

func TestConvertClampsProgressToNewTotal(t *testing.T) {
	e := newFixtureEnv(t)
	id := tvManual(t, e.env)
	e.tv.Data = tvShow("77", "Found")
	e.tv.Data.Fields["episodes"] = plugin.FieldValue{Num: num(12)}
	if rec := e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"77"}}, true); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if _, en := e.snapshot(t, id); en.Progress["episode"] != 12 || en.Status != library.Active || en.Comment != "mine" {
		t.Fatalf("%+v", en)
	}
}

func TestMatchFetchesBeforeAnyChange(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		code int
		want string
	}{
		"not found":    {plugin.ErrNotFound, 422, "Fake TV has no show with that id."},
		"unavailable":  {plugin.ErrUnavailable, 503, "Nothing was changed"},
		"rate limited": {plugin.ErrRateLimited, 503, "Nothing was changed"},
		"bad answer":   {fmt.Errorf("tvdata: %w", plugin.ErrBadResponse), 502, "Fake TV returned an answer we could not use. Please try again later. Nothing was changed."},
	} {
		t.Run(name, func(t *testing.T) {
			e := newFixtureEnv(t)
			tvManual(t, e.env)
			e.tv.Errs = map[string]error{plugintest.OpDetails: c.err}
			before := dump(t, e.env)
			rec := e.do("POST", "/tv/manual/m1/match", url.Values{"source_ref": {"77"}}, true)
			if rec.Code != c.code {
				t.Fatalf("%d", rec.Code)
			}
			mustContain(t, rec.Body.String(), c.want, `value="77"`)
			if dump(t, e.env) != before {
				t.Fatal("database changed")
			}
		})
	}
}

func TestMatchConversionIsAtomic(t *testing.T) {
	e := newFixtureEnv(t)
	id := tvManual(t, e.env)
	e.tv.Data = tvShow("77", "Found")
	e.tv.Data.Relations = []plugin.Relation{{Kind: "sequel", Item: plugin.ItemSummary{Source: "tvdata", ID: "78"}}}
	before := dump(t, e.env)
	if _, err := e.d.Exec(`CREATE TRIGGER boom BEFORE INSERT ON relations BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"77"}}, true); rec.Code != 500 {
		t.Fatalf("%d", rec.Code)
	}
	if _, err := e.d.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	if dump(t, e.env) != before {
		t.Fatal("not rolled back")
	}
	if it, _ := e.snapshot(t, id); it.Source != "manual" {
		t.Fatal("converted")
	}
}

func TestMatchConcurrentDoublePostNever500(t *testing.T) {
	e := newFixtureEnv(t)
	tvManual(t, e.env)
	e.tv.Data = tvShow("77", "Found")
	e.tv.Delay = 50 * time.Millisecond
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"77"}}, true).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case 204:
			ok++
		case 404, 422:
		default:
			t.Fatalf("codes %v", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("codes %v", codes)
	}
	if items := e.savedItems(t, "tv", "tvdata", "77"); len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
}

// twoSources gives the tv fixture a first source without lookup ("tvalt")
// before its lookup source "tvdata".
func twoSources(alt *plugintest.FakeSource) func(_, tv *plugintest.Plugin) {
	return func(_, tv *plugintest.Plugin) {
		tv.M.Sources = append([]plugin.SourceInfo{{ID: "tvalt", Name: "Alt TV", RequestsPerMinute: 10, URLPatterns: []string{"alt.example/s/{id}"}}}, tv.M.Sources...)
		tv.Sources["tvalt"] = alt
	}
}

func TestMatchSourcesFollowKind(t *testing.T) {
	e := newFixtureEnv(t, withComicKind, kindSources)
	e.saveFixture(t, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "b1", Kind: "book", Titles: ttl("My Book")}}, library.WithSyncState("done"))
	e.saveFixture(t, "book", "comic", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "c1", Kind: "comic", Titles: ttl("My Comic")}}, library.WithSyncState("done"))
	b := e.get("/book/manual/b1/match").Body.String()
	mustContain(t, b, "Match to Fake Books", "Fake Books URL or id")
	mustNotContain(t, b, "Open Library")
	b = e.get("/book/manual/c1/match?kind=comic").Body.String()
	mustContain(t, b, "Match to Open Library", "Open Library URL or id", `placeholder="https://openlib.example/w/123"`)
	mustNotContain(t, b, "Fake Books")
}

func TestMatchWithSeveralSources(t *testing.T) {
	alt := &plugintest.FakeSource{Data: tvShow("5", "Alt Show")}
	e := newFixtureEnv(t, twoSources(alt))
	id := tvManual(t, e.env)
	e.tv.Data = tvShow("77", "Found")
	// default: the first source with lookup support
	b := e.do("GET", "/tv/manual/m1/match", nil, true).Body.String()
	mustContain(t, b, `name="source" value="tvdata"`, "Fake TV", `href="/tv/manual/m1/match?source=tvalt"`)
	b = e.do("GET", "/tv/manual/m1/match?source=tvalt", nil, true).Body.String()
	mustContain(t, b, `name="source" value="tvalt"`, `placeholder="https://alt.example/s/123"`, `hx-get="/tv/manual/m1/match/search?source=tvalt"`)
	e.do("GET", "/tv/manual/m1/match/search?source=tvalt&q=x", nil, true)
	if alt.Calls(plugintest.OpSearch) != 1 || e.tv.Calls(plugintest.OpSearch) != 0 {
		t.Fatal("searched the wrong source")
	}
	// a URL of the other source's pattern does not match the chosen source
	if rec := e.do("POST", "/tv/manual/m1/match", url.Values{"source": {"tvalt"}, "source_ref": {"https://tv.example/show/77"}}, true); rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
	rec := e.do("POST", "/tv/manual/m1/match", url.Values{"source": {"tvalt"}, "source_ref": {"alt.example/s/5"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv/tvalt/5" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if it, _ := e.snapshot(t, id); it.Source != "tvalt" || it.SourceID != "5" {
		t.Fatalf("%+v", it)
	}
}

// A pick and a pasted reference are both any valid source id (a bare id of
// letters, digits, '_' and '-', or a URL matching the source's patterns);
// the source's Details verifies it either way.
func TestMatchPickAcceptsNonDigitIDs(t *testing.T) {
	e := newFixtureEnv(t)
	id := tvManual(t, e.env)
	e.tv.Data = tvShow("x", "Found")
	rec := e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"tt-77"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv/tvdata/tt-77" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	if it, _ := e.snapshot(t, id); it.SourceID != "tt-77" {
		t.Fatalf("%+v", it)
	}
	e2 := newFixtureEnv(t)
	id2 := tvManual(t, e2.env)
	e2.tv.Data = tvShow("x", "Found")
	if rec := e2.do("POST", "/tv/manual/m1/match", url.Values{"source_ref": {"tt-77"}}, true); rec.Code != 204 ||
		rec.Header().Get("HX-Redirect") != "/tv/tvdata/tt-77" {
		t.Fatalf("reference: %d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	if e2.tv.Calls(plugintest.OpDetails) != 1 {
		t.Fatalf("source calls for the reference = %d", e2.tv.Calls(plugintest.OpDetails))
	}
	if it, _ := e2.snapshot(t, id2); it.SourceID != "tt-77" {
		t.Fatalf("%+v", it)
	}
}

// The no-JS search form keeps ?kind= (browsers drop the action's query on a
// GET submit).
func TestMatchSearchFormKeepsKind(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.saveFixture(t, "book", "comic", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "c1", Kind: "comic", Titles: ttl("Mine")}},
		library.WithSyncState("done"))
	b := e.get("/book/manual/c1/match?kind=comic").Body.String()
	mustContain(t, b, `<input type="hidden" name="kind" value="comic">`, `action="/book/manual/c1/match?kind=comic"`)
	mustNotContain(t, e.get("/anime/manual/x/match").Body.String(), `name="kind"`)
}

// The note on an unmatched imported item comes from its importer's
// UnmatchedNote with every {id} replaced by the item's external id; without
// a note the core words a neutral one from the id label or type.
func TestUnmatchedNoteComesFromTheImporter(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Importers[0].UnmatchedNote = "Imported from <Books CSV>; no match for ISBN #{id} ({id})."
	})
	d := bookItem("m1", "Lost Book")
	d.Source, d.ExternalURL, d.ExternalIDs = "manual", "", map[string]string{"isbn": "9780"}
	e.saveFixture(t, "book", "book", d, library.WithSyncState("done"))
	b := e.get("/book/manual/m1").Body.String()
	mustContain(t, b, "Imported from &lt;Books CSV&gt;; no match for ISBN #9780 (9780).")

	// no note: neutral wording with the label, else the id type
	e2 := newFixtureEnv(t, func(book, _ *plugintest.Plugin) { book.M.Importers[0].ExternalIDLabel = "ISBN" })
	e2.saveFixture(t, "book", "book", d, library.WithSyncState("done"))
	mustContain(t, e2.get("/book/manual/m1").Body.String(), "Imported without a match (ISBN #9780).")
	e3 := newFixtureEnv(t)
	e3.saveFixture(t, "book", "book", d, library.WithSyncState("done"))
	mustContain(t, e3.get("/book/manual/m1").Body.String(), "Imported without a match (isbn #9780).")

	// an item without the importer's id gets no note
	plain := bookItem("m2", "Typed Book")
	plain.Source, plain.ExternalURL = "manual", ""
	e.saveFixture(t, "book", "book", plain, library.WithSyncState("done"))
	mustNotContain(t, e.get("/book/manual/m2").Body.String(), "Imported from", "Imported without")
}
