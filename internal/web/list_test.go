package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime/anilist"
)

// addEntry saves an anime with the given titles and a list entry.
func (e *env) addEntry(t *testing.T, src anilist.Source, sid string, eps *int, st library.Status, progress int, titles ...anilist.Title) int64 {
	t.Helper()
	if len(titles) == 0 {
		titles = []anilist.Title{{Lang: "en", Text: "Title " + sid}}
	}
	a := anilist.Anime{SearchResult: anilist.SearchResult{Source: src, ID: sid, Episodes: eps, Type: "TV", Titles: titles}}
	id, err := saveAnime(context.Background(), e.lib, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: st, Progress: progress}); err != nil {
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
	e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0, anilist.Title{Lang: "en", Text: "Alpha Show"})
	e.addEntry(t, anilist.AniList, "2", nil, library.Completed, 0, anilist.Title{Lang: "en", Text: "Beta Show"})
	all := e.get("/anime").Body.String()
	mustContain(t, all, "Alpha Show", "Beta Show")
	w := e.get("/anime?status=active&view=table")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	mustContain(t, w.Body.String(), "Alpha Show")
	mustNotContain(t, w.Body.String(), "Beta Show")
	c := e.get("/anime?status=completed&view=table").Body.String()
	mustContain(t, c, "Beta Show")
	mustNotContain(t, c, "Alpha Show")
}

func TestListTabsShowCountsAndCurrent(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	e.addEntry(t, anilist.AniList, "2", nil, library.Active, 0)
	e.addEntry(t, anilist.AniList, "3", nil, library.Dropped, 0)
	b := e.get("/anime?status=active").Body.String()
	mustContain(t, b, `href="/anime?status=all&amp;view=cards"`, `href="/anime?status=active&amp;view=cards"`, `href="/anime?status=completed&amp;view=cards"`,
		`href="/anime?status=planned&amp;view=cards"`, `href="/anime?status=paused&amp;view=cards"`, `href="/anime?status=dropped&amp;view=cards"`,
		"All (3)", "Watching (2)", "Dropped (1)", "Completed (0)", "Plan to watch (0)", "Paused (0)")
	// the sidebar marks the plugin itself (aria-current="page"); the tabs
	// nav marks its current tab with aria-current="true" (the view toggle
	// marks itself separately, outside this nav)
	b = b[strings.Index(b, "</header>"):]
	tabs := b[strings.Index(b, `id="list-tabs"`):]
	tabs = tabs[:strings.Index(tabs, "</nav>")]
	if n := strings.Count(tabs, `aria-current="true"`); n != 1 {
		t.Fatalf("aria-current count %d", n)
	}
	mustNotContain(t, b, `aria-current="page"`)
	i := strings.Index(tabs, `aria-current="true"`)
	if !strings.Contains(tabs[i:i+200], "Watching (2)") {
		t.Fatalf("wrong tab current: %s", tabs[i:i+200])
	}
	all := e.get("/anime").Body.String()
	all = all[strings.Index(all, "</header>"):]
	all = all[strings.Index(all, `id="list-tabs"`):]
	all = all[:strings.Index(all, "</nav>")]
	j := strings.Index(all, `aria-current="true"`)
	if !strings.Contains(all[j:j+100], "All (3)") {
		t.Fatal("All not current")
	}
	// every status tab carries its colour, matching the card corners
	mustContain(t, all, `text-indigo-300">Watching (2)`, `text-emerald-400">Completed (0)`,
		`text-amber-300">Paused (0)`, `text-red-300">Dropped (1)`, `text-slate-400">Plan to watch (0)`)
}

func TestListUnknownStatusShowsAll(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0, anilist.Title{Lang: "en", Text: "Alpha Show"})
	e.addEntry(t, anilist.AniList, "2", nil, library.Completed, 0, anilist.Title{Lang: "en", Text: "Beta Show"})
	rec := e.get("/anime?status=bogus&view=table")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "Alpha Show", "Beta Show")
}

func TestListShowsPreferredTitle(t *testing.T) {
	e := newEnv(t)
	e.addEntry(t, anilist.AniList, "77", nil, library.Active, 0,
		anilist.Title{Lang: "ja", Text: "デスノート"}, anilist.Title{Lang: "en", Text: "Death Note EN"})
	if err := e.set.SetTitleLang(context.Background(), "anime", "en"); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime?view=table").Body.String()
	mustContain(t, b, "Death Note EN", `href="/anime/anilist/77"`)
	mustNotContain(t, b, "デスノート")
	if err := e.set.SetTitleLang(context.Background(), "anime", "ja"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/anime?view=table").Body.String(), "デスノート")
}

func TestListRowContent(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	b := e.get("/anime?view=table").Body.String()
	mustContain(t, b, "3 / 12", `hx-post="/list/`+idStr(id)+`/increment"`, `hx-target="closest tr"`, `hx-swap="outerHTML"`,
		`href="/list/`+idStr(id)+`/edit`, `/list/`+idStr(id)+`/remove`, `hx-confirm`, "Watching", "partially synced", "TV", "–")
	mustNotContain(t, b, ">unmatched</span>")
	e2 := newEnv(t)
	e2.addEntry(t, anilist.AniList, "1", nil, library.Active, 3)
	mustContain(t, e2.get("/anime?view=table").Body.String(), "3 / ?")
}

func TestListScoreZeroAndRated(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Completed, 0)
	if err := upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Completed, Rating: ip(0)}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/anime?view=table").Body.String(), `data-score="0"`)
	mustContain(t, newEnvWithRated(t, 9).get("/anime?view=table").Body.String(), `data-score="9"`)
	mustContain(t, func() string {
		e := newEnv(t)
		e.addEntry(t, anilist.AniList, "1", nil, library.Completed, 0)
		return e.get("/anime?view=table").Body.String()
	}(), `data-score="none"`)
}

func newEnvWithRated(t *testing.T, r int) *env {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Completed, 0)
	if err := upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Completed, Rating: &r}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListSyncBadgeOnlyWhilePending(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	mustContain(t, e.get("/anime?view=table").Body.String(), "partially synced")
	if err := e.lib.SetSyncState(context.Background(), id, "done"); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get("/anime?view=table").Body.String(), "partially synced")
}

func TestListShowsUnmatchedBadge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(src anilist.Source, sid, mal string) {
		a := anilist.Anime{SearchResult: anilist.SearchResult{Source: src, ID: sid, Titles: []anilist.Title{{Lang: "en", Text: "T" + sid}}}, MALID: mal}
		id, err := saveAnime(ctx, e.lib, a, library.WithSyncState("done"))
		if err != nil {
			t.Fatal(err)
		}
		upsertEntry(ctx, e.lib, listEntry{ItemID: id, Status: library.Active})
	}
	mk(anilist.Manual, "m1", "555")
	b := e.get("/anime?view=table").Body.String()
	mustContain(t, b, ">unmatched</span>")

	e2 := newEnv(t)
	ctx = context.Background()
	e = e2
	mk(anilist.Manual, "m2", "")
	mk(anilist.AniList, "5", "123")
	mustNotContain(t, e2.get("/anime?view=table").Body.String(), ">unmatched</span>")
}

func TestEmptyListShowsHint(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/anime?view=table")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), `href="/anime/search"`, "Your list is empty")
	e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	b := e.get("/anime?status=dropped&view=table").Body.String()
	mustContain(t, b, "Nothing here yet")
	mustNotContain(t, b, "Your list is empty")
}

func TestListPagination(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 101; i++ {
		st := library.Active
		if i >= 90 {
			st = library.Dropped
		}
		e.addEntry(t, anilist.AniList, fmt.Sprint(i+1), nil, st, 0)
	}
	upsertEntry(context.Background(), e.lib, listEntry{ItemID: 1, Status: library.Active})
	// order: newest-updated first; all share timestamps within a second, tie broken by id desc
	p1 := e.get("/anime?view=table").Body.String()
	if n := strings.Count(p1, `data-row`); n != 100 {
		t.Fatalf("page 1 rows %d", n)
	}
	mustContain(t, p1, "Showing 1–100 of 101", `href="/anime?page=2&amp;view=table"`)
	mustNotContain(t, p1, "Previous")
	p2 := e.get("/anime?page=2&view=table").Body.String()
	if n := strings.Count(p2, `data-row`); n != 1 {
		t.Fatalf("page 2 rows %d", n)
	}
	mustContain(t, p2, "Showing 101–101 of 101", `href="/anime?status=all&amp;view=table"`, "Previous")
	mustNotContain(t, p2, "Next")
	for _, q := range []string{"?page=0", "?page=-3", "?page=abc", "?page="} {
		b := e.get("/anime?view=table&" + q).Body.String()
		mustContain(t, b, "Showing 1–100 of 101")
	}
	mustContain(t, e.get("/anime?page=99&view=table").Body.String(), "Showing 101–101 of 101")
	// filter kept
	d := e.get("/anime?status=active&view=table").Body.String()
	mustContain(t, d, "Showing 1–90 of 90")
	mustNotContain(t, d, "Next")
	// 101 rows of one status paginate with the filter in links
	e2 := newEnv(t)
	for i := 0; i < 101; i++ {
		e2.addEntry(t, anilist.AniList, fmt.Sprint(i+1), nil, library.Active, 0)
	}
	f := e2.get("/anime?status=active&view=table").Body.String()
	mustContain(t, f, `href="/anime?page=2&amp;status=active&amp;view=table"`)
	f2 := e2.get("/anime?status=active&page=2&view=table").Body.String()
	mustContain(t, f2, `href="/anime?status=active&amp;view=table"`, "Showing 101–101 of 101")
}

func TestIncrementReturnsUpdatedRow(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 3)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "4 / 12", "<tr")
	mustNotContain(t, b, "<html", "<body")
	en, _ := getEntry(context.Background(), e.lib, id)
	if en.Progress != 4 {
		t.Fatal(en.Progress)
	}
}

func TestIncrementAtTotalDoesNotExceed(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 12)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "12 / 12")
	mustNotContain(t, rec.Body.String(), "/increment")
	if en, _ := getEntry(context.Background(), e.lib, id); en.Progress != 12 {
		t.Fatal(en.Progress)
	}
	// the list page hides +1 at the total too
	mustNotContain(t, e.get("/anime?view=table").Body.String(), "/increment")
}

func TestIncrementPlainPostRedirects(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatal(rec.Code, rec.Header())
	}
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/anime?status=watching"}}, false)
	if rec.Header().Get("Location") != "/anime?status=watching" {
		t.Fatal(rec.Header())
	}
	if en, _ := getEntry(context.Background(), e.lib, id); en.Progress != 2 {
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
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Paused, 5)
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `action="/list/`+idStr(id)+`/edit"`, `<option value="paused" selected>`, `name="rating" value=""`,
		`value="5"`, `max="12"`, "/ 12", "Save")
	mustNotContain(t, b, "<html")
	// rating 0 shows 0
	upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Paused, Rating: ip(0), Comment: "note <b>x</b>"})
	b = e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="rating" value="0"`, "note &lt;b&gt;x&lt;/b&gt;")
	// no JS: full page with dialog
	full := e.get("/list/" + idStr(id) + "/edit").Body.String()
	mustContain(t, full, "<html", `id="dialog-slot"`, `name="rating" value="0"`, "Showing")
}

func TestEditGetDatesAndReturn(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Completed, 0)
	upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Completed, StartedOn: day(2024, 1, 2), FinishedOn: day(2024, 2, 3)})
	b := e.do("GET", "/list/"+idStr(id)+"/edit?return="+url.QueryEscape("/anime?status=completed"), nil, true).Body.String()
	mustContain(t, b, `value="2024-01-02"`, `value="2024-02-03"`, `name="return" value="/anime?status=completed"`)
	b = e.do("GET", "/list/"+idStr(id)+"/edit?return=//evil.com", nil, true).Body.String()
	mustContain(t, b, `name="return" value="/anime"`)
	mustNotContain(t, b, "evil.com")
}

func TestEditPostSuccessLeavesAnimeUntouched(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 1)
	before, _ := e.lib.Item(context.Background(), id)
	form := url.Values{"status": {"completed"}, "rating": {"0"}, "progress": {"12"}, "started_on": {"2024-01-02"}, "finished_on": {"2024-02-03"}, "comment": {"great"}, "return": {"/anime?status=watching"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime?status=watching" {
		t.Fatal(rec.Code, rec.Header())
	}
	en, _ := getEntry(context.Background(), e.lib, id)
	if en.Status != library.Completed || en.Rating == nil || *en.Rating != 0 || en.Progress != 12 || en.Comment != "great" ||
		en.StartedOn.Format("2006-01-02") != "2024-01-02" || en.FinishedOn.Format("2006-01-02") != "2024-02-03" {
		t.Fatalf("%+v", en)
	}
	after, _ := e.lib.Item(context.Background(), id)
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
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 1)
	cases := []struct {
		name string
		mod  func(url.Values)
		msg  string
	}{
		{"rating high", func(v url.Values) { v.Set("rating", "11") }, "Rating must be"},
		{"rating text", func(v url.Values) { v.Set("rating", "x") }, "Rating must be"},
		{"progress negative", func(v url.Values) { v.Set("progress", "-1") }, "Episode must be a whole number"},
		{"progress above total", func(v url.Values) { v.Set("progress", "13") }, "above the total of 12"},
		{"bad date", func(v url.Values) { v.Set("started_on", "01/02/2024") }, "Start date must be"},
		{"finish before start", func(v url.Values) { v.Set("started_on", "2024-05-02"); v.Set("finished_on", "2024-05-01") }, "before the start date"},
		{"bad status", func(v url.Values) { v.Set("status", "nope") }, "choose a status"},
	}
	for _, c := range cases {
		for _, htmx := range []bool{true, false} {
			form := url.Values{"status": {"active"}, "rating": {"7"}, "progress": {"2"}, "comment": {"keepme"}, "return": {"/anime?status=watching"}}
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
	en, _ := getEntry(context.Background(), e.lib, id)
	if en.Progress != 1 || en.Comment != "" {
		t.Fatalf("entry changed: %+v", en)
	}
}

func TestEditValidationKeepsInput(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 1)
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
		"/anime/../../evil":        "/anime", // a .. segment never passes
		"/anime/../other":          "/anime",
		"/anime/a/../b":            "/anime",
		"/anime/./x":               "/anime/./x", // a single dot stays: same location
		"/ANIME":                   "/anime",
		"http://localhost/anime":   "/anime",
		"/anime#frag":              "/anime",
		"/anime/%zz":               "/anime",
		"/anime/%2f":               "/anime",
		"/anime?x=%00":             "/anime",
		"/anime?status=watching#x": "/anime",
		// a filtered list URL carries escapes in its query (never in its path)
		"/anime?q=%C3%BC&status=active": "/anime?q=%C3%BC&status=active",
		"/anime?q=%2F%2Fevil.com":       "/anime?q=%2F%2Fevil.com",
		"/anime?q=100%25":               "/anime?q=100%25",
		"/anime?x=%1f":                  "/anime",
		"/anime?x=%7F":                  "/anime",
		"/anime?x=%zz":                  "/anime",
		"/anime?x=%2":                   "/anime",
		"/anime?x=%":                    "/anime",
		"/anime%3Fx=1":                  "/anime",
	} {
		if got := safeReturn(in, "anime"); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEditReturnHostileRedirectsToList(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	for _, h := range []string{"//evil.com", "https://evil.com", "/anime@evil", `/\evil`, "/animex"} {
		form := okAddForm()
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
		e.addEntry(t, anilist.AniList, "again"+h, nil, library.Active, 0) // keep an entry around
		id = e.addEntry(t, anilist.AniList, "n"+h, nil, library.Active, 0)
	}
}

func TestRemoveDeletesEntry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	other := e.addEntry(t, anilist.AniList, "2", nil, library.Active, 0)
	rec := e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatal(rec.Code, rec.Header())
	}
	if sv, _ := e.lib.Item(ctx, id); sv != nil {
		t.Fatal("anime remains")
	}
	if en, _ := getEntry(ctx, e.lib, other); en == nil {
		t.Fatal("other removed")
	}
	// htmx path
	rec = e.do("POST", "/list/"+idStr(other)+"/remove", url.Values{"return": {"/anime?status=watching"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime?status=watching" {
		t.Fatal(rec.Code, rec.Header())
	}
	if en, _ := getEntry(ctx, e.lib, other); en != nil {
		t.Fatal("not removed")
	}
	if rec := e.do("POST", "/list/"+idStr(other)+"/remove", url.Values{}, false); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestRemoveCascadesCastTitles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	if err := saveCast(ctx, e.lib, id, e.fp.cast); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{}, false)
	if c, _ := e.lib.Credits(ctx, id); len(c) != 0 {
		t.Fatal("cast remains")
	}
}

func TestListRowsRenderedWithRemoveFormForNoJS(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
	b := e.get("/anime?status=active&page=1&view=table").Body.String()
	mustContain(t, b, `method="post" action="/list/`+idStr(id)+`/remove"`, `name="return" value="/anime?status=active&amp;view=table"`,
		`hx-confirm=`, `method="post" action="/list/`+idStr(id)+`/increment"`)
}

func TestFormBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	id := e.addEntry(t, anilist.AniList, "1", nil, library.Active, 0)
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
	id := e.addEntry(t, anilist.AniList, "1", ip(12), library.Active, 1)
	rec := e.get("/list/" + idStr(id) + "/edit?return=" + url.QueryEscape("/anime/%zz"))
	if rec.Code != 200 {
		t.Fatalf("GET: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Showing 1–1 of 1", `name="return" value="/anime"`)
	form := url.Values{"status": {"active"}, "rating": {"11"}, "return": {"/anime/%zz"}}
	rec = e.do("POST", "/list/"+idStr(id)+"/edit", form, false)
	if rec.Code != 422 {
		t.Fatalf("POST: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Rating must be", "Showing 1–1 of 1")
}

func TestListPageParamClamping(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 101; i++ {
		e.addEntry(t, anilist.AniList, fmt.Sprint(i+1), nil, library.Active, 0)
	}
	for q, want := range map[string]string{
		"9999999999999999999": "Showing 101–101 of 101",
		"abc":                 "Showing 1–100 of 101",
		"-3":                  "Showing 1–100 of 101",
		"0":                   "Showing 1–100 of 101",
		"":                    "Showing 1–100 of 101",
	} {
		mustContain(t, e.get("/anime?view=table&page="+q).Body.String(), want)
	}
}

// --- generic list page (fixture plugins) ---

// TestParseListViewParam: the view parameter is parsed like any other list
// parameter (cards or table, anything else rejected with the notice), and
// status=all is an explicit "every status" that survives URL building.
func TestParseListViewParam(t *testing.T) {
	m := plugintest.TVPlugin().M
	parse := func(q string) listQuery {
		v, err := url.ParseQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		return parseListQuery(m, v)
	}
	if lq := parse("view=table"); lq.View != "table" {
		t.Errorf("view=table: %+v", lq)
	}
	if lq := parse("view=cards"); lq.View != "cards" {
		t.Errorf("view=cards: %+v", lq)
	}
	if lq := parse("view=nope"); lq.View != "" || !slices.Contains(lq.Rejected, "view") {
		t.Errorf("view=nope: %+v", lq)
	}
	if lq := parse("status=all"); lq.Status != "all" {
		t.Errorf("status=all: %+v", lq)
	}
	if lq := parse("status=never"); lq.Status != "" || !slices.Contains(lq.Rejected, "status") {
		t.Errorf("status=never: %+v", lq)
	}
}

// TestHomeSettingsApplyToTheList: without a view or status parameter the
// list opens with the plugin's home settings (cards by default, a chosen
// status filter); explicit URL parameters always win, and the All tab
// (status=all) stays reachable.
func TestHomeSettingsApplyToTheList(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("2", "Dune Days"), library.Entry{Status: library.Completed})
	// defaults: cards view, every status
	b := e.get("/tv").Body.String()
	mustContain(t, b, `<input type="hidden" name="view" value="cards">`, "Dark", "Dune Days")
	mustContain(t, b, `href="/tv?status=all&amp;view=cards"`)
	// the home settings: table view, only watching (active)
	if rec := e.do("POST", "/settings/plugins/tv/home", url.Values{"view": {"table"}, "statuses": {"active"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("home settings: %d %s", rec.Code, rec.Body.String())
	}
	b = e.get("/tv?view=table").Body.String()
	mustContain(t, b, `<input type="hidden" name="view" value="table">`, `<input type="hidden" name="status" value="active">`, "Dark")
	mustNotContain(t, b, "Dune Days")
	mustContain(t, b, `href="/tv?status=all&amp;view=table"`)
	// explicit URL parameters win over the settings
	b = e.get("/tv?view=cards&status=all").Body.String()
	mustContain(t, b, `<input type="hidden" name="view" value="cards">`, "Dark", "Dune Days")
	mustContain(t, b, `<input type="hidden" name="status" value="all">`)
	// invalid settings are refused
	if rec := e.do("POST", "/settings/plugins/tv/home", url.Values{"view": {"nope"}}, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid view: %d", rec.Code)
	}
	if rec := e.do("POST", "/settings/plugins/tv/home", url.Values{"statuses": {"never"}}, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: %d", rec.Code)
	}
}

// TestCardView: view=cards shows a grid of cards — cover, the title link in
// the overlay, the progress values, a +1 bound to the card, a colored
// status corner — and view=table keeps the table. The plugin's home
// default (cards) opens the grid without a parameter.
func TestCardView(t *testing.T) {
	e := newFixtureEnv(t)
	act := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Dark", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	e.addFixture(t, "tv", "show", tvWithEpisodes("2", "Dune Days", 8), library.Entry{Status: library.Completed})
	for target := range map[string]bool{"/tv?view=cards": true, "/tv": true} {
		b := e.get(target).Body.String()
		mustContain(t, b, `data-cards`)
		if n := strings.Count(b, "<div data-card "); n != 2 {
			t.Errorf("%s: %d cards", target, n)
		}
		mustContain(t, b, `href="/tv/tvdata/1"`, "Dark", "2 / 10",
			`border-t-indigo-500`,  // active
			`border-t-emerald-500`, // completed
			`action="/list/`+idStr(act)+`/increment"`, `hx-target="closest [data-card]"`,
			`name="view" value="cards"`, `aria-label="Add one episode to Dark"`)
		mustNotContain(t, b, `<table`)
	}
	// the table view is unchanged
	b := e.get("/tv?view=table").Body.String()
	mustContain(t, b, `<table`, `data-row`)
	mustNotContain(t, b, `data-cards`)
	// the toggle marks the current view; in cards the sort row is compact
	// (under the search bar, the direction as an icon), in the table the
	// panel keeps the small-screen selects
	mustContain(t, b, `aria-current="true"`, `href="/tv?view=cards"`,
		`id="filter-sort-control" class="flex flex-col gap-1 lg:hidden"`)
	c := e.get("/tv").Body.String()
	mustContain(t, c, `href="/tv?view=table"`,
		`id="filter-sort-control" class="mt-4 flex flex-wrap items-center gap-2"`,
		`id="filter-dir-toggle"`, `name="dir" value="asc"`, `aria-label="Sort ascending"`,
		`aria-hidden="true">arrow_downward`)
	mustNotContain(t, c, `id="filter-sort-control" class="flex flex-col gap-1 lg:hidden"`)
}

// TestIncrementFromACardAnswersTheCard: a +1 posted with view=cards answers
// the re-rendered card, not the table row.
func TestIncrementFromACardAnswersTheCard(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Dark", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/tv?view=cards"}, "view": {"cards"}}, true)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, `<div data-card`) {
		t.Fatalf("increment answer is not a card: %.120s", body)
	}
	mustContain(t, body, `hx-target="closest [data-card]"`, "3 / 10")
}

func TestSafeReturnPerPlugin(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "/book",
		"/book":                   "/book",
		"/book?status=active":     "/book?status=active",
		"/book/books/1":           "/book/books/1",
		"/book?kind=comic&page=2": "/book?kind=comic&page=2",
		"/anime":                  "/book", // another plugin's prefix
		"/tv":                     "/book",
		"/bookx":                  "/book",
		"/books":                  "/book",
		"/list":                   "/book", // the entry routes are never a return target
		"/list/1/edit":            "/book",
		"/people/1":               "/book",
		"/settings":               "/book",
		"//evil.com":              "/book",
		"/book//evil.com":         "/book",
		"/book\\evil":             "/book",
		"/book#x":                 "/book",
		"/book/%2f":               "/book",
		"https://evil.com/book":   "/book",
		"/book\r\nX: y":           "/book",
	} {
		if got := safeReturn(in, "book"); got != want {
			t.Errorf("safeReturn(%q, book) = %q, want %q", in, got, want)
		}
	}
	// an empty or odd plugin id never widens what is accepted
	for _, id := range []string{"", "/", "a/b"} {
		if got := safeReturn("//evil.com", id); strings.Contains(got, "evil") {
			t.Errorf("plugin %q: %q", id, got)
		}
	}
}

// rowOf returns the <tr data-row ...>...</tr> of the row whose html contains marker.
func rowOf(t *testing.T, body, marker string) string {
	t.Helper()
	for _, part := range strings.Split(body, "<tr data-row")[1:] {
		end := strings.Index(part, "</tr>")
		if end < 0 {
			continue
		}
		if row := "<tr data-row" + part[:end+5]; strings.Contains(row, marker) {
			return row
		}
	}
	t.Fatalf("no row containing %q", marker)
	return ""
}

func TestListShowsPluginStatusLabels(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "book", "book", bookItem("1", "Reading One"), library.Entry{Status: library.Active})
	e.addFixture(t, "book", "book", bookItem("2", "Read One"), library.Entry{Status: library.Completed})
	e.addFixture(t, "book", "book", bookItem("3", "Wanted One"), library.Entry{Status: library.Planned})
	rec := e.get("/book?view=table")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	b := rec.Body.String()
	main := b[strings.Index(b, "</header>"):]
	mustContain(t, main, "All (3)", "Reading (1)", "Read (1)", "Want to read (1)", "Paused (0)", "Dropped (0)",
		`href="/book?status=active&amp;view=table"`, `href="/book?status=completed&amp;view=table"`, `href="/book?status=planned&amp;view=table"`, `href="/book?status=all&amp;view=table"`)
	mustContain(t, rowOf(t, b, "Reading One"), ">Reading<")
	mustContain(t, rowOf(t, b, "Read One"), ">Read<")
	mustNotContain(t, main, "Watching", "Plan to watch")
	// the status filter uses core classes; labels are not values
	r := e.get("/book?status=active&view=table").Body.String()
	mustContain(t, r, "Reading One")
	mustNotContain(t, r, "Read One", "Wanted One")
	for _, q := range []string{"?status=reading", "?status=Reading", "?status=watching", "?status=%00"} {
		mustContain(t, e.get("/book?view=table&"+q).Body.String(), "Reading One", "Read One", "Wanted One")
	}
}

func TestListScopedToPlugin(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "book", "book", bookItem("1", "Book Title"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("1", "Show Title"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("2", "Other Show"), library.Entry{Status: library.Dropped})
	e.addEntry(t, anilist.AniList, "9", nil, library.Active, 0, anilist.Title{Lang: "en", Text: "Anime Title"})
	b := e.get("/book?view=table").Body.String()
	mustContain(t, b, "Book Title", "All (1)", "Reading (1)", "Dropped (0)", "Showing 1–1 of 1", `href="/book/books/1"`)
	mustNotContain(t, b, "Show Title", "Other Show", "Anime Title")
	tv := e.get("/tv?view=table").Body.String()
	mustContain(t, tv, "Show Title", "Other Show", "All (2)", "Watching (1)", "Dropped (1)", `href="/tv/tvdata/1"`)
	mustNotContain(t, tv, "Book Title", "Anime Title")
	a := e.get("/anime?view=table").Body.String()
	mustContain(t, a, "Anime Title", "All (1)")
	mustNotContain(t, a, "Book Title", "Show Title")
}

func TestListRowRendersEachProgressType(t *testing.T) {
	e := newFixtureEnv(t)
	paged := bookItem("1", "Paged Book")
	paged.Fields["pages"] = plugin.FieldValue{Num: num(300)}
	e.addFixture(t, "book", "book", paged, library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 12}})
	e.addFixture(t, "book", "book", bookItem("2", "Unknown Pages"), library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 5}})
	e.addFixture(t, "book", "book", bookItem("3", "Percent Book"), library.Entry{Status: library.Active, Progress: map[string]float64{"percent": 40}},
		library.WithProgressKeys([]string{"percent"}))
	b := e.get("/book?view=table").Body.String()
	mustContain(t, rowOf(t, b, "Paged Book"), "12 / 300", "Page")
	mustContain(t, rowOf(t, b, "Unknown Pages"), "5 / ?")
	pr := rowOf(t, b, "Percent Book")
	mustContain(t, pr, "40%", "Percent")
	mustNotContain(t, pr, "/increment") // the primary key (pages) is not active

	counted := tvShow("1", "Counted Show")
	counted.Fields["episodes"] = plugin.FieldValue{Num: num(10)}
	e.addFixture(t, "tv", "show", counted, library.Entry{Status: library.Active, Progress: map[string]float64{"season": 2, "episode": 5}},
		library.WithProgressKeys([]string{"season", "episode"}))
	e.addFixture(t, "tv", "show", tvShow("2", "Positioned Show"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 3, "position": 3725}},
		library.WithProgressKeys([]string{"episode", "position"}))
	tv := e.get("/tv?view=table").Body.String()
	cr := rowOf(t, tv, "Counted Show")
	mustContain(t, cr, "Season", ">2<", "Episode", "5 / 10")
	mustNotContain(t, cr, "2 / ", "Position")
	pos := rowOf(t, tv, "Positioned Show")
	mustContain(t, pos, "3 / ?", "Position", "1:02:05", `/increment"`)
	mustNotContain(t, pos, "Season")
}

func TestListTypeColumnAndScore(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Rated Zero"), library.Entry{Status: library.Completed, Rating: ip(0)})
	e.addFixture(t, "tv", "show", tvShow("2", "Unrated"), library.Entry{Status: library.Completed})
	tv := e.get("/tv?view=table").Body.String()
	// the first Show enum field (type) is the type column
	mustContain(t, tv, ">Type<", "series")
	mustContain(t, rowOf(t, tv, "Rated Zero"), `data-score="0"`, ">0<")
	mustContain(t, rowOf(t, tv, "Unrated"), `data-score="none"`, "–")
	// no Show enum: the first Show text field (author)
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	b := e.get("/book?view=table").Body.String()
	mustContain(t, b, ">Author<", "Ursula K. Le Guin")
}

func TestListKindSwitchOnlyForMultiKindPlugins(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.addFixture(t, "book", "book", bookItem("1", "The Novel"), library.Entry{Status: library.Active})
	cd := bookItem("2", "The Comic")
	cd.Kind = "comic"
	e.addFixture(t, "book", "comic", cd, library.Entry{Status: library.Active})
	b := e.get("/book?view=table").Body.String()
	// the first kind by default, with a switch to the others
	mustContain(t, b, "The Novel", `href="/book/books/1?kind=book"`, `href="/book?kind=comic&amp;view=table"`, ">Comics<", ">Books<", "All (1)")
	mustNotContain(t, b, "The Comic")
	c := e.get("/book?kind=comic&view=table").Body.String()
	mustContain(t, c, "The Comic", `href="/book/books/2?kind=comic"`, `href="/book?kind=comic&amp;status=active&amp;view=table"`, "All (1)")
	mustNotContain(t, c, "The Novel")
	// an unknown kind is ignored (first kind)
	mustContain(t, e.get("/book?kind=nope&view=table").Body.String(), "The Novel")
	// a single-kind plugin has no switch and ignores ?kind=
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	tv := e.get("/tv?kind=comic&view=table").Body.String()
	mustContain(t, tv, "Dark", `href="/tv/tvdata/1"`, `href="/tv?status=active&amp;view=table"`)
	mustNotContain(t, tv, "kind=")
}

func TestListPaginationViaLibrary(t *testing.T) {
	e := newFixtureEnv(t)
	for i := 0; i < 101; i++ {
		st := library.Active
		if i >= 90 {
			st = library.Dropped
		}
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), fmt.Sprintf("Show %03d", i+1)), library.Entry{Status: st})
	}
	p1 := e.get("/tv?view=table").Body.String()
	if n := strings.Count(p1, "data-row"); n != 100 {
		t.Fatalf("page 1 rows %d", n)
	}
	mustContain(t, p1, "Showing 1–100 of 101", `href="/tv?page=2&amp;view=table"`, "All (101)", "Watching (90)", "Dropped (11)")
	mustNotContain(t, p1, "Previous")
	p2 := e.get("/tv?page=2&view=table").Body.String()
	if n := strings.Count(p2, "data-row"); n != 1 {
		t.Fatalf("page 2 rows %d", n)
	}
	mustContain(t, p2, "Showing 101–101 of 101", `href="/tv"`, "Previous")
	for q, want := range map[string]string{
		"9999999999999999999999": "Showing 101–101 of 101",
		"2147483648":             "Showing 101–101 of 101",
		"0":                      "Showing 1–100 of 101",
		"-1":                     "Showing 1–100 of 101",
		"x":                      "Showing 1–100 of 101",
	} {
		mustContain(t, e.get("/tv?view=table&page="+q).Body.String(), want)
	}
	d := e.get("/tv?status=dropped&page=5&view=table").Body.String()
	mustContain(t, d, "Showing 1–11 of 11", `aria-current="page"`)
	mustNotContain(t, d, "Next", "Previous")
}

func TestListHostileTitleEscaped(t *testing.T) {
	e := newFixtureEnv(t)
	d := tvShow("1", `<script>alert(1)</script>`)
	d.Fields["type"] = plugin.FieldValue{Text: "series"}
	e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active, Comment: "<b>x</b>"})
	b := e.get("/tv?view=table").Body.String()
	mustContain(t, b, "&lt;script&gt;alert(1)&lt;/script&gt;")
	mustNotContain(t, b, "<script>alert(1)")
	id := e.savedItems(t, "tv", "tvdata", "1")[0].ID
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	mustContain(t, rec.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;")
	mustNotContain(t, rec.Body.String(), "<script>alert(1)")
	ed := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, ed, "&lt;b&gt;x&lt;/b&gt;")
	mustNotContain(t, ed, "<b>x</b>", "<script>alert(1)")
}

func TestListGenericWording(t *testing.T) {
	e := newFixtureEnv(t)
	main := func(b string) string { return strings.ToLower(b[strings.Index(b, "</header>"):]) }
	empty := main(e.get("/book?view=table").Body.String())
	mustContain(t, empty, "no books in your list yet", "your list is empty", `href="/book/search"`, "search for books")
	mustNotContain(t, empty, "anime", "episode", "watch")
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	id := e.savedItems(t, "book", "books", "1")[0].ID
	for _, b := range []string{
		main(e.get("/book?view=table").Body.String()),
		e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String(),
		e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true).Body.String(),
	} {
		b = strings.ToLower(b)
		mustNotContain(t, b, "anime", "episode", "watch")
	}
	mustContain(t, strings.ToLower(e.get("/book?view=table").Body.String()), "remove this book", "add one page to earthsea")
}

func TestListGetIsFullPageWithVary(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	for _, htmx := range []bool{false, true} {
		rec := e.do("GET", "/book?view=table", nil, htmx)
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Vary"), "HX-Request") {
			t.Fatalf("htmx=%v: %d %q", htmx, rec.Code, rec.Header().Get("Vary"))
		}
		mustContain(t, rec.Body.String(), "<html", "Earthsea", `id="dialog-slot"`)
	}
}

// ADAPTED: every plugin has the generic manual route now.
func TestEmptyListHintLinksManualOnlyWhenMounted(t *testing.T) {
	e := newFixtureEnv(t)
	b := e.get("/tv?view=table").Body.String()
	mustContain(t, b, "No shows in your list yet.", "Your list is empty", `href="/tv/search"`, `href="/tv/manual/new"`)
	mustContain(t, e.get("/anime?view=table").Body.String(), `href="/anime/manual/new"`, `href="/anime/search"`)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	d := e.get("/tv?status=dropped&view=table").Body.String()
	mustContain(t, d, "Nothing here yet")
	mustNotContain(t, d, "Your list is empty")
}
