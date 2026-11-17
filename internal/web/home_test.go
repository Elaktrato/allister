package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// stamp sets the entry's update time (the library stamps "now" in whole
// seconds, which cannot order a test's rows). The entry presents the newer of
// its own and its runs' times, so both are set.
func (e *env) stamp(t *testing.T, id int64, at time.Time) {
	t.Helper()
	for _, q := range []string{`UPDATE entries SET updated_at = ? WHERE item_id = ?`, `UPDATE entry_runs SET updated_at = ? WHERE item_id = ?`} {
		if _, err := e.d.Exec(q, at.UTC().Format("2006-01-02T15:04:05Z"), id); err != nil {
			t.Fatal(err)
		}
	}
}

// section returns the part of body between the heading of the section and the next h2.
func section(t *testing.T, body, heading string) string {
	t.Helper()
	i := strings.Index(body, ">"+heading+"</h2>")
	if i < 0 {
		t.Fatalf("no section %q in %s", heading, body)
	}
	rest := body[i+1:]
	if j := strings.Index(rest, "</h2>"); j >= 0 {
		rest = rest[j:]
	}
	if j := strings.Index(rest, "<h2"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestHomeRedirectsWithoutPlugins(t *testing.T) {
	e := newFixtureEnv(t)
	for _, id := range []string{"anime", "book", "tv"} {
		if rec := e.do("POST", "/settings/plugins/"+id, url.Values{"enabled": {"0"}}, false); rec.Code != http.StatusSeeOther {
			t.Fatal(rec.Code)
		}
	}
	rec := e.get("/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins" {
		t.Fatalf("/ -> %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHomeEmptyHidesSections(t *testing.T) {
	e := newFixtureEnv(t)
	rec := e.get("/")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, ">Home</h1>")
	mustNotContain(t, b, "<h2")
}

func TestHomeInProgressSpansPluginsInUpdateOrder(t *testing.T) {
	e := newFixtureEnv(t)
	base := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	a := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	b := e.addFixture(t, "book", "book", bookItem("2", "Earthsea"), library.Entry{Status: library.Active})
	c := e.addFixture(t, "tv", "show", tvShow("3", "Light"), library.Entry{Status: library.Active})
	p := e.addFixture(t, "tv", "show", tvShow("4", "Planned One"), library.Entry{Status: library.Planned})
	e.stamp(t, a, base.Add(3*time.Hour))
	e.stamp(t, b, base.Add(2*time.Hour))
	e.stamp(t, c, base.Add(time.Hour))
	e.stamp(t, p, base.Add(9*time.Hour))

	body := e.get("/").Body.String()
	sec := section(t, body, "Up next")
	ia, ib, ic := strings.Index(sec, "Dark"), strings.Index(sec, "Earthsea"), strings.Index(sec, "Light")
	if ia < 0 || ib < ia || ic < ib {
		t.Fatalf("order dark=%d earthsea=%d light=%d", ia, ib, ic)
	}
	mustNotContain(t, sec, "Planned One")
	mustContain(t, sec, "data-card", "/increment", "menu_book", "Book", "Show", `href="/book?status=active`, `href="/tv?status=active`)
}

func TestHomeInProgressLimit(t *testing.T) {
	e := newFixtureEnv(t)
	for i := 0; i < 15; i++ {
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i), fmt.Sprintf("Show%02d", i)), library.Entry{Status: library.Active})
	}
	sec := section(t, e.get("/").Body.String(), "Up next")
	if n := strings.Count(sec, "<div data-card class"); n != 12 {
		t.Fatalf("cards: %d", n)
	}
}

func TestHomeComingUpNextSevenDays(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	other := e.addFixture(t, "tv", "show", tvShow("2", "Light"), library.Entry{Status: library.Active})
	err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{
		{Number: 1, AirsAt: now.Add(-time.Hour)},                   // already aired
		{Number: 2, AirsAt: now.Add(3 * time.Hour)},                // today
		{Number: 3, AirsAt: now.Add(7*24*time.Hour - time.Minute)}, // last minute of the window
		{Number: 4, AirsAt: now.Add(7*24*time.Hour + time.Minute)}, // outside
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SaveSchedule(context.Background(), other, []library.Airing{{Number: 9, AirsAt: now.Add(48 * time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	sec := section(t, e.get("/").Body.String(), "Coming up")
	mustContain(t, sec, "Episode 2", "Episode 9", "Episode 3", `href="/tv/tvdata/1"`, `href="/calendar"`, "Tuesday, 10 November", "Thursday, 12 November", "Tuesday, 17 November")
	mustNotContain(t, sec, "Episode 1<", "Episode 4")
	if strings.Index(sec, "Episode 2") > strings.Index(sec, "Episode 9") || strings.Index(sec, "Episode 9") > strings.Index(sec, "Episode 3") {
		t.Fatal("not in air order")
	}
}

func TestHomeComingUpLimitAndHidden(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	mustNotContain(t, e.get("/").Body.String(), "Coming up")
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	var airs []library.Airing
	for i := 1; i <= 14; i++ {
		airs = append(airs, library.Airing{Number: i, AirsAt: now.Add(time.Duration(i) * time.Hour)})
	}
	if err := e.lib.SaveSchedule(context.Background(), id, airs, now); err != nil {
		t.Fatal(err)
	}
	sec := section(t, e.get("/").Body.String(), "Coming up")
	if n := strings.Count(sec, "Dark"); n != 10 {
		t.Fatalf("releases: %d", n)
	}
}

func TestHomeSeriesInProgress(t *testing.T) {
	e := newFixtureEnv(t)
	mustNotContain(t, e.get("/").Body.String(), "Series in progress")
	ser, _ := e.tvSeries(t, "Saga") // completed + planned + active: active, 1/3
	done := e.addFixture(t, "tv", "show", tvShow("d", "Done Show"), library.Entry{Status: library.Completed})
	if _, err := e.lib.CreateSeries(bg, done, "Done Set"); err != nil {
		t.Fatal(err)
	}
	sec := section(t, e.get("/").Body.String(), "Series in progress")
	mustContain(t, sec, "Saga", "1/3", `href="`+seriesURL("tv", ser.UID)+`"`)
	mustNotContain(t, sec, "Done Set")
}

func TestHomeSeriesLimit(t *testing.T) {
	e := newFixtureEnv(t)
	for i := 0; i < 8; i++ {
		e.tvSeries(t, fmt.Sprintf("Saga%d", i))
	}
	sec := section(t, e.get("/").Body.String(), "Series in progress")
	if n := strings.Count(sec, `/tv/series/`); n != 6 {
		t.Fatalf("series: %d", n)
	}
}

func TestHomeRecentlyUpdated(t *testing.T) {
	e := newFixtureEnv(t)
	base := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		id := e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i), fmt.Sprintf("Show%02d", i)), library.Entry{Status: library.Completed})
		e.stamp(t, id, base.Add(time.Duration(i)*time.Hour))
	}
	bk := e.addFixture(t, "book", "book", bookItem("b", "Newest Book"), library.Entry{Status: library.Planned})
	e.stamp(t, bk, base.Add(24*time.Hour))
	sec := section(t, e.get("/").Body.String(), "Recently updated")
	mustContain(t, sec, "Newest Book", "Want to read", "Show11", "Show03", "menu_book", `href="/tv/tvdata/11"`, "2025-12-01")
	mustNotContain(t, sec, "Show02", "Show00", "data-card")
	if strings.Index(sec, "Newest Book") > strings.Index(sec, "Show11") || strings.Index(sec, "Show11") > strings.Index(sec, "Show10") {
		t.Fatal("not newest first")
	}
	if n := strings.Count(sec, "<li"); n != 10 {
		t.Fatalf("entries: %d", n)
	}
}

func TestHomeSectionOrder(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 2, AirsAt: now.Add(time.Hour)}}, now)
	e.tvSeries(t, "Saga")
	b := e.get("/").Body.String()
	last := -1
	for _, h := range []string{"Up next", "Coming up", "Series in progress", "Recently updated"} {
		i := strings.Index(b, ">"+h+"</h2>")
		if i < 0 || i < last {
			t.Fatalf("section %q at %d (previous %d)", h, i, last)
		}
		last = i
	}
}

func TestHomeIncrementReturnsCard(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	body := e.get("/").Body.String()
	mustContain(t, body, `name="return" value="/"`, "/list/"+fmt.Sprint(id)+"/increment")
	rec := e.do("POST", "/list/"+fmt.Sprint(id)+"/increment", url.Values{"return": {"/"}, "view": {"cards"}}, true)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	got := rec.Body.String()
	mustContain(t, got, "data-card", `name="return" value="/"`, "Dark", "1 / ?")
	mustNotContain(t, got, "<tr")
	// the card keeps the plugin's badge like the home page's cards
	mustContain(t, got, `<span class="icon text-[0.875rem]" aria-hidden="true">category</span><span class="truncate">Show</span>`)
	// a +1 on a list answers the card without it
	list := e.do("POST", "/list/"+fmt.Sprint(id)+"/increment", url.Values{"return": {"/tv"}, "view": {"cards"}}, true).Body.String()
	mustContain(t, list, "data-card")
	mustNotContain(t, list, `class="truncate">Show</span>`, "left-0 top-0")
	// without htmx it goes back home
	rec = e.do("POST", "/list/"+fmt.Sprint(id)+"/increment", url.Values{"return": {"/"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHomeSidebarEntry(t *testing.T) {
	e := newFixtureEnv(t)
	nav := navOf(t, e.get("/").Body.String())
	mustContain(t, nav, `href="/"`, "home")
	if i, j := strings.Index(nav, `href="/"`), strings.Index(nav, `href="/book"`); !strings.Contains(nav, `aria-current="page"`) || strings.Count(nav, `aria-current="page"`) != 1 || i > j {
		t.Fatalf("nav: %s", nav)
	}
	cur := nav[strings.Index(nav, `aria-current="page"`)-12:]
	if !strings.HasPrefix(cur, `<a href="/"`) {
		t.Fatalf("current is not Home: %s", cur[:60])
	}
	// elsewhere Home is not current
	other := navOf(t, e.get("/tv").Body.String())
	mustNotContain(t, other, `<a href="/" aria-current`)
}

func TestHomeEscapesTitles(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", `<script>alert(1)</script>`), library.Entry{Status: library.Active})
	e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 2, AirsAt: now.Add(time.Hour)}}, now)
	e.tvSeries(t, `<b>Saga</b>`)
	b := e.get("/").Body.String()
	mustNotContain(t, b, "<script>alert", "<b>Saga</b>")
	mustContain(t, b, "&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;b&gt;Saga&lt;/b&gt;")
}

func TestHomeEditAndRemoveReturnHome(t *testing.T) {
	e := newFixtureEnv(t)
	id := fmt.Sprint(e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active}))
	mustContain(t, e.get("/").Body.String(), `id="dialog-slot"`, `data-swap-statuses=`)
	dlg := e.do("GET", "/list/"+id+"/edit?return=%2F", nil, true).Body.String()
	mustContain(t, dlg, `name="return" value="/"`)
	rec := e.do("POST", "/list/"+id+"/edit", url.Values{"status": {"paused"}, "return": {"/"}}, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("edit: %d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	rec = e.do("POST", "/list/"+id+"/remove", url.Values{"return": {"/"}}, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("remove: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	// other values keep the plugin list
	id2 := fmt.Sprint(e.addFixture(t, "tv", "show", tvShow("2", "Light"), library.Entry{Status: library.Active}))
	rec = e.do("POST", "/list/"+id2+"/edit", url.Values{"status": {"paused"}, "return": {"/settings"}}, true)
	if rec.Header().Get("HX-Redirect") != "/tv" {
		t.Fatalf("%q", rec.Header().Get("HX-Redirect"))
	}
}

func TestHomeOmitsDisabledPlugins(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Hidden Show"), library.Entry{Status: library.Active})
	e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 2, AirsAt: now.Add(time.Hour)}}, now)
	e.tvSeries(t, "Hidden Saga")
	e.addFixture(t, "book", "book", bookItem("b", "Visible Book"), library.Entry{Status: library.Active})
	mustContain(t, e.get("/").Body.String(), "Hidden Show", "Hidden Saga", "Coming up", "Series in progress")
	if rec := e.do("POST", "/settings/plugins/tv", url.Values{"enabled": {"0"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	b := e.get("/").Body.String()
	mustContain(t, b, "Visible Book")
	mustNotContain(t, b, "Hidden", "Coming up", "Series in progress", `href="/tv`)
}

func TestHomeOneLinkPerKindWithPluralLabel(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.addFixture(t, "book", "book", bookItem("b", "Earthsea"), library.Entry{Status: library.Active})
	sec := section(t, e.get("/").Body.String(), "Up next")
	mustContain(t, sec, "All active in Shows", "All active in Books")
}

func TestHomeSeriesBar(t *testing.T) {
	e := newFixtureEnv(t)
	e.tvSeries(t, "Saga") // 1 of 3 completed
	mustContain(t, section(t, e.get("/").Body.String(), "Series in progress"), `style="width: 33%"`)
}

// upNextShow is a tv fixture with an episode total (0: unbounded).
func upNextShow(id, title string, episodes float64) plugin.ItemData {
	d := tvShow(id, title)
	if episodes > 0 {
		d.Fields["episodes"] = plugin.FieldValue{Num: &episodes}
	}
	return d
}

func TestHomeUpNextHeading(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	b := e.get("/").Body.String()
	mustContain(t, b, `id="home-active"`, ">Up next</h2>")
	mustNotContain(t, b, ">In progress</h2>")
}

func TestHomeCardsShowNextUnit(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", upNextShow("1", "Dark", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 6}})
	e.addFixture(t, "tv", "show", tvShow("2", "Light"), library.Entry{Status: library.Active})
	e.addFixture(t, "book", "book", bookItem("b", "Earthsea"), library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 41}})
	body := e.get("/").Body.String()
	mustContain(t, cardOf(t, body, "Dark"), "Episode 7")
	mustContain(t, cardOf(t, body, "Light"), "Episode 1")
	mustContain(t, cardOf(t, body, "Earthsea"), "Page 42")
}

func TestHomeNextHiddenForDurationAndAtTotal(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", upNextShow("1", "Done Show", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 10}})
	body := e.get("/").Body.String()
	mustNotContain(t, cardOf(t, body, "Done Show"), "Episode 11", "data-next")
	// a duration or percent primary track has no next unit
	k := plugin.KindDef{Progress: plugin.ProgressSpec{PrimaryKey: "position",
		Eligible: []plugin.ProgressDef{{Key: "position", Type: plugin.Duration, Label: "Position"}}}}
	if got := nextLabel(k, []string{"position"}, library.Entry{Progress: map[string]float64{"position": 5}}, nil); got != "" {
		t.Fatalf("duration next label %q", got)
	}
}

func TestHomeAiredHint(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	ctx := context.Background()
	aired := func(id int64, n int) {
		var as []library.Airing
		for i := 1; i <= n; i++ {
			as = append(as, library.Airing{Number: i, AirsAt: now.Add(-time.Duration(i) * time.Hour)})
		}
		if err := e.lib.SaveSchedule(ctx, id, as, now); err != nil {
			t.Fatal(err)
		}
	}
	two := e.addFixture(t, "tv", "show", tvShow("1", "Two Aired"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	aired(two, 3)
	none := e.addFixture(t, "tv", "show", tvShow("2", "None Aired"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 3}})
	aired(none, 3)
	rep := e.addFixture(t, "tv", "show", tvShow("3", "Rerun"), library.Entry{Status: library.Completed})
	aired(rep, 3)
	if _, err := e.lib.StartRun(ctx, rep, now); err != nil {
		t.Fatal(err)
	}
	body := e.get("/").Body.String()
	mustContain(t, cardOf(t, body, "Two Aired"), "2 aired")
	mustNotContain(t, cardOf(t, body, "None Aired"), "aired")
	mustNotContain(t, cardOf(t, body, "Rerun"), "aired")
}

func TestHomeRewatchMarkerKept(t *testing.T) {
	e := newFixtureEnv(t)
	ctx := context.Background()
	id := completedShow(t, e.env, "1", "Again Show")
	if _, err := e.lib.StartRun(ctx, id, *day(2026, 1, 1)); err != nil {
		t.Fatal(err)
	}
	body := e.get("/").Body.String()
	mustContain(t, cardOf(t, body, "Again Show"), ">Again<")
	mustContain(t, section(t, body, "Up next"), "All active in Shows")
}

func TestListCardsUnchanged(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	if err := e.lib.SaveSchedule(context.Background(), id, []library.Airing{{Number: 2, AirsAt: now.Add(-time.Hour)}, {Number: 3, AirsAt: now.Add(-2 * time.Hour)}}, now); err != nil {
		t.Fatal(err)
	}
	card := cardOf(t, e.get("/tv?view=cards").Body.String(), "Dark")
	mustNotContain(t, card, "aired", "Episode 2", "data-next")
	mustContain(t, cardOf(t, e.get("/").Body.String(), "Dark"), "2 aired", "Episode 2")
}

func TestHomeIncrementSwapKeepsNextAndAired(t *testing.T) {
	e := newFixtureEnv(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	var as []library.Airing
	for i := 1; i <= 4; i++ {
		as = append(as, library.Airing{Number: i, AirsAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	if err := e.lib.SaveSchedule(context.Background(), id, as, now); err != nil {
		t.Fatal(err)
	}
	post := func(ret string) string {
		rec := e.do("POST", fmt.Sprintf("/list/%d/increment", id), url.Values{"return": {ret}, "view": {"cards"}}, true)
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
		return rec.Body.String()
	}
	b := post("/")
	mustContain(t, b, "Episode 3", "2 aired")
	// the list's swapped card stays plain
	b = post("/tv")
	mustNotContain(t, b, "aired", "data-next")
}
