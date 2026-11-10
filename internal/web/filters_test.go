package web

import (
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// rowTitles returns the titles of the list rows in body, in page order.
func rowTitles(body string) []string {
	const open = `class="font-medium text-indigo-300 hover:underline">`
	var out []string
	for _, part := range strings.Split(body, "<tr data-row")[1:] {
		i := strings.Index(part, open)
		if i < 0 {
			continue
		}
		rest := part[i+len(open):]
		if j := strings.Index(rest, "</a>"); j >= 0 {
			out = append(out, html.UnescapeString(rest[:j]))
		}
	}
	return out
}

// wantRows fails unless the list rows of body are exactly want (any order).
func wantRows(t *testing.T, what, body string, want ...string) {
	t.Helper()
	got := rowTitles(body)
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("%s: rows %q, want %q", what, got, want)
	}
}

// wantOrder fails unless the list rows of body are exactly want, in order.
func wantOrder(t *testing.T, what, body string, want ...string) {
	t.Helper()
	if got := rowTitles(body); !slices.Equal(got, want) {
		t.Errorf("%s: rows %q, want %q", what, got, want)
	}
}

// getWith is a GET with extra request headers.
func (e *env) getWith(target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// listGet is a plain GET that must answer 200.
func (e *env) listGet(t *testing.T, target string) string {
	t.Helper()
	rec := e.get(target)
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d", target, rec.Code)
	}
	return rec.Body.String()
}

// noticeOf returns the rejected-parameters notice of body ("" without one).
func noticeOf(body string) string {
	i := strings.Index(body, `data-filter-notice`)
	if i < 0 {
		return ""
	}
	end := strings.Index(body[i:], "</p>")
	if end < 0 {
		return body[i:]
	}
	return body[i : i+end]
}

// noticeKeysOf returns the keys the notice of body names, in order.
func noticeKeysOf(body string) []string {
	var out []string
	n := noticeOf(body)
	for {
		i := strings.Index(n, "<code>")
		if i < 0 {
			return out
		}
		n = n[i+len("<code>"):]
		j := strings.Index(n, "</code>")
		if j < 0 {
			return out
		}
		out = append(out, html.UnescapeString(n[:j]))
		n = n[j:]
	}
}

// animeData is valid data of the real anime plugin.
func animeData(id, title, typ string, year float64, studio string) plugin.ItemData {
	f := map[string]plugin.FieldValue{"type": {Text: typ}}
	if year > 0 {
		f["year"] = plugin.FieldValue{Num: num(year)}
	}
	if studio != "" {
		f["studio"] = plugin.FieldValue{Text: studio}
	}
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "anilist", ID: id, Kind: "anime", Titles: ttl(title), Fields: f},
		ExternalURL: "https://anilist.co/anime/" + id,
	}
}

func showWith(id, title string, genres []string, tags ...plugin.Tag) plugin.ItemData {
	d := tvShow(id, title)
	d.Genres, d.Tags = genres, tags
	return d
}

func TestFilterByTypeYearRangeStudio(t *testing.T) {
	e := newEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "anime", "anime", animeData("1", "Alpha", "TV", 2001, "Madhouse"), active)
	e.addFixture(t, "anime", "anime", animeData("2", "Beta", "MOVIE", 2005, "Bones"), active)
	e.addFixture(t, "anime", "anime", animeData("3", "Gamma", "TV", 2015, "MADHOUSE Tokyo"), active)
	e.addFixture(t, "anime", "anime", animeData("4", "Delta", "OVA", 0, ""), active)
	for q, want := range map[string][]string{
		"":                                 {"Alpha", "Beta", "Gamma", "Delta"},
		"f.type=TV":                        {"Alpha", "Gamma"},
		"f.type=OVA":                       {"Delta"},
		"f.year=2000..2010":                {"Alpha", "Beta"},
		"f.year=2010..":                    {"Gamma"},
		"f.year=..2001":                    {"Alpha"},
		"f.year=2005":                      {"Beta"},
		"f.year.from=2000&f.year.to=2010":  {"Alpha", "Beta"},
		"f.year.from=2002&f.year.to=":      {"Beta", "Gamma"},
		"f.studio=madhouse":                {"Alpha", "Gamma"},
		"f.studio=Bones":                   {"Beta"},
		"f.type=TV&f.year=2000..2010":      {"Alpha"},
		"f.type=TV&f.studio=madhouse+tok":  {"Gamma"},
		"f.type=&f.year=&f.studio=&q=":     {"Alpha", "Beta", "Gamma", "Delta"}, // an empty form
		"f.type=TV&f.type=MOVIE":           {"Alpha", "Beta", "Gamma"},
		"f.type=TV&f.year=1990..1995":      {},
		"f.year.from=2000&f.year.to=2010&": {"Alpha", "Beta"},
	} {
		b := e.listGet(t, "/anime?"+q)
		wantRows(t, q, b, want...)
		if n := noticeOf(b); n != "" {
			t.Errorf("%s: unexpected notice %q", q, n)
		}
	}
	b := e.listGet(t, "/anime?f.type=TV&f.year=2000..2010&f.studio=Madhouse")
	mustContain(t, b, "Showing 1–1 of 1")
	// malformed values are ignored with a notice naming exactly that parameter
	for q, key := range map[string]string{
		"f.type=Bogus":                 "f.type",
		"f.year=abc":                   "f.year",
		"f.year=2010..2000":            "f.year",
		"f.year=..":                    "f.year",
		"f.year.from=x":                "f.year",
		"f.year=..&f.year.from=":       "f.year",
		"f.studio=Madhouse&f.type=Bad": "f.type",
	} {
		b := e.listGet(t, "/anime?"+q)
		want := []string{"Alpha", "Beta", "Gamma", "Delta"}
		if strings.Contains(q, "studio") {
			want = []string{"Alpha", "Gamma"}
		}
		wantRows(t, q, b, want...)
		if got := noticeKeysOf(b); !slices.Equal(got, []string{key}) {
			t.Errorf("%s: notice keys %q, want [%s]", q, got, key)
		}
	}
	// the canonical value wins over the form aliases, which are named
	b = e.listGet(t, "/anime?f.year=2010..&f.year.from=1990&f.year.to=2000")
	wantRows(t, "alias dropped", b, "Gamma")
	if got := noticeKeysOf(b); !slices.Equal(got, []string{"f.year.from", "f.year.to"}) {
		t.Errorf("alias notice keys %q", got)
	}
	// the form shows the applied values again
	b = e.listGet(t, "/anime?f.type=TV&f.year=2000..2010&f.studio=Madhouse")
	mustContain(t, b, `<option value="TV" selected>`, `name="f.year.from" value="2000"`, `name="f.year.to" value="2010"`,
		`name="f.studio" value="Madhouse"`)
}

func TestFilterByGenresAnyAndAll(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "tv", "show", showWith("1", "Both", []string{"Drama", "Crime"}), active)
	e.addFixture(t, "tv", "show", showWith("2", "Only Drama", []string{"Drama"}), active)
	e.addFixture(t, "tv", "show", showWith("3", "Comedy", []string{"Comedy"}), active)
	for q, want := range map[string][]string{
		"genre=Drama":                                {"Both", "Only Drama"},
		"genre=drama":                                {"Both", "Only Drama"},
		"genre=DRAMA&genre=crime":                    {"Both", "Only Drama"},
		"genre=Drama&genre=Crime&genre_mode=any":     {"Both", "Only Drama"},
		"genre=Drama&genre=Crime&genre_mode=all":     {"Both"},
		"genre=Drama&genre=Comedy&genre_mode=all":    {},
		"genre=Comedy&genre=Crime":                   {"Both", "Comedy"},
		"genre=&genre_mode=any":                      {"Both", "Only Drama", "Comedy"},
		"genre=Drama&genre=Crime&genre_mode=all&q=b": {"Both"},
	} {
		b := e.listGet(t, "/tv?"+q)
		wantRows(t, q, b, want...)
		if n := noticeOf(b); n != "" {
			t.Errorf("%s: unexpected notice %q", q, n)
		}
	}
	b := e.listGet(t, "/tv?genre=Drama&genre=Crime&genre_mode=bogus")
	wantRows(t, "bad mode", b, "Both", "Only Drama")
	mustContain(t, noticeOf(b), "genre_mode")
	// the bar lists the genres with counts and the mode switch
	b = e.listGet(t, "/tv?genre=Crime&genre_mode=all")
	mustContain(t, b, `name="genre"`, `<option value="Drama">Drama (2)</option>`, `<option value="Crime" selected>Crime (1)</option>`,
		`<option value="Comedy">Comedy (1)</option>`, `name="genre_mode"`, `<option value="all" selected>`)
}

func TestFilterByTagsWithCounts(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "tv", "show", showWith("1", "Loop One", nil, plugin.Tag{Name: "Time Travel"}, plugin.Tag{Name: "Twist", Spoiler: true}), active)
	e.addFixture(t, "tv", "show", showWith("2", "Loop Two", nil, plugin.Tag{Name: "Time Travel"}), active)
	e.addFixture(t, "tv", "show", showWith("3", "Calm", nil, plugin.Tag{Name: "Slow"}), library.Entry{Status: library.Completed})
	b := e.listGet(t, "/tv")
	mustContain(t, b, `name="tag"`, `<option value="Time Travel">Time Travel (2)</option>`, `<option value="Twist">Twist (1)</option>`,
		`<option value="Slow">Slow (1)</option>`, `name="tag_mode"`)
	wantRows(t, "tag", e.listGet(t, "/tv?tag=time+travel"), "Loop One", "Loop Two")
	wantRows(t, "spoiler tag", e.listGet(t, "/tv?tag=Twist"), "Loop One")
	wantRows(t, "all", e.listGet(t, "/tv?tag=Twist&tag=Time+Travel&tag_mode=all"), "Loop One")
	wantRows(t, "any", e.listGet(t, "/tv?tag=Twist&tag=Slow"), "Loop One", "Calm")
	// counts follow the other filters (text, status), not the tag filter itself
	b = e.listGet(t, "/tv?q=two&tag=Slow")
	wantRows(t, "q+tag", b)
	mustContain(t, b, `<option value="Time Travel">Time Travel (1)</option>`, `<option value="Slow" selected>Slow (0)</option>`)
	mustNotContain(t, b, `Twist (`)
	b = e.listGet(t, "/tv?status=completed")
	mustContain(t, b, `Slow (1)`)
	mustNotContain(t, b, `Time Travel (`)
}

func TestGenreTagFilterHostileValuesIgnoredOrBound(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "tv", "show", showWith("1", "Drama Show", []string{"Drama"}, plugin.Tag{Name: "Time Travel"}), active)
	e.addFixture(t, "tv", "show", showWith("2", "Other Show", []string{"Comedy"}), active)
	long := strings.Repeat("x", 10000)
	for _, v := range []string{"%", "_", "%%", "Dr_ma", "Dr%", "' OR 1=1 --", `"); DROP TABLE items; --`, `\`, long, "\x00", "Drama\x00"} {
		for _, key := range []string{"genre", "tag"} {
			target := "/tv?" + url.Values{key: {v}}.Encode()
			rec := e.get(target)
			if rec.Code != 200 {
				t.Fatalf("%s: %d", target, rec.Code)
			}
			wantRows(t, fmt.Sprintf("%s=%.20q", key, v), rec.Body.String())
		}
	}
	// hostile values come back escaped as selected options
	b := e.listGet(t, "/tv?"+url.Values{"genre": {`<script>alert(1)</script>`}}.Encode())
	mustContain(t, b, "&lt;script&gt;alert(1)&lt;/script&gt;")
	mustNotContain(t, b, "<script>alert(1)")
	// nothing was harmed
	wantRows(t, "after", e.listGet(t, "/tv"), "Drama Show", "Other Show")
	// at most 50 values per term type: the rest is ignored with a notice
	vals := url.Values{}
	for i := 0; i < 60; i++ {
		vals.Add("genre", fmt.Sprintf("g%d", i))
	}
	vals.Add("genre", "Drama")
	b = e.listGet(t, "/tv?"+vals.Encode())
	mustContain(t, noticeOf(b), "genre")
	// a kind without a tag filter ignores tags with a notice
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), active)
	b = e.listGet(t, "/book?tag=Dragons")
	wantRows(t, "book tag", b, "Earthsea")
	mustContain(t, noticeOf(b), "tag")
}

func TestFilterByStatusTabAndText(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("2", "Darker"), library.Entry{Status: library.Completed})
	e.addFixture(t, "tv", "show", tvShow("3", "Light"), library.Entry{Status: library.Active})
	b := e.listGet(t, "/tv?status=active&q=dark")
	wantRows(t, "active+dark", b, "Dark")
	main := b[strings.Index(b, "</header>"):]
	// tabs count with the text filter and keep it
	mustContain(t, main, "All (2)", "Watching (1)", "Watched (1)", `href="/tv?q=dark&amp;status=all&amp;view=cards"`, `href="/tv?q=dark&amp;status=completed&amp;view=cards"`,
		`name="status" value="active"`, `name="q" value="dark"`)
	wantRows(t, "all dark", e.listGet(t, "/tv?q=DARK"), "Dark", "Darker")
	wantRows(t, "completed dark", e.listGet(t, "/tv?status=completed&q=dar"), "Darker")
	// a filter that matches nothing says so (not "your list is empty")
	b = e.listGet(t, "/tv?q=zzz")
	wantRows(t, "none", b)
	mustContain(t, b, "No shows match these filters")
	mustNotContain(t, b, "Your list is empty")
}

func TestSearchMatchesAnyLanguageTitle(t *testing.T) {
	e := newFixtureEnv(t)
	d := tvShow("1", "Dark")
	d.Titles = append(d.Titles, plugin.Title{Lang: "original", Text: "Dunkel"})
	e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active})
	j := tvShow("2", "Deep Sea")
	j.Titles = append(j.Titles, plugin.Title{Lang: "original", Text: "ダークシー"})
	e.addFixture(t, "tv", "show", j, library.Entry{Status: library.Active})
	for q, want := range map[string][]string{
		"Dunkel": {"Dark"},
		"dunk":   {"Dark"},
		"DUNKEL": {"Dark"},
		"dark":   {"Dark"},
		"ダーク":    {"Deep Sea"},
		"sea":    {"Deep Sea"},
		"d":      {"Dark", "Deep Sea"},
		"nope":   {},
	} {
		wantRows(t, q, e.listGet(t, "/tv?"+url.Values{"q": {q}}.Encode()), want...)
	}
}

func TestSearchEscapesWildcardsAndHostileInput(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	for i, title := range []string{"100% Pure", "snake_case", "Plain Title", `O'Brien "Q"`, `back\slash`} {
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), title), active)
	}
	all := []string{"100% Pure", "snake_case", "Plain Title", `O'Brien "Q"`, `back\slash`}
	for q, want := range map[string][]string{
		"%":                        {"100% Pure"},
		"_":                        {"snake_case"},
		"0%":                       {"100% Pure"},
		"e_c":                      {"snake_case"},
		"'":                        {`O'Brien "Q"`},
		`"Q"`:                      {`O'Brien "Q"`},
		`\`:                        {`back\slash`},
		`\s`:                       {`back\slash`},
		"' OR '1'='1":              {},
		"%' OR 1=1 --":             {},
		"x'); DROP TABLE items;":   {},
		strings.Repeat("a", 10000): {},
		"   ":                      all,
	} {
		rec := e.get("/tv?" + url.Values{"q": {q}}.Encode())
		if rec.Code != 200 {
			t.Fatalf("%.20q: %d", q, rec.Code)
		}
		wantRows(t, fmt.Sprintf("q=%.20q", q), rec.Body.String(), want...)
	}
	// q is capped at 200 runes (also in the form and links)
	b := e.listGet(t, "/tv?"+url.Values{"q": {strings.Repeat("é", 300)}}.Encode())
	mustContain(t, b, `value="`+strings.Repeat("é", 200)+`"`)
	mustNotContain(t, b, strings.Repeat("é", 201))
	// SQL fragments in keys and sort names are rejected, never used
	for _, q := range []string{
		"f.year');DROP%20TABLE%20items;--=1",
		"f.type=series", // not filterable in this kind
		"f.episodes=1..3",
		"sort=title;DROP%20TABLE%20items",
		"sort=year)--&dir=asc",
		"f.year%27%20OR%201=1=2000",
	} {
		b := e.listGet(t, "/tv?"+q)
		wantRows(t, q, b, all...)
		if noticeOf(b) == "" {
			t.Errorf("%s: no notice", q)
		}
	}
	wantRows(t, "intact", e.listGet(t, "/tv"), all...)
}

func TestRatingFiltersUnratedAndZeroSeparately(t *testing.T) {
	e := newFixtureEnv(t)
	for i, r := range []*int{ip(0), ip(5), ip(9), nil} {
		title := "Unrated"
		if r != nil {
			title = fmt.Sprintf("Rated %d", *r)
		}
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), title), library.Entry{Status: library.Completed, Rating: r})
	}
	all := []string{"Rated 0", "Rated 5", "Rated 9", "Unrated"}
	for q, want := range map[string][]string{
		"unrated=1":           {"Unrated"},
		"zero=1":              {"Rated 0"},
		"unrated=1&zero=1":    {"Unrated", "Rated 0"},
		"rmin=5":              {"Rated 5", "Rated 9"},
		"rmax=5":              {"Rated 0", "Rated 5"},
		"rmin=1&rmax=10":      {"Rated 5", "Rated 9"},
		"rmin=0&rmax=0":       {"Rated 0"},
		"rmin=6&unrated=1":    {"Rated 9", "Unrated"},
		"rmin=&rmax=&zero=":   all,
		"rmin=10":             {},
		"rmin=9&rmax=9&zero=": {"Rated 9"},
	} {
		b := e.listGet(t, "/tv?"+q)
		wantRows(t, q, b, want...)
		if n := noticeOf(b); n != "" {
			t.Errorf("%s: unexpected notice %q", q, n)
		}
	}
	for _, q := range []string{"rmin=11", "rmax=-1", "rmin=abc", "rmin=2.5", "rmin=7&rmax=3", "unrated=yes", "zero=2"} {
		b := e.listGet(t, "/tv?"+q)
		wantRows(t, q, b, all...)
		if noticeOf(b) == "" {
			t.Errorf("%s: no notice", q)
		}
	}
	b := e.listGet(t, "/tv?unrated=1&rmin=3")
	mustContain(t, b, `name="unrated" value="1" checked`, `name="rmin" value="3"`)
	mustNotContain(t, b, `name="zero" value="1" checked`)
}

func TestFlagsUnmatchedAndPartial(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	manual := bookItem("m1", "Manual With ISBN")
	manual.Source, manual.ExternalURL = "manual", ""
	manual.ExternalIDs = map[string]string{"isbn": "9780000000001"}
	e.addFixture(t, "book", "book", manual, active, library.WithSyncState("done"))
	plain := bookItem("m2", "Manual Plain")
	plain.Source, plain.ExternalURL = "manual", ""
	e.addFixture(t, "book", "book", plain, active, library.WithSyncState("done"))
	e.addFixture(t, "book", "book", bookItem("1", "Pending Sync"), active)
	e.addFixture(t, "book", "book", bookItem("2", "Synced"), active, library.WithSyncState("done"))
	b := e.listGet(t, "/book")
	mustContain(t, b, `name="unmatched" value="1"`, `name="partial" value="1"`)
	wantRows(t, "unmatched", e.listGet(t, "/book?unmatched=1"), "Manual With ISBN")
	wantRows(t, "partial", e.listGet(t, "/book?partial=1"), "Pending Sync")
	wantRows(t, "both", e.listGet(t, "/book?partial=1&unmatched=1"))
	b = e.listGet(t, "/book?unmatched=1")
	mustContain(t, b, `name="unmatched" value="1" checked`)
	for _, q := range []string{"unmatched=true", "partial=0"} {
		b := e.listGet(t, "/book?"+q)
		wantRows(t, q, b, "Manual With ISBN", "Manual Plain", "Pending Sync", "Synced")
		if noticeOf(b) == "" {
			t.Errorf("%s: no notice", q)
		}
	}
}

// The list filters by the state of the active progress tracks (the
// library's ProgressState): not started, in progress, finished.
func TestFilterByProgressState(t *testing.T) {
	e := newFixtureEnv(t)
	book := func(id, title string, pages float64) plugin.ItemData {
		d := bookItem(id, title)
		d.Fields["pages"] = plugin.FieldValue{Num: num(pages)}
		return d
	}
	e.addFixture(t, "book", "book", book("1", "Unread", 100), library.Entry{Status: library.Planned})
	e.addFixture(t, "book", "book", book("2", "Halfway", 100), library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 50}})
	e.addFixture(t, "book", "book", book("3", "Done", 100), library.Entry{Status: library.Completed, Progress: map[string]float64{"pages": 100}})
	e.addFixture(t, "book", "book", book("4", "Percent Done", 100), library.Entry{Status: library.Completed, Progress: map[string]float64{"percent": 100}},
		library.WithProgressKeys([]string{"percent"}))
	b := e.listGet(t, "/book")
	mustContain(t, b, `<select name="progress"`, `<option value="" selected>Any</option>`, `<option value="not_started">Not started</option>`,
		`<option value="in_progress">In progress</option>`, `<option value="finished">Finished</option>`)
	wantRows(t, "not started", e.listGet(t, "/book?progress=not_started"), "Unread")
	wantRows(t, "in progress", e.listGet(t, "/book?progress=in_progress"), "Halfway")
	b = e.listGet(t, "/book?progress=finished")
	wantRows(t, "finished", b, "Done", "Percent Done")
	mustContain(t, b, `<option value="finished" selected>Finished</option>`, "Clear filters")
	// the state combines with the other filters
	wantRows(t, "finished, text", e.listGet(t, "/book?progress=finished&q=percent"), "Percent Done")
	for _, q := range []string{"progress=any", "progress=FINISHED", "progress=finished&progress=not_started"} {
		b := e.listGet(t, "/book?"+q)
		wantRows(t, q, b, "Unread", "Halfway", "Done", "Percent Done")
		if !strings.Contains(noticeOf(b), "progress") {
			t.Errorf("%s: notice %q", q, noticeOf(b))
		}
	}
}

func TestSortOptionsAndDirection(t *testing.T) {
	e := newFixtureEnv(t)
	add := func(id, title string, year float64, rating *int, ep float64) {
		d := tvShow(id, title)
		d.Fields["year"] = plugin.FieldValue{Num: num(year)}
		e.addFixture(t, "tv", "show", d, library.Entry{Status: library.Active, Rating: rating, Progress: map[string]float64{"episode": ep}})
	}
	add("1", "Bravo", 2010, ip(7), 3)
	add("2", "alpha", 2020, nil, 1)
	add("3", "Charlie", 2000, ip(9), 8)
	for q, want := range map[string][]string{
		"sort=title":                {"alpha", "Bravo", "Charlie"},
		"sort=title&dir=asc":        {"alpha", "Bravo", "Charlie"},
		"sort=title&dir=desc":       {"Charlie", "Bravo", "alpha"},
		"sort=year":                 {"alpha", "Bravo", "Charlie"},
		"sort=year&dir=asc":         {"Charlie", "Bravo", "alpha"},
		"sort=rating":               {"Charlie", "Bravo", "alpha"},
		"sort=rating&dir=asc":       {"Bravo", "Charlie", "alpha"},
		"sort=progress":             {"Charlie", "Bravo", "alpha"},
		"sort=progress&dir=asc":     {"alpha", "Bravo", "Charlie"},
		"sort=updated":              {"Charlie", "alpha", "Bravo"},
		"sort=&dir=":                {"Charlie", "alpha", "Bravo"},
		"sort=title&dir=desc&q=a":   {"Charlie", "Bravo", "alpha"},
		"sort=year&genre=&dir=desc": {"alpha", "Bravo", "Charlie"},
	} {
		b := e.listGet(t, "/tv?"+q)
		wantOrder(t, q, b, want...)
		if n := noticeOf(b); n != "" {
			t.Errorf("%s: unexpected notice %q", q, n)
		}
	}
	for _, q := range []string{"sort=bogus", "sort=type", "sort=episodes", "sort=title&dir=sideways"} {
		b := e.listGet(t, "/tv?"+q)
		if len(rowTitles(b)) != 3 || noticeOf(b) == "" {
			t.Errorf("%s: rows %q notice %q", q, rowTitles(b), noticeOf(b))
		}
	}
	// the choices are the table head's sort links (sort_test.go has the
	// details), and the panel's compact sort control has them all selected
	b := e.listGet(t, "/tv?sort=year&dir=asc")
	mustContain(t, b, `<option value="year" selected>Year</option>`, `<option value="asc" selected>Ascending</option>`,
		`id="sort-updated"`, `id="sort-title"`, `id="sort-rating"`, `id="sort-progress"`, `id="sort-year"`)
	mustContain(t, headCell(t, thead(t, b), "Year"), `aria-sort="ascending"`)
	// the book fixture has no sortable field
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Active})
	bk := e.listGet(t, "/book")
	mustContain(t, bk, `id="sort-title"`, `id="sort-progress"`)
	mustNotContain(t, bk, `id="sort-year"`, `id="sort-author"`)
}

func TestFiltersSurvivePaginationLinks(t *testing.T) {
	e := newFixtureEnv(t)
	for i := 0; i < 101; i++ {
		e.addFixture(t, "tv", "show", showWith(fmt.Sprint(i+1), fmt.Sprintf("Show %03d", i+1), []string{"Drama"}), library.Entry{Status: library.Active})
	}
	e.addFixture(t, "tv", "show", showWith("200", "Show Extra", []string{"Comedy"}), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", showWith("201", "Elsewhere", []string{"Drama"}), library.Entry{Status: library.Active})
	const filters = "genre=drama&q=Show&sort=title&status=active&view=cards"
	b := e.listGet(t, "/tv?q=Show&genre=drama&sort=title&status=active")
	if n := len(rowTitles(b)); n != 100 {
		t.Fatalf("page 1 rows %d", n)
	}
	mustContain(t, b, "Showing 1–100 of 101", `href="/tv?genre=drama&amp;page=2&amp;q=Show&amp;sort=title&amp;status=active&amp;view=cards"`)
	// the tabs keep the filters (and start at page 1)
	mustContain(t, b, `href="/tv?genre=drama&amp;q=Show&amp;sort=title&amp;status=all&amp;view=cards"`, `href="/tv?genre=drama&amp;q=Show&amp;sort=title&amp;status=completed&amp;view=cards"`)
	// the row forms return to the filtered page
	ret := "/tv?" + filters
	mustContain(t, b, `name="return" value="`+html.EscapeString(ret)+`"`, "return="+url.QueryEscape(ret))
	p2 := e.listGet(t, "/tv?genre=drama&page=2&q=Show&sort=title&status=active")
	wantOrder(t, "page 2", p2, "Show 101")
	mustContain(t, p2, "Showing 101–101 of 101", `href="/tv?`+strings.ReplaceAll(filters, "&", "&amp;")+`"`, "Previous")
	mustNotContain(t, p2, ">Next<")
	// range filters keep their canonical form; the form aliases are folded into it
	p := e.listGet(t, "/tv?f.year.from=2000&f.year.to=2030&q=Show")
	mustContain(t, p, `href="/tv?f.year=2000..2030&amp;page=2&amp;q=Show&amp;view=cards"`)
}

func TestFilteredReturnSurvivesEdit(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Über"), library.Entry{Status: library.Active})
	ret := "/tv?q=%C3%9Cber&status=active"
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {ret}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != ret {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
	// the no-JS edit dialog opens over the filtered list it came from
	b := e.do("GET", "/list/"+idStr(id)+"/edit?"+url.Values{"return": {ret}}.Encode(), nil, false).Body.String()
	mustContain(t, b, `name="q" value="Über"`)
}

func TestPartialResponseHasOnlyRows(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", showWith("1", "Dark", []string{"Drama"}), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", showWith("2", "Light", []string{"Comedy"}), library.Entry{Status: library.Completed})
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	rec := e.getWith("/tv?q=dar&f.year.from=&f.year.to=&genre_mode=any&sort=updated&dir=", hx)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Vary"), "HX-Request") {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Vary"))
	}
	b := rec.Body.String()
	wantRows(t, "partial", b, "Dark")
	mustContain(t, b, "Showing 1–1 of 1", `id="list-tabs"`, `hx-swap-oob="true"`, "All (1)", `href="/tv?q=dar&amp;status=completed&amp;view=cards"`)
	mustNotContain(t, b, "<html", "<header", `id="list-rows"`, `method="get"`, `id="dialog-slot"`)
	// the address bar gets the canonical URL
	if got := rec.Header().Get("HX-Push-Url"); got != "/tv?q=dar&view=cards" {
		t.Errorf("HX-Push-Url %q", got)
	}
	// a history restore (cache miss) and other htmx requests get the full page
	for _, h := range []map[string]string{
		{"HX-Request": "true", "HX-Target": "list-rows", "HX-History-Restore-Request": "true"},
		{"HX-Request": "true"},
		{},
	} {
		rec := e.getWith("/tv?q=dar", h)
		b := rec.Body.String()
		mustContain(t, b, "<html", `id="list-rows"`, `id="dialog-slot"`, `<form id="list-filter" method="get" action="/tv"`)
		wantRows(t, fmt.Sprint(h), b, "Dark")
		if !strings.Contains(rec.Header().Get("Vary"), "HX-Request") || rec.Header().Get("HX-Push-Url") != "" {
			t.Errorf("%v: headers %v", h, rec.Header())
		}
		if strings.Contains(b, `hx-swap-oob`) {
			t.Errorf("%v: oob in full page", h)
		}
	}
}

func TestUnknownFilterKeyIgnoredWithNotice(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	b := e.listGet(t, "/tv?bogus=1&f.nope=2&q=dark")
	wantRows(t, "unknown", b, "Dark")
	n := noticeOf(b)
	mustContain(t, n, "bogus", "f.nope")
	mustNotContain(t, n, ">q<", "dark")
	// hostile keys are escaped
	b = e.listGet(t, "/tv?"+url.Values{"<script>x</script>": {"1"}}.Encode())
	mustContain(t, noticeOf(b), "&lt;script&gt;")
	mustNotContain(t, b, "<script>x")
	// many unknown keys: a bounded notice
	vals := url.Values{}
	for i := 0; i < 100; i++ {
		vals.Set(fmt.Sprintf("k%03d", i), "1")
	}
	b = e.listGet(t, "/tv?"+vals.Encode())
	n = noticeOf(b)
	mustContain(t, n, "k000", "more")
	mustNotContain(t, n, "k099")
	// the canonical links never carry rejected keys
	mustNotContain(t, e.listGet(t, "/tv?bogus=1&status=active"), "bogus=")
	// no notice without rejected keys
	if n := noticeOf(e.listGet(t, "/tv?q=&status=active&page=1")); n != "" {
		t.Errorf("notice %q", n)
	}
}

func TestFilterBarOnlyShowsFilterableFields(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "book", "book", bookItem("1", "Earthsea"), active)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), active)
	e.addFixture(t, "anime", "anime", animeData("1", "Alpha", "TV", 2001, "Madhouse"), active)
	form := func(b string) string {
		i := strings.Index(b, `<form id="list-filter" method="get"`)
		if i < 0 {
			t.Fatal("no filter form")
		}
		return b[i : i+strings.Index(b[i:], "</form>")]
	}
	bk := form(e.listGet(t, "/book"))
	mustContain(t, bk, `action="/book"`, `name="q"`, `name="f.author"`, ">Author<", `name="genre"`, `name="genre_mode"`,
		`hx-trigger="keyup changed delay:300ms, change"`, `hx-push-url="true"`, `hx-target="#list-rows"`, `hx-get="/book"`,
		`type="submit"`, `name="rmin"`, `name="rmax"`, `name="unrated"`, `name="zero"`, `name="sort"`, `name="dir"`)
	mustNotContain(t, bk, `name="f.pages`, `name="f.isbn`, `name="tag"`, `name="f.year`, "anime", "episode")
	tv := form(e.listGet(t, "/tv"))
	mustContain(t, tv, `name="f.year.from"`, `name="f.year.to"`, `type="number"`, ">Year<", `name="genre"`, `name="tag"`, `name="tag_mode"`)
	mustNotContain(t, tv, `name="f.type"`, `name="f.episodes`, `name="f.author"`)
	a := form(e.listGet(t, "/anime"))
	mustContain(t, a, `name="f.type"`, `<option value="TV_SHORT">TV_SHORT</option>`, `name="f.year.from"`, `name="f.studio"`, `name="genre"`, `name="tag"`)
	mustNotContain(t, a, `name="f.episodes`, `name="f.synopsis`, `name="f.started_on`, `name="f.author"`)
	// a multi-kind plugin keeps the kind in the form; a single-kind one does not
	mustNotContain(t, tv, `name="kind"`)
	e2 := newFixtureEnv(t, withComicKind)
	cd := bookItem("2", "The Comic")
	cd.Kind = "comic"
	e2.addFixture(t, "book", "comic", cd, active)
	mustContain(t, form(e2.listGet(t, "/book?kind=comic&status=active")), `name="kind" value="comic"`, `name="status" value="active"`)
	// an empty list has no filter bar
	e3 := newFixtureEnv(t)
	mustNotContain(t, e3.listGet(t, "/tv"), `id="list-filter"`, `method="get"`)
}

func TestClearFiltersLink(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	cd := bookItem("2", "The Comic")
	cd.Kind = "comic"
	e.addFixture(t, "book", "comic", cd, library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", showWith("1", "Dark", []string{"Drama"}), library.Entry{Status: library.Active})
	b := e.listGet(t, "/tv?q=x&status=active&genre=drama&sort=title")
	mustContain(t, b, `href="/tv?status=active&amp;view=cards">Clear filters</a>`)
	mustNotContain(t, e.listGet(t, "/tv"), "Clear filters")
	mustNotContain(t, e.listGet(t, "/tv?status=active"), "Clear filters")
	mustContain(t, e.listGet(t, "/book?kind=comic&q=zzz"), `href="/book?kind=comic&amp;view=cards">Clear filters</a>`)
}

// TestLibraryRefusalFallsBackWithNotice: should the library still refuse a
// checked query (here more field filters than it accepts at once), the list
// is shown without filters and says so instead of failing.
func TestLibraryRefusalFallsBackWithNotice(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		for i := 0; i < 21; i++ {
			k.Fields = append(k.Fields, plugin.FieldDef{Key: fmt.Sprintf("n%02d", i), Label: fmt.Sprintf("N%02d", i), Type: plugin.FieldNumber, Editable: true, Filterable: true})
		}
	})
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	vals := url.Values{"q": {"zzz"}}
	for i := 0; i < 21; i++ {
		vals.Set(fmt.Sprintf("f.n%02d", i), "1..")
	}
	b := e.listGet(t, "/tv?"+vals.Encode())
	wantRows(t, "fallback", b, "Dark")
	mustContain(t, noticeOf(b), "filters")
	mustNotContain(t, b, "Clear filters", `value="zzz"`)
}

// TestListHistoryRestoreFetchesFullPage: htmx must never restore a list from
// its snapshot (it keeps attributes, not typed values, so the bar would not
// match the rows): hx-history="false" makes it skip saving the snapshot and
// fetch the page with HX-History-Restore-Request, which gets the full page.
func TestListHistoryRestoreFetchesFullPage(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	for _, target := range []string{"/tv", "/tv?q=dar", "/tv?q=zzz"} {
		mustContain(t, e.listGet(t, target), `id="list-rows" hx-history="false"`)
	}
	// the empty list too (no live filtering, but the attribute costs nothing)
	mustContain(t, newFixtureEnv(t).listGet(t, "/tv"), `hx-history="false"`)
	rec := e.getWith("/tv?q=dar", map[string]string{"HX-Request": "true", "HX-Target": "list-rows", "HX-History-Restore-Request": "true"})
	b := rec.Body.String()
	mustContain(t, b, "<html", `name="q" value="dar"`, `hx-history="false"`)
	wantRows(t, "restore", b, "Dark")
}

func TestListVaryCoversAllRequestHeaders(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	for _, h := range []map[string]string{{}, {"HX-Request": "true", "HX-Target": "list-rows"}} {
		got := e.getWith("/tv", h).Header().Values("Vary")
		if !slices.Equal(got, []string{"HX-Request, HX-Target, HX-History-Restore-Request"}) {
			t.Errorf("%v: Vary %q", h, got)
		}
	}
}

func TestPartialSwapsTermCountsAndClearOutOfBand(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	e.addFixture(t, "tv", "show", showWith("1", "Dark", []string{"Drama"}, plugin.Tag{Name: "Loop"}), active)
	e.addFixture(t, "tv", "show", showWith("2", "Light", []string{"Comedy"}), active)
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	b := e.getWith("/tv?q=dar&genre=Comedy", hx).Body.String()
	// only the option lists go out of band, into the existing selects
	genre := oobBlock(t, b, "innerHTML:#filter-genre-select")
	mustContain(t, genre, `<option value="Drama">Drama (1)</option>`, `<option value="Comedy" selected>Comedy (0)</option>`)
	tag := oobBlock(t, b, "innerHTML:#filter-tag-select")
	// tag counts follow the genre and text filters: nothing matches both
	mustNotContain(t, tag, `Loop (`, "<option")
	mustContain(t, b, `id="filter-clear" hx-swap-oob="true"`, `href="/tv?view=cards">Clear filters</a>`)
	// never a select, fieldset, the any/all switch or the text inputs (focus stays)
	mustNotContain(t, b, "<select", "<fieldset", `name="genre_mode"`, `name="tag_mode"`, `name="q"`, `name="rmin"`, `name="sort"`,
		`id="filter-genre"`, `id="filter-tag"`)
	if n := strings.Count(b, "hx-swap-oob"); n != 7 { // tabs, clear, the Filters badge, the sort and dir option lists, the genre and tag option lists
		t.Errorf("%d oob blocks", n)
	}
	// without filters the Clear slot is swapped empty
	b = e.getWith("/tv", hx).Body.String()
	mustContain(t, b, `id="filter-clear" hx-swap-oob="true"></span>`, `<option value="Loop">Loop (1)</option>`)
	mustNotContain(t, b, "Clear filters")
	// the full page: the selects carry the stable target ids (unique), no oob
	full := e.listGet(t, "/tv?q=dar&genre=Comedy")
	for _, id := range []string{`id="filter-genre-select"`, `id="filter-tag-select"`, `id="filter-clear"`} {
		if n := strings.Count(full, id); n != 1 {
			t.Errorf("%s appears %d times", id, n)
		}
	}
	mustContain(t, full, `<select id="filter-genre-select" name="genre" multiple`, `<option value="Comedy" selected>Comedy (0)</option>`)
	mustNotContain(t, full, "hx-swap-oob")
	// a newer pick replaces an older in-flight request
	mustContain(t, full, `hx-sync="this:replace"`)
}

// oobBlock returns the inner html of the out-of-band element with the given
// hx-swap-oob value.
func oobBlock(t *testing.T, body, oob string) string {
	t.Helper()
	open := `hx-swap-oob="` + oob + `">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("no oob block %q", oob)
	}
	rest := body[i+len(open):]
	return rest[:strings.Index(rest, "</div>")]
}

func TestPushURLIsTheAppliedQuery(t *testing.T) {
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	e := newFixtureEnv(t)
	for i := 0; i < 101; i++ {
		e.addFixture(t, "tv", "show", tvShow(fmt.Sprint(i+1), fmt.Sprintf("Show %03d", i+1)), library.Entry{Status: library.Active})
	}
	for target, want := range map[string]string{
		"/tv?page=99":                    "/tv?page=2&view=cards",
		"/tv?q=Show&page=99999999999999": "/tv?page=2&q=Show&view=cards",
		"/tv?q=Show+001&page=5":          "/tv?q=Show+001&view=cards",
		"/tv?q=Show&bogus=1&sort=nope":   "/tv?q=Show&view=cards",
	} {
		if got := e.getWith(target, hx).Header().Get("HX-Push-Url"); got != want {
			t.Errorf("%s: HX-Push-Url %q, want %q", target, got, want)
		}
	}
	// filters the library refused are not pushed
	r := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		for i := 0; i < 21; i++ {
			k.Fields = append(k.Fields, plugin.FieldDef{Key: fmt.Sprintf("n%02d", i), Label: fmt.Sprintf("N%02d", i), Type: plugin.FieldNumber, Editable: true, Filterable: true})
		}
	})
	r.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	vals := url.Values{"q": {"zzz"}}
	for i := 0; i < 21; i++ {
		vals.Set(fmt.Sprintf("f.n%02d", i), "1..")
	}
	if got := r.getWith("/tv?"+vals.Encode(), hx).Header().Get("HX-Push-Url"); got != "/tv?view=cards" {
		t.Errorf("refused: HX-Push-Url %q", got)
	}
}

func TestUnreadableParameterNotice(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	b := e.listGet(t, "/tv?q=dark&a;b=1")
	wantRows(t, "readable pairs kept", b, "Dark")
	if got := noticeKeysOf(b); !slices.Equal(got, []string{"(unreadable parameter)"}) {
		t.Errorf("notice keys %q", got)
	}
	mustContain(t, b, `name="q" value="dark"`)
	if got := noticeKeysOf(e.listGet(t, "/tv?q=%zz")); !slices.Equal(got, []string{"(unreadable parameter)"}) {
		t.Errorf("bad escape: notice keys %q", got)
	}
}
