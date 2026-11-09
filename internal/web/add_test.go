package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

// okAddForm is a valid add form. The status is a core class (the generic
// add flow never accepts the legacy anime values watching/plan_to_watch);
// progress uses the shorthand that maps to the primary progress key.
func okAddForm() url.Values {
	return url.Values{"status": {"active"}, "rating": {"8"}, "progress": {"3"}, "started_on": {"2024-01-02"}, "finished_on": {""}, "comment": {"nice"}}
}

func TestAddDialogPartialVsFull(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/anime/anilist/1535/add", nil, true)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, `<form`, `name="status"`, `value="planned" selected`, ">Plan to watch<", `type="date"`, `name="started_on"`, `name="finished_on"`,
		`type="number" min="0" max="10" step="1"`, `placeholder="0–10"`, `max="37"`, "/ 37", "Cancel", `hx-post="/anime/anilist/1535/add"`)
	mustNotContain(t, b, "<html", "Light Yagami")
	if rec.Header().Get("Vary") == "" {
		t.Fatal("no Vary")
	}
	full := e.get("/anime/anilist/1535/add")
	if full.Code != 200 {
		t.Fatalf("got %d", full.Code)
	}
	mustContain(t, full.Body.String(), "<html", "Light Yagami", `name="status"`, `action="/anime/anilist/1535/add"`)
}

func TestAddCreatesEntryAndJob(t *testing.T) {
	e := newEnv(t)
	rec := e.post(okAddForm(), true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/anime" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
	ctx := context.Background()
	sv, _ := e.st.FindBySource(ctx, provider.AniList, "1535")
	if sv == nil {
		t.Fatal("not saved")
	}
	en, _ := e.st.GetListEntry(ctx, sv.ID)
	if en == nil || en.Status != store.Watching || en.Rating == nil || *en.Rating != 8 || en.Progress != 3 ||
		en.StartedOn == nil || en.StartedOn.Format("2006-01-02") != "2024-01-02" || en.FinishedOn != nil || en.Comment != "nice" {
		t.Fatalf("%+v", en)
	}
	if len(e.jobs.got) != 1 || e.jobs.got[0].Kind != jobs.KindSyncAnime || e.jobs.got[0].Payload != (jobs.SyncPayload{AnimeID: sv.ID, PluginID: "anime"}) {
		t.Fatalf("jobs %+v", e.jobs.got)
	}
}

func TestAddPlainPostRedirects303(t *testing.T) {
	e := newEnv(t)
	rec := e.post(okAddForm(), false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/anime" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
}

func TestAddRatingBlankAndZero(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want *int
	}{{"", nil}, {"0", ip(0)}} {
		e := newEnv(t)
		f := okAddForm()
		f.Set("rating", tc.in)
		if rec := e.post(f, true); rec.Code != 204 {
			t.Fatalf("got %d", rec.Code)
		}
		sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535")
		en, _ := e.st.GetListEntry(context.Background(), sv.ID)
		if (tc.want == nil) != (en.Rating == nil) || (tc.want != nil && *tc.want != *en.Rating) {
			t.Fatalf("%q -> %v", tc.in, en.Rating)
		}
	}
}

func TestAddRejectsInvalidRating(t *testing.T) {
	for _, bad := range []string{"11", "abc", "-1", "5.5"} {
		e := newEnv(t)
		f := okAddForm()
		f.Set("rating", bad)
		rec := e.post(f, true)
		if rec.Code != 422 {
			t.Fatalf("%q: got %d", bad, rec.Code)
		}
		mustContain(t, rec.Body.String(), "Rating", `value="`+bad+`"`, `value="nice"`[:0]+"nice")
		assertNothingSaved(t, e)
	}
}

func assertNothingSaved(t *testing.T, e *env) {
	t.Helper()
	if sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535"); sv != nil {
		t.Fatal("anime saved")
	}
	if len(e.jobs.got) != 0 {
		t.Fatal("job enqueued")
	}
}

func TestAddOtherValidationErrors(t *testing.T) {
	cases := map[string]func(url.Values){
		"progress over total": func(f url.Values) { f.Set("progress", "38") },
		"progress negative":   func(f url.Values) { f.Set("progress", "-1") },
		"progress text":       func(f url.Values) { f.Set("progress", "x") },
		"finish before start": func(f url.Values) { f.Set("started_on", "2024-02-01"); f.Set("finished_on", "2024-01-01") },
		"bad date":            func(f url.Values) { f.Set("started_on", "01/02/2024") },
		"unknown status":      func(f url.Values) { f.Set("status", "nope") },
		"missing status":      func(f url.Values) { f.Del("status") },
		"legacy status alias": func(f url.Values) { f.Set("status", "watching") },
	}
	for name, mod := range cases {
		e := newEnv(t)
		f := okAddForm()
		mod(f)
		rec := e.post(f, true)
		if rec.Code != 422 {
			t.Errorf("%s: got %d", name, rec.Code)
		}
		mustContain(t, rec.Body.String(), "<form")
		assertNothingSaved(t, e)
	}
}

func TestAddBlankProgressAndDatesAndCommentCap(t *testing.T) {
	e := newEnv(t)
	f := url.Values{"status": {"planned"}, "progress": {""}, "comment": {strings.Repeat("あ", 2500)}}
	if rec := e.post(f, true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535")
	en, _ := e.st.GetListEntry(context.Background(), sv.ID)
	if en.Progress != 0 || en.StartedOn != nil || len([]rune(en.Comment)) != 2000 {
		t.Fatalf("%+v", en)
	}
}

func TestAddDuplicateIsBlocked(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(okAddForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	rec := e.post(okAddForm(), true)
	if rec.Code != 409 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "already in your list", `href="/anime/anilist/1535"`)
	if len(e.jobs.got) != 1 {
		t.Fatalf("jobs %d", len(e.jobs.got))
	}
}

func TestAddSavedWithoutEntryGetsEntryNoJob(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	rec := e.post(okAddForm(), true)
	if rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	en, _ := e.st.GetListEntry(context.Background(), id)
	if en == nil || en.Status != store.Watching {
		t.Fatalf("%+v", en)
	}
	if len(e.jobs.got) != 0 {
		t.Fatal("job enqueued for existing anime")
	}
	if e.fp.animeCalls != 0 {
		t.Fatal("provider called")
	}
}

func TestAddSavedWithoutEntryValidatesAgainstStoredEpisodes(t *testing.T) {
	e := newEnv(t)
	id := e.saved(t)
	f := okAddForm()
	f.Set("progress", "99")
	if rec := e.post(f, true); rec.Code != 422 {
		t.Fatalf("got %d", rec.Code)
	}
	if en, _ := e.st.GetListEntry(context.Background(), id); en != nil {
		t.Fatal("entry created")
	}
}

func TestAddWithProviderDownUsesAlreadyFetchedDetails(t *testing.T) {
	e := newEnv(t)
	if rec := e.get("/anime/anilist/1535"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	e.fp.animeErr = provider.ErrUnavailable
	before := e.fp.animeCalls
	rec := e.post(okAddForm(), true)
	if rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	if e.fp.animeCalls != before {
		t.Fatal("provider was called again")
	}
	if sv, _ := e.st.FindBySource(context.Background(), provider.AniList, "1535"); sv == nil {
		t.Fatal("not saved")
	}
}

func TestAddCacheExpiresAfterTTL(t *testing.T) {
	e := newEnv(t)
	e.get("/anime/anilist/1535")
	*e.now = e.now.Add(11 * time.Minute)
	e.fp.animeErr = provider.ErrUnavailable
	rec := e.post(okAddForm(), true)
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<form", `value="nice"`[:0]+"nice", "AniList")
	assertNothingSaved(t, e)
	// within the TTL it is served from cache
	e.fp.animeErr = nil
	e.get("/anime/anilist/1535")
	*e.now = e.now.Add(9 * time.Minute)
	e.fp.animeErr = provider.ErrUnavailable
	if rec := e.post(okAddForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddCacheMissFetchesOnceAndNotFoundIs404(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(okAddForm(), true); rec.Code != 204 || e.fp.animeCalls != 1 {
		t.Fatalf("got %d calls %d", rec.Code, e.fp.animeCalls)
	}
	e = newEnv(t)
	e.fp.animeErr = provider.ErrNotFound
	if rec := e.post(okAddForm(), true); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddEnqueueFailureStillSucceeds(t *testing.T) {
	e := newEnv(t)
	e.jobs.fail = errors.New("boom")
	if rec := e.post(okAddForm(), true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAddManualNotSavedIs404(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/anime/manual/5/add", okAddForm(), true); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

// moviePlugin is a movie-like fixture (id "movie", kind "movie"): a single
// duration track and no primary (+1) key.
func moviePlugin() *plugintest.Plugin {
	p := plugintest.TVPlugin(plugintest.WithID("movie"))
	k := &p.M.Kinds[0]
	k.Key, k.Label, k.LabelPlural = "movie", "Movie", "Movies"
	k.Statuses = plugin.StatusLabels{Planned: "Want to watch", Active: "Watching", Paused: "Paused", Completed: "Seen", Dropped: "Dropped"}
	k.Progress = plugin.ProgressSpec{
		Eligible:     []plugin.ProgressDef{{Key: "position", Type: plugin.Duration, Label: "Position"}},
		Defaults:     []string{"position"},
		Combinations: [][]string{{"position"}},
	}
	p.M.Sources = []plugin.SourceInfo{{ID: "moviedata", Name: "Fake Movies", RequestsPerMinute: 30}}
	p.Sources = map[string]plugin.Source{"moviedata": &plugintest.FakeSource{Data: plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "moviedata", ID: "1", Kind: "movie", Titles: ttl("A Movie")}}}}
	p.M.Importers, p.Importers = nil, nil
	return p
}

// addEnv is a fixture env (book, tv) plus the movie fixture, with canned
// details: book "42" has 300 pages, tv "5" 10 episodes.
type addEnv struct {
	*env
	book, tv, movie *plugintest.FakeSource
}

func newAddEnv(t *testing.T, mod ...func(book, tv *plugintest.Plugin)) *addEnv {
	t.Helper()
	book, tv, movie := plugintest.BookPlugin(), plugintest.TVPlugin(), moviePlugin()
	for _, m := range mod {
		m(book, tv)
	}
	ae := &addEnv{}
	ae.book, _ = book.Sources["books"].(*plugintest.FakeSource)
	ae.tv, _ = tv.Sources["tvdata"].(*plugintest.FakeSource)
	ae.movie, _ = movie.Sources["moviedata"].(*plugintest.FakeSource)
	ae.book.Data = bookItem("42", "Earthsea")
	ae.book.Data.Fields["pages"] = plugin.FieldValue{Num: num(300)}
	ae.tv.Data = tvShow("5", "The Show")
	ae.tv.Data.Fields["episodes"] = plugin.FieldValue{Num: num(10)}
	ae.env = newEnvWith(t, withPlugins(book, tv, movie))
	return ae
}

// savedItems returns the saved items of plugin/source/id.
func (e *env) savedItems(t *testing.T, pluginID, source, id string) []library.Item {
	t.Helper()
	items, err := e.lib.ItemByPluginSource(context.Background(), pluginID, source, id)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// savedEntry returns the entry of the one saved item of plugin/source/id.
func (e *env) savedEntry(t *testing.T, pluginID, source, id string) *library.Entry {
	t.Helper()
	items := e.savedItems(t, pluginID, source, id)
	if len(items) != 1 {
		t.Fatalf("%d saved items", len(items))
	}
	en, err := e.lib.Entry(context.Background(), items[0].ID)
	if err != nil || en == nil {
		t.Fatalf("entry %+v %v", en, err)
	}
	return en
}

func (e *env) assertNothingSavedFor(t *testing.T, pluginID, source, id string) {
	t.Helper()
	if items := e.savedItems(t, pluginID, source, id); len(items) != 0 {
		t.Fatalf("saved %+v", items)
	}
	if len(e.jobs.got) != 0 {
		t.Fatal("job enqueued")
	}
}

func TestAddDialogShowsDefaultProgressInputs(t *testing.T) {
	e := newAddEnv(t)
	b := e.do("GET", "/book/books/42/add", nil, true).Body.String()
	mustContain(t, b, `name="progress.pages"`, `max="300"`, "/ 300", "Page", `hx-post="/book/books/42/add"`)
	mustNotContain(t, b, `name="progress.percent"`, `name="progress"`, "<html")
	b = e.do("GET", "/tv/tvdata/5/add", nil, true).Body.String()
	mustContain(t, b, `name="progress.episode"`, `max="10"`, "/ 10", "Episode")
	mustNotContain(t, b, `name="progress.season"`, `name="progress.position"`)
	// unknown total: no "/ total" and no max
	delete(e.tv.Data.Fields, "episodes")
	b = e.do("GET", "/tv/tvdata/6/add", nil, true).Body.String()
	mustContain(t, b, `name="progress.episode"`)
	mustNotContain(t, b, "/ 10", `max="10" step="1" name="progress.episode"`, "/ ?")
	// duration: a text input taking h:mm:ss
	b = e.do("GET", "/movie/moviedata/1/add", nil, true).Body.String()
	mustContain(t, b, `name="progress.position"`, `type="text"`, `placeholder="h:mm:ss"`, "Position")
	// percent: 0 to 100
	pe := newAddEnv(t, func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Progress.Defaults = []string{"percent"} })
	b = pe.do("GET", "/book/books/42/add", nil, true).Body.String()
	mustContain(t, b, `name="progress.percent"`, `min="0" max="100"`)
	mustNotContain(t, b, `name="progress.pages"`)
}

func TestAddStatusSelectShowsPluginLabels(t *testing.T) {
	e := newAddEnv(t)
	b := e.do("GET", "/book/books/42/add", nil, true).Body.String()
	mustContain(t, b, `<option value="planned" selected>Want to read</option>`, `<option value="active">Reading</option>`,
		`<option value="paused">Paused</option>`, `<option value="completed">Read</option>`, `<option value="dropped">Dropped</option>`)
	mustNotContain(t, b, "watching", "Plan to watch")
}

func TestAddFixtureCreatesEntryWithoutJob(t *testing.T) {
	e := newAddEnv(t)
	f := url.Values{"status": {"active"}, "rating": {"7"}, "progress.pages": {"12"}, "started_on": {"2024-03-05"}, "comment": {"  good  "}}
	rec := e.do("POST", "/book/books/42/add", f, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/book" {
		t.Fatalf("got %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	en := e.savedEntry(t, "book", "books", "42")
	if en.Status != library.Active || en.Rating == nil || *en.Rating != 7 || en.Progress["pages"] != 12 || en.Comment != "good" ||
		en.StartedOn == nil || !en.StartedOn.Equal(time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", en)
	}
	// no sync capability registered for the fixture: no job
	if len(e.jobs.got) != 0 {
		t.Fatalf("jobs %+v", e.jobs.got)
	}
	if rec := e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"planned"}}, false); rec.Code != 303 || rec.Header().Get("Location") != "/tv" {
		t.Fatalf("plain post: %d %v", rec.Code, rec.Header())
	}
}

func TestAddProgressShorthandMapsToPrimaryKey(t *testing.T) {
	e := newAddEnv(t)
	if rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "progress": {"12"}}, true); rec.Code != 204 {
		t.Fatalf("book: %d", rec.Code)
	}
	if en := e.savedEntry(t, "book", "books", "42"); en.Progress["pages"] != 12 {
		t.Fatalf("%+v", en.Progress)
	}
	if rec := e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"active"}, "progress": {"3"}}, true); rec.Code != 204 {
		t.Fatalf("tv: %d", rec.Code)
	}
	if en := e.savedEntry(t, "tv", "tvdata", "5"); en.Progress["episode"] != 3 {
		t.Fatalf("%+v", en.Progress)
	}
	// a kind without a primary key has no shorthand
	rec := e.do("POST", "/movie/moviedata/1/add", url.Values{"status": {"active"}, "progress": {"3"}}, true)
	if rec.Code != 422 {
		t.Fatalf("movie: %d", rec.Code)
	}
	e.assertNothingSavedFor(t, "movie", "moviedata", "1")
}

func TestAddParsesDurationInput(t *testing.T) {
	for in, want := range map[string]float64{"1:02:03": 3723, "12:34": 754, "90": 90, "0:00": 0, "100:00:00": 360000} {
		e := newAddEnv(t)
		rec := e.do("POST", "/movie/moviedata/1/add", url.Values{"status": {"active"}, "progress.position": {in}}, true)
		if rec.Code != 204 {
			t.Fatalf("%q: %d %s", in, rec.Code, rec.Body.String())
		}
		if got := e.savedEntry(t, "movie", "moviedata", "1").Progress["position"]; got != want {
			t.Errorf("%q stored %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"-1", "99:99:99", "1.5", "1:2:3", "1:60", "a:00", "1::00", ":30", "+5", "1:00:00:00", "99999999999999"} {
		e := newAddEnv(t)
		rec := e.do("POST", "/movie/moviedata/1/add", url.Values{"status": {"active"}, "progress.position": {bad}}, true)
		if rec.Code != 422 {
			t.Errorf("%q: got %d", bad, rec.Code)
			continue
		}
		// html/template writes "+" as &#43; inside attributes
		mustContain(t, rec.Body.String(), "Position must be a time like 1:02:03", `value="`+strings.ReplaceAll(bad, "+", "&#43;")+`"`)
		e.assertNothingSavedFor(t, "movie", "moviedata", "1")
	}
}

func TestAddPercentOver100Rejected(t *testing.T) {
	pct := func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Progress.Defaults = []string{"percent"} }
	for _, bad := range []string{"101", "-1", "50.5"} {
		e := newAddEnv(t, pct)
		rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "progress.percent": {bad}}, true)
		if rec.Code != 422 {
			t.Fatalf("%q: got %d", bad, rec.Code)
		}
		mustContain(t, rec.Body.String(), "Percent must be a whole number from 0 to 100")
		e.assertNothingSavedFor(t, "book", "books", "42")
	}
	e := newAddEnv(t, pct)
	if rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"completed"}, "progress.percent": {"100"}}, true); rec.Code != 204 {
		t.Fatalf("100: %d", rec.Code)
	}
	if en := e.savedEntry(t, "book", "books", "42"); en.Progress["percent"] != 100 {
		t.Fatalf("%+v", en.Progress)
	}
}

func TestAddRejectsProgressKeyNotActive(t *testing.T) {
	for _, tc := range []struct{ path, key, plugin, source, id string }{
		{"/tv/tvdata/5/add", "progress.season", "tv", "tvdata", "5"},
		{"/tv/tvdata/5/add", "progress.position", "tv", "tvdata", "5"},
		{"/tv/tvdata/5/add", "progress.nope", "tv", "tvdata", "5"},
		{"/book/books/42/add", "progress.percent", "book", "books", "42"},
	} {
		e := newAddEnv(t)
		rec := e.do("POST", tc.path, url.Values{"status": {"active"}, tc.key: {"1"}}, true)
		if rec.Code != 422 {
			t.Errorf("%s %s: got %d", tc.path, tc.key, rec.Code)
			continue
		}
		mustContain(t, rec.Body.String(), "is not a progress of this item")
		e.assertNothingSavedFor(t, tc.plugin, tc.source, tc.id)
	}
}

func TestAddProgressAboveTotalRejectedForFixture(t *testing.T) {
	e := newAddEnv(t)
	rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "progress.pages": {"301"}, "comment": {"<b>keep</b>"}}, true)
	if rec.Code != 422 {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "above the total", `value="301"`, "&lt;b&gt;keep&lt;/b&gt;")
	mustNotContain(t, b, "<b>keep</b>")
	e.assertNothingSavedFor(t, "book", "books", "42")
}

func TestAddValidationErrorFullPageKeepsInput(t *testing.T) {
	e := newAddEnv(t)
	rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "rating": {"11"}, "comment": {"typed"}}, false)
	if rec.Code != 422 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<html", "Earthsea", `action="/book/books/42/add"`, "typed", `value="11"`, `hx-on::before-swap=`)
	e.assertNothingSavedFor(t, "book", "books", "42")
}

func TestAddExistingEntryIs409WithLink(t *testing.T) {
	e := newAddEnv(t)
	e.addFixture(t, "tv", "show", e.tv.Data, library.Entry{Status: library.Planned})
	for _, rec := range []*httptest.ResponseRecorder{
		e.do("GET", "/tv/tvdata/5/add", nil, true),
		e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"active"}}, true),
	} {
		if rec.Code != 409 {
			t.Fatalf("got %d", rec.Code)
		}
		mustContain(t, rec.Body.String(), "already in your list", `href="/tv/tvdata/5"`)
	}
	if e.tv.Calls(plugintest.OpDetails) != 0 {
		t.Fatal("source called for a saved item")
	}
}

func TestAddSavedFixtureWithoutEntryAttaches(t *testing.T) {
	e := newAddEnv(t)
	d := e.tv.Data
	id := e.saveFixture(t, "tv", "show", d, library.WithProgressKeys([]string{"season", "episode"}))
	// the item's own active combination decides (season is active here)
	rec := e.do("GET", "/tv/tvdata/5/add", nil, true)
	mustContain(t, rec.Body.String(), `name="progress.season"`, `name="progress.episode"`)
	rec = e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"active"}, "progress.season": {"2"}, "progress.episode": {"4"}}, true)
	if rec.Code != 204 {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	en, _ := e.lib.Entry(context.Background(), id)
	if en == nil || en.Progress["season"] != 2 || en.Progress["episode"] != 4 {
		t.Fatalf("%+v", en)
	}
	if e.tv.Calls(plugintest.OpDetails) != 0 || len(e.jobs.got) != 0 {
		t.Fatal("source called or job enqueued for a saved item")
	}
}

func TestAddKindSelection(t *testing.T) {
	e := newAddEnv(t, withComicKind)
	// several kinds: the URL must name the kind
	if c := e.do("GET", "/book/books/42/add", nil, true).Code; c != 404 {
		t.Fatalf("without kind: %d", c)
	}
	rec := e.do("GET", "/book/books/42/add?kind=comic", nil, true)
	mustContain(t, rec.Body.String(), `hx-post="/book/books/42/add?kind=comic"`)
	if rec := e.do("POST", "/book/books/42/add?kind=comic", url.Values{"status": {"planned"}}, true); rec.Code != 204 {
		t.Fatalf("post: %d", rec.Code)
	}
	items := e.savedItems(t, "book", "books", "42")
	if len(items) != 1 || items[0].Kind != "comic" {
		t.Fatalf("%+v", items)
	}
	if c := e.do("POST", "/book/books/42/add?kind=show", url.Values{"status": {"planned"}}, true).Code; c != 404 {
		t.Fatalf("unknown kind: %d", c)
	}
}

func TestAddProviderErrorOnCacheMissIs503Dialog(t *testing.T) {
	e := newAddEnv(t)
	e.book.Err = plugin.ErrUnavailable
	rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "comment": {"typed"}}, true)
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "<form", "typed", "Fake Books is unavailable right now")
	e.assertNothingSavedFor(t, "book", "books", "42")
	e.book.Err = plugin.ErrNotFound
	if c := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}}, true).Code; c != 404 {
		t.Fatalf("not found: %d", c)
	}
}

func TestAddConcurrentDoubleSubmit(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.post(okAddForm(), true).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case 204:
			ok++
		case 409:
		default:
			t.Fatalf("codes %v", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("codes %v", codes)
	}
	if items := e.savedItems(t, "anime", "anilist", "1535"); len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	if len(e.jobs.got) != 1 {
		t.Fatalf("jobs %+v", e.jobs.got)
	}
}

func TestAddRoutesMovedToCoreRoutes(t *testing.T) {
	e := newAddEnv(t)
	for _, id := range []string{"book", "tv", "movie", "anime"} {
		got := strings.Join(e.srv.pluginRoutes[id], "\n")
		for _, want := range []string{"GET /" + id + "/search", "GET /" + id + "/{source}/{id}/add", "POST /" + id + "/{source}/{id}/add"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q in\n%s", id, want, got)
			}
		}
	}
	e.disable(t, "book")
	if c := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}}, true).Code; c != 404 {
		t.Fatalf("disabled: %d", c)
	}
	e.assertNothingSavedFor(t, "book", "books", "42")
}

func TestAddInvalidFormMakesNoSourceCall(t *testing.T) {
	for name, f := range map[string]url.Values{
		"rating":         {"status": {"active"}, "rating": {"11"}},
		"status":         {"status": {"watching"}},
		"inactive key":   {"status": {"active"}, "progress.percent": {"5"}},
		"bad date":       {"status": {"active"}, "started_on": {"2024-13-01"}},
		"finish < start": {"status": {"active"}, "started_on": {"2024-02-01"}, "finished_on": {"2024-01-01"}},
		"nul in comment": {"status": {"active"}, "comment": {"a\x00b"}},
	} {
		e := newAddEnv(t)
		e.book.Err = plugin.ErrUnavailable // would be a 503 if the source were asked
		rec := e.do("POST", "/book/books/42/add", f, true)
		if rec.Code != 422 {
			t.Errorf("%s: got %d", name, rec.Code)
		}
		if n := e.book.Calls(plugintest.OpDetails); n != 0 {
			t.Errorf("%s: %d source calls", name, n)
		}
		e.assertNothingSavedFor(t, "book", "books", "42")
	}
}

func TestAddValidFormWithSourceDownIs503(t *testing.T) {
	e := newAddEnv(t)
	e.book.Err = plugin.ErrRateLimited
	rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "progress.pages": {"5"}}, true)
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Fake Books is rate limiting", `value="5"`)
	e.assertNothingSavedFor(t, "book", "books", "42")
}

func TestAddDialogThenPostCostsOneDetailsCall(t *testing.T) {
	e := newAddEnv(t)
	if c := e.do("GET", "/book/books/42/add", nil, true).Code; c != 200 {
		t.Fatalf("dialog: %d", c)
	}
	if rec := e.do("POST", "/book/books/42/add", url.Values{"status": {"active"}, "progress.pages": {"300"}}, true); rec.Code != 204 {
		t.Fatalf("post: %d %s", rec.Code, rec.Body.String())
	}
	if n := e.book.Calls(plugintest.OpDetails); n != 1 {
		t.Fatalf("%d Details calls, want 1", n)
	}
	// the dialog's cache entry does not stand in for the full overview
	if b := e.get("/book/books/43").Body.String(); !strings.Contains(b, "Earthsea") {
		t.Fatal("overview broken")
	}
}

func TestAddDialogCacheEntryDoesNotHideOverviewSections(t *testing.T) {
	e := newAddEnv(t)
	e.book.Credits = []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p1", Name: "Ursula Person"}}}
	e.do("GET", "/book/books/42/add", nil, true)
	// no-JS dialog page of the same item: the people section is still there
	mustContain(t, e.get("/book/books/42/add").Body.String(), "Ursula Person")
}

func TestAddConcurrentAttachToSavedItem(t *testing.T) {
	e := newEnv(t)
	e.saved(t)
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.post(okAddForm(), i%2 == 0).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case 204, 303:
			ok++
		case 409:
		default:
			t.Fatalf("codes %v", codes)
		}
	}
	if ok != 1 || len(e.jobs.got) != 0 {
		t.Fatalf("codes %v jobs %d", codes, len(e.jobs.got))
	}
}

func TestAddExplicitProgressKeyBeatsShorthand(t *testing.T) {
	e := newAddEnv(t)
	f := url.Values{"status": {"active"}, "progress": {"7"}, "progress.pages": {"12"}}
	if rec := e.do("POST", "/book/books/42/add", f, true); rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	if got := e.savedEntry(t, "book", "books", "42").Progress["pages"]; got != 12 {
		t.Fatalf("pages = %v, want 12 (explicit key wins)", got)
	}
}

func TestAddRejectsThirteenDigitCount(t *testing.T) {
	e := newAddEnv(t)
	delete(e.tv.Data.Fields, "episodes") // unbounded: only the digit limit applies
	rec := e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"active"}, "progress.episode": {"1234567890123"}}, true)
	if rec.Code != 422 {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Episode must be a whole number")
	e.assertNothingSavedFor(t, "tv", "tvdata", "5")
	if rec := e.do("POST", "/tv/tvdata/5/add", url.Values{"status": {"active"}, "progress.episode": {"123456789012"}}, true); rec.Code != 204 {
		t.Fatalf("12 digits: %d", rec.Code)
	}
}

func TestAddRepeatedParametersFirstWins(t *testing.T) {
	e := newAddEnv(t)
	f := url.Values{"status": {"completed", "dropped"}, "rating": {"6", "x"}, "progress.pages": {"9", "999"}, "comment": {"one", "two"}}
	if rec := e.do("POST", "/book/books/42/add", f, true); rec.Code != 204 {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	en := e.savedEntry(t, "book", "books", "42")
	if en.Status != library.Completed || *en.Rating != 6 || en.Progress["pages"] != 9 || en.Comment != "one" {
		t.Fatalf("%+v", en)
	}
}
