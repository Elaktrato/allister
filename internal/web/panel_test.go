package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// filterForm returns the list's filter form.
func filterForm(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<form id="list-filter"`)
	if i < 0 {
		t.Fatalf("no filter form in %s", body)
	}
	return body[i : i+strings.Index(body[i:], "</form>")]
}

// panelOpenTag returns the opening tag of the filter panel.
func panelOpenTag(t *testing.T, form string) string {
	t.Helper()
	i := strings.Index(form, `<details id="filter-panel"`)
	if i < 0 {
		t.Fatalf("no filter panel in %s", form)
	}
	return form[i : i+strings.Index(form[i:], ">")+1]
}

// filterCount returns the inner html of the badge slot.
func filterCount(t *testing.T, body string) string {
	t.Helper()
	m := filterCountRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no badge slot in %s", body)
	}
	return m[1]
}

var filterCountRe = regexp.MustCompile(`<span id="filter-count"[^>]*>((?:<span class="[^"]*">\d+<span class="sr-only"> active</span></span>)?)</span>`)

func panelEnv(t *testing.T) *fixtureEnv {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "tv", "show", showWith("1", "Dark", []string{"Drama"}, plugin.Tag{Name: "Loop"}), active)
	e.addFixture(t, "tv", "show", showWith("2", "Light", []string{"Comedy"}), library.Entry{Status: library.Completed})
	return e
}

// TestFilterPanelClosedByDefault: the list shows a search bar (q, with a
// search icon) and a Filters toggle; everything else sits in a native
// <details> panel inside the same GET form, closed unless a filter other
// than q is set.
func TestFilterPanelClosedByDefault(t *testing.T) {
	e := panelEnv(t)
	for _, target := range []string{"/tv", "/tv?q=dar", "/tv?sort=title&dir=desc", "/tv?status=active&q=x"} {
		b := e.listGet(t, target)
		form := filterForm(t, b)
		if tag := panelOpenTag(t, form); strings.Contains(tag, " open") {
			t.Errorf("%s: panel open: %s", target, tag)
		}
		if c := filterCount(t, b); c != "" {
			t.Errorf("%s: badge %q", target, c)
		}
		// the search bar comes before the panel, with an accessible name and a decorative icon
		d := strings.Index(form, `<details id="filter-panel"`)
		top := form[:d]
		mustContain(t, top, `type="search" name="q"`, `<span class="sr-only">Search titles</span>`)
		mustIcon(t, top, "search")
		// the toggle is the panel's summary: icon, text label and badge slot
		sum := form[d : d+strings.Index(form[d:], "</summary>")]
		mustContain(t, sum, "<summary", "<span>Filters</span>", `<span id="filter-count">`)
		mustIcon(t, sum, "filter_list")
		// every other control is inside the panel (submitted with the form without JavaScript)
		panel := form[d:]
		for _, name := range []string{`name="f.year.from"`, `name="genre"`, `name="genre_mode"`, `name="tag"`, `name="progress"`,
			`name="rmin"`, `name="rmax"`, `name="unrated"`, `name="zero"`, `name="partial"`, `type="submit"`, `id="filter-clear"`} {
			if !strings.Contains(panel, name) || strings.Contains(top, name) {
				t.Errorf("%s: %s not (only) in the panel", target, name)
			}
		}
		// the status tabs stay outside the form, above it
		if i := strings.Index(b, `id="list-tabs"`); i < 0 || i > strings.Index(b, `<form id="list-filter"`) {
			t.Errorf("%s: status tabs not above the bar", target)
		}
	}
}

func TestFilterPanelOpensWithFiltersAndCountsThem(t *testing.T) {
	e := panelEnv(t)
	for target, want := range map[string]string{
		"/tv?genre=Drama":                       "1",
		"/tv?f.year=2000..2030&q=d":             "1",
		"/tv?rmin=2&rmax=8":                     "1", // one score range
		"/tv?genre=Drama&genre=Comedy&tag=Loop": "2",
		"/tv?rmin=2&rmax=8&genre=Drama&genre=Comedy&tag=Loop&progress=finished&zero=1&partial=1&unrated=1&sort=title&q=x": "7",
		"/tv?f.year.from=2000&genre_mode=all&genre=Drama":                                                                 "2",
	} {
		b := e.listGet(t, target)
		if tag := panelOpenTag(t, filterForm(t, b)); !strings.HasSuffix(tag, " open>") {
			t.Errorf("%s: panel not open: %s", target, tag)
		}
		if c := filterCount(t, b); !strings.Contains(c, ">"+want+`<span class="sr-only"> active</span>`) {
			t.Errorf("%s: badge %q, want %s", target, c, want)
		}
	}
}

// TestFilterPanelNotInPartial: a live update never swaps the panel (its
// open state stays as the user left it); only the badge goes out of band.
func TestFilterPanelNotInPartial(t *testing.T) {
	e := panelEnv(t)
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	b := e.getWith("/tv?genre=Drama&unrated=1&view=table", hx).Body.String()
	mustNotContain(t, b, "<details", "filter-panel", "<summary", `name="q"`)
	mustContain(t, b, `<span id="filter-count" hx-swap-oob="true">`)
	if c := filterCount(t, b); !strings.Contains(c, `>2<span class="sr-only"> active</span>`) {
		t.Errorf("badge %q", c)
	}
	mustContain(t, e.getWith("/tv?q=d&view=table", hx).Body.String(), `<span id="filter-count" hx-swap-oob="true"></span>`)
}

// TestListAddEntryPoint: the list header links the source search (with an
// add icon and a text label) and, when the plugin has it, the manual form.
func TestListAddEntryPoint(t *testing.T) {
	e := panelEnv(t)
	b := e.listGet(t, "/tv?view=table&view=table")
	i := strings.Index(b, `href="/tv/search"`)
	if i < 0 {
		t.Fatal("no add link")
	}
	a := b[strings.LastIndex(b[:i], "<a "):]
	a = a[:strings.Index(a, "</a>")]
	mustIcon(t, a, "add")
	mustContain(t, a, "<span>Add show</span>")
	an := newEnv(t)
	an.addEntry(t, "anilist", "1", nil, library.Active, 0)
	mustContain(t, an.get("/anime?view=table").Body.String(), `href="/anime/search"`, "<span>Add anime</span>", `href="/anime/manual/new"`)
}
