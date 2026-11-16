package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// bulkBooks adds books titled titles with the given statuses (one each).
func (fe *fixtureEnv) bulkBooks(t *testing.T, titles []string, st []library.Status) []int64 {
	t.Helper()
	var ids []int64
	for i, title := range titles {
		ids = append(ids, fe.addFixture(t, "book", "book", bookItem("b"+strconv.Itoa(i), title), library.Entry{Status: st[i]}))
	}
	return ids
}

func TestBulkCheckboxesOnRowsAndCards(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta"}, []library.Status{library.Active, library.Planned})
	for _, view := range []string{"table", "cards"} {
		body := fe.get("/book?view=" + view).Body.String()
		mustContain(t, body,
			`<form id="bulk-form" method="post" action="/book/bulk"`, `data-bulk-bar`, `data-bulk-total="2"`,
			`name="ids" value="`+idStr(ids[0])+`" form="bulk-form"`, `name="ids" value="`+idStr(ids[1])+`" form="bulk-form"`,
			`data-bulk-row`, `data-bulk-page-all`, `data-bulk-all-matching`, `name="scope" value="ids"`, `data-bulk-scope`,
			// the list's own parameters ride along for scope=filter, and
			// after a rows partial in the rows' bulk state
			`<input type="hidden" name="list" value="status=all&amp;view=`+view+`" data-bulk-list>`,
			`data-bulk-state data-bulk-total="2" data-bulk-list="status=all&amp;view=`+view+`"`,
			`<button type="submit"`)
		// no inline script or handler: the bar is driven by app.js
		mustNotContain(t, body, "<script>", "onclick=")
	}
	// the home page's cards carry no checkbox
	mustNotContain(t, fe.get("/").Body.String(), `form="bulk-form"`)
	// a +1 answer from the list keeps the row's checkbox
	rec := fe.do("POST", "/list/"+idStr(ids[0])+"/increment", url.Values{"return": {"/book?view=table"}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("+1: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `name="ids" value="`+idStr(ids[0])+`" form="bulk-form"`)
}

func TestBulkFilterScopeAsksWithTheCurrentCount(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta", "Gamma"}, []library.Status{library.Completed, library.Completed, library.Planned})
	v := url.Values{"scope": {"filter"}, "list": {"status=completed&view=table"}, "action": {"status"}, "to_status": {"dropped"}}
	rec := fe.do("POST", "/book/bulk", v, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustContain(t, body, "Change 2 entries?", `name="expect" value="2"`, `name="confirm" value="1"`,
		`name="scope" value="filter"`, `name="list" value="status=completed&amp;view=table"`, `name="to_status" value="dropped"`, `action="/book/bulk"`)
	for _, id := range ids {
		if e, _ := fe.lib.Entry(bg, id); e.Status == library.Dropped {
			t.Fatal("the confirm step wrote")
		}
	}
}

// Review Focus 2: a filter selection whose count changed since the owner
// saw it is shown again with the new count and nothing is written.
func TestBulkConfirmWithAStaleCountAsksAgain(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta", "Gamma"}, []library.Status{library.Completed, library.Completed, library.Planned})
	v := url.Values{"scope": {"filter"}, "list": {"status=completed"}, "action": {"status"}, "to_status": {"dropped"},
		"confirm": {"1"}, "expect": {"1"}}
	rec := fe.do("POST", "/book/bulk", v, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustContain(t, body, "Change 2 entries?", `name="expect" value="2"`, "The selection changed")
	for _, id := range ids {
		if e, _ := fe.lib.Entry(bg, id); e.Status == library.Dropped {
			t.Fatal("a stale count wrote")
		}
	}
	// the matching count confirms
	v.Set("expect", "2")
	rec = fe.do("POST", "/book/bulk", v, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Changed 2, skipped 0")
	for i, id := range ids {
		e, _ := fe.lib.Entry(bg, id)
		if (e.Status == library.Dropped) != (i < 2) {
			t.Fatalf("entry %d: %s", i, e.Status)
		}
	}
}

func TestBulkStatusAppliesAndReportsSkips(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta"}, []library.Status{library.Active, library.Planned})
	show := fe.addFixture(t, "tv", "show", tvShow("s1", "Other Show"), library.Entry{Status: library.Planned})
	v := url.Values{"ids": {idStr(ids[0]), idStr(ids[1]), idStr(show)}, "list": {"status=all&view=table"},
		"action": {"status"}, "to_status": {"completed"}}
	rec := fe.do("POST", "/book/bulk", v, false)
	mustContain(t, rec.Body.String(), "Change 3 entries?", `name="expect" value="3"`,
		`name="ids" value="`+idStr(ids[0])+`"`, `name="ids" value="`+idStr(show)+`"`)
	v.Set("confirm", "1")
	v.Set("expect", "3")
	rec = fe.do("POST", "/book/bulk", v, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// the list reloads with the notice
	mustContain(t, body, "Changed 2, skipped 1", "Other Show: not in this list", `data-replace-url="/book?status=all&amp;view=table"`, `id="list-rows"`)
	today := fe.now.Format("2006-01-02")
	for _, id := range ids {
		e, _ := fe.lib.Entry(bg, id)
		if e.Status != library.Completed || e.FinishedOn == nil || e.FinishedOn.Format("2006-01-02") != today {
			t.Fatalf("entry %d: %+v", id, e)
		}
	}
	if e, _ := fe.lib.Entry(bg, show); e.Status != library.Planned {
		t.Fatal("an entry of another plugin changed")
	}
}

func TestBulkNoticeListsFiveSkips(t *testing.T) {
	fe := newFixtureEnv(t)
	fe.bulkBooks(t, []string{"Alpha"}, []library.Status{library.Active})
	v := url.Values{"action": {"remove"}, "confirm": {"1"}, "expect": {"7"}}
	for i := range 7 {
		v.Add("ids", idStr(fe.addFixture(t, "tv", "show", tvShow("x"+strconv.Itoa(i), "Show "+strconv.Itoa(i)), library.Entry{Status: library.Planned})))
	}
	body := fe.do("POST", "/book/bulk", v, false).Body.String()
	mustContain(t, body, "Changed 0, skipped 7", "Show 0: not in this list", "Show 4: not in this list", "…and 2 more")
	mustNotContain(t, body, "Show 5: not in this list")
}

func TestBulkRemoveConfirmNamesTheCount(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta"}, []library.Status{library.Active, library.Planned})
	v := url.Values{"ids": {idStr(ids[0]), idStr(ids[1])}, "action": {"remove"}}
	body := fe.do("POST", "/book/bulk", v, false).Body.String()
	mustContain(t, body, "Remove 2 entries from your library?", `name="expect" value="2"`)
	if e, _ := fe.lib.Entry(bg, ids[0]); e == nil {
		t.Fatal("removed before the confirmation")
	}
	v.Set("confirm", "1")
	v.Set("expect", "2")
	mustContain(t, fe.do("POST", "/book/bulk", v, false).Body.String(), "Changed 2, skipped 0")
	if e, _ := fe.lib.Entry(bg, ids[0]); e != nil {
		t.Fatal("not removed")
	}
}

func TestBulkSeriesSelectListsOnlyThisPluginAndKind(t *testing.T) {
	fe := newFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		k := tv.M.Kinds[0]
		k.Key, k.Label, k.LabelPlural = "film", "Film", "Films"
		tv.M.Kinds = append(tv.M.Kinds, k)
	})
	ser, _ := fe.tvSeries(t, "Saga")
	fd := tvShow("f1", "A Film")
	fd.Kind = "film"
	film := fe.addFixture(t, "tv", "film", fd, library.Entry{Status: library.Planned})
	if _, err := fe.lib.CreateSeries(bg, film, "Film Set"); err != nil {
		t.Fatal(err)
	}
	book := fe.bulkBooks(t, []string{"Alpha"}, []library.Status{library.Active})
	if _, err := fe.lib.CreateSeries(bg, book[0], "Book Set"); err != nil {
		t.Fatal(err)
	}
	body := fe.get("/tv?kind=show&view=table").Body.String()
	at := strings.Index(body, `id="bulk-form"`)
	if at < 0 {
		t.Fatal("no bulk form")
	}
	bar := body[at:]
	mustContain(t, bar, `<option value="`+strconv.FormatInt(ser.ID, 10)+`">Saga</option>`, `<option value="series">`)
	mustNotContain(t, bar, "Film Set", "Book Set")

	// a series of another kind is refused like a missing one
	solo := fe.addFixture(t, "tv", "show", tvShow("solo", "Solo"), library.Entry{Status: library.Planned})
	fs, _, _ := fe.lib.SeriesOfItem(bg, film)
	rec := fe.do("POST", "/tv/bulk", url.Values{"ids": {idStr(solo)}, "list": {"kind=show"}, "action": {"series"}, "to_series": {strconv.FormatInt(fs.ID, 10)}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("other kind's series: %d", rec.Code)
	}
}

func TestBulkRefusesIncompleteActions(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta"}, []library.Status{library.Active, library.Active})
	// a list and a series exist, so both actions are offered
	if _, err := fe.lib.CreateList(bg, "Shelf"); err != nil {
		t.Fatal(err)
	}
	if _, err := fe.lib.CreateSeries(bg, ids[1], "Set"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		v    url.Values
		want string
	}{
		"empty tags":     {url.Values{"action": {"tags"}, "add_tags": {" , "}, "remove_tags": {""}}, "Name at least one tag to add or remove."},
		"unknown action": {url.Values{"action": {"explode"}}, "Choose an action."},
		"no status":      {url.Values{"action": {"status"}, "to_status": {""}}, "Choose a status."},
		"bad status":     {url.Values{"action": {"status"}, "to_status": {"nope"}}, "Choose a status."},
		"no list":        {url.Values{"action": {"list"}}, "Choose a list."},
		"unknown list":   {url.Values{"action": {"list"}, "to_list": {"999"}}, "Choose a list."},
		"no series":      {url.Values{"action": {"series"}}, "Choose a series."},
		"unknown series": {url.Values{"action": {"series"}, "to_series": {"999"}}, "Choose a series."},
	} {
		tc.v.Set("ids", idStr(ids[0]))
		tc.v.Set("confirm", "1")
		tc.v.Set("expect", "1")
		rec := fe.do("POST", "/book/bulk", tc.v, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: code %d", name, rec.Code)
			continue
		}
		// the form is shown again with the message and the selection
		mustContain(t, rec.Body.String(), tc.want, `name="action"`, `name="ids" value="`+idStr(ids[0])+`"`)
	}
	// nothing ticked
	rec := fe.do("POST", "/book/bulk", url.Values{"action": {"remove"}}, false)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Select at least one entry.") {
		t.Fatalf("empty selection: %d", rec.Code)
	}
	if e, _ := fe.lib.Entry(bg, ids[0]); e == nil || e.Status != library.Active {
		t.Fatal("a refused action wrote")
	}
}

func TestBulkTagsAndListApply(t *testing.T) {
	fe := newFixtureEnv(t)
	ids := fe.bulkBooks(t, []string{"Alpha", "Beta"}, []library.Status{library.Active, library.Planned})
	l, err := fe.lib.CreateList(bg, "Shelf")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, fe.get("/book?view=table").Body.String(), `<option value="`+strconv.FormatInt(l.ID, 10)+`">Shelf</option>`)
	v := url.Values{"ids": {idStr(ids[0]), idStr(ids[1])}, "action": {"list"}, "to_list": {strconv.FormatInt(l.ID, 10)}, "confirm": {"1"}, "expect": {"2"}}
	mustContain(t, fe.do("POST", "/book/bulk", v, false).Body.String(), "Changed 2, skipped 0")
	v = url.Values{"ids": {idStr(ids[0]), idStr(ids[1])}, "action": {"tags"}, "add_tags": {"Cozy, Reread"}, "confirm": {"1"}, "expect": {"2"}}
	mustContain(t, fe.do("POST", "/book/bulk", v, false).Body.String(), "Changed 2, skipped 0")
	rows, err := fe.lib.ListRows(bg, l.ID, "", nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list rows %d %v", len(rows), err)
	}
	body := fe.get("/book?view=table&ut=Reread").Body.String()
	mustContain(t, body, "Alpha", "Beta")
}
