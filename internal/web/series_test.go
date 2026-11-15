package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

var bg = context.Background()

// tvSeries creates a tv series of three shows (one completed) and returns it
// with the item ids in order.
func (fe *fixtureEnv) tvSeries(t *testing.T, title string) (*library.Series, []int64) {
	t.Helper()
	st := []library.Status{library.Completed, library.Planned, library.Active}
	var ids []int64
	for i, s := range st {
		n := strconv.Itoa(i+1) + strings.ReplaceAll(title, " ", "")
		ids = append(ids, fe.addFixture(t, "tv", "show", tvShow(n, "Show "+n), library.Entry{Status: s}))
	}
	ser, err := fe.lib.CreateSeries(bg, ids[0], title)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		if err := fe.lib.AddPart(bg, ser.ID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	return &ser, ids
}

func TestSeriesPageShowsParts(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, ids := fe.tvSeries(t, "Saga")
	rec := fe.get(seriesURL("tv", ser.UID))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Saga", "1/3", "Watched")
	// completed + planned + active: the header shows the active label
	h1, ol := strings.Index(body, "<h1"), strings.Index(body, "<ol")
	if h1 < 0 || ol < h1 {
		t.Fatalf("page structure: <h1 at %d, <ol at %d", h1, ol)
	}
	head := body[h1:ol]
	mustContain(t, head, "Watching")
	last := -1
	for i, id := range ids {
		href := itemPath("tv", "tvdata", strconv.Itoa(i+1)+"Saga")
		at := strings.Index(body, `href="`+href)
		if at < 0 || at < last {
			t.Fatalf("part %d (%d) missing or out of order (%s)", i, id, href)
		}
		last = at
	}
}

func TestSeriesRenameAndRefusals(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	base := seriesURL("tv", ser.UID)
	rec := fe.do("POST", base+"/rename", url.Values{"title": {"  New  "}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("rename: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	got, err := fe.lib.SeriesByUID(bg, "tv", ser.UID)
	if err != nil || got.Title != "New" {
		t.Fatalf("title = %+v %v", got, err)
	}
	if rec := fe.do("POST", base+"/rename", url.Values{"title": {"\x01"}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("control char: %d", rec.Code)
	}
	if rec := fe.do("POST", base+"/rename", url.Values{"title": {" "}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty: %d", rec.Code)
	}
	for _, c := range []struct{ m, p string }{{"GET", ""}, {"POST", "/rename"}, {"POST", "/delete"}, {"GET", "/candidates"}} {
		if rec := fe.do(c.m, seriesURL("tv", "nope")+c.p, url.Values{"title": {"x"}}, false); rec.Code != http.StatusNotFound {
			t.Fatalf("unknown %s %s: %d", c.m, c.p, rec.Code)
		}
	}
	// a series of another plugin is not found under this one
	if rec := fe.get(seriesURL("book", ser.UID)); rec.Code != http.StatusNotFound {
		t.Fatalf("other plugin: %d", rec.Code)
	}
}

func TestSeriesAddFromList(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	base := seriesURL("tv", ser.UID)
	extra := fe.addFixture(t, "tv", "show", tvShow("9", "Extra"), library.Entry{Status: library.Planned})
	rec := fe.do("POST", base+"/parts", url.Values{"item": {idStr(extra)}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("add: %d", rec.Code)
	}
	if _, n, _ := fe.lib.SeriesOfItem(bg, extra); n != 4 {
		t.Fatalf("position = %d", n)
	}
	rec = fe.do("POST", base+"/parts", url.Values{"item": {idStr(extra)}}, false)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This entry is already in a series.") {
		t.Fatalf("again: %d %s", rec.Code, rec.Body.String())
	}
	book := fe.addFixture(t, "book", "book", bookItem("1", "B"), library.Entry{Status: library.Planned})
	if rec := fe.do("POST", base+"/parts", url.Values{"item": {idStr(book)}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("other plugin: %d", rec.Code)
	}
	for _, v := range []string{"abc", "", "99999"} {
		if rec := fe.do("POST", base+"/parts", url.Values{"item": {v}}, false); rec.Code != http.StatusNotFound {
			t.Fatalf("item %q: %d", v, rec.Code)
		}
	}
}

func TestSeriesMoveRemoveDelete(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, ids := fe.tvSeries(t, "Saga")
	base := seriesURL("tv", ser.UID)
	pos := func(id int64) int {
		_, n, err := fe.lib.SeriesOfItem(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	move := func(id int64, dir string) int {
		return fe.do("POST", base+"/parts/"+idStr(id)+"/move", url.Values{"dir": {dir}}, false).Code
	}
	if c := move(ids[1], "up"); c != http.StatusSeeOther || pos(ids[1]) != 1 || pos(ids[0]) != 2 {
		t.Fatalf("up: %d %d %d", c, pos(ids[1]), pos(ids[0]))
	}
	if c := move(ids[1], "up"); c != http.StatusSeeOther || pos(ids[1]) != 1 {
		t.Fatalf("up at top: %d %d", c, pos(ids[1]))
	}
	if c := move(ids[1], "down"); c != http.StatusSeeOther || pos(ids[1]) != 2 {
		t.Fatalf("down: %d", c)
	}
	if c := move(ids[1], "sideways"); c != http.StatusNotFound {
		t.Fatalf("bad dir: %d", c)
	}
	rm := func(id int64) *httptest.ResponseRecorder {
		return fe.do("POST", base+"/parts/"+idStr(id)+"/remove", url.Values{}, false)
	}
	if rec := rm(ids[2]); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("remove: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := rm(ids[2]); rec.Code != http.StatusNotFound {
		t.Fatalf("remove again: %d", rec.Code)
	}
	rm(ids[1])
	if rec := rm(ids[0]); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv?tab=series" {
		t.Fatalf("last: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := fe.get(base); rec.Code != http.StatusNotFound {
		t.Fatalf("gone: %d", rec.Code)
	}

	for _, mode := range []string{"keep", "remove"} {
		s2, ids2 := fe.tvSeries(t, "Again "+mode)
		if rec := fe.do("POST", seriesURL("tv", s2.UID)+"/delete", url.Values{"entries": {"maybe"}}, false); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad choice: %d", rec.Code)
		}
		// a bad choice changes nothing: the series and its parts stay
		if got := fe.partIDs(t, s2); len(got) != len(ids2) {
			t.Fatalf("bad choice left %d of %d parts", len(got), len(ids2))
		}
		if rec := fe.get(seriesURL("tv", s2.UID)); rec.Code != http.StatusOK {
			t.Fatalf("bad choice removed the series: %d", rec.Code)
		}
		rec := fe.do("POST", seriesURL("tv", s2.UID)+"/delete", url.Values{"entries": {mode}}, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv?tab=series" {
			t.Fatalf("delete %s: %d %s", mode, rec.Code, rec.Header().Get("Location"))
		}
		if rec := fe.get(seriesURL("tv", s2.UID)); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: series still there: %d", mode, rec.Code)
		}
		for _, id := range ids2 {
			it, err := fe.lib.Item(bg, id)
			if mode == "keep" && (err != nil || it == nil) {
				t.Fatalf("keep removed the entry %d: %v", id, err)
			}
			if mode == "remove" && err == nil && it != nil {
				t.Fatalf("remove kept the entry %d", id)
			}
		}
	}
}

func TestSeriesCandidatesPreferHint(t *testing.T) {
	fe := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		k.Fields = append(k.Fields, plugin.FieldDef{Key: "saga", Label: "Saga", Type: plugin.FieldText, Editable: true})
		k.SeriesHint = &plugin.SeriesHintDef{TitleField: "saga"}
	})
	mk := func(id, title, saga string) int64 {
		d := tvShow(id, title)
		if saga != "" {
			d.Fields["saga"] = plugin.FieldValue{Text: saga}
		}
		return fe.addFixture(t, "tv", "show", d, library.Entry{Status: library.Planned})
	}
	first := mk("1", "Alpha", "")
	mk("2", "Beta", "")
	mk("3", "Zulu", " my SAGA ")
	mk("4", "Taken", "")
	if _, err := fe.lib.CreateSeries(bg, mk("5", "Other", ""), "Else"); err != nil {
		t.Fatal(err)
	}
	ser, err := fe.lib.CreateSeries(bg, first, "My Saga")
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.AddPart(bg, ser.ID, mk("6", "Part", ""), ""); err != nil {
		t.Fatal(err)
	}
	rec := fe.get(seriesURL("tv", ser.UID) + "/candidates")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	titles := regexp.MustCompile(`break-words">([^<]+)<`).FindAllStringSubmatch(body, -1)
	var got []string
	for _, m := range titles {
		got = append(got, m[1])
	}
	want := []string{"Zulu", "Beta", "Taken"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	rec = fe.get(seriesURL("tv", ser.UID) + "/candidates?q=tak")
	mustContain(t, rec.Body.String(), "Taken")
	mustNotContain(t, rec.Body.String(), "Beta", "Zulu")
}

func TestSeriesAddForwardsPositionField(t *testing.T) {
	fe := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := &tv.M.Kinds[0]
		k.Fields = append(k.Fields, plugin.FieldDef{Key: "saga", Label: "Saga", Type: plugin.FieldText, Editable: true})
		k.SeriesHint = &plugin.SeriesHintDef{TitleField: "saga", PositionField: "episodes"}
	})
	mk := func(id string, n float64) int64 {
		d := tvShow(id, "Show "+id)
		d.Fields["episodes"] = plugin.FieldValue{Num: num(n)}
		return fe.addFixture(t, "tv", "show", d, library.Entry{Status: library.Planned})
	}
	one, three, two := mk("1", 1), mk("3", 3), mk("2", 2)
	ser, err := fe.lib.CreateSeries(bg, one, "S")
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.AddPart(bg, ser.ID, three, ""); err != nil {
		t.Fatal(err)
	}
	rec := fe.do("POST", seriesURL("tv", ser.UID)+"/parts", url.Values{"item": {idStr(two)}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d", rec.Code)
	}
	if _, pos, _ := fe.lib.SeriesOfItem(bg, two); pos != 2 {
		t.Fatalf("position = %d, want 2", pos)
	}
}

func hintBook(book, _ *plugintest.Plugin) {
	book.M.Kinds[0].SeriesHint = &plugin.SeriesHintDef{TitleField: "author"}
}

func TestMakeSeriesFromOverview(t *testing.T) {
	fe := newFixtureEnv(t)
	id := fe.addFixture(t, "tv", "show", tvShow("5", "Solo"), library.Entry{Status: library.Planned})
	body := fe.get("/tv/tvdata/5").Body.String()
	mustContain(t, body, "Make this a series", `action="/tv/tvdata/5/series"`, `value="Solo"`)
	rec := fe.do("POST", "/tv/tvdata/5/series", url.Values{"title": {" Solo Saga "}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("post: %d %s", rec.Code, rec.Body.String())
	}
	ser, n, err := fe.lib.SeriesOfItem(bg, id)
	if err != nil || ser == nil || n != 1 || ser.Title != "Solo Saga" {
		t.Fatalf("series = %+v %d %v", ser, n, err)
	}
	if loc := rec.Header().Get("Location"); loc != seriesURL("tv", ser.UID) {
		t.Fatalf("location %q", loc)
	}
	if body := fe.get("/tv/tvdata/5").Body.String(); strings.Contains(body, "Make this a series") {
		t.Fatal("offered for a part")
	}
	if rec := fe.do("POST", "/tv/tvdata/5/series", url.Values{"title": {"Again"}}, false); rec.Code != http.StatusConflict {
		t.Fatalf("again: %d", rec.Code)
	}
	// an entry that is not in the list: no form, no series
	fe.saveFixture(t, "tv", "show", tvShow("6", "Unlisted"))
	mustNotContain(t, fe.get("/tv/tvdata/6").Body.String(), "Make this a series")
	if rec := fe.do("POST", "/tv/tvdata/6/series", url.Values{"title": {"X"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unlisted: %d", rec.Code)
	}
	// live (unsaved) overview
	fe.tv.Data = tvShow("7", "Live")
	mustNotContain(t, fe.get("/tv/tvdata/7").Body.String(), "Make this a series")
	fe.addFixture(t, "tv", "show", tvShow("8", "Blank"), library.Entry{Status: library.Planned})
	if rec := fe.do("POST", "/tv/tvdata/8/series", url.Values{"title": {" "}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank: %d", rec.Code)
	}
}

func TestMakeSeriesPrefillsFromHint(t *testing.T) {
	fe := newFixtureEnv(t, hintBook)
	fe.addFixture(t, "book", "book", bookItem("1", "Earthsea 1"), library.Entry{Status: library.Planned})
	mustContain(t, fe.get("/book/books/1").Body.String(), `value="Ursula K. Le Guin"`)
	fe.addFixture(t, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "books", ID: "2", Kind: "book", Titles: ttl("Plain")}},
		library.Entry{Status: library.Planned})
	mustContain(t, fe.get("/book/books/2").Body.String(), `value="Plain"`)
}

func TestPartOverviewShowsSeriesNav(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, ids := fe.tvSeries(t, "Saga")
	body := fe.get("/tv/tvdata/2Saga").Body.String()
	mustContain(t, body, "Part 2 of", "Saga", `href="`+seriesURL("tv", ser.UID)+`"`,
		`href="`+itemPath("tv", "tvdata", "1Saga")+`"`, `href="`+itemPath("tv", "tvdata", "3Saga")+`"`,
		"Remove from series", `action="`+seriesURL("tv", ser.UID)+"/parts/"+idStr(ids[1])+`/remove"`)
	mustNotContain(t, body, "Make this a series")
	first := fe.get("/tv/tvdata/1Saga").Body.String()
	mustContain(t, first, "Part 1 of")
	mustNotContain(t, first, "Previous part")
}

func TestSearchAddIntoSeries(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	fe.tv.Data = tvShow("5", "Fresh")
	form := url.Values{"status": {"planned"}}
	if rec := fe.do("POST", "/tv/tvdata/5/add?series=nope", form, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if items, _ := fe.lib.ItemByPluginSource(bg, "tv", "tvdata", "5"); len(items) != 0 {
		t.Fatal("saved despite unknown series")
	}
	base := seriesURL("tv", ser.UID)
	rec := fe.do("POST", "/tv/tvdata/5/add?series="+ser.UID, form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("add: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	items, _ := fe.lib.ItemByPluginSource(bg, "tv", "tvdata", "5")
	if len(items) != 1 {
		t.Fatal("not saved")
	}
	if s, n, _ := fe.lib.SeriesOfItem(bg, items[0].ID); s == nil || s.ID != ser.ID || n != 4 {
		t.Fatalf("not attached: %+v %d", s, n)
	}
	// an item already in the list is attached without a second add
	listed := fe.addFixture(t, "tv", "show", tvShow("6", "Listed"), library.Entry{Status: library.Planned})
	rec = fe.do("POST", "/tv/tvdata/6/add?series="+ser.UID, form, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("listed: %d", rec.Code)
	}
	if _, n, _ := fe.lib.SeriesOfItem(bg, listed); n != 5 {
		t.Fatalf("listed position %d", n)
	}
	// in another series: 409
	fe.tvSeries(t, "Other")
	rec = fe.do("POST", "/tv/tvdata/1Other/add?series="+ser.UID, form, false)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This entry is already in a series.") {
		t.Fatalf("other series: %d", rec.Code)
	}
	// the parameter rides on the search results, the overview and the dialog
	fe.tv.Summaries = []plugin.ItemSummary{tvShow("9", "Found").ItemSummary}
	mustContain(t, fe.get("/tv/search?q=x&series="+ser.UID).Body.String(), "series="+ser.UID)
	fe.tv.Data = tvShow("9", "Found")
	mustContain(t, fe.get("/tv/tvdata/9?series="+ser.UID).Body.String(), "series="+ser.UID)
	mustContain(t, fe.get("/tv/tvdata/9/add?series="+ser.UID).Body.String(), `action="/tv/tvdata/9/add?series=`+ser.UID+`"`)
	// a listed entry in no series is offered to join; one in a series is not
	fe.addFixture(t, "tv", "show", tvShow("10", "Solo"), library.Entry{Status: library.Planned})
	mustContain(t, fe.get("/tv/tvdata/10?series="+ser.UID).Body.String(), "Add to series", `/tv/tvdata/10/add?series=`+ser.UID)
	mustNotContain(t, fe.get("/tv/tvdata/1Saga?series="+ser.UID).Body.String(), "Add to series")
	mustNotContain(t, fe.get("/tv/tvdata/10?series=nope").Body.String(), "Add to series")
}

func TestSearchAddIntoSeriesOfOtherKindRefused(t *testing.T) {
	fe := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := tv.M.Kinds[0]
		k.Key, k.Label, k.LabelPlural = "film", "Film", "Films"
		tv.M.Kinds = append(tv.M.Kinds, k)
	})
	ser, _ := fe.tvSeries(t, "Saga")
	fe.tv.Data = tvShow("5", "A Film")
	rec := fe.do("POST", "/tv/tvdata/5/add?kind=film&series="+ser.UID, url.Values{"status": {"planned"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("other kind: %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := fe.lib.ItemByPluginSource(bg, "tv", "tvdata", "5"); len(items) != 0 {
		t.Fatal("saved despite the refusal")
	}
	// a series of another plugin is unknown under this one
	fe.book.Data = bookItem("1", "B")
	if rec := fe.do("POST", "/book/books/1/add?series="+ser.UID, url.Values{"status": {"planned"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("other plugin: %d", rec.Code)
	}
}

func TestSeriesRoutesAreGuarded(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	fe.disable(t, "tv")
	other := fe.get("/tv/tvdata/1Saga")
	got := fe.get(seriesURL("tv", ser.UID))
	if other.Code != http.StatusNotFound || got.Code != other.Code || got.Body.String() != other.Body.String() {
		t.Fatalf("series %d vs other %d", got.Code, other.Code)
	}
	for _, p := range []string{"/rename", "/delete", "/parts"} {
		if rec := fe.do("POST", seriesURL("tv", ser.UID)+p, url.Values{"title": {"x"}}, false); rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s: %d", p, rec.Code)
		}
	}
	if rec := fe.do("POST", "/tv/tvdata/1Saga/series", url.Values{"title": {"x"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("make series: %d", rec.Code)
	}
}

func TestSeriesPageLinksSearchIntoSeries(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	body := fe.get(seriesURL("tv", ser.UID)).Body.String()
	mustContain(t, body, `href="/tv/search?series=`+ser.UID+`"`, "Search Fake TV")
	mustNotContain(t, body, "kind=")

	// several kinds: the link names the series' kind
	fe = newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := tv.M.Kinds[0]
		k.Key, k.Label, k.LabelPlural = "film", "Film", "Films"
		tv.M.Kinds = append(tv.M.Kinds, k)
	})
	ser, _ = fe.tvSeries(t, "Saga")
	body = fe.get(seriesURL("tv", ser.UID)).Body.String()
	mustContain(t, body, `href="/tv/search?kind=show&amp;series=`+ser.UID+`"`)
}

func TestManualEntryFromSeriesSearchBecomesPart(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, ids := fe.tvSeries(t, "Saga")
	// the search page's manual link and the manual form carry the series
	mustContain(t, fe.get("/tv/search?q=x&series="+ser.UID).Body.String(), `href="/tv/manual/new?series=`+ser.UID+`"`)
	mustContain(t, fe.get("/tv/manual/new?series="+ser.UID).Body.String(), `action="/tv/manual/new?series=`+ser.UID+`"`)
	before := len(fe.partIDs(t, ser))
	rec := fe.postMultipart(t, "/tv/manual/new?series="+ser.UID,
		url.Values{"title": {"Hand Made"}, "status": {"planned"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != seriesURL("tv", ser.UID) {
		t.Fatalf("manual into series: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if got := fe.partIDs(t, ser); len(got) != before+1 || got[0] != ids[0] {
		t.Fatalf("parts %v", got)
	}
	// an unknown series is refused before anything is saved
	n := fe.itemCount(t)
	if rec := fe.postMultipart(t, "/tv/manual/new?series=nope", url.Values{"title": {"X"}, "status": {"planned"}}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown series: %d", rec.Code)
	}
	if fe.itemCount(t) != n {
		t.Fatal("saved despite the refusal")
	}
}

func (fe *fixtureEnv) partIDs(t *testing.T, ser *library.Series) []int64 {
	t.Helper()
	rows, err := fe.lib.SeriesParts(bg, ser.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, r := range rows {
		out = append(out, r.Item.ID)
	}
	return out
}

func TestSeriesMoveRemoveRefuseBadAndForeignItems(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, ids := fe.tvSeries(t, "Saga")
	_, oids := fe.tvSeries(t, "Other")
	base := seriesURL("tv", ser.UID)
	for _, item := range []string{"abc", idStr(oids[0])} {
		if rec := fe.do("POST", base+"/parts/"+item+"/move", url.Values{"dir": {"up"}}, false); rec.Code != http.StatusNotFound {
			t.Errorf("move %s: %d", item, rec.Code)
		}
		if rec := fe.do("POST", base+"/parts/"+item+"/remove", url.Values{}, false); rec.Code != http.StatusNotFound {
			t.Errorf("remove %s: %d", item, rec.Code)
		}
	}
	if got := fe.partIDs(t, ser); len(got) != len(ids) {
		t.Fatalf("parts changed: %v", got)
	}
	if _, n, _ := fe.lib.SeriesOfItem(bg, oids[0]); n != 1 {
		t.Fatal("foreign part touched")
	}
}

func TestSeriesDeleteWordingNamesPartCount(t *testing.T) {
	fe := newFixtureEnv(t)
	ser, _ := fe.tvSeries(t, "Saga")
	mustContain(t, fe.get(seriesURL("tv", ser.UID)).Body.String(), "Delete the series and remove its 3 entries from your list")
}

// The add succeeded but the attach failed (the series is gone, the item
// already belongs to another series): the entry is in the list, so the answer
// is its overview with a notice, not an error page. A real race cannot be
// scheduled in a test, so the shared handling is driven directly.
func TestAttachAfterAddFailureGoesToOverview(t *testing.T) {
	fe := newFixtureEnv(t)
	m, _ := fe.reg.Manifest("tv")
	ser, ids := fe.tvSeries(t, "Saga")
	fresh := fe.addFixture(t, "tv", "show", tvShow("7", "Fresh"), library.Entry{Status: library.Planned})
	overview := itemURL(m, "tvdata", "7", "show")
	run := func(ser *library.Series, item int64) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		fe.srv.attachAfterAdd(rec, httptest.NewRequest("POST", "/tv/tvdata/7/add", nil), m, ser, item, overview)
		return rec
	}
	noticeURL := overview + "?notice=series"

	// success: the series page
	if rec := run(ser, fresh); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != seriesURL("tv", ser.UID) {
		t.Fatalf("ok: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// already in a series
	if rec := run(ser, ids[0]); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != noticeURL {
		t.Fatalf("already: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// the series was deleted meanwhile
	removed, err := fe.lib.CreateSeries(bg, fe.addFixture(t, "tv", "show", tvShow("8", "Eight"), library.Entry{Status: library.Planned}), "Removed")
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.DeleteSeries(bg, removed.ID, false); err != nil {
		t.Fatal(err)
	}
	other := fe.addFixture(t, "tv", "show", tvShow("9", "Nine"), library.Entry{Status: library.Planned})
	rec := run(&removed, other)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != noticeURL {
		t.Fatalf("removed: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// htmx adds redirect through the header
	hx := httptest.NewRequest("POST", "/tv/tvdata/7/add", nil)
	hx.Header.Set("HX-Request", "true")
	hrec := httptest.NewRecorder()
	fe.srv.attachAfterAdd(hrec, hx, m, &removed, other, overview)
	if hrec.Header().Get("HX-Redirect") != noticeURL {
		t.Fatalf("htmx: %v", hrec.Header())
	}
}

func TestOverviewShowsSeriesAttachNotice(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.addFixture(t, "tv", "show", tvShow("7", "Fresh"), library.Entry{Status: library.Planned})
	const notice = "was added to your list, but could not be put into the series"
	mustContain(t, fe.get("/tv/tvdata/7?notice=series").Body.String(), notice)
	mustNotContain(t, fe.get("/tv/tvdata/7").Body.String(), notice)
	mustNotContain(t, fe.get("/tv/tvdata/7?notice=bogus").Body.String(), notice)
}
