package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
)

type fakeProvider struct {
	mu      sync.Mutex
	queries []string
	results []provider.SearchResult
	err     error
}

func (f *fakeProvider) Name() provider.Source { return provider.AniList }
func (f *fakeProvider) Search(ctx context.Context, q string) ([]provider.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.results, f.err
}
func (f *fakeProvider) Anime(ctx context.Context, id string) (provider.Anime, error) {
	return provider.Anime{}, provider.ErrNotFound
}
func (f *fakeProvider) Cast(ctx context.Context, id string) ([]provider.CastMember, error) {
	return nil, provider.ErrNotFound
}

func searchServer(t *testing.T, fp *fakeProvider) (http.Handler, *settings.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := settings.New(d)
	return NewServer(Deps{Settings: st, Providers: map[provider.Source]provider.Provider{provider.AniList: fp}}), st
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

func sampleResult() provider.SearchResult {
	return provider.SearchResult{
		Source: provider.AniList, ID: "1535",
		Titles: []provider.Title{{Lang: "ja", Text: "デスノート"}, {Lang: "ja-latn", Text: "Death Note"}, {Lang: "en", Text: "Death Note EN"}},
		Type:   "TV", Episodes: ip(37), Year: ip(2006), Studio: "Madhouse",
		Synopsis: "A notebook.", CoverURL: "https://img.example/c.jpg",
	}
}

func TestSearchUsesAniList(t *testing.T) {
	fp := &fakeProvider{results: []provider.SearchResult{sampleResult()}}
	h, _ := searchServer(t, fp)
	rec := doSearch(h, "death note", false)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if len(fp.queries) != 1 || fp.queries[0] != "death note" {
		t.Fatalf("queries %v", fp.queries)
	}
	body := rec.Body.String()
	for _, want := range []string{`href="/anime/anilist/1535"`, "Madhouse", "37", "2006", "TV",
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
	fp := &fakeProvider{results: []provider.SearchResult{sampleResult()}}
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
	fp := &fakeProvider{err: provider.ErrRateLimited}
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
	for _, e := range []error{provider.ErrUnavailable, errors.New("boom")} {
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
	fp := &fakeProvider{err: provider.ErrNotFound}
	h, _ := searchServer(t, fp)
	body := doSearch(h, "zzz", false).Body.String()
	if !strings.Contains(body, "No results for") || !strings.Contains(body, "/anime/manual/new") {
		t.Fatalf("got %s", body)
	}
}

func TestSearchPartialOnHTMX(t *testing.T) {
	fp := &fakeProvider{results: []provider.SearchResult{sampleResult()}}
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
	fp.err = provider.ErrRateLimited
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
	fp := &fakeProvider{results: []provider.SearchResult{r}}
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
	r := sampleResult()
	r.Episodes, r.Year, r.Studio, r.CoverURL = nil, nil, "", ""
	fp := &fakeProvider{results: []provider.SearchResult{r}}
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
	fp := &fakeProvider{results: []provider.SearchResult{sampleResult()}}
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
