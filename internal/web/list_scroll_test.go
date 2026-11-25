package web

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
)

// moreHeaders are the headers of the sentinel's htmx request.
var moreHeaders = map[string]string{"HX-Request": "true", "HX-Target": "list-more"}

// manyShows stores n active shows "Show 001".. in the tv fixture plugin.
func manyShows(t *testing.T, e *fixtureEnv, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), fmt.Sprintf("Show %03d", i+1)), library.Entry{Status: library.Active})
	}
}

// sentinel returns the list's load-more element (its opening tag through the
// end of its fallback link), "" when there is none.
func sentinel(body string) string {
	i := strings.Index(body, "data-list-more")
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<")
	end := strings.Index(body[i:], "</a>")
	if end < 0 {
		return body[start:]
	}
	return body[start : i+end+4]
}

// sentinelURL is the hx-get of the sentinel, unescaped.
func sentinelURL(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`hx-get="([^"]*)"`).FindStringSubmatch(sentinel(body))
	if m == nil {
		t.Fatalf("no sentinel hx-get in %q", sentinel(body))
	}
	return html.UnescapeString(m[1])
}

func TestListPagingSettingDefaultsToScrollAndValidates(t *testing.T) {
	e := newFixtureEnv(t)
	b := e.get("/settings").Body.String()
	mustContain(t, b, `action="/settings/list-paging"`, "Load more while scrolling", "Split into pages",
		`name="list_paging" value="scroll" checked`)
	mustNotContain(t, b, `value="pages" checked`)
	rec := e.do("POST", "/settings/list-paging", url.Values{"list_paging": {"endless"}}, false)
	if rec.Code != 422 {
		t.Fatalf("invalid paging: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Choose how lists load")
	if e.setting(t, "list_paging") != "" {
		t.Error("invalid paging stored")
	}
	if rec := e.do("POST", "/settings/list-paging", url.Values{"list_paging": {"pages"}}, false); rec.Code != 303 || rec.Header().Get("Location") != "/settings" {
		t.Fatalf("pages: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if e.setting(t, "list_paging") != "pages" {
		t.Error("pages not stored")
	}
	mustContain(t, e.get("/settings").Body.String(), `name="list_paging" value="pages" checked`)
	if rec := e.do("POST", "/settings/list-paging", url.Values{"list_paging": {"scroll"}}, false); rec.Code != 303 {
		t.Fatalf("scroll: %d", rec.Code)
	}
	if e.setting(t, "list_paging") != "scroll" {
		t.Error("scroll not stored")
	}
	// a stored value that is no mode means the default
	if err := e.set.Set(bg, "list_paging", "bogus"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/settings").Body.String(), `name="list_paging" value="scroll" checked`)
}

// In the default scroll mode the first page ends with a sentinel that loads
// the next page; the last page has none, and the pager links are gone.
func TestListScrollSentinel(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 101)
	for _, view := range []string{"table", "cards"} {
		b := e.listGet(t, "/tv?view="+view)
		s := sentinel(b)
		if s == "" {
			t.Fatalf("%s: no sentinel", view)
		}
		if got := sentinelURL(t, b); got != "/tv?page=2&view="+view {
			t.Errorf("%s: sentinel url %q", view, got)
		}
		mustContain(t, s, `id="list-more"`, `hx-trigger="intersect once"`, `hx-swap="outerHTML"`,
			// without JavaScript it is a plain link to the next page
			`href="/tv?page=2&amp;view=`+view+`"`, ">Load more</a>")
		mustNotContain(t, b, ">Next</a>", ">Previous</a>")
		if n := strings.Count(b, `id="list-more"`); n != 1 {
			t.Errorf("%s: %d sentinels", view, n)
		}
		// the sentinel sits inside the rows region, after the last row
		rows := b[strings.Index(b, `id="list-rows"`):]
		if strings.Index(rows, "data-list-more") < strings.LastIndex(rows[:strings.Index(rows, "data-list-more")], "Show 00") {
			t.Errorf("%s: sentinel before the rows", view)
		}
	}
	tbl := e.listGet(t, "/tv?view=table")
	// the table's sentinel is a row spanning every column (select, cover, title, columns, actions)
	cols := strings.Count(thead(t, tbl), "<th ")
	mustContain(t, sentinel(tbl), `<tr id="list-more"`, `colspan="`+strconv.Itoa(cols)+`"`)
	mustContain(t, sentinel(e.listGet(t, "/tv?view=cards")), `<div id="list-more"`, "col-span-full")
	// one page only: no sentinel
	one := newFixtureEnv(t)
	manyShows(t, one, 3)
	mustNotContain(t, one.listGet(t, "/tv?view=table"), "data-list-more")
}

// The sentinel's request answers only the next rows (or cards) and the next
// sentinel: no page, no head, no tabs, no URL push; Vary as on the list.
func TestListScrollFragment(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 201)
	rec := e.getWith("/tv?page=2&view=table", moreHeaders)
	if rec.Code != 200 {
		t.Fatalf("fragment: %d", rec.Code)
	}
	if got := rec.Header().Values("Vary"); !slices.Equal(got, []string{"HX-Request, HX-Target, HX-History-Restore-Request"}) {
		t.Errorf("Vary %q", got)
	}
	if rec.Header().Get("HX-Push-Url") != "" {
		t.Errorf("fragment pushes %q", rec.Header().Get("HX-Push-Url"))
	}
	b := rec.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(b), "<tr data-row") {
		t.Errorf("fragment does not start with a row: %.80q", strings.TrimSpace(b))
	}
	if n := strings.Count(b, "<tr data-row"); n != 100 {
		t.Errorf("fragment rows %d", n)
	}
	mustNotContain(t, b, "<html", "<table", "<thead", "<tbody", `id="list-tabs"`, `id="list-filter"`, "hx-swap-oob", "Showing", "data-bulk-state")
	if got := sentinelURL(t, b); got != "/tv?page=3&view=table" {
		t.Errorf("next sentinel %q", got)
	}
	// appended rows join the bulk form and keep +1, edit and the return to the list's filters (no page)
	mustContain(t, b, `form="bulk-form" data-bulk-row`, `hx-target="#dialog-slot"`, ">+1</button>",
		`name="return" value="/tv?view=table"`)
	mustNotContain(t, b, `name="return" value="/tv?page=`)
	// the last page: rows, no sentinel
	last := e.getWith("/tv?page=3&view=table", moreHeaders).Body.String()
	if n := strings.Count(last, "<tr data-row"); n != 1 {
		t.Errorf("last fragment rows %d", n)
	}
	mustNotContain(t, last, "data-list-more")
	// cards: cards only (no grid wrapper) and the next sentinel
	c := e.getWith("/tv?page=2&view=cards", moreHeaders).Body.String()
	if !strings.HasPrefix(strings.TrimSpace(c), "<div data-card") {
		t.Errorf("cards fragment starts %.80q", strings.TrimSpace(c))
	}
	if n := strings.Count(c, "<div data-card"); n != 100 {
		t.Errorf("fragment cards %d", n)
	}
	mustNotContain(t, c, "data-cards", "<html", `id="list-tabs"`)
	mustContain(t, c, `hx-target="closest [data-card]"`, `name="view" value="cards"`)
	if got := sentinelURL(t, c); got != "/tv?page=3&view=cards" {
		t.Errorf("cards next sentinel %q", got)
	}
	// without the sentinel's target the same URL is the full page (no-JS fallback, Back)
	mustContain(t, e.get("/tv?page=2&view=table").Body.String(), "<html", `id="list-rows"`)
}

// Pages mode is exactly the earlier pagination: pager links, no sentinel.
func TestListPagesModeKeepsPagination(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 101)
	pagesMode(t, e.env)
	for _, view := range []string{"table", "cards"} {
		b := e.listGet(t, "/tv?view="+view)
		mustContain(t, b, "Showing 1–100 of 101", `href="/tv?page=2&amp;view=`+view+`"`, ">Next</a>")
		mustNotContain(t, b, "data-list-more", "Load more")
	}
}

// A filter change answers page 1 with a sentinel carrying the filters, and
// pushes a URL without a page number.
func TestListScrollFilterChangeStartsAtPageOne(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 150)
	full := e.listGet(t, "/tv?view=table")
	mustNotContain(t, full[strings.Index(full, `id="list-filter"`):strings.Index(full, `id="list-rows"`)], `name="page"`)
	rec := e.getWith("/tv?q=Show&view=table", map[string]string{"HX-Request": "true", "HX-Target": "list-rows"})
	if got := rec.Header().Get("HX-Push-Url"); got != "/tv?q=Show&view=table" {
		t.Errorf("push %q", got)
	}
	b := rec.Body.String()
	if n := strings.Count(b, "<tr data-row"); n != 100 {
		t.Errorf("rows %d", n)
	}
	if got := sentinelURL(t, b); got != "/tv?page=2&q=Show&view=table" {
		t.Errorf("sentinel %q", got)
	}
}

// "Select all on this page" selects what is loaded in scroll mode.
func TestListScrollBulkSelectsAllLoaded(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 2)
	for _, view := range []string{"table", "cards"} {
		b := e.listGet(t, "/tv?view="+view)
		mustContain(t, b, "data-bulk-page-all", "Select all loaded")
		mustNotContain(t, b, "Select all on this page")
	}
	pagesMode(t, e.env)
	mustContain(t, e.listGet(t, "/tv?view=table"), `aria-label="Select all on this page"`)
	mustContain(t, e.listGet(t, "/tv?view=cards"), "Select all on this page")
}

// The Series tab loads more while scrolling too.
func TestSeriesTabScroll(t *testing.T) {
	fe := newFixtureEnv(t)
	for i := 0; i < listPageSize+1; i++ {
		n := strconv.Itoa(i)
		id := fe.addFixture(t, "tv", "show", tvShow("p"+n, "Show "+n), library.Entry{Status: library.Planned})
		if _, err := fe.lib.CreateSeries(bg, id, "Set "+n); err != nil {
			t.Fatal(err)
		}
	}
	for _, view := range []string{"table", "cards"} {
		b := fe.listGet(t, "/tv?tab=series&sort=title&view="+view)
		mustNotContain(t, b, ">Next</a>")
		u := sentinelURL(t, b)
		for _, want := range []string{"tab=series", "page=2", "sort=title", "view=" + view} {
			if !strings.Contains(u, want) {
				t.Errorf("%s: sentinel %q misses %s", view, u, want)
			}
		}
		f := fe.getWith(u, moreHeaders).Body.String()
		mustNotContain(t, f, "<html", "<table", `id="list-tabs"`, "data-list-more", "data-cards")
		if view == "table" {
			if !strings.HasPrefix(strings.TrimSpace(f), "<tbody>") || strings.Count(f, "data-series-row") != 1 {
				t.Errorf("series table fragment %.120q", f)
			}
			mustContain(t, sentinel(b), `<tbody id="list-more"`, `colspan="6"`)
		} else if strings.Count(f, "<div data-card") != 1 {
			t.Errorf("series cards fragment %.120q", f)
		}
	}
}

// pagesMode switches the lists of e to pages (Settings > General).
func pagesMode(t *testing.T, e *env) {
	t.Helper()
	if err := e.set.Set(bg, "list_paging", "pages"); err != nil {
		t.Fatal(err)
	}
}

// app.js wires rows appended later: the bulk state is recomputed after every
// swap (the select-all covers every loaded row) and a row the sentinel brings
// twice (the order moved while scrolling) is dropped.
func TestAppJSHandlesAppendedRows(t *testing.T) {
	js, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(js), `document.addEventListener("htmx:afterSwap", bulkInit)`,
		`querySelectorAll('input[data-bulk-row][form="bulk-form"]')`, `closest("[data-list-more]")`)
	// "Select all N matching" survives a load: the sentinel handler (which
	// runs before bulkInit) ticks the appended rows while the scope is filter
	h := string(js)[strings.Index(string(js), `closest("[data-list-more]")`):]
	h = h[:strings.Index(h, `document.addEventListener("htmx:afterSwap", bulkInit)`)]
	mustContain(t, h, `querySelector("#bulk-form [data-bulk-scope]")`, `scope.value === "filter"`, `.checked = true`)
}

// A click on "Load more" and the intersect trigger never both load.
func TestListMoreLinkSyncs(t *testing.T) {
	e := newFixtureEnv(t)
	manyShows(t, e, 101)
	mustContain(t, sentinel(e.listGet(t, "/tv?view=table")), `hx-sync="closest [data-list-more]:drop"`)
}
