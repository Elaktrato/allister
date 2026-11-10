package web

import (
	"context"
	"encoding/json"
	"github.com/allister/allister/internal/library"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// searchProvider is the sample provider with a working search.
type searchProvider struct{ *animeProvider }

func (s searchProvider) Search(ctx context.Context, q string) ([]anilist.SearchResult, error) {
	return []anilist.SearchResult{s.anime.SearchResult}, nil
}

// newE2EEnv is newEnv with a provider that also answers searches.
func newE2EEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	reg := animeRegistry(t, e.set, searchProvider{e.fp})
	_, e.h = newServer(Deps{
		Settings: e.set, Jobs: e.jobs, SyncJobs: testSyncJobs(), CoversDir: e.covers,
		Registry: reg, Library: library.New(e.d, reg),
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
	rec = e.do("POST", "/anime/anilist/1535/add", url.Values{"status": {"active"}, "progress": {"3"}}, true)
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

func mustID(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
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
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != jobs.KindSyncItem {
		t.Fatalf("jobs: %+v", e.jobs.got)
	}
	raw, _ := json.Marshal(e.jobs.got[0].Payload)
	sync := jobs.SyncItem(jobs.SyncDeps{
		Library: e.lib, Registry: e.reg, HTTP: imgs.Client(), CoversDir: e.covers,
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
	if en, _ := getEntry(context.Background(), e.lib, mustID(t, id)); en == nil || en.Rating == nil || *en.Rating != 0 {
		t.Fatalf("rating 0 not stored: %+v", en)
	}
	edit("")
	if en, _ := getEntry(context.Background(), e.lib, mustID(t, id)); en == nil || en.Rating != nil {
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

func TestMatchFlowEndToEnd(t *testing.T) {
	imgs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("img:" + r.URL.Path))
	}))
	t.Cleanup(imgs.Close)

	e := newMatchEnv(t)
	e.fp.anime.CoverURL = imgs.URL + "/cover.jpg"
	e.fp.cast[0].Character.ImageURL = imgs.URL + "/c.jpg"
	e.fp.cast[0].Actor.ImageURL = imgs.URL + "/a.jpg"
	id := e.unmatched(t) // status completed, rating 0, progress 3, start date, comment "c"

	list := e.get("/anime").Body.String()
	mustContain(t, list, "Lost Show", "unmatched")
	_, enBefore := e.snapshot(t, id)

	// the overview offers matching
	mustContain(t, e.get("/anime/manual/man1").Body.String(), "Match to AniList")

	rec := e.do("POST", "/anime/manual/man1/match", url.Values{"source_ref": {"https://anilist.co/anime/1535/Death-Note"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime/anilist/1535" {
		t.Fatalf("match: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}

	// the new URL shows the AniList data
	rec = e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("overview: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "デスノート", "Death Note EN", "Completed")
	mustNotContain(t, rec.Body.String(), "Match to AniList")

	// the list: no badge, same data, same anime id
	list = e.get("/anime").Body.String()
	mustContain(t, list, "デスノート")
	mustNotContain(t, list, "unmatched", "Lost Show")
	if got := listID(t, list); got != itoa(id) {
		t.Fatalf("anime id changed: %s != %d", got, id)
	}
	sv, en := e.snapshot(t, id)
	if sv.Source != "anilist" || !reflect.DeepEqual(en, enBefore) {
		t.Fatalf("%+v\n%+v\n%+v", sv, en, enBefore)
	}
	if en.Rating == nil || *en.Rating != 0 || en.Progress["episode"] != 3 || en.Comment != "c" || en.StartedOn == nil {
		t.Fatalf("list data: %+v", en)
	}

	// one sync job for the entry; running it brings the cast
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != jobs.KindSyncItem {
		t.Fatalf("jobs: %+v", e.jobs.got)
	}
	raw, _ := json.Marshal(e.jobs.got[0].Payload)
	sync := jobs.SyncItem(jobs.SyncDeps{
		Library: e.lib, Registry: e.reg, HTTP: imgs.Client(), CoversDir: e.covers,
	})
	if err := sync(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	body := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, body, "Light Yagami", "Mamoru Miyano")
	rec = e.get(e.personPath(t, "a1"))
	mustContain(t, rec.Body.String(), "Mamoru Miyano", "as Light Yagami")
}

func TestMatchRefusesAlreadySavedAnime(t *testing.T) {
	e := newMatchEnv(t)
	id := e.unmatched(t)
	existing := e.saved(t) // anilist 1535 is already tracked
	before, enBefore := e.snapshot(t, id)

	rec := e.do("POST", "/anime/manual/man1/match", url.Values{"pick": {"1535"}}, true)
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "already in your library", `href="/anime/anilist/1535"`)
	after, enAfter := e.snapshot(t, id)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(enBefore, enAfter) {
		t.Fatal("the unmatched entry changed")
	}
	if len(e.jobs.got) != 0 {
		t.Fatalf("jobs: %+v", e.jobs.got)
	}
	if sv, _ := e.lib.Item(context.Background(), existing); sv == nil || sv.Source != string(anilist.AniList) {
		t.Fatal("existing entry touched")
	}
	mustContain(t, e.get("/anime").Body.String(), "unmatched")
}
