package web

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
)

// addEntry saves an anime with the given titles and a list entry.
func (e *env) addEntry(t *testing.T, src provider.Source, sid string, eps *int, st store.Status, progress int, titles ...provider.Title) int64 {
	t.Helper()
	if len(titles) == 0 {
		titles = []provider.Title{{Lang: "en", Text: "Title " + sid}}
	}
	a := provider.Anime{SearchResult: provider.SearchResult{Source: src, ID: sid, Episodes: eps, Type: "TV", Titles: titles}}
	id, err := e.st.SaveAnime(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: id, Status: st, Progress: progress}); err != nil {
		t.Fatal(err)
	}
	return id
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }

func TestListFiltersByStatus(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, provider.Title{Lang: "en", Text: "Alpha Show"})
	e.addEntry(t, provider.AniList, "2", nil, store.Completed, 0, provider.Title{Lang: "en", Text: "Beta Show"})
	all := e.get("/anime").Body.String()
	mustContain(t, all, "Alpha Show", "Beta Show")
	w := e.get("/anime?status=watching")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	mustContain(t, w.Body.String(), "Alpha Show")
	mustNotContain(t, w.Body.String(), "Beta Show")
	c := e.get("/anime?status=completed").Body.String()
	mustContain(t, c, "Beta Show")
	mustNotContain(t, c, "Alpha Show")
}

func TestListTabsShowCountsAndCurrent(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	e.addEntry(t, provider.AniList, "2", nil, store.Watching, 0)
	e.addEntry(t, provider.AniList, "3", nil, store.Dropped, 0)
	b := e.get("/anime?status=watching").Body.String()
	mustContain(t, b, `href="/anime"`, `href="/anime?status=watching"`, `href="/anime?status=completed"`,
		`href="/anime?status=plan_to_watch"`, `href="/anime?status=paused"`, `href="/anime?status=dropped"`,
		"All (3)", "Watching (2)", "Dropped (1)", "Completed (0)", "Plan to watch (0)", "Paused (0)")
	// the header nav marks the plugin itself; the tabs live in the main content
	b = b[strings.Index(b, "</header>"):]
	if n := strings.Count(b, `aria-current="page"`); n != 1 {
		t.Fatalf("aria-current count %d", n)
	}
	i := strings.Index(b, `aria-current="page"`)
	if !strings.Contains(b[i:i+200], "Watching (2)") {
		t.Fatalf("wrong tab current: %s", b[i:i+200])
	}
	all := e.get("/anime").Body.String()
	all = all[strings.Index(all, "</header>"):]
	j := strings.Index(all, `aria-current="page"`)
	if !strings.Contains(all[j:j+100], "All (3)") {
		t.Fatal("All not current")
	}
}

func TestListUnknownStatusShowsAll(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0, provider.Title{Lang: "en", Text: "Alpha Show"})
	e.addEntry(t, provider.AniList, "2", nil, store.Completed, 0, provider.Title{Lang: "en", Text: "Beta Show"})
	rec := e.get("/anime?status=bogus")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "Alpha Show", "Beta Show")
}

func TestListShowsPreferredTitle(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, provider.AniList, "77", nil, store.Watching, 0,
		provider.Title{Lang: "ja", Text: "デスノート"}, provider.Title{Lang: "en", Text: "Death Note EN"})
	if err := e.set.Set(context.Background(), settings.KeyTitleLang, "en"); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime").Body.String()
	mustContain(t, b, "Death Note EN", `href="/anime/anilist/77"`)
	mustNotContain(t, b, "デスノート")
	if err := e.set.Set(context.Background(), settings.KeyTitleLang, "ja"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/anime").Body.String(), "デスノート")
}

func TestListRowContent(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 3)
	b := e.get("/anime").Body.String()
	mustContain(t, b, "3 / 12", `hx-post="/list/`+idStr(id)+`/increment"`, `hx-target="closest tr"`, `hx-swap="outerHTML"`,
		`href="/list/`+idStr(id)+`/edit`, `/list/`+idStr(id)+`/remove`, `hx-confirm`, "Watching", "partially synced", "TV", "–")
	mustNotContain(t, b, "unmatched")
	e2 := newEnv(t)
	e2.addEntry(t, provider.AniList, "1", nil, store.Watching, 3)
	mustContain(t, e2.get("/anime").Body.String(), "3 / ?")
}

func TestListScoreZeroAndRated(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Completed, 0)
	if err := e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: id, Status: store.Completed, Rating: ip(0)}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/anime").Body.String(), `data-score="0"`)
	mustContain(t, newEnvWithRated(t, 9).get("/anime").Body.String(), `data-score="9"`)
	mustContain(t, func() string {
		e := newEnv(t)
		e.addEntry(t, provider.AniList, "1", nil, store.Completed, 0)
		return e.get("/anime").Body.String()
	}(), `data-score="none"`)
}

func newEnvWithRated(t *testing.T, r int) *env {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Completed, 0)
	if err := e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: id, Status: store.Completed, Rating: &r}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListSyncBadgeOnlyWhilePending(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	mustContain(t, e.get("/anime").Body.String(), "partially synced")
	if err := e.st.SetSyncState(context.Background(), id, "done"); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get("/anime").Body.String(), "partially synced")
}

func TestListShowsUnmatchedBadge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(src provider.Source, sid, mal string) {
		a := provider.Anime{SearchResult: provider.SearchResult{Source: src, ID: sid, Titles: []provider.Title{{Lang: "en", Text: "T" + sid}}}, MALID: mal}
		id, err := e.st.SaveAnime(ctx, a, store.WithSyncState("done"))
		if err != nil {
			t.Fatal(err)
		}
		e.st.UpsertListEntry(ctx, store.ListEntry{AnimeID: id, Status: store.Watching})
	}
	mk(provider.Manual, "m1", "555")
	b := e.get("/anime").Body.String()
	mustContain(t, b, "unmatched")

	e2 := newEnv(t)
	ctx = context.Background()
	e = e2
	mk(provider.Manual, "m2", "")
	mk(provider.AniList, "5", "123")
	mustNotContain(t, e2.get("/anime").Body.String(), "unmatched")
}

func TestEmptyListShowsHint(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/anime")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), `href="/anime/search"`, "Your list is empty")
	e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	b := e.get("/anime?status=dropped").Body.String()
	mustContain(t, b, "Nothing here yet")
	mustNotContain(t, b, "Your list is empty")
}

func TestListPagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 101; i++ {
		st := store.Watching
		if i >= 90 {
			st = store.Dropped
		}
		e.addEntry(t, provider.AniList, fmt.Sprint(i+1), nil, st, 0)
	}
	e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: 1, Status: store.Watching})
	// order: newest-updated first; all share timestamps within a second, tie broken by id desc
	p1 := e.get("/anime").Body.String()
	if n := strings.Count(p1, `data-row`); n != 100 {
		t.Fatalf("page 1 rows %d", n)
	}
	mustContain(t, p1, "Showing 1–100 of 101", `href="/anime?page=2"`)
	mustNotContain(t, p1, "Previous")
	p2 := e.get("/anime?page=2").Body.String()
	if n := strings.Count(p2, `data-row`); n != 1 {
		t.Fatalf("page 2 rows %d", n)
	}
	mustContain(t, p2, "Showing 101–101 of 101", `href="/anime"`, "Previous")
	mustNotContain(t, p2, "Next")
	for _, q := range []string{"?page=0", "?page=-3", "?page=abc", "?page="} {
		b := e.get("/anime" + q).Body.String()
		mustContain(t, b, "Showing 1–100 of 101")
	}
	mustContain(t, e.get("/anime?page=99").Body.String(), "Showing 101–101 of 101")
	// filter kept
	d := e.get("/anime?status=watching").Body.String()
	mustContain(t, d, "Showing 1–90 of 90")
	mustNotContain(t, d, "Next")
	// 101 rows of one status paginate with the filter in links
	e2 := newEnv(t)
	for i := 0; i < 101; i++ {
		e2.addEntry(t, provider.AniList, fmt.Sprint(i+1), nil, store.Watching, 0)
	}
	f := e2.get("/anime?status=watching").Body.String()
	mustContain(t, f, `href="/anime?page=2&amp;status=watching"`)
	f2 := e2.get("/anime?status=watching&page=2").Body.String()
	mustContain(t, f2, `href="/anime?status=watching"`, "Showing 101–101 of 101")
}

func TestIncrementReturnsUpdatedRow(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 3)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "4 / 12", "<tr")
	mustNotContain(t, b, "<html", "<body")
	en, _ := e.st.GetListEntry(context.Background(), id)
	if en.Progress != 4 {
		t.Fatal(en.Progress)
	}
}

func TestIncrementAtTotalDoesNotExceed(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 12)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "12 / 12")
	mustNotContain(t, rec.Body.String(), "/increment")
	if en, _ := e.st.GetListEntry(context.Background(), id); en.Progress != 12 {
		t.Fatal(en.Progress)
	}
	// the list page hides +1 at the total too
	mustNotContain(t, e.get("/anime").Body.String(), "/increment")
}

func TestIncrementPlainPostRedirects(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatal(rec.Code, rec.Header())
	}
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/anime?status=watching"}}, false)
	if rec.Header().Get("Location") != "/anime?status=watching" {
		t.Fatal(rec.Header())
	}
	if en, _ := e.st.GetListEntry(context.Background(), id); en.Progress != 2 {
		t.Fatal(en.Progress)
	}
}

func TestListRoutesNotFound(t *testing.T) {
	e := newEnv(t)
	saved := e.saved(t) // anime without list entry
	for _, c := range []struct{ method, path string }{
		{"POST", "/list/999/increment"},
		{"POST", "/list/" + idStr(saved) + "/increment"},
		{"POST", "/list/abc/increment"},
		{"GET", "/list/abc/edit"},
		{"GET", "/list/999/edit"},
		{"GET", "/list/" + idStr(saved) + "/edit"},
		{"POST", "/list/abc/edit"},
		{"POST", "/list/999/edit"},
		{"POST", "/list/abc/remove"},
		{"POST", "/list/999/remove"},
		{"POST", "/list/-1/remove"},
	} {
		var form url.Values
		if c.method == "POST" {
			form = okForm()
		}
		for _, htmx := range []bool{false, true} {
			if rec := e.do(c.method, c.path, form, htmx); rec.Code != 404 {
				t.Errorf("%s %s htmx=%v: %d", c.method, c.path, htmx, rec.Code)
			}
		}
	}
}

func TestEditGetPrefilled(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Paused, 5)
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `action="/list/`+idStr(id)+`/edit"`, `<option value="paused" selected>`, `name="rating" value=""`,
		`value="5"`, `max="12"`, "/ 12", "Save")
	mustNotContain(t, b, "<html")
	// rating 0 shows 0
	e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: id, Status: store.Paused, Rating: ip(0), Comment: "note <b>x</b>"})
	b = e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="rating" value="0"`, "note &lt;b&gt;x&lt;/b&gt;")
	// no JS: full page with dialog
	full := e.get("/list/" + idStr(id) + "/edit").Body.String()
	mustContain(t, full, "<html", `id="dialog-slot"`, `name="rating" value="0"`, "Showing")
}

func TestEditGetDatesAndReturn(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Completed, 0)
	e.st.UpsertListEntry(context.Background(), store.ListEntry{AnimeID: id, Status: store.Completed, StartedOn: day(2024, 1, 2), FinishedOn: day(2024, 2, 3)})
	b := e.do("GET", "/list/"+idStr(id)+"/edit?return="+url.QueryEscape("/anime?status=completed"), nil, true).Body.String()
	mustContain(t, b, `value="2024-01-02"`, `value="2024-02-03"`, `name="return" value="/anime?status=completed"`)
	b = e.do("GET", "/list/"+idStr(id)+"/edit?return=//evil.com", nil, true).Body.String()
	mustContain(t, b, `name="return" value="/anime"`)
	mustNotContain(t, b, "evil.com")
}

func TestEditPostSuccessLeavesAnimeUntouched(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 1)
	before, _ := e.st.GetByID(context.Background(), id)
	form := url.Values{"status": {"completed"}, "rating": {"0"}, "progress": {"12"}, "started_on": {"2024-01-02"}, "finished_on": {"2024-02-03"}, "comment": {"great"}, "return": {"/anime?status=watching"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime?status=watching" {
		t.Fatal(rec.Code, rec.Header())
	}
	en, _ := e.st.GetListEntry(context.Background(), id)
	if en.Status != store.Completed || en.Rating == nil || *en.Rating != 0 || en.Progress != 12 || en.Comment != "great" ||
		en.StartedOn.Format("2006-01-02") != "2024-01-02" || en.FinishedOn.Format("2006-01-02") != "2024-02-03" {
		t.Fatalf("%+v", en)
	}
	after, _ := e.st.GetByID(context.Background(), id)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("anime changed:\n%+v\n%+v", before, after)
	}
	// plain post: 303
	rec = e.do("POST", "/list/"+idStr(id)+"/edit", form, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime?status=watching" {
		t.Fatal(rec.Code, rec.Header())
	}
	form.Del("return")
	rec = e.do("POST", "/list/"+idStr(id)+"/edit", form, false)
	if rec.Header().Get("Location") != "/anime" {
		t.Fatal(rec.Header())
	}
}

func TestEditValidationReturns422(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 1)
	cases := []struct {
		name string
		mod  func(url.Values)
		msg  string
	}{
		{"rating high", func(v url.Values) { v.Set("rating", "11") }, "Rating must be"},
		{"rating text", func(v url.Values) { v.Set("rating", "x") }, "Rating must be"},
		{"progress negative", func(v url.Values) { v.Set("progress", "-1") }, "Progress must be"},
		{"progress above total", func(v url.Values) { v.Set("progress", "13") }, "above the episode total"},
		{"bad date", func(v url.Values) { v.Set("started_on", "01/02/2024") }, "Start date must be"},
		{"finish before start", func(v url.Values) { v.Set("started_on", "2024-05-02"); v.Set("finished_on", "2024-05-01") }, "before start"},
		{"bad status", func(v url.Values) { v.Set("status", "nope") }, "choose a status"},
	}
	for _, c := range cases {
		for _, htmx := range []bool{true, false} {
			form := url.Values{"status": {"watching"}, "rating": {"7"}, "progress": {"2"}, "comment": {"keepme"}, "return": {"/anime?status=watching"}}
			c.mod(form)
			rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, htmx)
			if rec.Code != 422 {
				t.Errorf("%s htmx=%v: %d", c.name, htmx, rec.Code)
				continue
			}
			b := rec.Body.String()
			mustContain(t, b, c.msg, "keepme", `name="return" value="/anime?status=watching"`)
			if htmx {
				mustNotContain(t, b, "<html")
			} else {
				mustContain(t, b, "<html", "Showing")
			}
		}
	}
	en, _ := e.st.GetListEntry(context.Background(), id)
	if en.Progress != 1 || en.Comment != "" {
		t.Fatalf("entry changed: %+v", en)
	}
}

func TestEditValidationKeepsInput(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 1)
	form := url.Values{"status": {"paused"}, "rating": {"11"}, "progress": {"4"}, "started_on": {"2024-03-04"}}
	b := e.do("POST", "/list/"+idStr(id)+"/edit", form, true).Body.String()
	mustContain(t, b, `name="rating" value="11"`, `value="4"`, `value="2024-03-04"`, `<option value="paused" selected>`, "/ 12")
}

func TestSafeReturn(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "/anime",
		"/anime":                   "/anime",
		"/anime?status=watching":   "/anime?status=watching",
		"/anime?status=a&page=2":   "/anime?status=a&page=2",
		"/anime/search":            "/anime/search",
		"//evil.com":               "/anime",
		"https://evil.com":         "/anime",
		"/anime@evil":              "/anime",
		"/anime@evil.com/":         "/anime",
		"/\\evil":                  "/anime",
		"/anime\\evil":             "/anime",
		"/animex":                  "/anime",
		"/anime//evil.com":         "/anime",
		"/anime?x=//evil":          "/anime",
		"/anime\r\nSet-Cookie: a":  "/anime",
		"/anime?x=\x00":            "/anime",
		"/anime\t":                 "/anime",
		"/anime ":                  "/anime",
		"/other":                   "/anime",
		"anime":                    "/anime",
		"javascript:alert(1)":      "/anime",
		"/anime/../../evil":        "/anime/../../evil", // still same-origin path
		"/ANIME":                   "/anime",
		"http://localhost/anime":   "/anime",
		"/anime#frag":              "/anime",
		"/anime/%zz":               "/anime",
		"/anime/%2f":               "/anime",
		"/anime?x=%00":             "/anime",
		"/anime?status=watching#x": "/anime",
	} {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEditReturnHostileRedirectsToList(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	for _, h := range []string{"//evil.com", "https://evil.com", "/anime@evil", `/\evil`, "/animex"} {
		form := okForm()
		form.Set("return", h)
		rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, false)
		if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
			t.Errorf("%q: %d %v", h, rec.Code, rec.Header())
		}
		rec = e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
		if rec.Header().Get("HX-Redirect") != "/anime" {
			t.Errorf("%q htmx: %v", h, rec.Header())
		}
		rec = e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{"return": {h}}, false)
		if rec.Header().Get("Location") != "/anime" {
			t.Errorf("remove %q: %v", h, rec.Header())
		}
		e.addEntry(t, provider.AniList, "again"+h, nil, store.Watching, 0) // keep an entry around
		id = e.addEntry(t, provider.AniList, "n"+h, nil, store.Watching, 0)
	}
}

func TestRemoveDeletesEntry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	other := e.addEntry(t, provider.AniList, "2", nil, store.Watching, 0)
	rec := e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatal(rec.Code, rec.Header())
	}
	if sv, _ := e.st.GetByID(ctx, id); sv != nil {
		t.Fatal("anime remains")
	}
	if en, _ := e.st.GetListEntry(ctx, other); en == nil {
		t.Fatal("other removed")
	}
	// htmx path
	rec = e.do("POST", "/list/"+idStr(other)+"/remove", url.Values{"return": {"/anime?status=watching"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime?status=watching" {
		t.Fatal(rec.Code, rec.Header())
	}
	if en, _ := e.st.GetListEntry(ctx, other); en != nil {
		t.Fatal("not removed")
	}
	if rec := e.do("POST", "/list/"+idStr(other)+"/remove", url.Values{}, false); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestRemoveCascadesCastTitles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	if err := e.st.SaveCast(ctx, id, e.fp.cast); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{}, false)
	if c, _ := e.st.Cast(ctx, id); len(c) != 0 {
		t.Fatal("cast remains")
	}
}

func TestListRowsRenderedWithRemoveFormForNoJS(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	b := e.get("/anime?status=watching&page=1").Body.String()
	mustContain(t, b, `method="post" action="/list/`+idStr(id)+`/remove"`, `name="return" value="/anime?status=watching"`,
		`hx-confirm=`, `method="post" action="/list/`+idStr(id)+`/increment"`)
}

func TestFormBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", nil, store.Watching, 0)
	big := okForm()
	big.Set("comment", strings.Repeat("a", 70<<10))
	for _, path := range []string{"/list/" + idStr(id) + "/edit", "/anime/anilist/1535/add", "/list/" + idStr(id) + "/remove"} {
		if rec := e.do("POST", path, big, false); rec.Code != 413 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestMalformedReturnDoesNotPanic(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, provider.AniList, "1", ip(12), store.Watching, 1)
	rec := e.get("/list/" + idStr(id) + "/edit?return=" + url.QueryEscape("/anime/%zz"))
	if rec.Code != 200 {
		t.Fatalf("GET: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Showing 1–1 of 1", `name="return" value="/anime"`)
	form := url.Values{"status": {"watching"}, "rating": {"11"}, "return": {"/anime/%zz"}}
	rec = e.do("POST", "/list/"+idStr(id)+"/edit", form, false)
	if rec.Code != 422 {
		t.Fatalf("POST: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Rating must be", "Showing 1–1 of 1")
}

func TestListPageParamClamping(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 101; i++ {
		e.addEntry(t, provider.AniList, fmt.Sprint(i+1), nil, store.Watching, 0)
	}
	for q, want := range map[string]string{
		"9999999999999999999": "Showing 101–101 of 101",
		"abc":                 "Showing 1–100 of 101",
		"-3":                  "Showing 1–100 of 101",
		"0":                   "Showing 1–100 of 101",
		"":                    "Showing 1–100 of 101",
	} {
		mustContain(t, e.get("/anime?page="+q).Body.String(), want)
	}
}
