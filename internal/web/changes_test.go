package web

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// --- update from source (GET|POST /{source}/{id}/refresh) ---

// refreshEnv saves show 1 (type series, year 2019, no episodes, no genres)
// and lets the source answer year 2020, 10 episodes and genre Drama: the
// type is the same, so three parts differ (year, episodes, genres).
func refreshEnv(t *testing.T, mod ...func(book, tv *plugintest.Plugin)) (*fixtureEnv, int64) {
	t.Helper()
	e := newFixtureEnv(t, mod...)
	id := e.saveFixture(t, "tv", "show", tvShow("1", "Show"), library.WithSyncState("done"))
	d := tvShow("1", "Show")
	d.Fields["year"] = plugin.FieldValue{Num: num(2020)}
	d.Fields["episodes"] = plugin.FieldValue{Num: num(10)}
	d.Genres = []string{"Drama"}
	e.tv.Data = d
	return e, id
}

func TestRefreshShowsOnlyDifferingRows(t *testing.T) {
	e, _ := refreshEnv(t, func(_, tv *plugintest.Plugin) { tv.M.Kinds[0].Fields[1].Unit = "eps" })
	before := dump(t, e.env)
	rec := e.get("/tv/tvdata/1/refresh")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	// a stored value starts unticked, an empty stored side starts ticked
	mustContain(t, b, `value="field.year" class=`, `value="field.episodes" checked`, `value="genres" checked`,
		`action="/tv/tvdata/1/refresh"`, "Apply selected", "2019", "2020", "10 eps", "Drama", `href="/tv/tvdata/1"`)
	mustNotContain(t, b, `value="field.type"`, `value="titles"`, `value="credits"`, "up to date")
	if dump(t, e.env) != before {
		t.Fatal("GET changed the library")
	}
	if n := e.tv.Calls(plugintest.OpDetails); n != 1 {
		t.Fatalf("details calls %d", n)
	}
}

func TestRefreshAppliesOnlyPostedKeys(t *testing.T) {
	e, id := refreshEnv(t)
	// the page was shown with year 2020; the source now says 2021, and the
	// form carries a value of its own: only the source's current answer counts
	if rec := e.get("/tv/tvdata/1/refresh"); rec.Code != 200 {
		t.Fatalf("GET %d", rec.Code)
	}
	e.tv.Data.Fields["year"] = plugin.FieldValue{Num: num(2021)}
	form := url.Values{"key": {"field.year", "genres", "nope"}, "year": {"1999"}, "field.year": {"1999"}}
	rec := e.do("POST", "/tv/tvdata/1/refresh", form, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1?updated=2" {
		t.Fatalf("got %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if n := e.tv.Calls(plugintest.OpDetails); n != 2 {
		t.Fatalf("POST did not re-ask the source: details calls %d", n)
	}
	it := e.item(t, id)
	if y := it.Fields["year"].Num; y == nil || *y != 2021 {
		t.Fatalf("year %v", y)
	}
	if !reflect.DeepEqual(it.Genres, []string{"Drama"}) {
		t.Fatalf("genres %v", it.Genres)
	}
	if _, ok := it.Fields["episodes"]; ok {
		t.Fatal("episodes written without being picked")
	}
	if it.SyncState != "done" {
		t.Fatalf("sync state %q", it.SyncState)
	}
	mustContain(t, e.get("/tv/tvdata/1?updated=2").Body.String(), "Updated 2 values from Fake TV.")
	if len(e.jobs.got) != 0 {
		t.Fatalf("enqueued %+v", e.jobs.got)
	}
}

func TestRefreshUpToDate(t *testing.T) {
	e := newFixtureEnv(t)
	e.saveFixture(t, "tv", "show", tvShow("1", "Show"))
	e.tv.Data = tvShow("1", "Show")
	rec := e.get("/tv/tvdata/1/refresh")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	mustContain(t, b, "This item is up to date with Fake TV.", `href="/tv/tvdata/1"`)
	mustNotContain(t, b, `action="/tv/tvdata/1/refresh"`, "Apply selected", `name="key"`)
	// posting nothing applies nothing
	rec = e.do("POST", "/tv/tvdata/1/refresh", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1?updated=0" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, e.get("/tv/tvdata/1?updated=0").Body.String(), "Nothing was updated.")
}

func TestRefreshManualAndUnsavedItemsNotFound(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Live Show")
	man := tvShow("3", "Hand Made")
	man.Source = "manual"
	e.saveFixture(t, "tv", "show", man)
	for _, target := range []string{"/tv/tvdata/1/refresh", "/tv/manual/3/refresh", "/tv/manual/77/refresh", "/tv/other/1/refresh"} {
		for _, method := range []string{"GET", "POST"} {
			if rec := e.do(method, target, nil, false); rec.Code != 404 {
				t.Fatalf("%s %s: %d", method, target, rec.Code)
			}
		}
	}
	if n := e.tv.Calls(plugintest.OpDetails); n != 0 {
		t.Fatalf("source calls %d", n)
	}
	// a declared source the plugin does not provide has no page either
	e2 := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	e2.saveFixture(t, "tv", "show", tvShow("4", "No Source"))
	if rec := e2.get("/tv/tvdata/4/refresh"); rec.Code != 404 {
		t.Fatalf("unimplemented source: %d", rec.Code)
	}
}

func TestRefreshSourceError(t *testing.T) {
	e, _ := refreshEnv(t)
	before := dump(t, e.env)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	for _, method := range []string{"GET", "POST"} {
		rec := e.do(method, "/tv/tvdata/1/refresh", url.Values{"key": {"field.year"}}, false)
		if rec.Code != 503 {
			t.Fatalf("%s: got %d: %s", method, rec.Code, rec.Body.String())
		}
		mustContain(t, rec.Body.String(), needsKeyMessage, `href="/tv/tvdata/1/refresh"`, "Try again")
	}
	if dump(t, e.env) != before {
		t.Fatal("changed on a source error")
	}
	// an answer the library cannot store is a bad gateway
	e.tv.Errs = nil
	e.tv.Data.Fields["episodes"] = plugin.FieldValue{Num: num(-5)}
	if rec := e.get("/tv/tvdata/1/refresh"); rec.Code != 502 {
		t.Fatalf("invalid answer: %d", rec.Code)
	}
	if rec := e.do("POST", "/tv/tvdata/1/refresh", url.Values{"key": {"field.year"}}, false); rec.Code != 502 {
		t.Fatalf("invalid answer POST: %d", rec.Code)
	}
	if dump(t, e.env) != before {
		t.Fatal("changed on an invalid answer")
	}
}

func TestRefreshCredits(t *testing.T) {
	e, id := refreshEnv(t)
	e.tv.Credits = []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Ann Actor"}}}
	b := e.get("/tv/tvdata/1/refresh").Body.String()
	mustContain(t, b, `value="credits" checked`, "Ann Actor")
	// a failing People call still shows the page, without a credits row
	e.tv.Errs = map[string]error{plugintest.OpPeople: plugin.ErrUnavailable}
	rec := e.get("/tv/tvdata/1/refresh")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `value="field.year"`)
	mustNotContain(t, rec.Body.String(), `value="credits"`, "Ann Actor")
	rec = e.do("POST", "/tv/tvdata/1/refresh", url.Values{"key": {"credits", "field.year"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1?updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cs, err := e.lib.Credits(context.Background(), id); err != nil || len(cs) != 0 {
		t.Fatalf("credits %+v %v", cs, err)
	}
	// with People answering, credits are applied when picked
	e.tv.Errs = nil
	rec = e.do("POST", "/tv/tvdata/1/refresh", url.Values{"key": {"credits"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1?updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cs, err := e.lib.Credits(context.Background(), id); err != nil || len(cs) != 1 || cs[0].Person.Name != "Ann Actor" {
		t.Fatalf("credits %+v %v", cs, err)
	}
}

// A kind without a people section never asks for People.
func TestRefreshWithoutPeopleSectionSkipsPeople(t *testing.T) {
	e, _ := refreshEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		var secs []plugin.SectionDef
		for _, s := range k.Sections {
			if s.Type != plugin.SectionPeople {
				secs = append(secs, s)
			}
		}
		k.Sections = secs
	})
	e.tv.Credits = []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Ann Actor"}}}
	mustNotContain(t, e.get("/tv/tvdata/1/refresh").Body.String(), `value="credits"`)
	if n := e.tv.Calls(plugintest.OpPeople); n != 0 {
		t.Fatalf("people calls %d", n)
	}
}

func TestRefreshCoverEnqueuesCoverJob(t *testing.T) {
	e, id := refreshEnv(t)
	e.tv.Data.CoverURL = "https://img.example/new.jpg"
	mustContain(t, e.get("/tv/tvdata/1/refresh").Body.String(), `value="cover" checked`, "https://img.example/new.jpg")
	rec := e.do("POST", "/tv/tvdata/1/refresh", url.Values{"key": {"cover"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv/tvdata/1?updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := e.item(t, id).CoverPath; got != "https://img.example/new.jpg" {
		t.Fatalf("cover %q", got)
	}
	want := []enqueued{{jobs.KindSyncItem, jobs.SyncPayload{ItemID: id, PluginID: "tv", CoverOnly: true}}}
	if !reflect.DeepEqual(e.jobs.got, want) {
		t.Fatalf("enqueued %+v", e.jobs.got)
	}
}

func TestRefreshLongTextIsFolded(t *testing.T) {
	e := newFixtureEnv(t)
	e.saveFixture(t, "book", "book", bookItem("1", "Book"))
	d := bookItem("1", "Book")
	long := strings.Repeat("word ", 100) + "THE END"
	d.Fields["author"] = plugin.FieldValue{Text: long}
	e.book.Data = d
	b := e.get("/book/books/1/refresh").Body.String()
	mustContain(t, b, "<details", "Show all", "THE END", "Ursula K. Le Guin")
	if i, j := strings.Index(b, "<details"), strings.Index(b, "THE END"); i < 0 || j < i {
		t.Fatal("the full text is not inside the details")
	}
}

func TestRefreshKeepsKindParameter(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	cd := bookItem("7", "The Comic")
	cd.Kind = "comic"
	e.saveFixture(t, "book", "comic", cd)
	mustContain(t, e.get("/book/books/7?kind=comic").Body.String(), `href="/book/books/7/refresh?kind=comic"`)
	d := bookItem("7", "The Comic")
	d.Kind = "comic"
	d.Fields["isbn"] = plugin.FieldValue{Text: "978-7"}
	e.book.Data = d
	mustContain(t, e.get("/book/books/7/refresh?kind=comic").Body.String(),
		`action="/book/books/7/refresh?kind=comic"`, `value="field.isbn" checked`, `href="/book/books/7?kind=comic"`)
	rec := e.do("POST", "/book/books/7/refresh?kind=comic", url.Values{"key": {"field.isbn"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/book/books/7?kind=comic&updated=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, e.get("/book/books/7?kind=comic&updated=1").Body.String(), "Updated 1 value from Fake Books.")
}

func TestItemShowsUpdateLinkOnlyForSourcedItems(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Data = tvShow("1", "Live Show")
	mustNotContain(t, e.get("/tv/tvdata/1").Body.String(), "/tv/tvdata/1/refresh", "Update from source")
	e.saveFixture(t, "tv", "show", tvShow("2", "Saved Show"))
	b := e.get("/tv/tvdata/2").Body.String()
	mustContain(t, b, `href="/tv/tvdata/2/refresh"`, "Update from source")
	mustNotContain(t, b, `action="/tv/tvdata/2/refresh"`)
	man := tvShow("3", "Hand Made")
	man.Source = "manual"
	e.saveFixture(t, "tv", "show", man)
	mustNotContain(t, e.get("/tv/manual/3").Body.String(), "Update from source")
	e2 := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.Sources = nil })
	e2.saveFixture(t, "tv", "show", tvShow("4", "No Source"))
	mustNotContain(t, e2.get("/tv/tvdata/4").Body.String(), "Update from source")
}

func TestUpdateNotice(t *testing.T) {
	for n, want := range map[int]string{0: "Nothing was updated.", 1: "Updated 1 value from X.", 3: "Updated 3 values from X."} {
		if got := updateNotice(n, "X"); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
}
