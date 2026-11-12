package web

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/settings"
)

// Overview tests moved from anime_test.go (assertions unchanged) followed by
// the generic item overview tests.

func TestOverviewLiveForUnsaved(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "デスノート", "Line one.", "Line two.", "Madhouse", "2006-10-04", "2007-06-27", "MANGA", "37 eps",
		"Light Yagami", "Mamoru Miyano", "Sequel Title", `href="/anime/anilist/2000"`, `href="https://anilist.co/manga/30021"`,
		"https://img.example/c.jpg", "https://img.example/l.jpg",
		"View on AniList", `href="https://anilist.co/anime/1535"`, "Add to list", `hx-get="/anime/anilist/1535/add"`)
	mustNotContain(t, b, "Cast unavailable", "partially synced")
	if e.fp.animeCalls != 1 || e.fp.castCall != 1 {
		t.Fatalf("calls %d %d", e.fp.animeCalls, e.fp.castCall)
	}
	if ar, _ := findBySource(context.Background(), e.lib, anilist.AniList, "1535"); ar != nil {
		t.Fatal("overview must not save")
	}
}

func TestOverviewUsesPreferredLanguage(t *testing.T) {
	e := newEnv(t)
	if err := e.set.SetTitleLang(context.Background(), "anime", "en"); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, "<h1", "Death Note EN</h1>")
}

func TestOverviewFromDatabaseForSaved(t *testing.T) {
	e := newEnv(t)
	e.saved(t)
	e.fp.anime.Synopsis = "LIVE CHANGED"
	e.fp.castErr = anilist.ErrUnavailable
	rec := e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "Madhouse", "Sequel Title", "partially synced")
	mustNotContain(t, b, "LIVE CHANGED")
	if e.fp.castCall != 0 {
		t.Fatal("cast fetched live for a saved entry")
	}
	// recommendations come live (loaded late from their endpoint), failures
	// never break the page
	mustContain(t, e.htmxGet("/anime/anilist/1535/recommendations").Body.String(), "Rec Title")
	e.fp.animeErr = anilist.ErrUnavailable
	rec = e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), "Rec Title")
	*e.now = e.now.Add(cacheTTL) // past the cached result
	rec = e.htmxGet("/anime/anilist/1535/recommendations")
	if rec.Code != 200 {
		t.Fatalf("recommendations: %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), "Rec Title")
}

func TestOverviewShowsAllTitleLanguages(t *testing.T) {
	e := newEnv(t)
	for _, saved := range []bool{false, true} {
		if saved {
			e.saved(t)
		}
		b := e.get("/anime/anilist/1535").Body.String()
		mustContain(t, b, "Japanese (native)", "デスノート", "Romaji", "Death Note", "English", "Death Note EN", "Deutsch", "Todesnotiz")
	}
}

func TestOverviewSynopsisHasNoRawHTML(t *testing.T) {
	e := newEnv(t)
	e.fp.anime.Synopsis = "<script>alert(1)</script>\n<b>bold</b> & more"
	b := e.get("/anime/anilist/1535").Body.String()
	mustNotContain(t, b, "<script>alert", "<b>bold</b>")
	mustContain(t, b, "&lt;script&gt;alert(1)&lt;/script&gt;", "&amp; more")
	if strings.Count(b, "<p class=\"synopsis\"") != 2 {
		t.Fatalf("want 2 synopsis paragraphs")
	}
}

func TestOverviewCastFailureStillRenders(t *testing.T) {
	e := newEnv(t)
	e.fp.castErr = anilist.ErrUnavailable
	rec := e.get("/anime/anilist/1535")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Cast unavailable right now", "Sequel Title")
}

func TestOverviewProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{anilist.ErrNotFound, 404}, {anilist.ErrUnavailable, 503}, {anilist.ErrRateLimited, 503}} {
		e := newEnv(t)
		e.fp.animeErr = tc.err
		rec := e.get("/anime/anilist/1535")
		if rec.Code != tc.code {
			t.Fatalf("%v: got %d", tc.err, rec.Code)
		}
		b := rec.Body.String()
		mustNotContain(t, b, "goroutine", "panic")
		if tc.code == 503 {
			mustContain(t, b, `href="/anime/anilist/1535"`, "Try again")
		}
	}
}

func TestOverviewNilProviderIs503(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { d.Close() })
	set := settings.New(d)
	reg := animeRegistry(t, set, nil)
	h := NewServer(Deps{Settings: set, Registry: reg, Library: library.New(d, reg)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/anime/anilist/1535", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestOverviewUnknownSourceAndBadID(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/anime/mal/1", "/anime/anilist/abc", "/anime/foo/1/add"} {
		if rec := e.get(p); rec.Code != 404 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	if e.fp.animeCalls != 0 {
		t.Fatal("provider called")
	}
}

func TestOverviewManualFromDatabaseOnly(t *testing.T) {
	e := newEnv(t)
	if rec := e.get("/anime/manual/77"); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
	m := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "77", Type: "OVA",
		Titles: []anilist.Title{{Lang: "en", Text: "My Manual Show"}}}}
	if _, err := saveAnime(context.Background(), e.lib, m, library.WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	rec := e.get("/anime/manual/77")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "My Manual Show")
	mustNotContain(t, rec.Body.String(), "partially synced", "View on AniList")
	if e.fp.animeCalls != 0 {
		t.Fatal("provider called for manual entry")
	}
}

func TestExternalLinksOnlyHTTPS(t *testing.T) {
	e := newEnv(t)
	e.fp.anime.ExternalURL = "http://anilist.co/anime/1535"
	e.fp.cast[0].Character.URL = "javascript:alert(1)"
	e.fp.cast[0].Actor.URL = "http://anilist.co/staff/a1"
	b := e.get("/anime/anilist/1535").Body.String()
	mustNotContain(t, b, "View on AniList", "javascript:", `href="http://anilist.co`)
}

func TestPartialSyncBadge(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	for _, st := range []string{"pending", "partial"} {
		if err := e.lib.SetSyncState(context.Background(), id, st); err != nil {
			t.Fatal(err)
		}
		mustContain(t, e.get("/anime/anilist/1535").Body.String(), "partially synced")
	}
	e.lib.SetSyncState(context.Background(), id, "done")
	mustNotContain(t, e.get("/anime/anilist/1535").Body.String(), "partially synced")
}

func TestPersonLinksExternalOnlyWhenUnsaved(t *testing.T) {
	e := newEnv(t)
	b := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, `href="https://anilist.co/character/c1"`, `href="https://anilist.co/staff/a1"`)
	mustNotContain(t, b, "/people/")

	id := e.saved(t)
	if err := saveCast(context.Background(), e.lib, id, e.fp.cast); err != nil {
		t.Fatal(err)
	}
	b = e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, `href="https://anilist.co/character/c1"`, `href="https://anilist.co/staff/a1"`, `href="/people/1"`, `href="/people/2"`)
}

func TestSavedWithEntryShowsSummaryInsteadOfButton(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	if err := upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Active, Rating: ip(9), Progress: 5}); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, "Watching", "9", "5 / 37", `href="/list/1/edit"`)
	mustNotContain(t, b, "Add to list")
}

// --- generic overview (fixture plugins) ---

// fixtureEnv is an env with the book and tv fixture plugins (after the real
// anime plugin) and their fake sources.
type fixtureEnv struct {
	*env
	book, tv *plugintest.FakeSource
}

func newFixtureEnv(t *testing.T, mod ...func(book, tv *plugintest.Plugin)) *fixtureEnv {
	t.Helper()
	book, tv := plugintest.BookPlugin(), plugintest.TVPlugin()
	for _, m := range mod {
		m(book, tv)
	}
	fe := &fixtureEnv{}
	fe.book, _ = book.Sources["books"].(*plugintest.FakeSource)
	fe.tv, _ = tv.Sources["tvdata"].(*plugintest.FakeSource)
	fe.env = newEnvWith(t, withPlugins(book, tv))
	return fe
}

func ttl(text string) []plugin.Title { return []plugin.Title{{Lang: "en", Text: text}} }

func num(f float64) *float64 { return &f }

// tvShow is valid tv data with source id id.
func tvShow(id, title string) plugin.ItemData {
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: id, Kind: "show", Titles: ttl(title),
			Fields: map[string]plugin.FieldValue{"type": {Text: "series"}, "year": {Num: num(2019)}}},
		ExternalURL: "https://tv.example/show/" + id,
	}
}

func bookItem(id, title string) plugin.ItemData {
	return plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "books", ID: id, Kind: "book", Titles: ttl(title),
			Fields: map[string]plugin.FieldValue{"author": {Text: "Ursula K. Le Guin"}}},
		ExternalURL: "https://books.example/work/" + id,
		Genres:      []string{"Fantasy"},
	}
}

// addFixture saves d into the list of plugin/kind and returns the item id.
func (e *env) addFixture(t *testing.T, pluginID, kind string, d plugin.ItemData, en library.Entry, opts ...library.SaveOption) int64 {
	t.Helper()
	id, err := e.lib.AddToList(context.Background(), pluginID, kind, d, en, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) saveFixture(t *testing.T, pluginID, kind string, d plugin.ItemData, opts ...library.SaveOption) int64 {
	t.Helper()
	id, err := e.lib.SaveItem(context.Background(), pluginID, kind, d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOverviewRendersOnlyDeclaredSections(t *testing.T) {
	e := newFixtureEnv(t)
	d := bookItem("1", "A Wizard of Earthsea")
	d.Tags = []plugin.Tag{{Name: "Dragons"}}
	e.book.Data = d
	e.book.Credits = []plugin.Credit{
		{Role: "author", Person: plugin.PersonRef{ID: "u", Name: "Ursula Writer", URL: "https://books.example/a/u"}},
		{Role: "narrator", Person: plugin.PersonRef{ID: "n", Name: "Nora Narrator"}},
	}
	rec := e.get("/book/books/1")
	if rec.Code != 200 {
		t.Fatalf("book: %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "A Wizard of Earthsea</h1>", ">Details<", "Author", "Ursula K. Le Guin", ">Authors<", "Ursula Writer",
		`href="https://books.example/a/u"`, ">Genres<", "Fantasy", "View on Fake Books", `href="https://books.example/work/1"`)
	// no cast, tags, titles or recommendations: the book kind does not declare them
	mustNotContain(t, b, "Nora Narrator", "Dragons", ">Tags<", ">Titles<", "Recommendations", ">Cast<")
	if e.book.Calls(plugintest.OpRecommendations) != 0 {
		t.Fatal("recommendations fetched for a kind without the section")
	}

	// tv: the episode progress and the position as h:mm:ss
	e.tv.Data = tvShow("1", "Dark")
	e.addFixture(t, "tv", "show", tvShow("5", "Saved Show"),
		library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 3, "position": 3725}},
		library.WithProgressKeys([]string{"episode", "position"}))
	b = e.get("/tv/tvdata/5").Body.String()
	mustContain(t, b, "Saved Show</h1>", "Episode: 3 / ?", "Position: 1:02:05", ">Details<", "Type", "series", "Year", "2019")
	mustNotContain(t, b, "Authors", "Season:")
}

func TestOverviewShowsProgressPerActiveType(t *testing.T) {
	e := newFixtureEnv(t)
	show := tvShow("1", "Counted")
	show.Fields["episodes"] = plugin.FieldValue{Num: num(10)}
	e.addFixture(t, "tv", "show", show, library.Entry{Status: library.Active, Progress: map[string]float64{"season": 2, "episode": 5}},
		library.WithProgressKeys([]string{"season", "episode"}))
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, "Season: 2<", "Episode: 5 / 10")
	mustNotContain(t, b, "Position:")

	pages := bookItem("p", "Paged")
	pages.Fields["pages"] = plugin.FieldValue{Num: num(300)}
	e.addFixture(t, "book", "book", pages, library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 12}})
	mustContain(t, e.get("/book/books/p").Body.String(), "Page: 12 / 300")
	e.addFixture(t, "book", "book", bookItem("q", "Percent"), library.Entry{Status: library.Active, Progress: map[string]float64{"percent": 40}},
		library.WithProgressKeys([]string{"percent"}))
	b = e.get("/book/books/q").Body.String()
	mustContain(t, b, "Percent: 40%")
	mustNotContain(t, b, "Page:")
}

func TestOverviewStatusLabelAndRating(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Rated Zero"), library.Entry{Status: library.Completed, Rating: ip(0)})
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, "Watched", "Rating: 0<", `href="/list/1/edit"`, ">Edit<")
	mustNotContain(t, b, "Add to list", "Completed")
	e.addFixture(t, "tv", "show", tvShow("2", "Unrated"), library.Entry{Status: library.Planned})
	b = e.get("/tv/tvdata/2").Body.String()
	mustContain(t, b, "Planned", "Rating: –")
	// not in the list: the add button instead of the summary
	e.saveFixture(t, "tv", "show", tvShow("3", "Only Saved"))
	b = e.get("/tv/tvdata/3").Body.String()
	mustContain(t, b, "Add to list", `hx-get="/tv/tvdata/3/add"`, `id="dialog-slot"`, `data-swap-statuses=`)
	mustNotContain(t, b, "Rating:")
}

func TestOverviewPartialSyncBadgeForFixture(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.saveFixture(t, "tv", "show", tvShow("1", "Syncing"))
	mustContain(t, e.get("/tv/tvdata/1").Body.String(), "partially synced")
	if err := e.lib.SetSyncState(context.Background(), id, "done"); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get("/tv/tvdata/1").Body.String(), "partially synced")
}

func TestOverviewUnknownPluginOrSource404(t *testing.T) {
	e := newFixtureEnv(t)
	e.book.Data = bookItem("1", "B")
	for _, p := range []string{"/nope/books/1", "/book/nope/1", "/book/manual/1", "/book/books/1?kind=nope", "/book/books/1?kind=show",
		"/book/books/%01", "/book/books/" + strings.Repeat("9", 300)} {
		if rec := e.get(p); rec.Code != 404 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	if n := e.book.Calls(plugintest.OpDetails); n != 0 {
		t.Fatalf("source called %d times", n)
	}
	// a saved manual item exists: manual items come from the library only
	man := bookItem("m1", "Hand Made")
	man.Source = "manual"
	e.saveFixture(t, "book", "book", man, library.WithSyncState("done"))
	if rec := e.get("/book/manual/m1"); rec.Code != 200 {
		t.Fatalf("saved manual: %d", rec.Code)
	}
	if n := e.book.Calls(plugintest.OpDetails); n != 0 {
		t.Fatalf("source called %d times for a manual item", n)
	}
	// the kind parameter, when given, must match the saved item
	if rec := e.get("/book/books/1?kind=book"); rec.Code != 200 {
		t.Fatalf("explicit kind: %d", rec.Code)
	}
}

func TestOverviewOfDisabledPlugin404(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Hidden")
	e.saveFixture(t, "tv", "show", tvShow("2", "Saved Hidden"))
	if c := e.get("/tv/tvdata/1").Code; c != 200 {
		t.Fatalf("enabled: %d", c)
	}
	e.disable(t, "tv")
	for _, p := range []string{"/tv/tvdata/1", "/tv/tvdata/2"} {
		rec := e.get(p)
		if rec.Code != 404 {
			t.Fatalf("%s disabled: %d", p, rec.Code)
		}
		mustNotContain(t, rec.Body.String(), "Hidden")
	}
	if c := e.get("/book/books/x").Code; c != 200 {
		t.Fatalf("book plugin blocked by tv's flag: %d", c)
	}
}

func TestOverviewKindParameterResolvesAmbiguity(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.saveFixture(t, "book", "book", bookItem("7", "The Novel"))
	cd := bookItem("7", "The Comic")
	cd.Kind = "comic"
	e.saveFixture(t, "book", "comic", cd)
	mustContain(t, e.get("/book/books/7?kind=book").Body.String(), "The Novel</h1>")
	mustContain(t, e.get("/book/books/7?kind=comic").Body.String(), "The Comic</h1>")
	if c := e.get("/book/books/7?kind=show").Code; c != 404 {
		t.Fatalf("unknown kind: %d", c)
	}
	// unsaved: a plugin with several kinds needs the kind
	e.book.Data = bookItem("9", "Live Comic")
	if c := e.get("/book/books/9").Code; c != 404 {
		t.Fatalf("ambiguous live: %d", c)
	}
	if e.book.Calls(plugintest.OpDetails) != 0 {
		t.Fatal("source called without a kind")
	}
	rec := e.get("/book/books/9?kind=comic")
	if rec.Code != 200 {
		t.Fatalf("live with kind: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Live Comic</h1>")
}

func TestRelationLinkingRules(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		k.Sections = append(k.Sections, plugin.SectionDef{Type: plugin.SectionRelated, Label: "Related"})
	})
	d := tvShow("1", "Base")
	d.Relations = []plugin.Relation{
		{Kind: "sequel", Item: plugin.ItemSummary{Source: "tvdata", ID: "2 b", Kind: "show", Titles: ttl("Internal Sequel"), Fields: map[string]plugin.FieldValue{"type": {Text: "miniseries"}}}},
		{Kind: "source", Item: plugin.ItemSummary{Source: "books", ID: "b1", Kind: "book", Titles: ttl("Book Source"), URL: "https://books.example/work/b1"}},
		{Kind: "other", Item: plugin.ItemSummary{Source: "x", ID: "z", Kind: "y", Titles: ttl("Insecure Other"), URL: "http://insecure.example/z"}},
		{Kind: "PREQUEL", Item: plugin.ItemSummary{Source: "tvdata", ID: "m", Kind: "movie", Titles: ttl("Foreign Kind")}},
		{Kind: "side_story", Item: plugin.ItemSummary{Source: "tvdata", ID: "s", Kind: "show", URL: "javascript:alert(1)"}},
	}
	e.tv.Data = d
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, ">Related<", "Sequel", `href="/tv/tvdata/2%20b"`, "Internal Sequel", "(miniseries)",
		"Source", `href="https://books.example/work/b1"`, "Book Source",
		"Other", "Insecure Other", "Prequel", "Foreign Kind", "Side story", `href="/tv/tvdata/s"`)
	mustNotContain(t, b, "insecure.example", "javascript:", `href="/tv/tvdata/m"`)
}

func TestGenreAndTagChipsRender(t *testing.T) {
	e := newFixtureEnv(t)
	d := tvShow("1", "Chips")
	d.Genres = []string{"Drama", "<b>Bold</b>"}
	d.Tags = []plugin.Tag{{Name: "Time Travel", Rank: ip(85)}, {Name: "Plot Twist", Rank: ip(60), Spoiler: true}, {Name: "<i>Tag</i>"}}
	e.tv.Data = d
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, ">Genres<", "Drama", "&lt;b&gt;Bold&lt;/b&gt;", ">Tags<", "Time Travel", "85%", "&lt;i&gt;Tag&lt;/i&gt;",
		"<details", "<summary", "Show spoiler tags</summary>", "Plot Twist")
	mustNotContain(t, b, "<b>Bold</b>", "<i>Tag</i>")
	if i, j := strings.Index(b, "Show spoiler tags"), strings.Index(b, "Plot Twist"); i < 0 || j < i {
		t.Fatal("spoiler tag not inside the collapsed control")
	}
	if i, j := strings.Index(b, "Time Travel"), strings.Index(b, "<details"); i > j {
		t.Fatal("plain tag hidden with the spoilers")
	}
	// no spoiler tags: no control
	d.Tags = d.Tags[:1]
	e.tv.Data = d
	mustNotContain(t, e.get("/tv/tvdata/1").Body.String(), "<details", "Show spoiler tags")
}

func TestOverviewEmptySectionsRenderNothing(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: ttl("Bare")}}
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, "Bare</h1>", ">Titles<")
	mustNotContain(t, b, ">Details<", ">Synopsis<", ">Cast<", ">Genres<", ">Tags<", ">Recommendations<", "View on Fake TV", "unavailable right now")
}

func TestOverviewCreditsAndCounterpart(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Cast Show")
	actor := plugin.PersonRef{ID: "a1", Name: "Ann Actor", URL: "https://tv.example/p/a1", ImageURL: "https://img.example/a1.jpg"}
	e.tv.Credits = []plugin.Credit{
		{Role: "actor", Detail: "LEAD", Person: plugin.PersonRef{ID: "c1", Name: "Carl Character"}, With: &actor, WithRole: "Voice actor"},
		{Role: "director", Person: plugin.PersonRef{ID: "d1", Name: "Dora Director"}},
	}
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, ">Cast<", "Carl Character", "LEAD", "Voice actor", "Ann Actor", `href="https://tv.example/p/a1"`, `src="https://img.example/a1.jpg"`)
	mustNotContain(t, b, "Dora Director", "/people/")
}

func TestOverviewPeopleFailureNotice(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "No Cast")
	e.tv.Errs = map[string]error{plugintest.OpPeople: plugin.ErrUnavailable, plugintest.OpRecommendations: plugin.ErrUnavailable}
	rec := e.get("/tv/tvdata/1")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Cast unavailable right now", "No Cast</h1>")
	mustNotContain(t, rec.Body.String(), ">Recommendations<")
}

func TestOverviewSourceErrorsForFixture(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		msg  string
	}{{plugin.ErrNotFound, 404, ""}, {plugin.ErrRateLimited, 503, "Fake TV is rate limiting"}, {plugin.ErrUnavailable, 503, "Fake TV is unavailable"}} {
		e := newFixtureEnv(t)
		e.tv.Errs = map[string]error{plugintest.OpDetails: tc.err}
		rec := e.get("/tv/tvdata/1")
		if rec.Code != tc.code {
			t.Fatalf("%v: got %d", tc.err, rec.Code)
		}
		if tc.code == 503 {
			mustContain(t, rec.Body.String(), tc.msg, `href="/tv/tvdata/1"`, "Try again")
		}
	}
	// a declared source the plugin does not provide: 503, no panic
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	if rec := e.get("/tv/tvdata/1"); rec.Code != 503 {
		t.Fatalf("nil source: %d", rec.Code)
	}
}

func TestOverviewFixtureFromDatabaseForSaved(t *testing.T) {
	e := newFixtureEnv(t)
	d := tvShow("1", "Stored Title")
	id := e.saveFixture(t, "tv", "show", d)
	if err := e.lib.SaveCredits(context.Background(), id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "a", Name: "Stored Actor", URL: "https://tv.example/p/a"}}}); err != nil {
		t.Fatal(err)
	}
	e.tv.Data = tvShow("1", "LIVE TITLE")
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, "Stored Title</h1>", "Stored Actor", `href="/people/1"`, `href="https://tv.example/p/a"`)
	mustNotContain(t, b, "LIVE TITLE")
	if e.tv.Calls(plugintest.OpDetails) != 0 || e.tv.Calls(plugintest.OpPeople) != 0 {
		t.Fatal("saved item fetched live")
	}
}

func TestLiveCacheIsKeyedByPlugin(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "TV One")
	e.get("/tv/tvdata/1")
	if _, ok := e.srv.cache.get(liveKey("tv", "tvdata", "show", "1")); !ok {
		t.Fatal("live data not cached")
	}
	for _, k := range []string{liveKey("tv2", "tvdata", "show", "1"), liveKey("tv", "tvdata", "movie", "1"), liveKey("tv", "other", "show", "1")} {
		if _, ok := e.srv.cache.get(k); ok {
			t.Fatalf("%s hit", k)
		}
	}
	if liveKey("a/b", "c", "d", "e") == liveKey("a", "b/c", "d", "e") {
		t.Fatal("keys collide")
	}
}

func TestSectionsEscapeHostileValues(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		k.Fields = append(k.Fields, plugin.FieldDef{Key: "synopsis", Label: "Synopsis", Type: plugin.FieldText},
			plugin.FieldDef{Key: "home", Label: "Home<b>", Type: plugin.FieldURL, Show: true})
		k.Sections[1].Field = "synopsis"
		k.Sections = append(k.Sections, plugin.SectionDef{Type: plugin.SectionRelated, Label: "Related"})
	})
	const x = `<script>alert(1)</script>`
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "tvdata", ID: "1", Kind: "show", Titles: []plugin.Title{{Lang: "en", Text: "T" + x}, {Lang: "x<y", Text: "O" + x}},
			CoverURL: "javascript:alert(1)",
			Fields:   map[string]plugin.FieldValue{"type": {Text: "E" + x}, "synopsis": {Text: "S" + x + "\nline2"}, "home": {Text: "javascript:alert(2)"}}},
		ExternalURL: "javascript:alert(3)",
		Relations:   []plugin.Relation{{Kind: "zz" + x, Item: plugin.ItemSummary{Source: "tvdata", ID: `"><x`, Kind: "show", Titles: ttl("R" + x)}}},
		Genres:      []string{"G" + x},
		Tags:        []plugin.Tag{{Name: "Tg" + x, Spoiler: true}},
	}
	e.tv.Data = d
	e.tv.Credits = []plugin.Credit{{Role: "actor", Detail: "D" + x, Person: plugin.PersonRef{ID: "p", Name: "P" + x, URL: "javascript:alert(4)", ImageURL: "javascript:alert(5)"},
		With: &plugin.PersonRef{ID: "w", Name: "W" + x}, WithRole: "WR" + x}}
	rec := e.get("/tv/tvdata/1")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	if strings.Contains(b, "<script>") || strings.Contains(b, `href="javascript`) || strings.Contains(b, `src="javascript`) {
		t.Fatalf("hostile markup rendered:\n%s", b)
	}
	// a non-https url field value is shown as text, never as a link
	mustContain(t, b, "javascript:alert(2)")
	mustNotContain(t, b, "javascript:alert(1)", "javascript:alert(3)", "javascript:alert(4)", "javascript:alert(5)", "javascript:alert(6)")
	for _, p := range []string{"T", "O", "E", "S", "z", "R", "G", "Tg", "D", "P", "W", "WR"} {
		mustContain(t, b, p+"&lt;script&gt;alert(1)&lt;/script&gt;")
	}
	mustContain(t, b, "Home&lt;b&gt;", "x&lt;y", `href="/tv/tvdata/%22%3E%3Cx"`)
}

func TestHostileNavMarkerIsEscaped(t *testing.T) {
	e := newEnv(t)
	a := e.fp.anime
	a.Titles = []anilist.Title{{Lang: "ja", Text: "T" + navMarker}}
	a.Synopsis = "S" + navMarker
	id, err := saveAnime(context.Background(), e.lib, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := upsertEntry(context.Background(), e.lib, listEntry{ItemID: id, Status: library.Active, Comment: "C" + navMarker}); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, "T&lt;!--allister-nav--&gt;", "S&lt;!--allister-nav--&gt;", "C&lt;!--allister-nav--&gt;")
	if n := strings.Count(b, "<span>Settings</span></a>"); n != 1 {
		t.Fatalf("navigation rendered %d times", n)
	}
	if strings.Contains(b, navMarker) {
		t.Fatal("raw marker left in the page")
	}
}

func TestSavedAnimeRelationKeepsExternalURL(t *testing.T) {
	e := newEnv(t)
	e.saved(t)
	b := e.get("/anime/anilist/1535").Body.String()
	mustContain(t, b, `href="/anime/anilist/2000"`, `href="https://anilist.co/manga/30021"`, "Manga Title")
}

// withComicKind adds a second kind "comic" (a copy of book) to the book fixture.
func withComicKind(book, _ *plugintest.Plugin) {
	comic := book.M.Kinds[0]
	comic.Key, comic.Label, comic.LabelPlural = "comic", "Comic", "Comics"
	comic.Sections = append([]plugin.SectionDef(nil), comic.Sections...)
	book.M.Kinds = append(book.M.Kinds, comic)
}

func TestOverviewSeveralSavedKindsNeedKind(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	e.saveFixture(t, "book", "book", bookItem("7", "The Novel"))
	// one saved kind: no ?kind= needed
	mustContain(t, e.get("/book/books/7").Body.String(), "The Novel</h1>")
	cd := bookItem("7", "The Comic")
	cd.Kind = "comic"
	e.saveFixture(t, "book", "comic", cd)
	rec := e.get("/book/books/7")
	if rec.Code != 404 {
		t.Fatalf("several saved kinds without ?kind=: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Several kinds share this id: add ?kind=…")
	mustNotContain(t, rec.Body.String(), "The Novel", "The Comic")
	mustContain(t, e.get("/book/books/7?kind=book").Body.String(), "The Novel</h1>")
	mustContain(t, e.get("/book/books/7?kind=comic").Body.String(), "The Comic</h1>")
}

func TestFmtProgressClampsBadValues(t *testing.T) {
	count := plugin.ProgressDef{Key: "episode", Type: plugin.Count, TotalField: "episodes"}
	dur := plugin.ProgressDef{Key: "position", Type: plugin.Duration}
	pct := plugin.ProgressDef{Key: "percent", Type: plugin.Percent}
	fields := map[string]plugin.FieldValue{"episodes": {Num: num(10)}}
	for _, tc := range []struct {
		def  plugin.ProgressDef
		v    float64
		want string
	}{
		{dur, -59, "0:00:00"},
		{dur, 3723, "1:02:03"},
		{dur, 1e12, "999:59:59"},
		{dur, 999*3600 + 59*60 + 59, "999:59:59"},
		{count, -3, "0 / 10"},
		{pct, -5, "0%"},
		{count, 4, "4 / 10"},
	} {
		if got := fmtProgress(tc.def, tc.v, fields); got != tc.want {
			t.Errorf("fmtProgress(%s, %v) = %q, want %q", tc.def.Type, tc.v, got, tc.want)
		}
	}
}

func TestTitlesBlockStartsWithResolvedTitle(t *testing.T) {
	e := newFixtureEnv(t)
	d := tvShow("1", "")
	// the preferred language (ja) is missing: ResolveTitle falls back to en
	d.Titles = []plugin.Title{{Lang: "original", Text: "Orig Title"}, {Lang: "en", Text: "Eng Title"}}
	e.saveFixture(t, "tv", "show", d)
	b := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, b, "Eng Title</h1>")
	sec := b[strings.Index(b, "</h1>"):]
	if i, j := strings.Index(sec, "Eng Title"), strings.Index(sec, "Orig Title"); i < 0 || j < 0 || i > j {
		t.Fatalf("titles block order: en at %d, original at %d", i, j)
	}
}

func TestFieldUnitRendersAfterNumbers(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		f := &book.M.Kinds[0].Fields[1] // pages
		f.Unit, f.Show = "pages", true
	})
	d := bookItem("1", "Unit Book")
	d.Fields["pages"] = plugin.FieldValue{Num: num(320)}
	e.saveFixture(t, "book", "book", d)
	mustContain(t, e.get("/book/books/1").Body.String(), "Pages:</dt> <dd class=\"inline\">320 pages</dd>")
	e.book.Summaries = []plugin.ItemSummary{d.ItemSummary}
	mustContain(t, e.get("/book/search?q=u").Body.String(), "Ursula K. Le Guin · 320 pages")
}

// A source that answered with data the core cannot use is a bad gateway: the
// readable page with the retry link, not a bare internal error.
func TestOverviewBadSourceAnswerIs502(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpDetails: fmt.Errorf("tvdata: malformed: %w", plugin.ErrBadResponse)}
	rec := e.get("/tv/tvdata/1")
	if rec.Code != 502 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Fake TV returned an answer we could not use. Please try again later.", `href="/tv/tvdata/1"`, "Try again")
	// an error of our own code stays an internal error
	e.tv.Errs = map[string]error{plugintest.OpDetails: errors.New("disk on fire")}
	if rec := e.get("/tv/tvdata/1"); rec.Code != 500 {
		t.Fatalf("plain error: %d", rec.Code)
	}
}

// --- update from source (POST /{source}/{id}/refresh) ---

func TestItemRefreshAddsMissingFields(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	if _, ok := e.item(t, id).Fields["duration"]; ok {
		t.Fatal("fixture already has a duration")
	}
	e.fp.anime.Duration = ip(24)
	rec := e.do("POST", "/anime/anilist/1535/refresh", nil, false)
	if rec.Code != 303 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/anime/anilist/1535?updated=1" {
		t.Fatalf("location %q", loc)
	}
	if n := e.item(t, id).Fields["duration"].Num; n == nil || *n != 24 {
		t.Fatalf("duration %v", n)
	}
	if e.fp.animeCalls != 1 {
		t.Fatalf("source calls %d", e.fp.animeCalls)
	}
	mustContain(t, e.get("/anime/anilist/1535?updated=1").Body.String(), "1 field added from AniList.")
	mustNotContain(t, e.get("/anime/anilist/1535").Body.String(), "added from AniList")
}

func TestItemRefreshKeepsExistingFields(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	before := e.item(t, id)
	e.fp.anime.Duration = ip(24)
	e.fp.anime.Studio = "CHANGED"
	e.fp.anime.Synopsis = "CHANGED"
	e.fp.anime.StartDate, e.fp.anime.EndDate = nil, nil
	rec := e.do("POST", "/anime/anilist/1535/refresh", nil, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime/anilist/1535?updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	it := e.item(t, id)
	if it.Fields["studio"].Text != before.Fields["studio"].Text || it.Fields["synopsis"].Text != before.Fields["synopsis"].Text {
		t.Fatalf("existing fields overwritten:\n%+v\n%+v", before.Fields, it.Fields)
	}
	if it.Fields["started_on"].Date == nil || !it.Fields["started_on"].Date.Equal(*before.Fields["started_on"].Date) {
		t.Fatalf("started_on overwritten: %+v", it.Fields["started_on"])
	}
	if n := it.Fields["duration"].Num; n == nil || *n != 24 {
		t.Fatalf("duration %v", n)
	}
	if !reflect.DeepEqual(it.Titles, before.Titles) || !reflect.DeepEqual(it.Genres, before.Genres) ||
		!reflect.DeepEqual(it.Tags, before.Tags) || it.CoverPath != before.CoverPath {
		t.Fatalf("other parts touched:\n%+v\n%+v", before, it)
	}
	// a second run finds nothing new
	rec = e.do("POST", "/anime/anilist/1535/refresh", nil, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime/anilist/1535?updated=0" {
		t.Fatalf("second run: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, e.get("/anime/anilist/1535?updated=0").Body.String(), "Nothing new; every source field is already filled.")
}

func TestItemRefreshManualItemNotFound(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/anime/anilist/1535/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("unsaved item: %d", rec.Code)
	}
	if rec := e.do("POST", "/anime/manual/77/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("unsaved manual item: %d", rec.Code)
	}
	m := anilist.Media{SearchResult: anilist.SearchResult{Source: anilist.Manual, ID: "77", Type: "OVA",
		Titles: []anilist.Title{{Lang: "en", Text: "My Manual Show"}}}}
	if _, err := saveAnime(context.Background(), e.lib, m, library.WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", "/anime/manual/77/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("saved manual item: %d", rec.Code)
	}
	if e.fp.animeCalls != 0 {
		t.Fatalf("source calls %d", e.fp.animeCalls)
	}
}

func TestItemRefreshSourceError(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.saveFixture(t, "tv", "show", tvShow("1", "Show"))
	before := dump(t, e.env)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec := e.do("POST", "/tv/tvdata/1/refresh", nil, false)
	if rec.Code != 503 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), needsKeyMessage, `href="/tv/tvdata/1"`, "Try again")
	if dump(t, e.env) != before {
		t.Fatal("changed on a source error")
	}
	if e.tv.Calls(plugintest.OpDetails) != 1 {
		t.Fatalf("source calls %d", e.tv.Calls(plugintest.OpDetails))
	}
	if e.item(t, id).Fields["duration"].Num != nil {
		t.Fatal("fields written")
	}
}

func TestItemRefreshKeepsKindParameter(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	cd := bookItem("7", "The Comic")
	cd.Kind = "comic"
	e.saveFixture(t, "book", "comic", cd)
	mustContain(t, e.get("/book/books/7?kind=comic").Body.String(), `action="/book/books/7/refresh?kind=comic"`)
	d := bookItem("7", "The Comic")
	d.Kind = "comic"
	d.Fields["isbn"] = plugin.FieldValue{Text: "978-7"}
	e.book.Data = d
	rec := e.do("POST", "/book/books/7/refresh?kind=comic", nil, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/book/books/7?kind=comic&updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, e.get("/book/books/7?kind=comic&updated=1").Body.String(), "1 field added from Fake Books.")
}

func TestItemShowsRefreshActionOnlyForSourcedItems(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Live Show")
	mustNotContain(t, e.get("/tv/tvdata/1").Body.String(), `action="/tv/tvdata/1/refresh"`, "Update from source")
	if rec := e.do("POST", "/tv/tvdata/1/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("unsaved: %d", rec.Code)
	}
	e.saveFixture(t, "tv", "show", tvShow("2", "Saved Show"))
	mustContain(t, e.get("/tv/tvdata/2").Body.String(), `action="/tv/tvdata/2/refresh"`, "Update from source")
	man := tvShow("3", "Hand Made")
	man.Source = "manual"
	e.saveFixture(t, "tv", "show", man)
	mustNotContain(t, e.get("/tv/manual/3").Body.String(), "Update from source")
	if rec := e.do("POST", "/tv/manual/3/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("manual: %d", rec.Code)
	}
	if rec := e.do("POST", "/tv/other/2/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("undeclared source: %d", rec.Code)
	}
	// a declared source the plugin does not provide has no action either
	e2 := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	e2.saveFixture(t, "tv", "show", tvShow("4", "No Source"))
	mustNotContain(t, e2.get("/tv/tvdata/4").Body.String(), "Update from source")
	if rec := e2.do("POST", "/tv/tvdata/4/refresh", nil, false); rec.Code != 404 {
		t.Fatalf("unimplemented source: %d", rec.Code)
	}
}
