package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime/anilist"
	"github.com/allister/allister/internal/settings"
)

type fakeProvider struct {
	mu      sync.Mutex
	queries []string
	results []anilist.SearchResult
	err     error
}

func (f *fakeProvider) Name() anilist.Source { return anilist.AniList }
func (f *fakeProvider) Search(ctx context.Context, q string) ([]anilist.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.results, f.err
}
func (f *fakeProvider) Anime(ctx context.Context, id string) (anilist.Anime, error) {
	return anilist.Anime{}, anilist.ErrNotFound
}
func (f *fakeProvider) Cast(ctx context.Context, id string) ([]anilist.CastMember, error) {
	return nil, anilist.ErrNotFound
}

func searchServer(t *testing.T, fp *fakeProvider) (http.Handler, *settings.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := settings.New(d)
	return NewServer(Deps{Settings: st, Providers: map[anilist.Source]anilist.Provider{anilist.AniList: fp}}), st
}

func doSearch(h http.Handler, q string, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/anime/search?q="+url.QueryEscape(q), nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func ip(i int) *int { return &i }

func sampleResult() anilist.SearchResult {
	return anilist.SearchResult{
		Source: anilist.AniList, ID: "1535",
		Titles: []anilist.Title{{Lang: "ja", Text: "デスノート"}, {Lang: "ja-latn", Text: "Death Note"}, {Lang: "en", Text: "Death Note EN"}},
		Type:   "TV", Episodes: ip(37), Year: ip(2006), Studio: "Madhouse",
		Synopsis: "A notebook.", CoverURL: "https://img.example/c.jpg",
	}
}

func TestSearchUsesPluginSource(t *testing.T) {
	fp := &fakeProvider{results: []anilist.SearchResult{sampleResult()}}
	h, _ := searchServer(t, fp)
	rec := doSearch(h, "death note", false)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if len(fp.queries) != 1 || fp.queries[0] != "death note" {
		t.Fatalf("queries %v", fp.queries)
	}
	body := rec.Body.String()
	for _, want := range []string{`href="/anime/anilist/1535"`, "Madhouse", "37 eps", "2006", "TV", "TV · 37 eps · 2006 · Madhouse",
		`src="https://img.example/c.jpg"`, `loading="lazy"`, `href="/anime/manual/new"`, "Can't find it? Add manually"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestSearchEmptyQueryMakesNoRequest(t *testing.T) {
	for _, q := range []string{"", "   ", "\t \n"} {
		fp := &fakeProvider{}
		h, _ := searchServer(t, fp)
		rec := doSearch(h, q, false)
		if rec.Code != 200 {
			t.Fatalf("q=%q got %d", q, rec.Code)
		}
		if len(fp.queries) != 0 {
			t.Fatalf("q=%q made %d provider calls", q, len(fp.queries))
		}
		if strings.Contains(rec.Body.String(), "No results") {
			t.Errorf("empty query should be neutral, got no-results message")
		}
	}
}

func TestSearchShowsPreferredTitle(t *testing.T) {
	fp := &fakeProvider{results: []anilist.SearchResult{sampleResult()}}
	h, st := searchServer(t, fp)
	if err := st.Set(context.Background(), settings.KeyTitleLang, "en"); err != nil {
		t.Fatal(err)
	}
	body := doSearch(h, "x", false).Body.String()
	if !strings.Contains(body, "Death Note EN") {
		t.Fatal("english title missing")
	}
	if strings.Contains(body, "デスノート") {
		t.Fatal("native title shown")
	}
}

func TestSearchProviderRateLimitedShowsMessage(t *testing.T) {
	fp := &fakeProvider{err: anilist.ErrRateLimited}
	h, _ := searchServer(t, fp)
	rec := doSearch(h, "x", false)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "try again") || !strings.Contains(body, `hx-get="/anime/search?q=x"`) {
		t.Fatalf("missing message or retry: %s", body)
	}
}

func TestSearchUnavailableShowsMessage(t *testing.T) {
	for _, e := range []error{anilist.ErrUnavailable, errors.New("boom")} {
		fp := &fakeProvider{err: e}
		h, _ := searchServer(t, fp)
		rec := doSearch(h, "x", false)
		if rec.Code != 200 {
			t.Fatalf("got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Search is unavailable right now, please try again") {
			t.Fatal("missing unavailable message")
		}
	}
}

func TestSearchNotFoundIsZeroResults(t *testing.T) {
	fp := &fakeProvider{err: anilist.ErrNotFound}
	h, _ := searchServer(t, fp)
	body := doSearch(h, "zzz", false).Body.String()
	if !strings.Contains(body, "No results for") || !strings.Contains(body, "/anime/manual/new") {
		t.Fatalf("got %s", body)
	}
}

func TestSearchPartialOnHTMX(t *testing.T) {
	fp := &fakeProvider{results: []anilist.SearchResult{sampleResult()}}
	h, _ := searchServer(t, fp)
	part := doSearch(h, "x", true).Body.String()
	full := doSearch(h, "x", false).Body.String()
	if strings.Contains(part, "<html") || strings.Contains(part, "<form") {
		t.Fatal("partial contains page chrome")
	}
	if !strings.Contains(part, "/anime/anilist/1535") {
		t.Fatal("partial missing results")
	}
	if !strings.Contains(full, "<html") || !strings.Contains(full, `id="results"`) || !strings.Contains(full, "/anime/anilist/1535") {
		t.Fatal("full page incomplete")
	}
	if !strings.Contains(full, `hx-trigger="keyup changed delay:400ms, search"`) || !strings.Contains(full, `hx-push-url="true"`) {
		t.Fatal("htmx attrs missing")
	}
}

func TestSearchHostileQueryEscaped(t *testing.T) {
	fp := &fakeProvider{}
	h, _ := searchServer(t, fp)
	q := `"><script>alert(1)</script>`
	for _, htmx := range []bool{false, true} {
		body := doSearch(h, q, htmx).Body.String()
		if strings.Contains(body, "<script>alert(1)") {
			t.Fatalf("htmx=%v unescaped query", htmx)
		}
	}
	fp.err = anilist.ErrRateLimited
	if strings.Contains(doSearch(h, q, false).Body.String(), "<script>alert(1)") {
		t.Fatal("unescaped in retry")
	}
}

func TestSearchQueryCappedAt200Runes(t *testing.T) {
	fp := &fakeProvider{}
	h, _ := searchServer(t, fp)
	doSearch(h, strings.Repeat("あ", 300), false)
	if got := []rune(fp.queries[0]); len(got) != 200 {
		t.Fatalf("got %d runes", len(got))
	}
}

func TestSearchSynopsisTruncatedAtRune(t *testing.T) {
	r := sampleResult()
	r.Synopsis = strings.Repeat("あ", 500)
	fp := &fakeProvider{results: []anilist.SearchResult{r}}
	h, _ := searchServer(t, fp)
	body := doSearch(h, "x", true).Body.String()
	if !strings.Contains(body, strings.Repeat("あ", 200)+"…") || strings.Contains(body, strings.Repeat("あ", 201)) {
		t.Fatal("not truncated to 200 runes with ellipsis")
	}
	if strings.ContainsRune(body, '�') {
		t.Fatal("broken rune")
	}
}

func TestSearchOmitsNilFields(t *testing.T) {
	// present: the episode count carries the field's unit
	full := doSearch(func() http.Handler {
		h, _ := searchServer(t, &fakeProvider{results: []anilist.SearchResult{sampleResult()}})
		return h
	}(), "x", true).Body.String()
	if !strings.Contains(full, "37 eps") {
		t.Fatalf("missing \"37 eps\" in %s", full)
	}
	r := sampleResult()
	r.Episodes, r.Year, r.Studio, r.CoverURL = nil, nil, "", ""
	fp := &fakeProvider{results: []anilist.SearchResult{r}}
	h, _ := searchServer(t, fp)
	body := doSearch(h, "x", true).Body.String()
	for _, bad := range []string{"eps", "Madhouse", "<img", "<nil>", "2006"} {
		if strings.Contains(body, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestIsPartialRequest(t *testing.T) {
	cases := []struct {
		hx, restore string
		want        bool
	}{
		{"", "", false},
		{"true", "", true},
		{"true", "true", false},
		{"", "true", false},
		{"false", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.hx != "" {
			r.Header.Set("HX-Request", c.hx)
		}
		if c.restore != "" {
			r.Header.Set("HX-History-Restore-Request", c.restore)
		}
		if got := isPartialRequest(r); got != c.want {
			t.Errorf("hx=%q restore=%q got %v", c.hx, c.restore, got)
		}
	}
}

func TestSearchHistoryRestoreGetsFullPage(t *testing.T) {
	fp := &fakeProvider{results: []anilist.SearchResult{sampleResult()}}
	h, _ := searchServer(t, fp)
	req := httptest.NewRequest(http.MethodGet, "/anime/search?q=x", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("history restore got partial")
	}
}

func TestSearchVaryHXRequest(t *testing.T) {
	fp := &fakeProvider{}
	h, _ := searchServer(t, fp)
	for _, htmx := range []bool{false, true} {
		vary := strings.Join(doSearch(h, "x", htmx).Header().Values("Vary"), ",")
		if !strings.Contains(vary, "HX-Request") {
			t.Fatalf("htmx=%v Vary=%q", htmx, vary)
		}
	}
}

func bookSummary(id, title string) plugin.ItemSummary {
	return plugin.ItemSummary{Source: "books", ID: id, Kind: "book", Titles: ttl(title),
		Fields: map[string]plugin.FieldValue{"author": {Text: "Ursula K. Le Guin"}, "pages": {Num: num(300)}}}
}

func TestSearchUsesFixturePluginSource(t *testing.T) {
	e := newFixtureEnv(t)
	e.book.Summaries = []plugin.ItemSummary{bookSummary("42", "A Wizard of Earthsea")}
	rec := e.get("/book/search?q=wizard")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if n := e.book.Calls(plugintest.OpSearch); n != 1 {
		t.Fatalf("search calls %d", n)
	}
	b := rec.Body.String()
	mustContain(t, b, "A Wizard of Earthsea", "Ursula K. Le Guin", `href="/book/books/42"`, "Search books",
		`action="/book/search"`, `href="/book/manual/new"`, "Can't find it? Add manually", "Search Fake Books")
	// pages is not a Show field; a single source shows no badge or switch
	mustNotContain(t, b, "300", `name="source"`, "data-source-badge")
	mustNotContain(t, b, "AniList")
	// the empty state names the source too
	mustContain(t, e.get("/book/search").Body.String(), "Type a title to search Fake Books.")
	if n := e.book.Calls(plugintest.OpSearch); n != 1 {
		t.Fatalf("empty query searched: %d", n)
	}
}

func TestSearchResultsCappedAt20(t *testing.T) {
	e := newFixtureEnv(t)
	for i := 0; i < 30; i++ {
		e.book.Summaries = append(e.book.Summaries, bookSummary(strconv.Itoa(i), "Book "+strconv.Itoa(i)))
	}
	b := e.get("/book/search?q=b").Body.String()
	if n := strings.Count(b, `href="/book/books/`); n != 20 {
		t.Fatalf("%d result links, want 20", n)
	}
}

func TestSearchSourceSwitchOnlyWithSeveralSources(t *testing.T) {
	other := &plugintest.FakeSource{Summaries: []plugin.ItemSummary{{Source: "openlib", ID: "OL1", Kind: "book", Titles: ttl("Other Source Book")}}}
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Sources = append(book.M.Sources, plugin.SourceInfo{ID: "openlib", Name: "Open Library", RequestsPerMinute: 60})
		book.Sources["openlib"] = other
	})
	e.book.Summaries = []plugin.ItemSummary{bookSummary("42", "Default Source Book")}
	b := e.get("/book/search?q=x").Body.String()
	mustContain(t, b, "Default Source Book", `href="/book/books/42"`, `name="source"`, "Open Library", "data-source-badge")
	mustNotContain(t, b, "Other Source Book")
	b = e.get("/book/search?q=x&source=openlib").Body.String()
	mustContain(t, b, "Other Source Book", `href="/book/openlib/OL1"`, `value="openlib" selected`)
	if other.Calls(plugintest.OpSearch) != 1 || e.book.Calls(plugintest.OpSearch) != 1 {
		t.Fatalf("calls default %d other %d", e.book.Calls(plugintest.OpSearch), other.Calls(plugintest.OpSearch))
	}
	// the retry control keeps the selected source
	other.Err = plugin.ErrRateLimited
	mustContain(t, e.get("/book/search?q=x&source=openlib").Body.String(), `hx-get="/book/search?q=x&amp;source=openlib"`, "Open Library is rate limiting")
	if c := e.get("/book/search?q=x&source=nope").Code; c != 404 {
		t.Fatalf("unknown source: %d", c)
	}
	// anime has one source: no switch
	mustNotContain(t, e.get("/anime/search?q=x").Body.String(), `name="source"`)
}

func TestSearchKindSelection(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.book.Summaries = []plugin.ItemSummary{bookSummary("42", "Some Book")}
	b := e.get("/book/search?q=x").Body.String()
	mustContain(t, b, `href="/book/books/42?kind=book"`, `name="kind"`, "Comics")
	b = e.get("/book/search?q=x&kind=comic").Body.String()
	mustContain(t, b, `href="/book/books/42?kind=comic"`, `value="comic" selected`, "Search comics")
	if c := e.get("/book/search?q=x&kind=show").Code; c != 404 {
		t.Fatalf("unknown kind: %d", c)
	}
	// the result link opens a working overview
	e.book.Data = bookItem("42", "Some Book")
	if c := e.get("/book/books/42?kind=comic").Code; c != 200 {
		t.Fatalf("overview from result link: %d", c)
	}
}

func TestSearchCardShowsKindFieldsAndTruncatedText(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		tv.M.Kinds[0].Fields = append(tv.M.Kinds[0].Fields,
			plugin.FieldDef{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText, Show: true},
			plugin.FieldDef{Key: "homepage", Label: "Homepage", Type: plugin.FieldURL, Show: true})
		tv.M.Kinds[0].Sections[1].Field = "synopsis"
	})
	sum := plugin.ItemSummary{Source: "tvdata", ID: "5", Kind: "show", Titles: ttl("The Show"), CoverURL: "",
		Fields: map[string]plugin.FieldValue{"type": {Text: "series"}, "year": {Num: num(2019)},
			"synopsis": {Text: strings.Repeat("é", 300)}, "homepage": {Text: "https://tv.example/x"}}}
	e.tv.Summaries = []plugin.ItemSummary{sum}
	b := e.get("/tv/search?q=x").Body.String()
	mustContain(t, b, "series · 2019", strings.Repeat("é", 200)+"…", "data-cover-placeholder")
	mustNotContain(t, b, strings.Repeat("é", 201), "tv.example/x", "<img")
}

func TestSearchOfDisabledPlugin404(t *testing.T) {
	e := newFixtureEnv(t)
	e.disable(t, "book")
	if c := e.get("/book/search?q=x").Code; c != 404 {
		t.Fatalf("got %d", c)
	}
	if e.book.Calls(plugintest.OpSearch) != 0 {
		t.Fatal("searched while disabled")
	}
}

func TestSearchSwitchesRequestOnChange(t *testing.T) {
	other := &plugintest.FakeSource{}
	e := newFixtureEnv(t, withComicKind, func(book, _ *plugintest.Plugin) {
		book.M.Sources = append(book.M.Sources, plugin.SourceInfo{ID: "openlib", Name: "Open Library", RequestsPerMinute: 60})
		book.Sources["openlib"] = other
	})
	b := e.get("/book/search?q=x").Body.String()
	for _, name := range []string{"kind", "source"} {
		mustContain(t, b, `<select name="`+name+`" aria-label=`, `hx-get="/book/search" hx-trigger="change" hx-target="#results" hx-include="closest form" hx-push-url="true"`)
	}
	if n := strings.Count(b, `hx-trigger="change"`); n != 2 {
		t.Fatalf("%d change triggers, want 2", n)
	}
	// the plain GET form still works without JavaScript
	mustContain(t, b, `<form method="get" action="/book/search"`, `<button type="submit"`)
	// one kind and one source: no selects, no change triggers
	mustNotContain(t, e.get("/tv/search?q=x").Body.String(), `hx-trigger="change"`, "<select")
}
