package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// cardTitles are the titles of the list cards of body, in order.
func cardTitles(body string) []string {
	const open = `class="line-clamp-2 text-sm font-medium leading-snug text-white hover:underline">`
	out := []string{}
	for _, part := range strings.Split(body, open)[1:] {
		if j := strings.Index(part, "</a>"); j >= 0 {
			out = append(out, html.UnescapeString(part[:j]))
		}
	}
	return out
}

// newCustomList creates a list through the page and returns its id.
func (e *env) newCustomList(t *testing.T, name string) int64 {
	t.Helper()
	rec := e.do("POST", "/lists", url.Values{"name": {name}}, false)
	loc := rec.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(loc, "/lists/"), 10, 64)
	if rec.Code != http.StatusSeeOther || err != nil {
		t.Fatalf("create list %q: %d %s", name, rec.Code, loc)
	}
	return id
}

func listPath(id int64) string { return "/lists/" + strconv.FormatInt(id, 10) }

func (e *env) joinList(t *testing.T, listID int64, items ...int64) {
	t.Helper()
	for _, id := range items {
		rec := e.do("POST", listPath(listID)+"/items", url.Values{"item": {idStr(id)}}, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(listID) {
			t.Fatalf("add %d to list: %d %s", id, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// Review Focus 5: names are unique ignoring case, also beyond ASCII.
func TestCustomListNamesUniqueIgnoringCase(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Comfort")
	rec := e.do("POST", "/lists", url.Values{"name": {"comfort"}}, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "A list with this name already exists.", `value="comfort"`)
	other := e.newCustomList(t, "Ä")
	if rec := e.do("POST", "/lists", url.Values{"name": {"ä"}}, false); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate ä: %d", rec.Code)
	}
	if rec := e.do("POST", listPath(other), url.Values{"name": {"COMFORT"}}, false); rec.Code != http.StatusConflict {
		t.Fatalf("rename onto another: %d", rec.Code)
	}
	if rec := e.do("POST", listPath(other), url.Values{"name": {"ä"}}, false); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(other) {
		t.Fatalf("rename to own name: %d", rec.Code)
	}
	for _, bad := range []string{"", "a\x01b", strings.Repeat("x", 81)} {
		if rec := e.do("POST", "/lists", url.Values{"name": {bad}}, false); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("bad name %q: %d", bad, rec.Code)
		}
	}
	body := e.get("/lists").Body.String()
	mustContain(t, body, ">Comfort<", ">ä<", `href="`+listPath(id)+`"`, `action="/lists"`)
	// the sidebar lists them under the plugins, with "New list"
	mustContain(t, e.get("/").Body.String(), `href="`+listPath(id)+`"`, `href="/lists"`, "New list")
	for _, p := range []string{"/lists/999", "/lists/abc", "/lists/0"} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
}

// One page mixes plugins: each card carries its own plugin's status label
// and +1; the progress line counts the completed ones.
func TestCustomListMixesPlugins(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Franchise")
	show := e.addFixture(t, "tv", "show", tvShow("1", "The Show"), library.Entry{Status: library.Active})
	book := e.addFixture(t, "book", "book", bookItem("1", "The Book"), library.Entry{Status: library.Completed})
	plan := e.addFixture(t, "book", "book", bookItem("2", "Next Book"), library.Entry{Status: library.Planned})
	e.joinList(t, id, show, book, plan)
	body := e.get(listPath(id)).Body.String()
	mustContain(t, body, "<h1", "Franchise", "1 of 3 done",
		`title="Watching"`, `title="Read"`, `title="Want to read"`,
		`action="/list/`+idStr(show)+`/increment"`, `name="return" value="`+listPath(id)+`"`,
		`id="dialog-slot"`)
	if got, want := cardTitles(body), []string{"Next Book", "The Book", "The Show"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cards %q, want %q (sorted by title)", got, want)
	}
	// +1 from a list card answers the card and keeps the list as its return
	rec := e.do("POST", "/list/"+idStr(show)+"/increment", url.Values{"return": {listPath(id)}, "view": {"cards"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("+1: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "data-card", `value="`+listPath(id)+`"`)
	rec = e.do("POST", "/list/"+idStr(show)+"/increment", url.Values{"return": {listPath(id) + "?sort=status"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(id)+"?sort=status" {
		t.Fatalf("+1 without htmx: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	for _, bad := range []string{"/lists/1/../x", "/lists/x", "/lists/1?sort=nope", "/lists/1?next=//evil"} {
		if got := returnTarget(bad, "tv"); got != "/tv" {
			t.Errorf("returnTarget(%q) = %q", bad, got)
		}
	}
	// the overview's progress line too
	mustContain(t, e.get("/lists").Body.String(), "1 of 3 done")
}

func TestCustomListManualOrder(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Order")
	a := e.addFixture(t, "tv", "show", tvShow("1", "A"), library.Entry{Status: library.Planned})
	b := e.addFixture(t, "book", "book", bookItem("1", "B"), library.Entry{Status: library.Planned})
	c := e.addFixture(t, "tv", "show", tvShow("2", "C"), library.Entry{Status: library.Planned})
	e.joinList(t, id, c, a, b)
	move := func(item int64, dir string) *httptest.ResponseRecorder {
		return e.do("POST", listPath(id)+"/items/"+idStr(item)+"/move", url.Values{"dir": {dir}}, false)
	}
	if res := move(a, "up"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("move in a sorted list: %d", res.Code)
	}
	if rec := e.do("POST", listPath(id), url.Values{"order": {"manual"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("order: %d", rec.Code)
	}
	body := e.get(listPath(id)).Body.String()
	if got, want := cardTitles(body), []string{"C", "A", "B"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manual %q, want %q", got, want)
	}
	mustContain(t, body, `action="`+listPath(id)+"/items/"+idStr(a)+`/move"`, "Move up", "Move down", `href="`+listPath(id)+`?sort=position"`)
	if res := move(b, "up"); res.Code != http.StatusSeeOther || res.Header().Get("Location") != listPath(id) {
		t.Fatalf("move: %d", res.Code)
	}
	if got, want := cardTitles(e.get(listPath(id)).Body.String()), []string{"C", "B", "A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after move %q, want %q", got, want)
	}
	if res := move(b, "sideways"); res.Code != http.StatusNotFound {
		t.Fatalf("bad dir: %d", res.Code)
	}
	other := e.addFixture(t, "tv", "show", tvShow("3", "Other"), library.Entry{Status: library.Planned})
	if res := move(other, "up"); res.Code != http.StatusNotFound {
		t.Fatalf("non-member: %d", res.Code)
	}
	// remove from the list: the entry stays
	if rec := e.do("POST", listPath(id)+"/items/"+idStr(c)+"/delete", url.Values{}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	if got, want := cardTitles(e.get(listPath(id)).Body.String()), []string{"B", "A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after remove %q, want %q", got, want)
	}
	if en, _ := e.lib.Entry(bg, c); en == nil {
		t.Fatal("removing from a list removed the entry")
	}
	if rec := e.do("POST", listPath(id), url.Values{"order": {"bogus"}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad order: %d", rec.Code)
	}
}

func TestCustomListSortModes(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Sorted")
	z := e.addFixture(t, "tv", "show", tvShow("1", "zebra"), library.Entry{Status: library.Completed})
	a := e.addFixture(t, "book", "book", bookItem("1", "Apple"), library.Entry{Status: library.Dropped})
	m := e.addFixture(t, "tv", "show", tvShow("2", "mango"), library.Entry{Status: library.Planned})
	e.joinList(t, id, z, a, m)
	if _, err := e.d.Exec(`UPDATE list_items SET added_at = CASE item_id WHEN ? THEN '2026-01-03T00:00:00Z' WHEN ? THEN '2026-01-01T00:00:00Z' ELSE '2026-01-02T00:00:00Z' END`, z, a); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string][]string{
		"":                {"Apple", "mango", "zebra"},
		"?sort=title":     {"Apple", "mango", "zebra"},
		"?sort=added":     {"zebra", "mango", "Apple"},
		"?sort=status":    {"mango", "zebra", "Apple"},
		"?sort=position":  {"Apple", "mango", "zebra"}, // not offered while sorted: the default
		"?sort=%00broken": {"Apple", "mango", "zebra"},
	} {
		body := e.get(listPath(id) + q).Body.String()
		if got := cardTitles(body); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	body := e.get(listPath(id) + "?sort=added").Body.String()
	mustContain(t, body, `href="`+listPath(id)+`?sort=title"`, `href="`+listPath(id)+`?sort=status"`, `aria-current="true"`, ">Date added<")
	mustNotContain(t, body, "sort=position")
}

// An entry removed from the library leaves the list; a deleted list keeps
// its entries.
func TestCustomListFollowsEntries(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Keep")
	a := e.addFixture(t, "tv", "show", tvShow("1", "Gone"), library.Entry{Status: library.Planned})
	b := e.addFixture(t, "book", "book", bookItem("1", "Stays"), library.Entry{Status: library.Planned})
	e.joinList(t, id, a, b)
	if rec := e.do("POST", "/list/"+idStr(a)+"/remove", url.Values{"return": {listPath(id)}}, false); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(id) {
		t.Fatalf("remove entry: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body := e.get(listPath(id)).Body.String()
	if got := cardTitles(body); !reflect.DeepEqual(got, []string{"Stays"}) {
		t.Fatalf("cards %q", got)
	}
	mustContain(t, body, "0 of 1 done")
	if rec := e.do("POST", listPath(id)+"/delete", url.Values{}, false); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/lists" {
		t.Fatalf("delete list: %d", rec.Code)
	}
	if rec := e.get(listPath(id)); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted list: %d", rec.Code)
	}
	if en, _ := e.lib.Entry(bg, b); en == nil {
		t.Fatal("deleting the list removed its entry")
	}
	if rec := e.do("POST", listPath(id)+"/delete", url.Values{}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

// Add from search: the list rides on the search, the overview and the
// dialog; the new entry lands as planned and in the list.
func TestCustomListAddFromSearch(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "From search")
	lp := "list=" + idStr(id)
	body := e.get(listPath(id)).Body.String()
	mustContain(t, body, `href="/tv/search?`+lp+`"`, `href="/book/search?`+lp+`"`, "Search Fake TV")
	e.tv.Summaries = []plugin.ItemSummary{tvShow("9", "Found").ItemSummary}
	mustContain(t, e.get("/tv/search?q=x&"+lp).Body.String(), `href="/tv/tvdata/9?`+lp+`"`, `name="list" value="`+idStr(id)+`"`, `href="/tv/manual/new?`+lp+`"`)
	e.tv.Data = tvShow("9", "Found")
	mustContain(t, e.get("/tv/tvdata/9?"+lp).Body.String(), `/tv/tvdata/9/add?`+lp)
	mustContain(t, e.get("/tv/tvdata/9/add?"+lp).Body.String(), `action="/tv/tvdata/9/add?`+lp+`"`)
	form := url.Values{"status": {"planned"}}
	if rec := e.do("POST", "/tv/tvdata/9/add?list=999", form, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown list: %d", rec.Code)
	}
	if items, _ := e.lib.ItemByPluginSource(bg, "tv", "tvdata", "9"); len(items) != 0 {
		t.Fatal("saved despite an unknown list")
	}
	rec := e.do("POST", "/tv/tvdata/9/add?"+lp, form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(id) {
		t.Fatalf("add: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	items, _ := e.lib.ItemByPluginSource(bg, "tv", "tvdata", "9")
	if len(items) != 1 {
		t.Fatal("not saved")
	}
	if en, _ := e.lib.Entry(bg, items[0].ID); en == nil || en.Status != library.Planned {
		t.Fatalf("entry %+v", en)
	}
	if got := cardTitles(e.get(listPath(id)).Body.String()); !reflect.DeepEqual(got, []string{"Found"}) {
		t.Fatalf("cards %q", got)
	}
	// with htmx the dialog is told where to go
	e.tv.Data = tvShow("8", "Other")
	rec = e.do("POST", "/tv/tvdata/8/add?"+lp, form, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != listPath(id) {
		t.Fatalf("htmx add: %d %s", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	// an entry already in the library is offered to join, and joins
	listed := e.addFixture(t, "tv", "show", tvShow("10", "Listed"), library.Entry{Status: library.Active})
	mustContain(t, e.get("/tv/tvdata/10?"+lp).Body.String(), "Add to list “From search”", `/tv/tvdata/10/add?`+lp)
	rec = e.do("POST", "/tv/tvdata/10/add?"+lp, form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(id) {
		t.Fatalf("listed: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if en, _ := e.lib.Entry(bg, listed); en.Status != library.Active {
		t.Fatalf("joining changed the entry: %+v", en)
	}
	// a manual entry from the list's search lands there too
	mustContain(t, e.get("/tv/manual/new?"+lp).Body.String(), `action="/tv/manual/new?`+lp+`"`)
	rec = e.postMultipart(t, "/tv/manual/new?"+lp, url.Values{"title": {"Hand Made"}, "status": {"planned"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != listPath(id) {
		t.Fatalf("manual: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if got := cardTitles(e.get(listPath(id)).Body.String()); len(got) != 4 {
		t.Fatalf("cards %q", got)
	}
}

// The list page searches the whole library and adds a found entry.
func TestCustomListLibrarySearch(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Search")
	show := e.addFixture(t, "tv", "show", tvShow("1", "Kingdom Show"), library.Entry{Status: library.Planned})
	book := e.addFixture(t, "book", "book", bookItem("1", "Kingdom Book"), library.Entry{Status: library.Planned})
	e.joinList(t, id, book)
	rec := e.do("GET", listPath(id)+"/search?q=kingdom", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "Kingdom Show", "Kingdom Book", `name="item" value="`+idStr(show)+`"`, "In this list")
	mustNotContain(t, b, `name="item" value="`+idStr(book)+`"`, "<html")
	// without htmx: the whole page with the results
	mustContain(t, e.get(listPath(id)+"/search?q=kingdom").Body.String(), "<html", `name="item" value="`+idStr(show)+`"`, `value="kingdom"`)
	e.joinList(t, id, show)
	for _, bad := range []string{"0", "abc", "999"} {
		if rec := e.do("POST", listPath(id)+"/items", url.Values{"item": {bad}}, false); rec.Code != http.StatusNotFound {
			t.Errorf("add %q: %d", bad, rec.Code)
		}
	}
	// a disabled plugin's entry cannot be added
	other := e.addFixture(t, "tv", "show", tvShow("2", "Hidden"), library.Entry{Status: library.Planned})
	e.disable(t, "tv")
	if rec := e.do("POST", listPath(id)+"/items", url.Values{"item": {idStr(other)}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled plugin: %d", rec.Code)
	}
}

// A disabled plugin's entries are hidden from the page and its counts but
// stay in the list.
func TestCustomListHidesDisabledPlugins(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.newCustomList(t, "Mixed")
	show := e.addFixture(t, "tv", "show", tvShow("1", "The Show"), library.Entry{Status: library.Completed})
	book := e.addFixture(t, "book", "book", bookItem("1", "The Book"), library.Entry{Status: library.Planned})
	e.joinList(t, id, show, book)
	e.disable(t, "tv")
	body := e.get(listPath(id)).Body.String()
	if got := cardTitles(body); !reflect.DeepEqual(got, []string{"The Book"}) {
		t.Fatalf("cards %q", got)
	}
	mustContain(t, body, "0 of 1 done")
	mustNotContain(t, body, `href="/tv/search`)
	if err := e.reg.SetEnabled(bg, "tv", true); err != nil {
		t.Fatal(err)
	}
	if got := cardTitles(e.get(listPath(id)).Body.String()); len(got) != 2 {
		t.Fatalf("after enabling: %q", got)
	}
}

// A save changes only the lists the form showed: a membership added in
// another tab after the page loaded (a list not on the form) survives, and a
// shown list deleted meanwhile does not fail the save.
func TestItemPageListPickerKeepsOtherLists(t *testing.T) {
	e := newFixtureEnv(t)
	one := e.newCustomList(t, "One")
	gone := e.newCustomList(t, "Gone")
	e.tv.Data = tvShow("1", "Picked")
	show := e.addFixture(t, "tv", "show", tvShow("1", "Picked"), library.Entry{Status: library.Planned})
	e.get("/tv/tvdata/1") // the page is loaded with One and Gone
	later := e.newCustomList(t, "Later")
	e.joinList(t, later, show)
	if err := e.lib.DeleteList(bg, gone); err != nil {
		t.Fatal(err)
	}
	rec := e.do("POST", "/list/"+idStr(show)+"/lists", url.Values{
		"shown": {idStr(one), idStr(gone)}, "list": {idStr(one), idStr(gone)}, "return": {"/tv/tvdata/1"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	of, err := e.lib.ListsOfItem(bg, show)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, x := range of {
		ids = append(ids, x.ID)
	}
	if !reflect.DeepEqual(ids, []int64{later, one}) {
		t.Fatalf("lists of item %v, want Later and One", ids)
	}
}

// The item page offers every list with a checkbox and saves the membership
// of the lists it showed in one request.
func TestItemPageListPicker(t *testing.T) {
	e := newFixtureEnv(t)
	one := e.newCustomList(t, "One")
	two := e.newCustomList(t, "Two")
	e.tv.Data = tvShow("1", "Picked")
	show := e.addFixture(t, "tv", "show", tvShow("1", "Picked"), library.Entry{Status: library.Planned})
	e.joinList(t, one, show)
	body := e.get("/tv/tvdata/1").Body.String()
	mustContain(t, body, `action="/list/`+idStr(show)+`/lists"`,
		`name="list" value="`+idStr(one)+`" checked`, `name="list" value="`+idStr(two)+`">`, "Lists",
		`name="shown" value="`+idStr(one)+`"`, `name="shown" value="`+idStr(two)+`"`)
	rec := e.do("POST", "/list/"+idStr(show)+"/lists", url.Values{
		"shown": {idStr(one), idStr(two)}, "list": {idStr(two)}, "return": {"/tv/tvdata/1"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv/tvdata/1" {
		t.Fatalf("save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	of, err := e.lib.ListsOfItem(bg, show)
	if err != nil || len(of) != 1 || of[0].ID != two {
		t.Fatalf("lists of item %+v %v", of, err)
	}
	for _, field := range []string{"list", "shown"} {
		if rec := e.do("POST", "/list/"+idStr(show)+"/lists", url.Values{field: {"x"}}, false); rec.Code != http.StatusNotFound {
			t.Errorf("malformed %s: %d", field, rec.Code)
		}
	}
	if of, _ := e.lib.ListsOfItem(bg, show); len(of) != 1 {
		t.Fatalf("a refused save changed the lists: %+v", of)
	}
	// an unsaved item has no picker
	e.tv.Data = tvShow("2", "Unsaved")
	mustNotContain(t, e.get("/tv/tvdata/2").Body.String(), `name="list" value=`)
}
