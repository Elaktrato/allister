package web

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// mergePair adds the books "Earthsea" (d1, planned, notes "mine") and "A
// Wizard of Earthsea" (d2, completed, notes "theirs", own tags Fav and Old,
// in the custom list Shelf), both by the same author.
func (fe *fixtureEnv) mergePair(t *testing.T) (int64, int64) {
	t.Helper()
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned, Notes: "mine"})
	b := fe.addFixture(t, "book", "book", bookItem("d2", "A Wizard of Earthsea"), library.Entry{Status: library.Completed, Notes: "theirs"})
	if err := fe.lib.SetEntryTags(bg, b, []string{"Fav", "Old"}); err != nil {
		t.Fatal(err)
	}
	ls, err := fe.lib.CreateList(bg, "Shelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.AddToCustomList(bg, ls.ID, b); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func mergeQuery(keep, other int64) string {
	return url.Values{"keep": {idStr(keep)}, "other": {idStr(other)}}.Encode()
}

func TestMergePageComparesTheOtherItemAndSummarisesWhatMoves(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.mergePair(t)
	before := dump(t, fe.env)
	rec := fe.get("/book/merge?" + mergeQuery(a, b))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustContain(t, body, "Merge “A Wizard of Earthsea” into “Earthsea”", "From A Wizard of Earthsea", `value="titles"`,
		`action="/book/merge"`, `name="keep" value="`+idStr(a)+`"`, `name="other" value="`+idStr(b)+`"`,
		"1 run, notes, 2 own tags, 1 custom list",
		"This removes A Wizard of Earthsea from your library; its data moves to Earthsea.",
		`href="/book/merge?keep=`+idStr(b)+`&amp;other=`+idStr(a)+`"`, "Swap", `href="/book/books/d1"`, ">Merge</button>")
	mustNotContain(t, body, `value="field.author"`, "up to date")
	if dump(t, fe.env) != before {
		t.Fatal("GET changed the library")
	}
}

func TestMergePageHidesCreditsAcrossSources(t *testing.T) {
	fe := newFixtureEnv(t)
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	m := bookItem("m1", "Earthsea")
	m.Source = manualSource
	b := fe.saveFixture(t, "book", "book", m)
	for id, name := range map[int64]string{a: "Ursula Writer", b: "Somebody"} {
		if err := fe.lib.SaveCredits(bg, id, []plugin.Credit{{Role: "author", Person: plugin.PersonRef{ID: "p", Name: name}}}); err != nil {
			t.Fatal(err)
		}
	}
	body := fe.get("/book/merge?" + mergeQuery(a, b)).Body.String()
	mustContain(t, body, "Nothing differs", ">Merge</button>", "nothing else moves")
	mustNotContain(t, body, `value="credits"`, "Somebody")
	// a manual survivor takes the other's source: the credits are offered
	body = fe.get("/book/merge?" + mergeQuery(b, a)).Body.String()
	mustContain(t, body, `value="credits"`)
}

func TestMergePostMergesAndRedirectsToTheSurvivor(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.mergePair(t)
	rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(a)}, "other": {idStr(b)}, "key": {"titles"}}, false)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/book/books/d1?") {
		t.Fatalf("got %d %q: %s", rec.Code, loc, rec.Body.String())
	}
	mustContain(t, fe.get(loc).Body.String(), "Merged A Wizard of Earthsea into this item.")
	if it := fe.item(t, a); it.Titles[0].Text != "A Wizard of Earthsea" {
		t.Fatalf("titles %+v", it.Titles)
	}
	if it, err := fe.lib.Item(bg, b); err != nil || it != nil {
		t.Fatalf("other item still there: %v %v", it, err)
	}
	// the other item's source URL leads to the survivor
	rec = fe.get("/book/books/d2")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/book/books/d1" {
		t.Fatalf("other URL: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// a merged manual item is gone for good
	m := bookItem("m1", "Earthsea")
	m.Source = manualSource
	c := fe.saveFixture(t, "book", "book", m)
	if rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(a)}, "other": {idStr(c)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("manual merge: %d", rec.Code)
	}
	if code := fe.get("/book/manual/m1").Code; code != http.StatusNotFound {
		t.Fatalf("merged manual item: %d", code)
	}
}

func TestMergeRefusalIsShownOnThePage(t *testing.T) {
	fe := newFixtureEnv(t)
	long := strings.Repeat("x", library.MaxNotesRunes-2)
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned, Notes: long})
	b := fe.addFixture(t, "book", "book", bookItem("d2", "Earthsea Two"), library.Entry{Status: library.Planned, Notes: "theirs"})
	before := dump(t, fe.env)
	rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(a)}, "other": {idStr(b)}, "key": {"titles"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "shorten one of them first", `value="titles" checked`, `action="/book/merge"`)
	if dump(t, fe.env) != before {
		t.Fatal("a refused merge changed the library")
	}
}

func TestMergeUnknownOrMismatchedItemsAre404(t *testing.T) {
	fe := newFixtureEnv(t, withComicKind)
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	cd := bookItem("c1", "Earthsea")
	cd.Kind = "comic"
	comic := fe.saveFixture(t, "book", "comic", cd)
	show := fe.saveFixture(t, "tv", "show", tvShow("t1", "Earthsea"))
	before := dump(t, fe.env)
	for name, q := range map[string]url.Values{
		"stale other":  {"keep": {idStr(a)}, "other": {"999"}},
		"stale keep":   {"keep": {"999"}, "other": {idStr(a)}},
		"same":         {"keep": {idStr(a)}, "other": {idStr(a)}},
		"bad id":       {"keep": {"x"}, "other": {idStr(a)}},
		"missing":      {"keep": {idStr(a)}},
		"other kind":   {"keep": {idStr(a)}, "other": {idStr(comic)}},
		"other plugin": {"keep": {idStr(a)}, "other": {idStr(show)}},
		"both foreign": {"keep": {idStr(show)}, "other": {idStr(show)}},
	} {
		if code := fe.get("/book/merge?" + q.Encode()).Code; code != http.StatusNotFound {
			t.Errorf("GET %s: %d", name, code)
		}
		if code := fe.do("POST", "/book/merge", q, false).Code; code != http.StatusNotFound {
			t.Errorf("POST %s: %d", name, code)
		}
	}
	if dump(t, fe.env) != before {
		t.Fatal("a refused merge changed the library")
	}
}

func TestMergePickListsItemsOfTheSameKind(t *testing.T) {
	fe := newFixtureEnv(t, withComicKind)
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	b := fe.addFixture(t, "book", "book", bookItem("d2", "Tales from Earthsea"), library.Entry{Status: library.Planned})
	cd := bookItem("c1", "Earthsea Comic")
	cd.Kind = "comic"
	fe.addFixture(t, "book", "comic", cd, library.Entry{Status: library.Planned})
	fe.addFixture(t, "tv", "show", tvShow("t1", "Earthsea Show"), library.Entry{Status: library.Planned})

	mustContain(t, fe.get("/book/books/d1").Body.String(), `href="/book/merge/pick?keep=`+idStr(a)+`"`, "Merge with…")
	rec := fe.get("/book/merge/pick?keep=" + idStr(a) + "&q=earth")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Tales from Earthsea", `href="/book/merge?keep=`+idStr(a)+`&amp;other=`+idStr(b)+`"`, `name="q" value="earth"`)
	mustNotContain(t, body, "Earthsea Comic", "Earthsea Show", `other=`+idStr(a))
	if code := fe.get("/book/merge/pick?keep=999").Code; code != http.StatusNotFound {
		t.Fatalf("unknown keep: %d", code)
	}
}

func TestMergeDefaultSurvivorIsTheSourcedItem(t *testing.T) {
	fe := newFixtureEnv(t)
	m := bookItem("m1", "Earthsea")
	m.Source = manualSource
	manual := fe.addFixture(t, "book", "book", m, library.Entry{Status: library.Planned})
	sourced := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	mustContain(t, fe.get("/book/duplicates").Body.String(), `/book/merge?keep=`+idStr(sourced)+`&amp;other=`+idStr(manual))
	mustContain(t, fe.get("/book/merge/pick?keep="+idStr(manual)+"&q=earth").Body.String(),
		`/book/merge?keep=`+idStr(sourced)+`&amp;other=`+idStr(manual))
}

func TestMergePageShowsCoversAsThumbnails(t *testing.T) {
	fe := newFixtureEnv(t)
	a, b := fe.mergePair(t)
	if err := fe.lib.SetCoverPath(bg, a, "keep.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.SetCoverPath(bg, b, "other.jpg"); err != nil {
		t.Fatal(err)
	}
	body := fe.get("/book/merge?" + mergeQuery(a, b)).Body.String()
	mustContain(t, body, `value="cover"`, `<img src="/covers/keep.jpg"`, `<img src="/covers/other.jpg"`)
	mustNotContain(t, body, "local cover")
}

// TestMergeQueuesCoverAndSyncJobs: a taken remote cover is downloaded by the
// cover-only job; a manual survivor that takes over a source link is synced.
func TestMergeQueuesCoverAndSyncJobs(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.srv.deps.SyncJobs = map[string]func(int64) (string, any){"book": jobs.SyncJobFor("book")}
	fe.srv.deps.CoverJobs = map[string]func(int64) (string, any){"book": jobs.CoverJobFor("book")}
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	withCover := bookItem("d2", "Earthsea")
	withCover.CoverURL = "https://img.example/d2.jpg"
	b := fe.saveFixture(t, "book", "book", withCover)
	if rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(a)}, "other": {idStr(b)}, "key": {"cover"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	want := []enqueued{{jobs.KindSyncItem, jobs.SyncPayload{ItemID: a, PluginID: "book", CoverOnly: true}}}
	if !reflect.DeepEqual(fe.jobs.got, want) {
		t.Fatalf("cover merge enqueued %+v, want %+v", fe.jobs.got, want)
	}
	// no cover taken: nothing to download
	fe.jobs.got = nil
	again := bookItem("d4", "Earthsea")
	again.CoverURL = "https://img.example/d2.jpg"
	c := fe.saveFixture(t, "book", "book", again)
	if rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(a)}, "other": {idStr(c)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("merge: %d", rec.Code)
	}
	if len(fe.jobs.got) != 0 {
		t.Fatalf("merge without cover enqueued %+v", fe.jobs.got)
	}
	// a manual survivor takes over d3's source link and is synced
	fe.jobs.got = nil
	m := bookItem("m1", "Earthsea")
	m.Source = manualSource
	keep := fe.addFixture(t, "book", "book", m, library.Entry{Status: library.Planned})
	d := fe.saveFixture(t, "book", "book", bookItem("d3", "Earthsea"))
	if rec := fe.do("POST", "/book/merge", url.Values{"keep": {idStr(keep)}, "other": {idStr(d)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("takeover merge: %d", rec.Code)
	}
	want = []enqueued{{jobs.KindSyncItem, jobs.SyncPayload{ItemID: keep, PluginID: "book"}}}
	if !reflect.DeepEqual(fe.jobs.got, want) {
		t.Fatalf("takeover enqueued %+v, want %+v", fe.jobs.got, want)
	}
}

// TestMergePickSearchesOnlyThePluginAndKind: more than 200 matches of
// another plugin (sorted first) do not hide the candidates.
func TestMergePickSearchesOnlyThePluginAndKind(t *testing.T) {
	fe := newFixtureEnv(t)
	a := fe.addFixture(t, "book", "book", bookItem("d1", "Earthsea"), library.Entry{Status: library.Planned})
	b := fe.addFixture(t, "book", "book", bookItem("d2", "Tales from Earthsea"), library.Entry{Status: library.Planned})
	for i := range 210 {
		fe.addFixture(t, "tv", "show", tvShow("t"+idStr(int64(i)), "An Earthsea Show "+idStr(int64(i))), library.Entry{Status: library.Planned})
	}
	body := fe.get("/book/merge/pick?keep=" + idStr(a) + "&q=earth").Body.String()
	mustContain(t, body, "Tales from Earthsea", `other=`+idStr(b))
	mustNotContain(t, body, "An Earthsea Show")
}
