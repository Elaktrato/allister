package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// --- generic entry routes (fixture plugins) ---

// entryOf returns the stored entry of item id.
func (e *env) entryOf(t *testing.T, id int64) *library.Entry {
	t.Helper()
	en, err := e.lib.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return en
}

func (e *env) itemOf(t *testing.T, id int64) *library.Item {
	t.Helper()
	it, err := e.lib.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// storedProgress reads a progress value straight from the table (active or
// retained); ok is false when there is none.
func (e *env) storedProgress(t *testing.T, id int64, key string) (float64, bool) {
	t.Helper()
	var v float64
	err := e.d.QueryRow(`SELECT value FROM entry_progress WHERE item_id = ? AND key = ?`, id, key).Scan(&v)
	if err != nil {
		return 0, false
	}
	return v, true
}

// tvWithEpisodes is a tv show with a known episode total.
func tvWithEpisodes(id, title string, total float64) plugin.ItemData {
	d := tvShow(id, title)
	d.Fields["episodes"] = plugin.FieldValue{Num: num(total)}
	return d
}

func TestIncrementFixtureRowAndCap(t *testing.T) {
	e := newFixtureEnv(t)
	paged := bookItem("1", "Paged")
	paged.Fields["pages"] = plugin.FieldValue{Num: num(3)}
	id := e.addFixture(t, "book", "book", paged, library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 1}})
	rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "<tr data-row", "2 / 3", `/increment"`)
	mustNotContain(t, rec.Body.String(), "<html")
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
	mustContain(t, rec.Body.String(), "3 / 3")
	mustNotContain(t, rec.Body.String(), "/increment") // hidden at the total
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/book?status=active"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/book?status=active" {
		t.Fatal(rec.Code, rec.Header())
	}
	if en := e.entryOf(t, id); en.Progress["pages"] != 3 {
		t.Fatalf("capped: %v", en.Progress)
	}
	// a plain post without a usable return goes to the item's plugin list
	rec = e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/anime"}}, false)
	if rec.Header().Get("Location") != "/book" {
		t.Fatal(rec.Header())
	}
}

func TestIncrementHiddenWithoutPrimaryType(t *testing.T) {
	e := newAddEnv(t)
	movie := e.addFixture(t, "movie", "movie", e.movie.Data, library.Entry{Status: library.Active, Progress: map[string]float64{"position": 60}})
	b := e.get("/movie").Body.String()
	mustContain(t, rowOf(t, b, "A Movie"), "0:01:00")
	mustNotContain(t, b, "/increment", "+1")
	// the primary key exists but is not active for this book
	pct := e.addFixture(t, "book", "book", bookItem("9", "Percent"), library.Entry{Status: library.Active, Progress: map[string]float64{"percent": 10}},
		library.WithProgressKeys([]string{"percent"}))
	mustNotContain(t, e.get("/book").Body.String(), "/increment")
	// posting anyway is a documented no-op answering the unchanged row
	before := dump(t, e.env)
	for _, id := range []int64{movie, pct} {
		rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, true)
		if rec.Code != 200 {
			t.Fatalf("%d: %d", id, rec.Code)
		}
		mustContain(t, rec.Body.String(), "<tr data-row")
		mustNotContain(t, rec.Body.String(), "/increment")
		if rec := e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{}, false); rec.Code != 303 {
			t.Fatalf("plain %d: %d", id, rec.Code)
		}
	}
	if after := dump(t, e.env); after != before {
		t.Fatalf("no-op increment changed the database:\n%s\n%s", before, after)
	}
}

func TestRowHelperSharedByPageAndIncrement(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Shared", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	inc := strings.TrimSpace(e.do("POST", "/list/"+idStr(id)+"/increment", url.Values{"return": {"/tv"}}, true).Body.String())
	page := rowOf(t, e.get("/tv").Body.String(), "Shared")
	if inc != page {
		t.Fatalf("increment row differs from the page row:\n%s\n---\n%s", inc, page)
	}
}

func TestEditFixtureDialogPrefill(t *testing.T) {
	e := newFixtureEnv(t)
	paged := bookItem("1", "Paged")
	paged.Fields["pages"] = plugin.FieldValue{Num: num(300)}
	id := e.addFixture(t, "book", "book", paged, library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 12},
		StartedOn: day(2024, 1, 2), Comment: "c"})
	rec := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, `action="/list/`+idStr(id)+`/edit"`, `<option value="active" selected>Reading</option>`, ">Want to read<",
		`name="progress.pages"`, `value="12"`, `max="300"`, "/ 300", `name="rating" value=""`, `value="2024-01-02"`,
		`name="return" value="/book"`, "Edit list entry")
	mustNotContain(t, b, "<html")
	// two allowed combinations: the other track's input is shown muted
	mustContain(t, b, `name="progress.percent"`, "used when tracking Percent")
	mustNotContain(t, b, "used when tracking Page")
	// two allowed combinations: a selector with the active one selected
	mustContain(t, b, `name="progress_keys"`, `<option value="pages" selected>Page</option>`, `<option value="percent">Percent</option>`)

	pct := e.addFixture(t, "book", "book", bookItem("2", "Pct"), library.Entry{Status: library.Paused, Rating: ip(0), Progress: map[string]float64{"percent": 40}},
		library.WithProgressKeys([]string{"percent"}))
	b = e.do("GET", "/list/"+idStr(pct)+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="progress.percent"`, `value="40"`, `max="100"`, `name="rating" value="0"`, `<option value="percent" selected>`)
	// full page without JavaScript: the list with the dialog open
	full := e.get("/list/" + idStr(pct) + "/edit").Body.String()
	mustContain(t, full, "<html", `id="dialog-slot"`, `name="progress.percent"`, "Showing 1–2 of 2")

	// one allowed combination: no selector
	e.addEntry(t, "anilist", "5", ip(12), library.Active, 3)
	aid := e.savedItems(t, "anime", "anilist", "5")[0].ID
	mustNotContain(t, e.do("GET", "/list/"+idStr(aid)+"/edit", nil, true).Body.String(), "progress_keys")
}

func TestEditDurationRoundTrip(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Positioned"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 3, "position": 3725}},
		library.WithProgressKeys([]string{"episode", "position"}))
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="progress.position" value="1:02:05"`, `name="progress.episode"`, `value="3"`)
	form := url.Values{"status": {"active"}, "progress.episode": {"3"}, "progress.position": {"1:02:05"}, "progress_keys": {"episode,position"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if en := e.entryOf(t, id); en.Progress["position"] != 3725 || en.Progress["episode"] != 3 {
		t.Fatalf("%v", en.Progress)
	}
	form.Set("progress.position", "12:34")
	e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if en := e.entryOf(t, id); en.Progress["position"] != 754 {
		t.Fatalf("%v", en.Progress)
	}
	// beyond the h:mm:ss display cap the value is prefilled as plain seconds
	if err := e.lib.UpsertEntry(context.Background(), library.Entry{ItemID: id, Status: library.Active, Progress: map[string]float64{"position": 4000000}}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String(), `name="progress.position" value="4000000"`)
	form.Set("progress.position", "1:2:3")
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 422 {
		t.Fatal(rec.Code)
	} else {
		mustContain(t, rec.Body.String(), "Position must be a time", `value="1:2:3"`)
	}
}

func TestEditChangesActiveCombinationOnlyWithinAllowed(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Switcher", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	mustContain(t, b, `name="progress_keys"`, `<option value="episode" selected>Episode</option>`,
		`<option value="season,episode">Season &#43; Episode</option>`, `<option value="episode,position">Episode &#43; Position</option>`)
	if n := strings.Count(b, "<option value=\"") - 5; n != 3 { // five status options
		t.Fatalf("%d combinations offered", n)
	}
	keys := func() []string { return e.itemOf(t, id).ProgressKeys }

	// ineligible combinations are refused and change nothing
	before := dump(t, e.env)
	for _, bad := range []string{"season,position", "season", "position", "episode,episode", "percent", "x", ",", "episode,season,position"} {
		for _, htmx := range []bool{true, false} {
			form := url.Values{"status": {"completed"}, "progress.episode": {"3"}, "progress_keys": {bad}, "comment": {"keep"}}
			rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, htmx)
			if rec.Code != 422 {
				t.Fatalf("%q htmx=%v: %d", bad, htmx, rec.Code)
			}
			mustContain(t, rec.Body.String(), "Please choose one of the offered progress combinations.", "keep")
		}
	}
	if after := dump(t, e.env); after != before {
		t.Fatal("refused combination changed the database")
	}

	// switch to episode + position: the new combination's values are accepted
	form := url.Values{"status": {"active"}, "progress.episode": {"4"}, "progress.position": {"0:10:00"}, "progress_keys": {"episode,position"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if k := keys(); !reflect.DeepEqual(k, []string{"episode", "position"}) {
		t.Fatalf("keys %v", k)
	}
	if en := e.entryOf(t, id); en.Progress["episode"] != 4 || en.Progress["position"] != 600 {
		t.Fatalf("%v", en.Progress)
	}
	// back to episode only: the position value is retained, not deleted
	form = url.Values{"status": {"active"}, "progress.episode": {"5"}, "progress.position": {"0:20:00"}, "progress_keys": {"episode"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if k := keys(); !reflect.DeepEqual(k, []string{"episode"}) {
		t.Fatalf("keys %v", k)
	}
	if v, ok := e.storedProgress(t, id, "position"); !ok || v != 600 {
		t.Fatalf("inactive position not retained unchanged: %v %v", v, ok)
	}
	if en := e.entryOf(t, id); en.Progress["episode"] != 5 || len(en.Progress) != 1 {
		t.Fatalf("%v", en.Progress)
	}
	// the retained value is shown in the muted input of the inactive track
	mustContain(t, e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String(), `name="progress.position" value="0:10:00"`,
		"used when tracking Episode &#43; Position")
	// and reactivated it reappears
	form = url.Values{"status": {"active"}, "progress.episode": {"5"}, "progress_keys": {"position,episode"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	mustContain(t, e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String(), `name="progress.position" value="0:10:00"`)

	// a value above the total under the new combination fails as a whole:
	// neither the combination nor the entry changes
	before = dump(t, e.env)
	form = url.Values{"status": {"active"}, "progress.season": {"1"}, "progress.episode": {"11"}, "progress_keys": {"season,episode"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 422 {
		t.Fatal(rec.Code)
	}
	mustContain(t, rec.Body.String(), "above the total of 10")
	if after := dump(t, e.env); after != before {
		t.Fatal("failed edit changed the database")
	}
}

func TestEditFixtureValidationAndReturn(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "book", "book", bookItem("1", "Pct"), library.Entry{Status: library.Active, Progress: map[string]float64{"percent": 10}},
		library.WithProgressKeys([]string{"percent"}))
	before := dump(t, e.env)
	for _, c := range []struct {
		form url.Values
		msg  string
	}{
		{url.Values{"status": {"active"}, "progress.percent": {"101"}}, "Percent must be a whole number from 0 to 100."},
		{url.Values{"status": {"active"}, "progress.bogus": {"3"}}, "“bogus” is not a progress of this item."},
		{url.Values{"status": {"reading"}}, "Please choose a status."},
		{url.Values{"status": {"active"}, "progress": {"3"}}, "is not a progress of this item"}, // shorthand: pages is not active
	} {
		c.form.Set("comment", "keepme")
		rec := e.do("POST", "/list/"+idStr(id)+"/edit", c.form, true)
		if rec.Code != 422 {
			t.Fatalf("%v: %d", c.form, rec.Code)
		}
		mustContain(t, rec.Body.String(), c.msg, "keepme", `name="return" value="/book"`)
	}
	if after := dump(t, e.env); after != before {
		t.Fatal("invalid edit changed the database")
	}
	itemBefore := e.itemOf(t, id)
	form := url.Values{"status": {"completed"}, "rating": {"0"}, "progress.percent": {"100"}, "return": {"/book/books/1"}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/book/books/1" {
		t.Fatal(rec.Code, rec.Header())
	}
	en := e.entryOf(t, id)
	if en.Status != library.Completed || en.Rating == nil || *en.Rating != 0 || en.Progress["percent"] != 100 {
		t.Fatalf("%+v", en)
	}
	if after := e.itemOf(t, id); !reflect.DeepEqual(itemBefore, after) {
		t.Fatalf("item changed:\n%+v\n%+v", itemBefore, after)
	}
	for _, ret := range []string{"/anime", "/tv", "//evil.com", "/list/1/edit", ""} {
		form.Set("return", ret)
		if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, false); rec.Code != 303 || rec.Header().Get("Location") != "/book" {
			t.Fatalf("%q: %d %v", ret, rec.Code, rec.Header())
		}
	}
}

func TestRemoveFixtureCascades(t *testing.T) {
	e := newFixtureEnv(t)
	ctx := context.Background()
	id := e.addFixture(t, "tv", "show", tvShow("1", "Gone"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	other := e.addFixture(t, "tv", "show", tvShow("2", "Stays"), library.Entry{Status: library.Active})
	if err := e.lib.SaveCredits(ctx, id, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Pat Actor"}}}); err != nil {
		t.Fatal(err)
	}
	people, err := e.lib.ItemPeople(ctx, id)
	if err != nil || len(people) != 1 {
		t.Fatalf("%v %v", people, err)
	}
	b := e.get("/tv").Body.String()
	mustContain(t, rowOf(t, b, "Gone"), `method="post" action="/list/`+idStr(id)+`/remove"`, `hx-confirm="Remove this show and all its saved details from your list?"`,
		`name="return" value="/tv"`)
	rec := e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{"return": {"/tv?status=active"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv?status=active" {
		t.Fatal(rec.Code, rec.Header())
	}
	if it := e.itemOf(t, id); it != nil {
		t.Fatal("item remains")
	}
	var n int
	for _, tb := range []string{"entries", "entry_progress", "item_titles", "credits"} {
		if err := e.d.QueryRow(`SELECT COUNT(*) FROM `+tb+` WHERE item_id = ?`, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", tb, n, err)
		}
	}
	if p, err := e.lib.Person(ctx, people[0].ID); err != nil || p == nil {
		t.Fatalf("person removed: %v", err)
	}
	if e.entryOf(t, other) == nil {
		t.Fatal("other removed")
	}
	if rec := e.do("POST", "/list/"+idStr(id)+"/remove", url.Values{}, false); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	rec = e.do("POST", "/list/"+idStr(other)+"/remove", url.Values{"return": {"/book"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/tv" {
		t.Fatal(rec.Code, rec.Header())
	}
}

func TestEntryRoutesGuardedByItemPlugin(t *testing.T) {
	e := newFixtureEnv(t)
	book := e.addFixture(t, "book", "book", bookItem("1", "A Book"), library.Entry{Status: library.Active, Progress: map[string]float64{"pages": 1}})
	tv := e.addFixture(t, "tv", "show", tvShow("1", "A Show"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	e.disable(t, "book")
	before := dump(t, e.env)
	big := url.Values{"status": {"completed"}, "comment": {strings.Repeat("a", 70<<10)}}
	for _, c := range []struct {
		method, path string
		form         url.Values
	}{
		{"GET", "/book", nil},
		{"GET", "/book?status=active", nil},
		{"POST", "/list/" + idStr(book) + "/increment", url.Values{}},
		{"GET", "/list/" + idStr(book) + "/edit", nil},
		{"POST", "/list/" + idStr(book) + "/edit", url.Values{"status": {"completed"}, "progress.pages": {"5"}}},
		{"POST", "/list/" + idStr(book) + "/edit", big}, // the guard runs before the body is read
		{"POST", "/list/" + idStr(book) + "/remove", url.Values{}},
	} {
		for _, htmx := range []bool{false, true} {
			rec := e.do(c.method, c.path, c.form, htmx)
			if rec.Code != 404 {
				t.Errorf("%s %s htmx=%v: %d", c.method, c.path, htmx, rec.Code)
			}
		}
	}
	if after := dump(t, e.env); after != before {
		t.Fatalf("disabled plugin's entry changed:\n%s\n%s", before, after)
	}
	// other plugins are unaffected
	if rec := e.get("/tv"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("POST", "/list/"+idStr(tv)+"/increment", url.Values{}, true); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("GET", "/list/"+idStr(tv)+"/edit", nil, true); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	// unknown, non-numeric and oversized ids
	for _, p := range []string{"/list/999/edit", "/list/abc/edit", "/list/-1/edit", "/list/1234567890123/edit", "/list/%20/edit"} {
		if rec := e.get(p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	// enabled again: everything works
	if err := e.reg.SetEnabled(context.Background(), "book", true); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", "/list/"+idStr(book)+"/increment", url.Values{}, true); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func TestEntryRoutesGuardUninstalledPlugin(t *testing.T) {
	// an item of a plugin that is no longer installed answers 404
	e := newEnvWith(t, withPlugins(plugintest.TVPlugin()))
	id := e.addFixture(t, "tv", "show", tvShow("1", "Orphan"), library.Entry{Status: library.Active})
	if _, err := e.d.Exec(`UPDATE items SET plugin_id = 'gone' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/list/" + idStr(id) + "/increment"},
		{"GET", "/list/" + idStr(id) + "/edit"},
		{"POST", "/list/" + idStr(id) + "/edit"},
		{"POST", "/list/" + idStr(id) + "/remove"},
	} {
		var form url.Values
		if c.method == "POST" {
			form = url.Values{"status": {"active"}}
		}
		if rec := e.do(c.method, c.path, form, true); rec.Code != 404 {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if it := e.itemOf(t, id); it == nil {
		t.Fatal("removed")
	}
}

func TestEntryRoutesRefuseCrossOrigin(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Csrf"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}})
	before := dump(t, e.env)
	for _, path := range []string{"/list/" + idStr(id) + "/increment", "/list/" + idStr(id) + "/edit", "/list/" + idStr(id) + "/remove"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(url.Values{"status": {"dropped"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if after := dump(t, e.env); after != before {
		t.Fatal("refused request changed the database")
	}
}

func TestOverviewEditLinkOpensEntryDialog(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Linked"), library.Entry{Status: library.Active})
	b := e.get("/tv/tvdata/1").Body.String()
	// without JavaScript the link opens the list with the dialog; htmx loads
	// the dialog into the overview and returns to it
	hx := "/list/" + idStr(id) + "/edit?return=%2Ftv%2Ftvdata%2F1"
	mustContain(t, b, `href="/list/`+idStr(id)+`/edit"`, `hx-get="`+hx+`"`, `hx-target="#dialog-slot"`)
	d := e.do("GET", hx, nil, true).Body.String()
	mustContain(t, d, `name="return" value="/tv/tvdata/1"`, "Edit list entry")
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"completed"}, "return": {"/tv/tvdata/1"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv/tvdata/1" {
		t.Fatal(rec.Code, rec.Header())
	}
	full := e.get("/list/" + idStr(id) + "/edit")
	if full.Code != 200 {
		t.Fatal(full.Code)
	}
	mustContain(t, full.Body.String(), "<html", "Linked", `name="return" value="/tv"`)
}

// dialogInputs counts the progress inputs of a dialog per track key.
func dialogInputs(b string) map[string]int {
	out := map[string]int{}
	for _, part := range strings.Split(b, `name="progress.`)[1:] {
		out[part[:strings.Index(part, `"`)]]++
	}
	return out
}

func TestEditDialogShowsEveryAllowedTrackOnce(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "Tracks", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	if got := dialogInputs(b); !reflect.DeepEqual(got, map[string]int{"season": 1, "episode": 1, "position": 1}) {
		t.Fatalf("inputs %v", got)
	}
	// manifest order; the inactive tracks name a combination that uses them
	si, ei, pi := strings.Index(b, `name="progress.season"`), strings.Index(b, `name="progress.episode"`), strings.Index(b, `name="progress.position"`)
	if !(si < ei && ei < pi) {
		t.Fatalf("order %d %d %d", si, ei, pi)
	}
	mustContain(t, b, "used when tracking Season &#43; Episode", "used when tracking Episode &#43; Position", `max="10"`, "/ 10", "data-inactive")
	if n := strings.Count(b, "data-inactive"); n != 2 {
		t.Fatalf("%d muted inputs", n)
	}
	// a single-combination kind is unchanged: only its own input, nothing muted
	e.addEntry(t, "anilist", "5", ip(12), library.Active, 3)
	aid := e.savedItems(t, "anime", "anilist", "5")[0].ID
	ab := e.do("GET", "/list/"+idStr(aid)+"/edit", nil, true).Body.String()
	if got := dialogInputs(ab); !reflect.DeepEqual(got, map[string]int{"episode": 1}) {
		t.Fatalf("anime inputs %v", got)
	}
	mustNotContain(t, ab, "used when tracking", "data-inactive")
}

func TestEditSwitchAndFillNewTrackInOneSave(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvWithEpisodes("1", "One Save", 10), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 2}})
	// what the no-JS dialog posts: every shown input, the season one typed
	// although the chosen combination does not use it (even invalid text)
	form := url.Values{"status": {"active"}, "progress_keys": {"episode,position"},
		"progress.season": {"abc"}, "progress.episode": {"4"}, "progress.position": {"0:10:00"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, false); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if en := e.entryOf(t, id); en.Progress["episode"] != 4 || en.Progress["position"] != 600 || len(en.Progress) != 2 {
		t.Fatalf("%v", en.Progress)
	}
	if _, ok := e.storedProgress(t, id, "season"); ok {
		t.Fatal("typed value of a track outside the combination was stored")
	}
	// back to episode: the position input is ignored, its value retained
	form = url.Values{"status": {"active"}, "progress_keys": {"episode"},
		"progress.season": {"3"}, "progress.episode": {"5"}, "progress.position": {"0:20:00"}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if v, ok := e.storedProgress(t, id, "position"); !ok || v != 600 {
		t.Fatalf("position %v %v", v, ok)
	}
	if _, ok := e.storedProgress(t, id, "season"); ok {
		t.Fatal("season stored")
	}
	if k := e.itemOf(t, id).ProgressKeys; !reflect.DeepEqual(k, []string{"episode"}) {
		t.Fatalf("keys %v", k)
	}
}

func TestEditBlankProgressClears(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Cleared"), library.Entry{Status: library.Active, Progress: map[string]float64{"season": 2, "episode": 7}},
		library.WithProgressKeys([]string{"season", "episode"}))
	// make position a retained (inactive) value
	if err := e.lib.UpdateEntry(context.Background(), library.Entry{ItemID: id, Status: library.Active, Progress: map[string]float64{"position": 600}},
		[]string{"episode", "position"}); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetActiveProgress(context.Background(), id, []string{"season", "episode"}); err != nil {
		t.Fatal(err)
	}
	// blank active inputs clear to 0; a blank inactive input leaves the retained value
	form := url.Values{"status": {"active"}, "progress.season": {""}, "progress.episode": {""}, "progress.position": {""}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for key, want := range map[string]float64{"season": 0, "episode": 0, "position": 600} {
		if v, ok := e.storedProgress(t, id, key); !ok || v != want {
			t.Fatalf("%s = %v %v, want %v", key, v, ok, want)
		}
	}
	row := rowOf(t, e.get("/tv").Body.String(), "Cleared")
	mustContain(t, row, ">0<", "0 / ?")
	mustContain(t, e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String(), `name="progress.episode" value="0"`)
	// an absent field (not posted at all) keeps its value
	e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"active"}, "progress.episode": {"4"}}, true)
	e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"paused"}}, true)
	if en := e.entryOf(t, id); en.Progress["episode"] != 4 || en.Status != library.Paused {
		t.Fatalf("%+v", en)
	}
	// blank percent and duration clear too
	pct := e.addFixture(t, "book", "book", bookItem("1", "Pct"), library.Entry{Status: library.Active, Progress: map[string]float64{"percent": 40}},
		library.WithProgressKeys([]string{"percent"}))
	if rec := e.do("POST", "/list/"+idStr(pct)+"/edit", url.Values{"status": {"active"}, "progress.percent": {""}}, true); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if v, ok := e.storedProgress(t, pct, "percent"); !ok || v != 0 {
		t.Fatalf("percent %v %v", v, ok)
	}
	dur := e.addFixture(t, "tv", "show", tvShow("2", "Dur"), library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1, "position": 99}},
		library.WithProgressKeys([]string{"episode", "position"}))
	if rec := e.do("POST", "/list/"+idStr(dur)+"/edit", url.Values{"status": {"active"}, "progress.position": {""}}, true); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if en := e.entryOf(t, dur); en.Progress["position"] != 0 || en.Progress["episode"] != 1 {
		t.Fatalf("%v", en.Progress)
	}
	mustContain(t, rowOf(t, e.get("/tv").Body.String(), "Dur"), "0:00:00")
}

func TestEditDurationBoundaries(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Bounds"), library.Entry{Status: library.Active},
		library.WithProgressKeys([]string{"episode", "position"}))
	for secs, text := range map[float64]string{
		0: "0:00:00", 59: "0:00:59", 60: "0:01:00", 3599: "0:59:59", 3600: "1:00:00", 86399: "23:59:59",
		3599999: "999:59:59", 3600000: "3600000",
	} {
		if err := e.lib.UpsertEntry(context.Background(), library.Entry{ItemID: id, Status: library.Active, Progress: map[string]float64{"position": secs}}); err != nil {
			t.Fatal(err)
		}
		b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
		mustContain(t, b, `name="progress.position" value="`+text+`"`)
		if rec := e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"active"}, "progress.position": {text}}, true); rec.Code != 204 {
			t.Fatalf("%v: %d %s", secs, rec.Code, rec.Body.String())
		}
		if v, _ := e.storedProgress(t, id, "position"); v != secs {
			t.Fatalf("%q parsed back as %v, want %v", text, v, secs)
		}
	}
}
