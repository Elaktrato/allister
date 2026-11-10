package web

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/allister/allister/internal/library"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
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
	rec = e.get("/anime?view=table")
	mustContain(t, rec.Body.String(), "デスノート", "3 /")
	id := listID(t, rec.Body.String())
	// +1
	rec = e.do("POST", "/list/"+id+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatalf("increment: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "4 /")
	mustContain(t, e.get("/anime?view=table").Body.String(), "4 /")
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
	id := listID(t, e.get("/anime?view=table").Body.String())
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
	mustNotContain(t, e.get("/anime?view=table").Body.String(), "デスノート")
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

	list := e.get("/anime?view=table", false).Body.String()
	mustContain(t, list, "T1", "Lost Show", ">unmatched</span>")
	if n := strings.Count(list, "unmatched</span>"); n != 1 {
		t.Fatalf("want one unmatched badge, got %d", n)
	}
	count := func() int {
		return strings.Count(e.get("/anime?view=table", false).Body.String(), "<tr data-row")
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

	list := e.get("/anime?view=table").Body.String()
	mustContain(t, list, "Lost Show", ">unmatched</span>")
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
	list = e.get("/anime?view=table").Body.String()
	mustContain(t, list, "デスノート")
	mustNotContain(t, list, ">unmatched</span>", "Lost Show")
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
	mustContain(t, e.get("/anime?view=table").Body.String(), ">unmatched</span>")
}

// -----------------------------------------------------------------------------
// End-to-end checks of the finished iteration. They run on the three-plugin
// server of contract_proof_test.go (wired like cmd/allister, with the real
// anime plugin, the real job queue and an image server) and drive it the way
// a browser does: links are followed from the rendered HTML, forms are read
// from the rendered HTML (browserForm) and submitted as same-origin posts.
// The contract proof (contract_proof_test.go) covers the fixture plugins;
// these tests cover the user's anime path through Settings, the list search,
// Edit details and the backup.

var (
	e2eFormRe        = regexp.MustCompile(`(?s)<form [^>]*>.*?</form>`)
	e2eHrefRe        = regexp.MustCompile(`href="([^"]*)"`)
	e2eMultiSelectRe = regexp.MustCompile(`(?s)<select [^>]*\bmultiple\b[^>]*>.*?</select>`)
)

// formWith returns the first form of page whose start tag contains attr.
func formWith(t *testing.T, page, attr string) string {
	t.Helper()
	for _, f := range e2eFormRe.FindAllString(page, -1) {
		if strings.Contains(f[:strings.Index(f, ">")], attr) {
			return f
		}
	}
	t.Fatalf("no form with %s in page", attr)
	return ""
}

// submitted is what a browser submits for form: browserForm, except that a
// multiple select with nothing selected sends nothing.
func submitted(t *testing.T, form string) url.Values {
	t.Helper()
	return browserForm(t, e2eMultiSelectRe.ReplaceAllStringFunc(form, func(s string) string {
		if strings.Contains(s, " selected") {
			return s
		}
		return ""
	}))
}

// hrefWith returns the first link of page whose (unescaped) target starts
// with prefix.
func hrefWith(t *testing.T, page, prefix string) string {
	t.Helper()
	for _, m := range e2eHrefRe.FindAllStringSubmatch(page, -1) {
		if h := html.UnescapeString(m[1]); strings.HasPrefix(h, prefix) {
			return h
		}
	}
	t.Fatalf("no link starting with %q in page", prefix)
	return ""
}

// browserPost submits form to target as a same-origin browser form post.
func (e *env) browserPost(target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// togglePlugin presses the Disable/Enable button of a plugin's row in
// Settings > Plugins and expects the redirect back to that page.
func (e *proofEnv) togglePlugin(t *testing.T, id, wantButton string) {
	t.Helper()
	page := e.page(t, "/settings/plugins")
	i := strings.Index(page, `data-plugin="`+id+`"`)
	if i < 0 {
		t.Fatalf("no settings row for %s", id)
	}
	row := page[i : i+strings.Index(page[i:], "</li>")]
	mustContain(t, row, ">"+wantButton+"</button>")
	form := formWith(t, row, `action="/settings/plugins/`+id+`"`)
	rec := e.browserPost("/settings/plugins/"+id, submitted(t, form))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins" {
		t.Fatalf("%s %s: %d %q", wantButton, id, rec.Code, rec.Header().Get("Location"))
	}
}

// addFromSearch searches the anime source, follows the result to the
// overview and adds it with form (htmx, like the add dialog).
func (e *proofEnv) addFromSearch(t *testing.T, q string, form url.Values) string {
	t.Helper()
	overview := hrefWith(t, e.page(t, "/anime/search?q="+url.QueryEscape(q)), "/anime/anilist/")
	mustContain(t, e.page(t, overview), "Add to list")
	rec := e.do("POST", overview+"/add", form, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/anime" {
		t.Fatalf("add %s: %d %q %s", overview, rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	return overview
}

// manualFromForm opens the manual entry page, fills its rendered form with
// set and posts it; it returns the new overview URL.
func (e *proofEnv) manualFromForm(t *testing.T, set url.Values, up *upload) string {
	t.Helper()
	v := submitted(t, formWith(t, e.page(t, "/anime/manual/new"), `action="/anime/manual/new"`))
	for k, vals := range set {
		v[k] = vals
	}
	rec := e.postMultipart(t, "/anime/manual/new", v, up)
	if rec.Code != http.StatusSeeOther || !manualLoc.MatchString(rec.Header().Get("Location")) {
		t.Fatalf("manual: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func TestPluginToggleEndToEnd(t *testing.T) {
	e := newProofEnv(t)
	overview := e.addFromSearch(t, "death", url.Values{"status": {"active"}, "progress": {"4"}})
	id := e.itemID(t, "anime", "anilist", "1535")

	// switched off from its Settings row before its sync job ran
	e.togglePlugin(t, "anime", "Disable")
	e.drain(t)
	if got := e.jobRows(t); len(got) != 1 || got[0].State != "pending" {
		t.Fatalf("the anime sync job must wait while the plugin is off: %+v", got)
	}
	settings := e.page(t, "/settings")
	mustNotContain(t, navOf(t, settings), `href="/anime"`)
	mustNotContain(t, settings, `href="/anime/export/json"`)
	if rec := e.get("/"); rec.Header().Get("Location") != "/book" {
		t.Fatalf("home with anime off: %q", rec.Header().Get("Location"))
	}
	for _, p := range []string{"/anime", "/anime/search?q=death", overview, overview + "/recommendations", overview + "/edit",
		"/anime/manual/new", "/anime/export/json", "/list/" + id + "/edit"} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with anime off: %d", p, rec.Code)
		}
	}
	if rec := e.browserPost("/list/"+id+"/increment", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("increment with anime off: %d", rec.Code)
	}
	// the universal backup still holds the switched-off plugin's entry
	if _, items, _ := backupJSON(t, e.download(t)); len(items) != 1 || items[0].Entry == nil {
		t.Fatalf("backup with anime off: %+v", items)
	}

	// switched on again: the paused job runs, nothing was lost
	e.togglePlugin(t, "anime", "Enable")
	e.drain(t)
	list := e.page(t, "/anime?view=table&view=table")
	mustRows(t, list, "デスノート")
	mustContain(t, list, "4 / 37", statusCell("Watching"))
	mustNotContain(t, list, "partially synced")
	body := e.page(t, overview)
	mustContain(t, body, "Light Yagami", "Mamoru Miyano")
	mustContain(t, e.page(t, overview+"/recommendations"), "Rec Title")
	actor := e.personPath(t, "a1")
	mustContain(t, e.page(t, actor), "as Light Yagami", `href="`+overview+`"`)

	// off once more: the person page of the plugin goes too, and comes back
	e.togglePlugin(t, "anime", "Disable")
	if rec := e.get(actor); rec.Code != http.StatusNotFound {
		t.Fatalf("person page with anime off: %d", rec.Code)
	}
	e.togglePlugin(t, "anime", "Enable")
	mustContain(t, e.page(t, actor), "as Light Yagami")
	if rec := e.do("POST", "/list/"+id+"/increment", url.Values{}, true); rec.Code != http.StatusOK {
		t.Fatalf("increment: %d", rec.Code)
	}
	mustContain(t, e.page(t, "/anime?view=table&view=table"), "5 / 37")
}

func TestListSearchEndToEnd(t *testing.T) {
	e := newProofEnv(t)
	e.addFromSearch(t, "death", url.Values{"status": {"active"}, "progress": {"37"}, "rating": {"9"}})
	e.manualFromForm(t, url.Values{"title": {"Cowboy Bebop"}, "field.type": {"TV"}, "field.year": {"1998"},
		"field.studio": {"Sunrise"}, "field.episodes": {"26"}, "genres": {"Space\nWestern"}, "status": {"active"}, "progress": {"5"}}, nil)
	e.manualFromForm(t, url.Values{"title": {"Perfect Blue"}, "field.type": {"MOVIE"}, "field.year": {"1997"},
		"field.studio": {"Madhouse"}, "genres": {"Thriller"}, "status": {"planned"}}, nil)
	e.drain(t)

	list := e.page(t, "/anime?view=table&view=table")
	mustRows(t, list, "Perfect Blue", "Cowboy Bebop", "デスノート")
	bar := formWith(t, list, `role="search"`)
	// a browser submits every field of the bar, most of them empty: only the
	// filled ones count and nothing is reported as unreadable
	search := func(set url.Values) (string, url.Values) {
		t.Helper()
		v := submitted(t, bar)
		for k, vals := range set {
			v[k] = vals
		}
		body := e.page(t, "/anime?view=table&"+v.Encode())
		if n := noticeOf(body); n != "" {
			t.Fatalf("notice for %v: %s", set, n)
		}
		return body, v
	}
	// free text matches a title in any language (de "Todesnotiz")
	body, _ := search(url.Values{"q": {"todesnotiz"}})
	mustRows(t, body, "デスノート")
	// schema filters (studio contains, year range) together
	body, _ = search(url.Values{"f.studio": {"madhouse"}, "f.year.from": {"1990"}, "f.year.to": {"2000"}})
	mustRows(t, body, "Perfect Blue")
	// type enum + sort by title ascending
	body, v := search(url.Values{"f.type": {"TV"}, "sort": {"title"}, "dir": {"asc"}})
	mustRows(t, body, "Cowboy Bebop", "デスノート")
	// the same query as a live (htmx) request answers only the rows and
	// pushes the canonical, bookmarkable URL; that URL reproduces the result
	rec := e.getWith("/anime?view=table&"+v.Encode(), map[string]string{"HX-Request": "true", "HX-Target": "list-rows"})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("live search: %d", rec.Code)
	}
	pushed := rec.Header().Get("HX-Push-Url")
	if pushed == "" || strings.Contains(pushed, "q=") || strings.Contains(pushed, "rmin=") {
		t.Fatalf("pushed URL %q", pushed)
	}
	mustRows(t, e.page(t, pushed), "Cowboy Bebop", "デスノート")
	// genre and progress-state filters from the same bar
	body, _ = search(url.Values{"genre": {"Western"}})
	mustRows(t, body, "Cowboy Bebop")
	body, _ = search(url.Values{"progress": {"finished"}})
	mustRows(t, body, "デスノート")
	body, _ = search(url.Values{"progress": {"not_started"}})
	mustRows(t, body, "Perfect Blue")
	// the status tab keeps the search, Clear filters drops it
	body, _ = search(url.Values{"q": {"e"}})
	tab := hrefWith(t, body, "/anime?q=e&status=active")
	mustRows(t, e.page(t, tab), "Cowboy Bebop", "デスノート")
	mustRows(t, e.page(t, hrefWith(t, e.page(t, tab), "/anime?status=active")), "Cowboy Bebop", "デスノート")
	i := strings.Index(body, `id="filter-clear"`)
	if i < 0 {
		t.Fatal("no Clear filters")
	}
	mustRows(t, e.page(t, hrefWith(t, body[i:], "/anime")), "Perfect Blue", "Cowboy Bebop", "デスノート")
}

func TestEditDetailsEndToEnd(t *testing.T) {
	e := newProofEnv(t)
	overview := e.addFromSearch(t, "death", url.Values{"status": {"active"}, "progress": {"3"}, "comment": {"keep me"}})
	id := e.itemID(t, "anime", "anilist", "1535")
	entryBefore, _ := getEntry(context.Background(), e.lib, mustID(t, id))

	// Edit details is a link on the overview, opened before the sync job ran
	edit := hrefWith(t, e.page(t, overview), overview+"/edit")
	page := e.page(t, edit)
	form := formWith(t, page, `action="`+overview+`/edit"`)
	mustNotContain(t, form, `name="status"`, `name="rating"`, `name="progress`)
	v := submitted(t, form)
	if v.Get("return") != overview {
		t.Fatalf("return %q", v.Get("return"))
	}
	if v.Get("field.studio") != "Madhouse" || v.Get("field.type") != "TV" {
		t.Fatalf("form not prefilled: %v", v)
	}
	// rename the Japanese title, add genres, replace the cover
	for i, l := range v["title_lang"] {
		if l == "ja" {
			v["title"][i] = "デスノート改"
		}
	}
	v.Set("genres", "Mystery\r\nThriller")
	cover := png1("user cover")
	rec := e.postMultipart(t, overview+"/edit", v, &upload{field: "cover", filename: "c.png", ctype: "image/png", data: cover})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != overview {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	edited := e.savedItems(t, "anime", "anilist", "1535")[0]
	if !strings.HasSuffix(edited.CoverPath, ".png") {
		t.Fatalf("cover %q", edited.CoverPath)
	}

	// the sync job that was queued at add time runs now: it brings the cast
	// but neither the remote cover nor anything else overwrites the edit
	e.drain(t)
	after := e.savedItems(t, "anime", "anilist", "1535")[0]
	if after.CoverPath != edited.CoverPath || after.SyncState != "done" {
		t.Fatalf("after sync: cover %q state %q", after.CoverPath, after.SyncState)
	}
	body := e.page(t, overview)
	mustContain(t, body, "デスノート改", ">Mystery</li>", ">Thriller</li>", `src="/covers/`+edited.CoverPath+`"`, "Light Yagami")
	if got := e.get("/covers/" + edited.CoverPath); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), cover) {
		t.Fatalf("cover served: %d", got.Code)
	}
	// the entry is untouched, and the list search sees the new title
	if en, _ := getEntry(context.Background(), e.lib, mustID(t, id)); !reflect.DeepEqual(en, entryBefore) {
		t.Fatalf("entry changed: %+v -> %+v", entryBefore, en)
	}
	mustRows(t, e.page(t, "/anime?view=table&q="+url.QueryEscape("改")), "デスノート改")
	mustRows(t, e.page(t, "/anime?genre=Thriller&view=table"), "デスノート改")
	// progress stays in the entry dialog, not in Edit details
	mustContain(t, e.page(t, "/list/"+id+"/edit"), `name="progress`)
}

func TestBackupEndToEnd(t *testing.T) {
	a := newProofEnv(t)
	overview := a.addFromSearch(t, "death", url.Values{"status": {"completed"}, "rating": {"0"}, "progress": {"37"}, "comment": {"twice"}})
	cover := png1("manual cover")
	manual := a.manualFromForm(t, url.Values{"title": {"Lost Show"}, "field.type": {"OVA"}, "field.episodes": {"2"},
		"genres": {"Drama"}, "status": {"paused"}, "progress": {"1"}, "rating": {"7"}},
		&upload{field: "cover", filename: "m.png", ctype: "image/png", data: cover})
	a.drain(t)

	// the per-plugin export is a JSON list of the plugin's entries; it is not a backup
	settings := a.page(t, "/settings/plugins")
	export := a.get(hrefWith(t, settings, "/anime/export/"))
	if export.Code != 200 || !strings.Contains(export.Header().Get("Content-Disposition"), "anime-json-") {
		t.Fatalf("export: %d %q", export.Code, export.Header().Get("Content-Disposition"))
	}
	mustContain(t, export.Body.String(), "Lost Show", "Death Note")

	// the universal backup: Settings > Backup > Download backup
	backupPage := a.page(t, hrefWith(t, settings, "/settings/backup"))
	dl := a.get(hrefWith(t, backupPage, "/settings/backup/download"))
	if dl.Code != 200 || dl.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %q", dl.Code, dl.Header().Get("Content-Type"))
	}
	data := dl.Body.Bytes()

	// restore into an empty server through its Backup page form
	b := newProofEnv(t)
	form := formWith(t, b.page(t, "/settings/backup"), `enctype="multipart/form-data"`)
	action := formAttrs(form)["action"]
	if rec := b.restoreAt(t, action, export.Body.Bytes(), "anime.json"); rec.Code < 400 {
		t.Fatalf("an export must not restore: %d", rec.Code)
	}
	if n := b.itemCount(t); n != 0 {
		t.Fatalf("export restored %d items", n)
	}
	rec := b.restoreAt(t, action, data, "allister-backup.zip")
	if rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `data-count="created">2<`)
	// person photos are not in the archive: the restored server's sync fetches them
	b.drain(t)
	list := b.page(t, "/anime?view=table")
	mustRows(t, list, "Lost Show", "デスノート")
	mustContain(t, list, "37 / 37", "1 / 2", `data-score="0"`, `data-score="7"`, statusCell("Paused"), statusCell("Completed"))
	mustRows(t, b.page(t, "/anime?genre=Drama&view=table"), "Lost Show")
	mustContain(t, b.page(t, overview), "Light Yagami", "twice")
	actor := b.personPath(t, "a1")
	mustContain(t, b.page(t, actor), "as Light Yagami")
	people := b.people(t)
	if len(people) != 2 {
		t.Fatalf("people after restore: %+v", people)
	}
	for _, person := range people {
		if person.ImagePath == "" || strings.Contains(person.ImagePath, "/") {
			t.Errorf("person photo not fetched again after restore: %+v", person)
		} else if got := b.get("/covers/" + person.ImagePath); got.Code != 200 {
			t.Errorf("person photo %q served: %d", person.ImagePath, got.Code)
		}
	}
	mb := b.page(t, manual)
	mi := b.savedItems(t, "anime", "manual", strings.TrimPrefix(manual, "/anime/manual/"))[0]
	mustContain(t, mb, "Lost Show", `src="/covers/`+mi.CoverPath+`"`)
	if got := b.get("/covers/" + mi.CoverPath); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), cover) {
		t.Fatalf("restored manual cover: %d", got.Code)
	}
	// restoring the same archive again changes nothing
	mustContain(t, b.restoreAt(t, action, data, "allister-backup.zip").Body.String(), `data-count="created">0<`, `data-count="skipped">2<`)
}

// restoreAt uploads data as the backup file to target.
func (e *proofEnv) restoreAt(t *testing.T, target string, data []byte, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, nil, &upload{field: "file", filename: name, ctype: "application/zip", data: data})
	req := httptest.NewRequest("POST", target, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// switchOffSource is a book source that switches its plugin off when it is
// asked to look up value (as if the user pressed Disable at that moment).
type switchOffSource struct {
	*plugintest.Catalog
	value string
	reg   **plugin.Registry
}

func (s switchOffSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if value == s.value {
		if err := (*s.reg).SetEnabled(ctx, "book", false); err != nil {
			return plugin.ItemData{}, err
		}
	}
	return s.Catalog.Lookup(ctx, kind, idType, value)
}

func TestDisablingAPluginPausesItsRunningImport(t *testing.T) {
	var reg *plugin.Registry
	e := newProofEnv(t, func(book, _ *plugintest.Plugin, bc, _ *plugintest.Catalog) {
		book.Sources = map[string]plugin.Source{"books": switchOffSource{Catalog: bc, value: "9780000000002", reg: &reg}}
	})
	reg = e.reg
	rec := e.uploadImport(t, "book/books-csv", "books.txt",
		"9780000000002;Dune;completed;pages=600\n9780000000001;A Wizard of Earthsea;active;pages=10\n9999;Lost Book;planned\n")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	// the plugin goes off during the first row: that row finishes, then the
	// import pauses with its job pending
	e.drain(t)
	if n := e.book.Calls(plugintest.OpLookup); n != 1 {
		t.Fatalf("lookups after the plugin went off: %d", n)
	}
	var importJobs []jobRow
	for _, j := range e.jobRows(t) {
		if j.Kind == importer.KindRun {
			importJobs = append(importJobs, j)
		}
	}
	if len(importJobs) != 1 || importJobs[0].State != "pending" {
		t.Fatalf("import job: %+v", importJobs)
	}
	b := e.page(t, "/settings/import")
	mustContain(t, b, `data-state="running"`, `data-count="imported">1<`,
		"This import is paused because the Books plugin is disabled. Enable it on the Plugins page of Settings to continue, or cancel the import.")
	if n := e.itemCount(t); n != 1 {
		t.Fatalf("%d items saved while paused", n)
	}
	// enabled again: the remaining rows import once, nothing twice
	if rec := e.do("POST", "/settings/plugins/book", url.Values{"enabled": {"1"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable: %d", rec.Code)
	}
	e.drain(t)
	if n := e.book.Calls(plugintest.OpLookup); n != 3 {
		t.Fatalf("lookups after resume: %d", n)
	}
	b = e.page(t, "/settings/import")
	mustContain(t, b, `data-state="done"`, `data-count="imported">2<`, `data-count="unmatched">1<`, `data-count="failed">0<`)
	mustNotContain(t, b, "is paused because")
	mustRows(t, e.page(t, "/book?view=table&view=table"), "Lost Book", "A Wizard of Earthsea", "Dune")
	if n := e.itemCount(t); n != 3 {
		t.Fatalf("%d items after resume", n)
	}
}
