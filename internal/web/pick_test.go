package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
)

var (
	totalMeasure = plugin.LengthDef{Key: "total", Label: "Total time", UnitsField: "episodes", PerUnitField: "duration"}
	countMeasure = plugin.LengthDef{Key: "count", Label: "Count", UnitsField: "episodes"}
	fieldMeasure = plugin.LengthDef{Key: "main", Label: "Main", Field: "main"}
)

func fnum(f float64) *float64 { return &f }

func TestParseLengthFilter(t *testing.T) {
	k := plugin.KindDef{Length: []plugin.LengthDef{totalMeasure, countMeasure}}
	cases := []struct {
		name         string
		k            plugin.KindDef
		q            string
		measure      string
		min, max     *float64
		wantRejected []string
	}{
		{"nothing", k, "", "", nil, nil, nil},
		{"minutes", k, "len=total&len_min=30&len_max=90", "total", fnum(30), fnum(90), nil},
		{"count one bound", k, "len=count&len_max=12.5", "count", nil, fnum(12.5), nil},
		{"empty values are absent", k, "len=&len_min=&len_max=%20", "", nil, nil, nil},
		{"unknown measure", k, "len=nope&len_min=1", "", nil, nil, []string{"len", "len_min"}},
		{"bound without measure", k, "len_min=5", "", nil, nil, []string{"len_min"}},
		{"inverted", k, "len=total&len_min=90&len_max=30", "total", nil, nil, []string{"len_max", "len_min"}},
		{"not a number", k, "len=total&len_min=abc&len_max=20", "total", nil, fnum(20), []string{"len_min"}},
		{"nan and inf", k, "len=total&len_min=NaN&len_max=Inf", "total", nil, nil, []string{"len_max", "len_min"}},
		{"negative", k, "len=total&len_min=-3", "total", nil, nil, []string{"len_min"}},
		{"repeated", k, "len=total&len=count", "", nil, nil, []string{"len"}},
		{"kind without length", plugin.KindDef{}, "len=total&len_min=1&len_max=2", "", nil, nil, []string{"len", "len_max", "len_min"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, _ := url.ParseQuery(c.q)
			lf, rej := parseLengthFilter(c.k, q)
			if lf.Measure.Key != c.measure {
				t.Errorf("measure %q, want %q", lf.Measure.Key, c.measure)
			}
			for _, b := range []struct {
				name      string
				got, want *float64
			}{{"min", lf.Min, c.min}, {"max", lf.Max, c.max}} {
				if (b.got == nil) != (b.want == nil) || (b.got != nil && *b.got != *b.want) {
					t.Errorf("%s %v, want %v", b.name, b.got, b.want)
				}
			}
			slices.Sort(rej)
			if !slices.Equal(rej, c.wantRejected) {
				t.Errorf("rejected %v, want %v", rej, c.wantRejected)
			}
		})
	}
}

func TestEntryLength(t *testing.T) {
	cases := []struct {
		name   string
		m      plugin.LengthDef
		fields map[string]float64
		want   float64
		ok     bool
	}{
		{"field", fieldMeasure, map[string]float64{"main": 90}, 90, true},
		{"units times per unit", totalMeasure, map[string]float64{"episodes": 12, "duration": 24}, 288, true},
		{"units alone", countMeasure, map[string]float64{"episodes": 12}, 12, true},
		{"field missing", fieldMeasure, map[string]float64{}, 0, false},
		{"field zero", fieldMeasure, map[string]float64{"main": 0}, 0, false},
		{"units missing", totalMeasure, map[string]float64{"duration": 24}, 0, false},
		{"per unit missing", totalMeasure, map[string]float64{"episodes": 12}, 0, false},
		{"per unit zero", totalMeasure, map[string]float64{"episodes": 12, "duration": 0}, 0, false},
		{"units zero", countMeasure, map[string]float64{"episodes": 0}, 0, false},
	}
	for _, c := range cases {
		got, ok := entryLength(c.m, c.fields)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: (%v, %v), want (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// addPick saves an anime entry of the given status with episodes eps and
// minutes per episode dur (nil: unset).
func addPick(t *testing.T, e *env, id, title string, status library.Status, eps, dur *int, genres ...string) int64 {
	t.Helper()
	a := anilist.Media{
		SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: id, Type: "TV", Episodes: eps, Duration: dur,
			Titles: []anilist.Title{{Lang: "en", Text: title}}},
		Genres: genres,
	}
	itemID, err := addToList(context.Background(), e.lib, a, listEntry{Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return itemID
}

var pickTitleRE = regexp.MustCompile(`data-pick-title[^>]*>([^<]*)<`)

// pickedTitle is the title of the pick card of a pick answer ("" for none).
func pickedTitle(body string) string {
	if m := pickTitleRE.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// pick requests /anime/pick as htmx does, with rand returning idx (clamped)
// and recording the number of candidates in *n.
func pick(t *testing.T, e *env, query string, idx int, n *int) *httpResult {
	t.Helper()
	e.srv.rand = func(c int) int {
		if n != nil {
			*n = c
		}
		return min(idx, c-1)
	}
	rec := e.do("GET", "/anime/pick?"+query, nil, true)
	return &httpResult{rec.Code, rec.Body.String(), rec.Header()}
}

type httpResult struct {
	Code   int
	Body   string
	Header http.Header
}

func TestPickDrawsFromMatches(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(10), ip(24))
	addPick(t, e, "2", "Bravo", library.Planned, ip(20), ip(24))
	addPick(t, e, "3", "Charlie", library.Planned, ip(30), ip(24))
	var n int
	// Bravo and Charlie match 15..35 episodes; the title order makes the draw stable
	for idx, want := range []string{"Bravo", "Charlie"} {
		r := pick(t, e, "len=episodes&len_min=15&len_max=35", idx, &n)
		if r.Code != 200 || n != 2 {
			t.Fatalf("status %d with %d candidates, want 200 and 2", r.Code, n)
		}
		if got := pickedTitle(r.Body); got != want {
			t.Errorf("rand %d drew %q, want %q", idx, got, want)
		}
	}
	// minutes: 10x24=240, 20x24=480, 30x24=720; 300..800 matches two as well
	if r := pick(t, e, "len=total&len_min=300&len_max=800", 0, &n); pickedTitle(r.Body) != "Bravo" || n != 2 {
		t.Errorf("total time: drew %q of %d", pickedTitle(r.Body), n)
	}
	mustContain(t, pick(t, e, "len=total&len_min=300&len_max=800", 0, &n).Body, "8h")
}

func TestPickExcludesUnsetLength(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Planned, nil, ip(24)) // episodes unset
	addPick(t, e, "3", "Charlie", library.Planned, ip(0), nil)
	addPick(t, e, "4", "Delta", library.Planned, ip(8), nil) // per-episode minutes unset
	var n int
	for _, q := range []string{"len=episodes&len_min=1", "len=episodes&len_max=100", "len=episodes&len_min=1&len_max=100"} {
		r := pick(t, e, q, 5, &n)
		if r.Code != 200 || n != 2 || pickedTitle(r.Body) != "Delta" { // Alpha, Delta by title; idx clamps to last
			t.Errorf("%s: status %d, %d candidates, drew %q", q, r.Code, n, pickedTitle(r.Body))
		}
	}
	// the total needs both fields: only Alpha has them
	if r := pick(t, e, "len=total&len_max=100000", 0, &n); n != 1 || pickedTitle(r.Body) != "Alpha" {
		t.Errorf("total: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
	// without a bound nothing is excluded
	if r := pick(t, e, "len=episodes", 0, &n); n != 4 {
		t.Errorf("no bound: %d candidates, want 4 (%q)", n, pickedTitle(r.Body))
	}
}

// TestPickCapNotice: more matches than the cap are cut to the first ones in
// title order with a notice; at the cap exactly there is none. The cap is a
// server field so the test does not need 20,000 rows.
func TestPickCapNotice(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Planned, ip(12), ip(24))
	addPick(t, e, "3", "Charlie", library.Planned, ip(12), ip(24))
	if e.srv.pickCap != 20000 {
		t.Fatalf("default cap = %d, want 20000", e.srv.pickCap)
	}
	var n int
	e.srv.pickCap = 2
	r := pick(t, e, "", 5, &n)
	if n != 2 || pickedTitle(r.Body) != "Bravo" { // Alpha, Bravo by title; Charlie is cut
		t.Fatalf("%d candidates, drew %q", n, pickedTitle(r.Body))
	}
	mustContain(t, r.Body, "Only the first 2 matching", "were considered")
	e.srv.pickCap = 3
	r = pick(t, e, "", 5, &n)
	if n != 3 || pickedTitle(r.Body) != "Charlie" {
		t.Fatalf("%d candidates, drew %q", n, pickedTitle(r.Body))
	}
	mustNotContain(t, r.Body, "were considered")
}

func TestPickOneMatchRollsSameEntry(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Planned, ip(99), ip(24))
	var n int
	for range 3 {
		r := pick(t, e, "len=episodes&len_min=50", 0, &n)
		if n != 1 || pickedTitle(r.Body) != "Bravo" {
			t.Fatalf("%d candidates, drew %q", n, pickedTitle(r.Body))
		}
	}
}

func TestPickRollAgainSameURL(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24), "Drama")
	r := pick(t, e, "len=episodes&len_min=5&genre=Drama&kind=anime&page=3&sort=title", 0, nil)
	m := regexp.MustCompile(`data-roll-again[^>]*hx-get="([^"]+)"`).FindStringSubmatch(r.Body)
	if m == nil {
		t.Fatalf("no roll again button in %s", r.Body)
	}
	href := strings.ReplaceAll(m[1], "&amp;", "&")
	u, err := url.Parse(href)
	if err != nil || u.Path != "/anime/pick" {
		t.Fatalf("roll again url %q", href)
	}
	q := u.Query()
	if q.Get("status") != "planned" || q.Get("len") != "episodes" || q.Get("len_min") != "5" || q.Get("genre") != "Drama" {
		t.Errorf("roll again %q lost a parameter", href)
	}
	if q.Has("page") {
		t.Errorf("roll again %q keeps the page", href)
	}
	if r2 := pick(t, e, u.RawQuery, 0, nil); pickedTitle(r2.Body) != "Alpha" {
		t.Errorf("rolling again drew %q", pickedTitle(r2.Body))
	}
}

func TestPickDefaultsToPlanned(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Active, ip(12), ip(24))
	addPick(t, e, "3", "Charlie", library.Completed, ip(12), ip(24))
	var n int
	if r := pick(t, e, "", 0, &n); n != 1 || pickedTitle(r.Body) != "Alpha" {
		t.Errorf("no status: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
	// a plugin home status does not leak into the pick
	if err := e.set.SetHomeStatuses(context.Background(), "anime", "completed"); err != nil {
		t.Fatal(err)
	}
	if r := pick(t, e, "", 0, &n); n != 1 || pickedTitle(r.Body) != "Alpha" {
		t.Errorf("home status: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
}

func TestPickHonoursExplicitStatus(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Active, ip(12), ip(24))
	addPick(t, e, "3", "Charlie", library.Completed, ip(12), ip(24))
	var n int
	if r := pick(t, e, "status=active", 0, &n); n != 1 || pickedTitle(r.Body) != "Bravo" {
		t.Errorf("active: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
	if r := pick(t, e, "status=all", 0, &n); n != 3 {
		t.Errorf("all: %d candidates, want 3 (%q)", n, pickedTitle(r.Body))
	}
	// an invalid status is named and means all, never a 500
	r := pick(t, e, "status=bogus", 0, &n)
	if r.Code != 200 || n != 3 {
		t.Errorf("bogus status: %d, %d candidates", r.Code, n)
	}
	mustContain(t, r.Body, "<code>status</code>")
}

func TestPickHtmxPartialAndFullPage(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	e.srv.rand = func(int) int { return 0 }

	part := e.do("GET", "/anime/pick", nil, true)
	if part.Code != 200 {
		t.Fatalf("partial status %d", part.Code)
	}
	if v := part.Header().Get("Vary"); !strings.Contains(v, "HX-Request") || !strings.Contains(v, "HX-Target") || !strings.Contains(v, "HX-History-Restore-Request") {
		t.Errorf("Vary %q", v)
	}
	mustNotContain(t, part.Body.String(), "<html", "id=\"list-rows\"")
	mustContain(t, part.Body.String(), "data-pick-title")

	full := e.get("/anime/pick")
	if full.Code != 200 {
		t.Fatalf("full status %d", full.Code)
	}
	if v := full.Header().Get("Vary"); !strings.Contains(v, "HX-Request") {
		t.Errorf("full Vary %q", v)
	}
	mustContain(t, full.Body.String(), "<html", `id="list-rows"`, `id="pick-result"`, "data-pick-title")

	// a history restore of the pick URL gets the full page too
	restore := e.getWith("/anime/pick", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	mustContain(t, restore.Body.String(), "<html")

	// the list page offers the button and the length controls
	list := e.get("/anime?status=planned").Body.String()
	mustContain(t, list, `id="pick-result"`, `formaction="/anime/pick"`, `name="len_min"`, `name="len_max"`, `name="len"`)
	mustContain(t, list, "Total time (min)", "Episodes (eps)")
}

func TestPickStartButtonOnlyForPlanned(t *testing.T) {
	e := newEnv(t)
	planned := addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	addPick(t, e, "2", "Bravo", library.Active, ip(12), ip(24))
	start := fmt.Sprintf(`action="/list/%d/start"`, planned)
	mustContain(t, pick(t, e, "status=planned", 0, nil).Body, start, "Start")
	mustNotContain(t, pick(t, e, "status=active", 0, nil).Body, "/start\"")
	// status=all draws Alpha (first by title) with a Start button
	mustContain(t, pick(t, e, "status=all", 0, nil).Body, start)
}

func TestPostStartActivates(t *testing.T) {
	e := newEnv(t)
	id := addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	rec := e.do("POST", fmt.Sprintf("/list/%d/start", id), url.Values{}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/anime/anilist/1" {
		t.Fatalf("status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	en, err := getEntry(context.Background(), e.lib, id)
	if err != nil || en == nil || en.Status != library.Active {
		t.Fatalf("entry %+v, err %v: want active", en, err)
	}
	// starting again changes nothing (no 500, still active)
	rec = e.do("POST", fmt.Sprintf("/list/%d/start", id), url.Values{}, false)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("second start: status %d", rec.Code)
	}
	if en, _ := getEntry(context.Background(), e.lib, id); en.Status != library.Active {
		t.Errorf("second start left %s", en.Status)
	}
	// a completed entry is not touched
	done := addPick(t, e, "2", "Bravo", library.Completed, ip(12), ip(24))
	e.do("POST", fmt.Sprintf("/list/%d/start", done), url.Values{}, false)
	if en, _ := getEntry(context.Background(), e.lib, done); en.Status != library.Completed {
		t.Errorf("completed entry became %s", en.Status)
	}
	// unknown item
	if rec := e.do("POST", "/list/99999/start", url.Values{}, false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item: status %d", rec.Code)
	}
}

func TestPickGenreAndOwnTagFilters(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24), "Drama")
	addPick(t, e, "2", "Bravo", library.Planned, ip(12), ip(24), "Comedy")
	c := addPick(t, e, "3", "Charlie", library.Planned, ip(12), ip(24), "Drama")
	if err := e.lib.SetEntryTags(context.Background(), c, []string{"cosy"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if r := pick(t, e, "genre=Drama", 0, &n); n != 2 || pickedTitle(r.Body) != "Alpha" {
		t.Errorf("genre: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
	if r := pick(t, e, "ut=cosy", 0, &n); n != 1 || pickedTitle(r.Body) != "Charlie" {
		t.Errorf("own tag: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
	if r := pick(t, e, "genre=Drama&ut=cosy&len=episodes&len_min=5", 0, &n); n != 1 || pickedTitle(r.Body) != "Charlie" {
		t.Errorf("combined: %d candidates, drew %q", n, pickedTitle(r.Body))
	}
}

func TestPickEmptyState(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24), "Drama")
	called := false
	e.srv.rand = func(c int) int { called = true; return 0 }
	rec := e.do("GET", "/anime/pick?len=episodes&len_min=500&genre=Drama", nil, true)
	body := rec.Body.String()
	if rec.Code != 200 || called {
		t.Fatalf("status %d, rand called %v", rec.Code, called)
	}
	mustContain(t, body, "Nothing matches", "Episodes", "500", "Drama", "Plan to watch")
	mustNotContain(t, body, "data-pick-title", "data-roll-again")
	// an empty list gets the empty state as well
	e2 := newEnv(t)
	body = e2.do("GET", "/anime/pick", nil, true).Body.String()
	mustContain(t, body, "Nothing matches")
}

func TestPickBadLengthParamsNeverFail(t *testing.T) {
	e := newEnv(t)
	addPick(t, e, "1", "Alpha", library.Planned, ip(12), ip(24))
	for _, q := range []string{
		"len=nope", "len=nope&len_min=1", "len_min=3", "len=episodes&len_min=9&len_max=2",
		"len=episodes&len_min=abc", "len=episodes&len_max=NaN", "len=episodes&len_min=-1", "len=episodes&len=total",
		"len=episodes&len_min=1e999", "len=%00&len_min=%ff", "len_min=" + strings.Repeat("9", 5000),
	} {
		for _, htmx := range []bool{true, false} {
			rec := e.do("GET", "/anime/pick?"+q, nil, htmx)
			if rec.Code != 200 {
				t.Errorf("%.60s htmx=%v: status %d", q, htmx, rec.Code)
				continue
			}
			mustContain(t, rec.Body.String(), "data-filter-notice")
		}
	}
	// the ignored length means no length filter: the entry is still drawn
	e.srv.rand = func(int) int { return 0 }
	r := e.do("GET", "/anime/pick?len=episodes&len_min=9&len_max=2", nil, true)
	if pickedTitle(r.Body.String()) != "Alpha" {
		t.Errorf("inverted bounds drew %q", pickedTitle(r.Body.String()))
	}
	mustContain(t, r.Body.String(), "<code>len_max</code>", "<code>len_min</code>")

	// a kind without a length: len is ignored with a notice
	b := newEnvWith(t, withPlugins(plugintest.BookPlugin()))
	if _, err := b.lib.AddToList(context.Background(), "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: "books", ID: "b1", Titles: []plugin.Title{{Lang: "en", Text: "Book One"}}}}, library.Entry{Status: library.Planned}); err != nil {
		t.Fatal(err)
	}
	b.srv.rand = func(int) int { return 0 }
	rec := b.do("GET", "/book/pick?len=main&len_min=1", nil, true)
	if rec.Code != 200 || pickedTitle(rec.Body.String()) != "Book One" {
		t.Fatalf("book: status %d, drew %q", rec.Code, pickedTitle(rec.Body.String()))
	}
	mustContain(t, rec.Body.String(), "<code>len</code>")
	mustNotContain(t, b.get("/book").Body.String(), `name="len_min"`)
}

func TestPickUnknownOrDisabledPlugin(t *testing.T) {
	e := newEnv(t)
	if rec := e.get("/nope/pick"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown plugin: status %d", rec.Code)
	}
}
