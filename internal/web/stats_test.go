package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
)

// newStatsEnv is the book/tv fixture environment with statistics declared on
// the fixture kinds: pages for books and episodes for shows. Time comes from
// the real anime plugin, whose kind already declares it.
func newStatsEnv(t *testing.T) *fixtureEnv {
	t.Helper()
	return newFixtureEnv(t, func(book, tv *plugintest.Plugin) {
		book.M.Kinds[0].Stats = &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "pages", LabelPlural: "Pages"}}
		tv.M.Kinds[0].Stats = &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "episode", LabelPlural: "Episodes"}}
	})
}

// animeMedia is a minimal valid anime item with the given id and title.
func animeMedia(id, title string) anilist.Media {
	return anilist.Media{SearchResult: anilist.SearchResult{
		Source: anilist.AniList, ID: id, Type: "TV", Episodes: ip(24),
		Titles: []anilist.Title{{Lang: "en", Text: title}},
	}}
}

// addStatsAnime saves a with the list entry en (its ItemID is ignored).
func (e *env) addStatsAnime(t *testing.T, a anilist.Media, en listEntry) int64 {
	t.Helper()
	id, err := saveAnime(context.Background(), e.lib, a)
	if err != nil {
		t.Fatal(err)
	}
	en.ItemID = id
	if err := e.lib.UpsertEntry(context.Background(), en.entry()); err != nil {
		t.Fatal(err)
	}
	return id
}

// statsBookItem is a book with pages as its total and genre.
func statsBookItem(id, title string, pages float64, genres ...string) plugin.ItemData {
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "books", ID: id, Kind: "book", Titles: ttl(title),
			Fields: map[string]plugin.FieldValue{"pages": {Num: num(pages)}}},
		Genres: genres,
	}
}

// statsShowItem is a show with episodes as its total.
func statsShowItem(id, title string, episodes float64) plugin.ItemData {
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: id, Kind: "show", Titles: ttl(title),
			Fields: map[string]plugin.FieldValue{"episodes": {Num: num(episodes)}}},
	}
}

// statHTML returns the card element of the summary card with the key.
func statHTML(t *testing.T, body, key string) string {
	t.Helper()
	i := strings.Index(body, `data-stat="`+key+`"`)
	if i < 0 {
		t.Fatalf("no card %q in %s", key, body)
	}
	start := strings.LastIndex(body[:i], "<li")
	end := strings.Index(body[i:], "</li>")
	if start < 0 || end < 0 {
		t.Fatalf("card %q is not an <li> in %s", key, body)
	}
	return body[start : i+end+len("</li>")]
}

// statValue returns the value of the summary card with the key.
func statValue(t *testing.T, body, key string) string {
	t.Helper()
	card := statHTML(t, body, key)
	i := strings.Index(card, "data-value")
	if i < 0 {
		t.Fatalf("card %q has no value in %s", key, card)
	}
	rest := card[i:]
	open := strings.Index(rest, ">")
	if open < 0 {
		t.Fatalf("card %q value is malformed in %s", key, card)
	}
	end := strings.Index(rest[open:], "</p>")
	if end < 0 {
		t.Fatalf("card %q value is malformed in %s", key, card)
	}
	return strings.TrimSpace(rest[open+1 : open+end])
}

// kindRow returns the comparison row of the kind plugin/kind.
func kindRow(t *testing.T, body, media string) string {
	t.Helper()
	i := strings.Index(body, `data-kind="`+media+`"`)
	if i < 0 {
		t.Fatalf("no comparison row %q in %s", media, body)
	}
	start := strings.LastIndex(body[:i], "<tr")
	end := strings.Index(body[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("comparison row %q is not a <tr> in %s", media, body)
	}
	return body[start : i+end+len("</tr>")]
}

// dimensionSection returns the dimension card with the key.
func dimensionSection(t *testing.T, body, key string) string {
	t.Helper()
	i := strings.Index(body, `data-dimension="`+key+`"`)
	if i < 0 {
		t.Fatalf("no dimension %q in %s", key, body)
	}
	start := strings.LastIndex(body[:i], "<section")
	end := strings.Index(body[i:], "</section>")
	if start < 0 || end < 0 {
		t.Fatalf("dimension %q is not a <section> in %s", key, body)
	}
	return body[start : i+end+len("</section>")]
}

func TestStatsPageRendersSummary(t *testing.T) {
	e := newStatsEnv(t)
	a := animeMedia("1", "Death Note")
	a.Duration = ip(24)
	e.addStatsAnime(t, a, listEntry{Status: library.Completed, Rating: ip(8), Progress: 12, FinishedOn: day(2025, 12, 1)})
	rec := e.get("/stats?media=anime/anime")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	if got := statValue(t, b, "entries"); got != "1" {
		t.Errorf("entries = %q", got)
	}
	if got := statValue(t, b, "completed"); got != "1" {
		t.Errorf("completed = %q", got)
	}
	mustContain(t, statHTML(t, b, "completed"), "100% of entries")
	if got := statValue(t, b, "time"); got != "4h 48m" {
		t.Errorf("time = %q", got)
	}
	mustContain(t, statHTML(t, b, "time"), "Watchtime", "across 1 entry with time")
	if got := statValue(t, b, "avg-time"); got != "4h 48m" {
		t.Errorf("average time = %q", got)
	}
	if got := statValue(t, b, "units"); got != "12" {
		t.Errorf("units = %q", got)
	}
	mustContain(t, statHTML(t, b, "units"), "Episodes")
	if got := statValue(t, b, "avg-units"); got != "12" {
		t.Errorf("average units = %q", got)
	}
	if got := statValue(t, b, "avg-rating"); got != "8.0" {
		t.Errorf("average rating = %q", got)
	}
	mustContain(t, statHTML(t, b, "avg-rating"), "from 1 rated entry")
	if got := statValue(t, b, "rated"); got != "1" {
		t.Errorf("rated = %q", got)
	}
	mustContain(t, statHTML(t, b, "rated"), "0 unrated")
}

func TestStatsPageMediaFilter(t *testing.T) {
	e := newStatsEnv(t)
	a := animeMedia("1", "Death Note")
	a.Duration = ip(24)
	e.addStatsAnime(t, a, listEntry{Status: library.Completed, Rating: ip(8), Progress: 12, FinishedOn: day(2025, 12, 1)})
	e.addFixture(t, "book", "book", statsBookItem("b1", "Earthsea", 300, "Fantasy"),
		library.Entry{Status: library.Completed, Progress: map[string]float64{"pages": 250}, FinishedOn: day(2025, 12, 2)})

	all := e.get("/stats").Body.String()
	mustContain(t, all, "data-kind-compare", `data-kind="anime/anime"`, `data-kind="book/book"`,
		`href="/stats?media=anime/anime"`, `href="/stats?media=book/book"`,
		`<option value="book/book">Books</option>`)
	mustNotContain(t, all, `data-stat="units"`)
	// All media names the status classes neutrally, not with a kind's labels
	mustContain(t, all, `<option value="active">Active</option>`, `<option value="completed">Completed</option>`)
	mustNotContain(t, all, `<option value="active">Watching</option>`)
	// a bare plugin id is accepted when the plugin has one kind
	bare := e.get("/stats?media=book")
	if bare.Code != 200 {
		t.Fatalf("bare media: %d", bare.Code)
	}
	mustContain(t, bare.Body.String(), `<option value="book/book" selected>`)

	book := e.get("/stats?media=book/book")
	if book.Code != 200 {
		t.Fatalf("book: %d", book.Code)
	}
	b := book.Body.String()
	if got := statValue(t, b, "entries"); got != "1" {
		t.Errorf("book entries = %q", got)
	}
	if got := statValue(t, b, "units"); got != "250" {
		t.Errorf("book units = %q", got)
	}
	mustContain(t, statHTML(t, b, "units"), "Pages")
	mustContain(t, b, `<option value="book/book" selected>`, `name="f.author"`)
	// a book has no time metric and the single-kind view has no comparison
	mustNotContain(t, b, `data-stat="time"`, `data-stat="avg-time"`, "data-kind-compare", "Watchtime", `name="f.type"`)
}

func TestStatsPagePeriodFilter(t *testing.T) {
	e := newStatsEnv(t)
	finished := func(id string, y int, m time.Month, d int) {
		a := animeMedia(id, "Show "+id)
		a.Duration = ip(24)
		e.addStatsAnime(t, a, listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(y, m, d)})
	}
	finished("old", 2025, 12, 15)
	finished("new", 2026, 3, 10)
	// the inclusive ends of Last 12 months at the env clock (2026-01-01):
	// 2025-02-01 through 2026-01-31
	finished("start", 2025, 2, 1)
	finished("end", 2026, 1, 31)
	e.addStatsAnime(t, animeMedia("undated", "Undated"), listEntry{Status: library.Completed, Progress: 1})

	all := e.get("/stats?media=anime/anime")
	if got := statValue(t, all.Body.String(), "entries"); got != "5" {
		t.Errorf("all time entries = %q", got)
	}
	// All time includes the entry without a finished date; a period does not
	year := e.get("/stats?media=anime/anime&period=year")
	if got := statValue(t, year.Body.String(), "entries"); got != "2" {
		t.Errorf("this year entries = %q", got)
	}
	mustContain(t, year.Body.String(), `<option value="year" selected>`, "1 completed entry has no finished date")
	// the custom date inputs only exist for the custom range
	mustNotContain(t, year.Body.String(), `name="from"`, `name="to"`)
	last12 := e.get("/stats?media=anime/anime&period=12m")
	if got := statValue(t, last12.Body.String(), "entries"); got != "3" {
		t.Errorf("last 12 months entries = %q", got)
	}
	// a present range implies a custom one, and both ends are inclusive
	custom := e.get("/stats?media=anime/anime&from=2025-02-01&to=2025-12-15")
	b := custom.Body.String()
	if got := statValue(t, b, "entries"); got != "2" {
		t.Errorf("custom entries = %q", got)
	}
	mustContain(t, b, `<option value="custom" selected>`, `name="from" value="2025-02-01"`, `name="to" value="2025-12-15"`)
}

func TestStatsPageStatusFilter(t *testing.T) {
	e := newStatsEnv(t)
	e.addStatsAnime(t, animeMedia("1", "One"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	e.addStatsAnime(t, animeMedia("2", "Two"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 2)})
	e.addStatsAnime(t, animeMedia("3", "Three"), listEntry{Status: library.Active, Progress: 1})
	completed := e.get("/stats?media=anime/anime&status=completed")
	if got := statValue(t, completed.Body.String(), "entries"); got != "2" {
		t.Errorf("completed entries = %q", got)
	}
	mustContain(t, completed.Body.String(), `<option value="completed" selected>Completed</option>`)
	active := e.get("/stats?media=anime/anime&status=active")
	if got := statValue(t, active.Body.String(), "entries"); got != "1" {
		t.Errorf("active entries = %q", got)
	}
	// the single-kind view uses the kind's own status labels
	mustContain(t, active.Body.String(), `<option value="active" selected>Watching</option>`)
}

func TestStatsPageGenreAndTagFilters(t *testing.T) {
	e := newStatsEnv(t)
	add := func(id, title string, genres []string, tag string) {
		a := animeMedia(id, title)
		a.Duration = ip(24)
		a.Genres = genres
		if tag != "" {
			a.Tags = []anilist.Tag{{Name: tag}}
		}
		e.addStatsAnime(t, a, listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	}
	add("1", "One", []string{"Action", "Drama"}, "T1")
	add("2", "Two", []string{"Drama"}, "T2")
	add("3", "Three", []string{"Action"}, "")

	any := e.get("/stats?media=anime/anime&genre=Action")
	if got := statValue(t, any.Body.String(), "entries"); got != "2" {
		t.Errorf("genre any entries = %q", got)
	}
	// the counts ignore the genre filter itself
	mustContain(t, any.Body.String(), `<option value="Action" selected>Action (2)</option>`, `<option value="Drama">Drama (2)</option>`)
	all := e.get("/stats?media=anime/anime&genre=Action&genre=Drama&genre_mode=all")
	if got := statValue(t, all.Body.String(), "entries"); got != "1" {
		t.Errorf("genre all entries = %q", got)
	}
	mustContain(t, all.Body.String(), `<option value="all" selected>All of these</option>`)
	tag := e.get("/stats?media=anime/anime&tag=T1")
	if got := statValue(t, tag.Body.String(), "entries"); got != "1" {
		t.Errorf("tag entries = %q", got)
	}
	mustContain(t, dimensionSection(t, tag.Body.String(), "tags"), "T1")
}

func TestStatsPageFieldFilter(t *testing.T) {
	e := newStatsEnv(t)
	a := animeMedia("1", "One")
	a.Duration = ip(24)
	a.Year = ip(2006)
	a.Studio = "Madhouse"
	e.addStatsAnime(t, a, listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	b := animeMedia("2", "Two")
	b.Duration = ip(24)
	b.Type = "MOVIE"
	b.Year = ip(2020)
	b.Studio = "Ghibli"
	e.addStatsAnime(t, b, listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 2)})

	typed := e.get("/stats?media=anime/anime&f.type=TV")
	if got := statValue(t, typed.Body.String(), "entries"); got != "1" {
		t.Errorf("type filter entries = %q", got)
	}
	mustContain(t, typed.Body.String(), `<option value="TV" selected>TV</option>`)
	ranged := e.get("/stats?media=anime/anime&f.type=MOVIE&f.year=2010..2025")
	if got := statValue(t, ranged.Body.String(), "entries"); got != "1" {
		t.Errorf("range filter entries = %q", got)
	}
	mustContain(t, dimensionSection(t, ranged.Body.String(), "type"), "MOVIE")
}

func TestStatsPageAllMediaComparison(t *testing.T) {
	e := newStatsEnv(t)
	a := animeMedia("1", "Death Note")
	a.Duration = ip(24)
	e.addStatsAnime(t, a, listEntry{Status: library.Completed, Rating: ip(8), Progress: 12, FinishedOn: day(2025, 12, 1)})
	e.addFixture(t, "book", "book", statsBookItem("b1", "Earthsea", 300),
		library.Entry{Status: library.Completed, Progress: map[string]float64{"pages": 250}, FinishedOn: day(2025, 12, 2)})
	e.addFixture(t, "tv", "show", statsShowItem("t1", "Dark", 10),
		library.Entry{Status: library.Completed, Progress: map[string]float64{"episode": 5}, FinishedOn: day(2025, 12, 3)})

	b := e.get("/stats").Body.String()
	if got := statValue(t, b, "entries"); got != "3" {
		t.Errorf("entries = %q", got)
	}
	mustContain(t, b, "data-kind-compare", ">Kind<")
	for media, want := range map[string][]string{
		"anime/anime": {"Anime", "4h 48m", "12"},
		"book/book":   {"Books", "250"},
		"tv/show":     {"Shows", "5"},
	} {
		row := kindRow(t, b, media)
		mustContain(t, row, want...)
	}
	mustNotContain(t, b, `data-stat="units"`)
}

func TestStatsPageDimensions(t *testing.T) {
	e := newStatsEnv(t)
	a := animeMedia("1", "One")
	a.Duration = ip(24)
	a.Genres = []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10", "G11", "G12"}
	a.Tags = []anilist.Tag{{Name: "Tagged"}}
	a.Year = ip(2006)
	a.Studio = "Madhouse"
	e.addStatsAnime(t, a, listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})

	b := e.get("/stats?media=anime/anime").Body.String()
	genres := dimensionSection(t, b, "genres")
	mustContain(t, genres, "Show all (2 more)")
	top, more, ok := strings.Cut(genres, "<details")
	if !ok {
		t.Fatalf("the genre dimension has no Show all disclosure: %s", genres)
	}
	if n := strings.Count(top, "data-value="); n != 10 {
		t.Errorf("top genre rows = %d", n)
	}
	if n := strings.Count(more, "data-value="); n != 2 {
		t.Errorf("hidden genre rows = %d", n)
	}
	// a specific kind shows its filterable fields, not the non-filterable ones
	for _, key := range []string{"genres", "tags", "type", "year", "studio"} {
		dimensionSection(t, b, key)
	}
	mustNotContain(t, b, `data-dimension="synopsis"`, `data-dimension="started_on"`)

	// All media merges genres and tags and has no per-kind field dimensions
	all := e.get("/stats").Body.String()
	mustContain(t, all, `data-dimension="genres"`, `data-dimension="tags"`)
	mustNotContain(t, all, `data-dimension="type"`, `data-dimension="year"`)
}

func TestStatsPageInvalidParamsNotice(t *testing.T) {
	e := newStatsEnv(t)
	e.addStatsAnime(t, animeMedia("1", "One"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	e.addStatsAnime(t, animeMedia("2", "Two"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2026, 3, 1)})
	rec := e.get("/stats?media=nope/nope&period=weird&status=bogus&from=not-a-date&to=2025-12-31&f.type=TV")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "Ignored unknown or invalid parameters:",
		"<code>f.type</code>", "<code>from</code>", "<code>media</code>", "<code>period</code>", "<code>status</code>")
	// the invalid values were ignored: all media, all time
	if got := statValue(t, b, "entries"); got != "2" {
		t.Errorf("entries = %q", got)
	}
	mustContain(t, b, `<option value="" selected>All media</option>`, `<option value="all" selected>All time</option>`)

	reversed := e.get("/stats?period=custom&from=2025-12-31&to=2025-01-01")
	rb := reversed.Body.String()
	mustContain(t, rb, "<code>from</code>", "<code>to</code>")
	if got := statValue(t, rb, "entries"); got != "2" {
		t.Errorf("reversed range entries = %q", got)
	}
}

func TestStatsPageAllMediaIgnoresFieldFilters(t *testing.T) {
	e := newStatsEnv(t)
	e.addStatsAnime(t, animeMedia("1", "One"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	e.addFixture(t, "book", "book", statsBookItem("b1", "Earthsea", 300),
		library.Entry{Status: library.Completed, Progress: map[string]float64{"pages": 100}, FinishedOn: day(2025, 12, 2)})

	all := e.get("/stats?f.type=TV").Body.String()
	mustContain(t, all, "<code>f.type</code>", "Ignored unknown or invalid parameters:")
	mustNotContain(t, all, `name="f.type"`)
	if got := statValue(t, all, "entries"); got != "2" {
		t.Errorf("all-media entries = %q", got)
	}
	one := e.get("/stats?media=anime/anime&f.type=TV").Body.String()
	mustNotContain(t, one, "Ignored unknown or invalid parameters")
	mustContain(t, one, `name="f.type"`)
}

func TestStatsPageEmpty(t *testing.T) {
	// no plugin enabled: the page explains where to switch one on
	none := newEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry { return plugin.NewRegistry(es) })
	rec := none.get("/stats")
	if rec.Code != 200 {
		t.Fatalf("no plugins: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "No plugins are enabled", `href="/settings/plugins"`)

	// nothing tracked yet
	e := newStatsEnv(t)
	b := e.get("/stats").Body.String()
	mustContain(t, b, "No entries in your list yet", `href="/"`)
	mustNotContain(t, b, `data-stat="entries"`)

	// entries exist but no row matches the filters
	e.addStatsAnime(t, animeMedia("1", "One"), listEntry{Status: library.Completed, Progress: 1, FinishedOn: day(2025, 12, 1)})
	filtered := e.get("/stats?media=anime/anime&genre=Missing").Body.String()
	mustContain(t, filtered, "No entries match these filters", `href="/stats"`)
	mustNotContain(t, filtered, `data-stat="entries"`)
}

// TestStatsPageMissingTimeNotice: entries without the metadata behind a time
// total are named, and the note points at the item action that fills it.
func TestStatsPageMissingTimeNotice(t *testing.T) {
	e := newStatsEnv(t)
	e.addStatsAnime(t, animeMedia("1", "No duration"), listEntry{Status: library.Completed, Progress: 5, FinishedOn: day(2025, 12, 1)})
	b := e.get("/stats?media=anime/anime").Body.String()
	mustContain(t, b, "1 entry is missing the data needed for their time total",
		"Update from source", `href="/anime"`, "None of these entries have enough stored data for a time total")
}

func TestStatsNavRow(t *testing.T) {
	e := newStatsEnv(t)
	nav := mainNav(t, e.get("/stats").Body.String())
	row := navRow(t, nav, "/stats")
	mustIcon(t, row, "bar_chart")
	mustContain(t, row, "<span>Statistics</span>", `aria-current="page"`)
	stats, calendar := strings.Index(nav, `href="/stats"`), strings.Index(nav, `href="/calendar"`)
	if stats < 0 || calendar < 0 || stats > calendar {
		t.Fatalf("Statistics is not before Calendar: %s", nav)
	}
	// both live in the bottom list, below the media rows
	if i := strings.LastIndex(nav, "<ul"); i < 0 || i > stats {
		t.Fatalf("Statistics is not in the bottom list: %s", nav)
	}
}
