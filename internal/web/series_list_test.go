package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
)

func TestSeriesTabListsSeries(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga") // completed + planned + active: Watching, 1/3
	fe.addFixture(t, "tv", "show", tvShow("solo", "Solo Show"), library.Entry{Status: library.Planned})
	other := fe.addFixture(t, "tv", "show", tvShow("o1", "Other One"), library.Entry{Status: library.Completed})
	if _, err := fe.lib.CreateSeries(bg, other, "Done Set"); err != nil {
		t.Fatal(err)
	}

	rec := fe.get("/tv?tab=series&view=table")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Saga", "1/3", "Watching", "Done Set", "1/1", "All (2)", "Watched (1)", "Watching (1)")
	mustNotContain(t, body, "Solo Show")
	if !strings.Contains(body, `href="/tv/series/`) {
		t.Fatal("no series link")
	}
	mustNotContain(t, body, "+1")

	// the status tab filters by the derived status
	c := fe.get("/tv?tab=series&view=table&status=completed").Body.String()
	mustContain(t, c, "Done Set")
	mustNotContain(t, c, "Saga")
	// q matches the series title
	q := fe.get("/tv?tab=series&view=table&q=saga").Body.String()
	mustContain(t, q, "Saga")
	mustNotContain(t, q, "Done Set")
	// cards link to the series page
	cards := fe.get("/tv?tab=series&view=cards").Body.String()
	mustContain(t, cards, "data-card", `href="/tv/series/`)
}

func TestSeriesTabHiddenWithoutSeries(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.addFixture(t, "tv", "show", tvShow("solo", "Solo Show"), library.Entry{Status: library.Planned})
	body := fe.get("/tv").Body.String()
	mustNotContain(t, body, "tab=series")
	// tab=series without any series renders the normal list
	rec := fe.get("/tv?tab=series&view=table")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Solo Show")
	fe.tvSeries(t, "Saga")
	mustContain(t, fe.get("/tv").Body.String(), "tab=series")
}

func TestSeriesTabExpandFragment(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	rec := fe.get("/tv?tab=series&view=table")
	mustContain(t, rec.Body.String(), `id="list-rows" hx-history="false"`, `hx-get="`+seriesURL("tv", ser.UID)+`/parts"`, `href="`+seriesURL("tv", ser.UID)+`"`)
	if v := rec.Header().Get("Vary"); v != "HX-Request, HX-Target, HX-History-Restore-Request" {
		t.Fatalf("Vary = %q", v)
	}
	part := fe.get(seriesURL("tv", ser.UID) + "/parts")
	if part.Code != http.StatusOK {
		t.Fatalf("code %d", part.Code)
	}
	b := part.Body.String()
	mustNotContain(t, b, "<html")
	i1, i2, i3 := strings.Index(b, "Show 1Saga"), strings.Index(b, "Show 2Saga"), strings.Index(b, "Show 3Saga")
	if i1 < 0 || i2 < i1 || i3 < i2 {
		t.Fatalf("parts out of order: %d %d %d", i1, i2, i3)
	}
	mustContain(t, b, "data-row")
	// the rows fit the Series tab's head: one cell per head column
	head := rec.Body.String()
	heads := strings.Count(head[strings.Index(head, "<thead"):strings.Index(head, "</thead>")], "<th ")
	for _, tr := range strings.Split(b, "<tr")[1:] {
		if n := strings.Count(tr, "<td"); n != heads {
			t.Fatalf("part row has %d cells, head %d: %s", n, heads, tr)
		}
	}
	mustContain(t, b, `href="/tv/`)
	if rec := fe.get(seriesURL("tv", "nope") + "/parts"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	// the rows partial for the Series tab keeps the canonical URL
	hx := fe.getWith("/tv?tab=series&view=table", map[string]string{"HX-Request": "true", "HX-Target": "list-rows"})
	if hx.Code != 200 || strings.Contains(hx.Body.String(), "<html") || hx.Header().Get("HX-Push-Url") == "" {
		t.Fatalf("partial: %d %q", hx.Code, hx.Header().Get("HX-Push-Url"))
	}
}

func TestPartBadgeOnEntryTab(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	fe.addFixture(t, "tv", "show", tvShow("solo", "Solo Show"), library.Entry{Status: library.Planned})
	for _, view := range []string{"table", "cards"} {
		body := fe.get("/tv?view=" + view).Body.String()
		mustContain(t, body, `href="`+seriesURL("tv", ser.UID)+`"`, "2 · Saga")
		if n := strings.Count(body, "· Saga"); n != 3 {
			t.Fatalf("%s: %d badges, want 3", view, n)
		}
	}
}

func TestSeriesTabExpandOffersCollapse(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga")
	body := fe.get("/tv?tab=series&view=table").Body.String()
	mustContain(t, body, `data-series-toggle aria-expanded="false"`, `data-label-open="Hide the parts of Saga"`)
	js, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(js), "[data-series-toggle]", `"aria-expanded"`, "replaceChildren")
}

func TestIncrementKeepsSeriesBadge(t *testing.T) {
	fe := newFixtureEnv(t)
	_, ids := fe.tvSeries(t, "Saga")
	for _, view := range []string{"", "cards"} {
		rec := fe.do("POST", "/list/"+idStr(ids[1])+"/increment", url.Values{"return": {"/tv"}, "view": {view}}, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: code %d", view, rec.Code)
		}
		mustContain(t, rec.Body.String(), "2 · Saga")
	}
}

func TestSeriesTabNamesIgnoredEntryParameters(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga")
	body := fe.get("/tv?tab=series&view=table&genre=x&f.year=2019&sort=updated&rmin=3").Body.String()
	got := strings.Join(noticeKeysOf(body), ",")
	for _, k := range []string{"genre", "f.year", "sort", "rmin"} {
		if !strings.Contains(","+got+",", ","+k+",") {
			t.Errorf("notice %q misses %s", got, k)
		}
	}
	// what the tab does use is not a notice
	if n := noticeOf(fe.get("/tv?tab=series&view=table&sort=title&q=saga&status=planned").Body.String()); n != "" {
		t.Errorf("unexpected notice %q", n)
	}
}

func TestSeriesTabHasItsOwnLabels(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.tvSeries(t, "Saga")
	body := fe.get("/tv?tab=series&view=table").Body.String()
	mustContain(t, body, "<title>TV series - Allister</title>", `<h1 class="text-3xl font-semibold">Series</h1>`)
	mustNotContain(t, body, "Add show")
}
