package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/provider"
)

// searchProvider is the sample provider with a working search.
type searchProvider struct{ *animeProvider }

func (s searchProvider) Search(ctx context.Context, q string) ([]provider.SearchResult, error) {
	return []provider.SearchResult{s.anime.SearchResult}, nil
}

// newE2EEnv is newEnv with a provider that also answers searches.
func newE2EEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	_, e.h = newServer(Deps{
		Settings: e.set, Store: e.st, Jobs: e.jobs, CoversDir: e.covers,
		Providers: map[provider.Source]provider.Provider{provider.AniList: searchProvider{e.fp}},
	}, func() time.Time { return *e.now })
	return e
}

func TestSearchAddListFlow(t *testing.T) {
	e := newE2EEnv(t)
	// search -> result links to the overview
	rec := e.do("GET", "/anime/search?q=death", nil, false)
	if rec.Code != 200 {
		t.Fatalf("search: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "デスノート", `href="/anime/anilist/1535"`)
	// overview offers adding
	rec = e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("overview: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Add to list")
	// add
	rec = e.do("POST", "/anime/anilist/1535/add", url.Values{"status": {"watching"}, "progress": {"3"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime" {
		t.Fatalf("add: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	// list shows title and progress
	rec = e.get("/anime")
	mustContain(t, rec.Body.String(), "デスノート", "3 /")
	id := listID(t, rec.Body.String())
	// +1
	rec = e.do("POST", "/list/"+id+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatalf("increment: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "4 /")
	mustContain(t, e.get("/anime").Body.String(), "4 /")
}

var listIDRe = regexp.MustCompile(`/list/(\d+)/increment`)

func listID(t *testing.T, body string) string {
	t.Helper()
	m := listIDRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no list row with increment link in page")
	}
	return m[1]
}

var personRe = regexp.MustCompile(`href="(/people/\d+)"`)

func TestManualEntryAndPersonFlow(t *testing.T) {
	imgs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("img:" + r.URL.Path))
	}))
	t.Cleanup(imgs.Close)

	e := newE2EEnv(t)
	e.fp.anime.CoverURL = imgs.URL + "/cover.jpg"
	e.fp.cast[0].Character.ImageURL = imgs.URL + "/c.jpg"
	e.fp.cast[0].Actor.ImageURL = imgs.URL + "/a.jpg"

	if rec := e.do("POST", "/anime/anilist/1535/add", url.Values{"status": {"completed"}, "rating": {"8"}}, true); rec.Code != 204 {
		t.Fatalf("add: %d", rec.Code)
	}
	// run the queued sync job so cast and people exist
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != jobs.KindSyncAnime {
		t.Fatalf("jobs: %+v", e.jobs.got)
	}
	raw, _ := json.Marshal(e.jobs.got[0].Payload)
	sync := jobs.SyncAnime(jobs.SyncDeps{
		Store: e.st, Providers: map[provider.Source]provider.Provider{provider.AniList: e.fp},
		HTTP: imgs.Client(), CoversDir: e.covers,
	})
	if err := sync(context.Background(), raw); err != nil {
		t.Fatal(err)
	}

	// overview links to the people
	body := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, body, "Light Yagami", "Mamoru Miyano")
	links := personRe.FindAllStringSubmatch(body, -1)
	if len(links) < 2 {
		t.Fatalf("want person links, got %v", links)
	}
	actor := e.personPath(t, "a1")
	mustContain(t, body, `href="`+actor+`"`)

	// person page lists the anime with the character
	rec := e.get(actor)
	if rec.Code != 200 {
		t.Fatalf("person: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Mamoru Miyano", "デスノート", "as Light Yagami", `href="/anime/anilist/1535"`)

	// edit: rating 0 is accepted, blank means unrated
	id := listID(t, e.get("/anime").Body.String())
	edit := func(rating string) {
		t.Helper()
		rec := e.do("POST", "/list/"+id+"/edit", url.Values{"status": {"completed"}, "rating": {rating}, "progress": {"37"}}, true)
		if rec.Code != 204 {
			t.Fatalf("edit %q: %d %s", rating, rec.Code, rec.Body.String())
		}
	}
	edit("0")
	if en, _ := e.st.GetListEntry(context.Background(), mustAtoi(t, id)); en == nil || en.Rating == nil || *en.Rating != 0 {
		t.Fatalf("rating 0 not stored: %+v", en)
	}
	edit("")
	if en, _ := e.st.GetListEntry(context.Background(), mustAtoi(t, id)); en == nil || en.Rating != nil {
		t.Fatalf("blank rating must be unrated: %+v", en)
	}

	// remove: the person page no longer lists the anime
	if rec := e.do("POST", "/list/"+id+"/remove", url.Values{}, true); rec.Code != 204 {
		t.Fatalf("remove: %d", rec.Code)
	}
	mustNotContain(t, e.get("/anime").Body.String(), "デスノート")
	rec = e.get(actor)
	if rec.Code != 200 {
		t.Fatalf("person after remove: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Not in any anime in your list")
	mustNotContain(t, rec.Body.String(), "as Light Yagami")
}

func mustAtoi(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func TestImportFlow(t *testing.T) {
	e := newImpEnv(t, notFoundExcept("1"))
	csv := []byte(impHeader + impRow(1, "Found One", "Completed") + impRow(2, "Lost Show", "Planning"))

	if rec := e.upload(t, "file", "yamtrack.csv", csv); rec.Code != 303 || rec.Header().Get("Location") != "/settings/import" {
		t.Fatalf("upload: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	e.runJob(t)
	mustContain(t, e.get("/settings/import", false).Body.String(), "Done", `data-count="imported">1<`, `data-count="unmatched">1<`)

	list := e.get("/anime", false).Body.String()
	mustContain(t, list, "T1", "Lost Show", "unmatched")
	if n := strings.Count(list, "unmatched</span>"); n != 1 {
		t.Fatalf("want one unmatched badge, got %d", n)
	}
	count := func() int {
		return strings.Count(e.get("/anime", false).Body.String(), "<tr data-row")
	}
	if count() != 2 {
		t.Fatalf("want 2 rows, got %d", count())
	}

	// a second upload of the same file after completion creates no duplicates
	e.jobs.got = nil
	if rec := e.upload(t, "file", "yamtrack.csv", csv); rec.Code != 303 {
		t.Fatalf("second upload: %d", rec.Code)
	}
	e.runJob(t)
	if count() != 2 {
		t.Fatalf("duplicates after re-import: %d rows", count())
	}
}
