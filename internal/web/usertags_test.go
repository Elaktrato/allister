package web

import (
	"context"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
)

func (e *env) setTags(t *testing.T, id int64, names ...string) {
	t.Helper()
	if err := e.lib.SetEntryTags(context.Background(), id, names); err != nil {
		t.Fatal(err)
	}
}

func (e *env) tagsOf(t *testing.T, id int64) []string {
	t.Helper()
	got, err := e.lib.EntryTags(context.Background(), []int64{id})
	if err != nil {
		t.Fatal(err)
	}
	return got[id]
}

func (e *env) userTagID(t *testing.T, name string) int64 {
	t.Helper()
	tags, err := e.lib.UserTags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range tags {
		if tg.Name == name {
			return tg.ID
		}
	}
	t.Fatalf("no tag %q in %v", name, tags)
	return 0
}

func TestOwnTagFilterAnyAllAndRoundTrip(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	both := e.addFixture(t, "tv", "show", tvShow("1", "Both"), active)
	one := e.addFixture(t, "tv", "show", tvShow("2", "Only Comfort"), active)
	e.addFixture(t, "tv", "show", tvShow("3", "Plain"), active)
	book := e.addFixture(t, "book", "book", bookItem("1", "A Book"), active)
	e.setTags(t, both, "Comfort", "Äpfel")
	e.setTags(t, one, "comfort")
	e.setTags(t, book, "Äpfel")
	for q, want := range map[string][]string{
		"ut=comfort":                        {"Both", "Only Comfort"},
		"ut=COMFORT&ut=%C3%A4pfel":          {"Both", "Only Comfort"},
		"ut=comfort&ut=%C3%84PFEL&ut_all=1": {"Both"},
		"ut=nope":                           {},
		"ut=&ut_all=1":                      {"Both", "Only Comfort", "Plain"},
	} {
		b := e.listGet(t, "/tv?view=table&"+q)
		wantRows(t, q, b, want...)
		if n := noticeOf(b); n != "" {
			t.Errorf("%s: unexpected notice %q", q, n)
		}
	}
	// every kind filters by own tags: the book list too
	wantRows(t, "book", e.listGet(t, "/book?view=table&ut=%C3%A4pfel"), "A Book")
	b := e.listGet(t, "/tv?ut=comfort&ut_all=yes&view=table")
	wantRows(t, "bad all", b, "Both", "Only Comfort")
	mustContain(t, noticeOf(b), "ut_all")
	// the bar: the tags of the matching entries with counts, the all checkbox
	b = e.listGet(t, "/tv?ut=comfort&ut_all=1&view=table")
	mustContain(t, b, `<select id="filter-ut-select" name="ut" multiple`, `<option value="Comfort" selected>Comfort (2)</option>`,
		`<option value="Äpfel">Äpfel (1)</option>`, `name="ut_all" value="1" checked`, ">Own tags<")
	// the links keep the filter
	mustContain(t, b, `href="/tv?status=all&amp;ut=comfort&amp;ut_all=1&amp;view=table"`)
	if !strings.Contains(b, `href="/tv?status=completed&amp;ut=comfort&amp;ut_all=1&amp;view=table"`) {
		t.Errorf("status tab drops the own-tag filter")
	}
	// the rows partial swaps the option list into the existing select
	hx := map[string]string{"HX-Request": "true", "HX-Target": "list-rows"}
	p := e.getWith("/tv?ut=nope&view=table", hx).Body.String()
	opts := oobBlock(t, p, "innerHTML:#filter-ut-select")
	mustContain(t, opts, `<option value="Comfort">Comfort (2)</option>`, `<option value="nope" selected>nope (0)</option>`)
	mustNotContain(t, p, `name="ut_all"`, "<select")
	if got := e.getWith("/tv?ut=comfort&view=table", hx).Header().Get("HX-Push-Url"); got != "/tv?ut=comfort&view=table" {
		t.Errorf("push url %q", got)
	}
}

func TestOwnTagFilterHiddenWithoutTags(t *testing.T) {
	e := newFixtureEnv(t)
	e.addFixture(t, "tv", "show", tvShow("1", "Plain"), library.Entry{Status: library.Active})
	mustNotContain(t, e.listGet(t, "/tv?view=table"), `name="ut"`, "Own tags")
}

func TestEntryDialogSetsOwnTags(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), active)
	other := e.addFixture(t, "book", "book", bookItem("1", "A Book"), active)
	e.setTags(t, other, "Comfort", "with-partner")
	e.setTags(t, id, "with-partner")
	b := e.do("GET", "/list/"+idStr(id)+"/edit", nil, true).Body.String()
	// the existing names of every plugin are suggested
	mustContain(t, b, `<datalist id="user-tag-names"><option value="Comfort"></option><option value="with-partner"></option></datalist>`,
		`name="user_tag" value="with-partner"`, `list="user-tag-names"`, "Own tags")
	form := url.Values{"status": {"active"}, "user_tag": {"with-partner", "COMFORT", " Neu ", ""}}
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true); rec.Code != 204 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.tagsOf(t, id); !reflect.DeepEqual(got, []string{"Comfort", "Neu", "with-partner"}) {
		t.Fatalf("tags = %q", got)
	}
	// a refused name: the dialog again with the typed names, nothing written
	before := dump(t, e.env)
	form = url.Values{"status": {"completed"}, "user_tag": {"ok", strings.Repeat("x", library.MaxUserTagRunes+1)}}
	rec := e.do("POST", "/list/"+idStr(id)+"/edit", form, true)
	if rec.Code != 422 {
		t.Fatalf("long name: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "longer than 40 characters", `name="user_tag" value="ok"`)
	if after := dump(t, e.env); after != before {
		t.Fatal("a refused tag changed the database")
	}
	// a form without the tag inputs (an older page) keeps the tags
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"paused"}}, true); rec.Code != 204 {
		t.Fatalf("no tags field: %d", rec.Code)
	}
	if got := e.tagsOf(t, id); len(got) != 3 {
		t.Fatalf("tags after a form without them = %q", got)
	}
	// clearing every input removes them
	if rec := e.do("POST", "/list/"+idStr(id)+"/edit", url.Values{"status": {"paused"}, "user_tag": {"", ""}}, true); rec.Code != 204 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if got := e.tagsOf(t, id); len(got) != 0 {
		t.Fatalf("tags after clearing = %q", got)
	}
}

func TestOwnTagsOnItemPageAndColumn(t *testing.T) {
	e := newFixtureEnv(t)
	id := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), library.Entry{Status: library.Active})
	e.setTags(t, id, "Comfort", "Äpfel")
	m, _ := e.reg.Manifest("tv")
	k, _ := kindDef(m, "show")
	b := e.get(itemURL(m, "tvdata", "1", "show")).Body.String()
	mustContain(t, b, `<a href="/tv?ut=Comfort" class=`, `>Comfort</a>`, `<a href="/tv?ut=%C3%84pfel" class=`)
	// a column option of every kind, not shown by default
	b = e.listGet(t, "/tv?view=table")
	mustNotContain(t, thead(t, b), "Own tags")
	f := columnForm(t, e.get("/settings/plugins/tv").Body.String(), "show")
	mustContain(t, f, `name="show" value="user_tags"`, ">Own tags<")
	order := []string{"user_tags"}
	for _, c := range kindColumns(k) {
		if c.Key != "user_tags" {
			order = append(order, c.Key)
		}
	}
	if rec := e.postColumns("tv", url.Values{"kind": {"show"}, "order": order, "show": {"user_tags"}}, nil); rec.Code != 303 {
		t.Fatalf("columns: %d %s", rec.Code, rec.Body.String())
	}
	b = e.listGet(t, "/tv?view=table")
	if got := strings.Join(headLabels(t, b), "|"); got != "Title|Own tags" {
		t.Fatalf("head = %s", got)
	}
	mustContain(t, rowOf(t, b, "Dark"), ">Comfort, Äpfel</td>")
}

func TestSettingsTagsRenameMergeDelete(t *testing.T) {
	e := newFixtureEnv(t)
	active := library.Entry{Status: library.Active}
	a := e.addFixture(t, "tv", "show", tvShow("1", "Dark"), active)
	b := e.addFixture(t, "book", "book", bookItem("1", "A Book"), active)
	e.setTags(t, a, "comfy", "Comfort")
	e.setTags(t, b, "comfy", "gone")
	page := e.get("/settings/tags")
	if page.Code != 200 {
		t.Fatal(page.Code)
	}
	body := page.Body.String()
	comfy, gone := e.userTagID(t, "comfy"), e.userTagID(t, "gone")
	mustContain(t, body, `href="/settings/tags" aria-current="true"`, "<h1", "Tags</h1>",
		`action="/settings/tags/`+strconv.FormatInt(comfy, 10)+`"`, `name="name" value="comfy"`, "2 entries",
		`action="/settings/tags/`+strconv.FormatInt(gone, 10)+`/delete"`, "1 entry")
	mustContain(t, e.get("/settings").Body.String(), `href="/settings/tags"`)
	// rename onto an existing name merges
	rec := e.do("POST", "/settings/tags/"+strconv.FormatInt(comfy, 10), url.Values{"name": {"COMFORT"}}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings/tags" {
		t.Fatalf("rename: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := e.tagsOf(t, a); !reflect.DeepEqual(got, []string{"Comfort"}) {
		t.Fatalf("a = %q", got)
	}
	if got := e.tagsOf(t, b); !reflect.DeepEqual(got, []string{"Comfort", "gone"}) {
		t.Fatalf("b = %q", got)
	}
	// a refused name: the page again with the error, 422
	rec = e.do("POST", "/settings/tags/"+strconv.FormatInt(gone, 10), url.Values{"name": {strings.Repeat("x", 41)}}, false)
	if rec.Code != 422 {
		t.Fatalf("long: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `role="alert"`, "longer than 40 characters")
	// delete removes it from the entries
	if rec := e.do("POST", "/settings/tags/"+strconv.FormatInt(gone, 10)+"/delete", url.Values{}, false); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if got := e.tagsOf(t, b); !reflect.DeepEqual(got, []string{"Comfort"}) {
		t.Fatalf("b after delete = %q", got)
	}
	for _, path := range []string{"/settings/tags/" + strconv.FormatInt(gone, 10), "/settings/tags/" + strconv.FormatInt(gone, 10) + "/delete", "/settings/tags/x"} {
		if rec := e.do("POST", path, url.Values{"name": {"x"}}, false); rec.Code != 404 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	// no tags: the page says so
	e.setTags(t, a)
	e.setTags(t, b)
	if err := e.lib.DeleteUserTag(context.Background(), e.userTagID(t, "Comfort")); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/settings/tags").Body.String(), "No own tags yet")
}
