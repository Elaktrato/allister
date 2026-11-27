package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// flaggedTV gives the tv fixture an adult flag (field "rated", AdultField)
// and a Related section; the book fixture stays without one.
func flaggedTV(_, tv *plugintest.Plugin) {
	k := &tv.M.Kinds[0]
	k.Fields = append(k.Fields, plugin.FieldDef{Key: "rated", Label: "Rated", Type: plugin.FieldEnum, EnumValues: []string{"yes", "no"}, Show: true, Editable: true, Filterable: true})
	k.AdultField = "rated"
	k.Sections = append(k.Sections, plugin.SectionDef{Type: plugin.SectionRelated, Label: "Related"})
}

func ratedShow(id, title, rated string) plugin.ItemData {
	d := tvShow(id, title)
	if rated != "" {
		d.Fields["rated"] = plugin.FieldValue{Text: rated}
	}
	return d
}

func ratedSummary(id, title, rated string) plugin.ItemSummary {
	s := plugin.ItemSummary{Source: "tvdata", ID: id, Kind: "show", Titles: ttl(title)}
	if rated != "" {
		s.Fields = map[string]plugin.FieldValue{"rated": {Text: rated}}
	}
	return s
}

func TestHideAdultSetting(t *testing.T) {
	e := newFixtureEnv(t, flaggedTV)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	e.now = &now
	plain := e.addFixture(t, "tv", "show", ratedShow("1", "Plain Show", "no"), library.Entry{Status: library.Active})
	flagged := e.addFixture(t, "tv", "show", ratedShow("2", "Flagged Show", "yes"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", ratedShow("3", "Unknown Show", ""), library.Entry{Status: library.Active})
	for _, id := range []int64{plain, flagged} {
		if err := e.lib.SaveSchedule(bg, id, []library.Airing{{Number: 3, AirsAt: time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)}}, now); err != nil {
			t.Fatal(err)
		}
	}
	// the live page of show 9 relates to both and recommends both
	related := ratedShow("9", "Live Show", "no")
	related.Relations = []plugin.Relation{
		{Kind: "sequel", Item: ratedSummary("2", "Flagged Show", "yes")},
		{Kind: "prequel", Item: ratedSummary("1", "Plain Show", "no")},
	}
	e.tv.Data = related
	e.tv.Recs = []plugin.ItemSummary{ratedSummary("21", "Flagged Rec", "yes"), ratedSummary("22", "Plain Rec", "no")}
	e.tv.Summaries = []plugin.ItemSummary{ratedSummary("31", "Flagged Hit", "yes"), ratedSummary("32", "Plain Hit", "")}

	pickTitles := func() []string {
		var got []string
		for i := 0; i < 3; i++ {
			e.srv.rand = func(c int) int { return min(i, c-1) }
			got = append(got, pickedTitle(e.do("GET", "/tv/pick?status=active", nil, true).Body.String()))
		}
		return got
	}
	pages := map[string]string{
		"table":        "/tv?view=table",
		"cards":        "/tv?view=cards",
		"home":         "/",
		"calendar":     "/calendar?month=2026-11",
		"calendar.ics": "/calendar.ics",
		"related":      "/tv/tvdata/9",
		"recs":         "/tv/tvdata/9/recommendations",
		"search":       "/tv/search?q=show",
	}
	stats := e.get("/stats").Body.String()

	// off by default: everything shows
	for name, p := range pages {
		b := e.get(p).Body.String()
		switch name {
		case "recs":
			mustContain(t, b, "Flagged Rec", "Plain Rec")
		case "search":
			mustContain(t, b, "Flagged Hit", "Plain Hit")
		default:
			if !strings.Contains(b, "Flagged Show") {
				t.Errorf("off: %s misses the flagged item", name)
			}
		}
	}
	if got := strings.Join(pickTitles(), ","); !strings.Contains(got, "Flagged Show") {
		t.Errorf("off: picks %s", got)
	}

	// the checkbox only for plugins whose kinds declare the flag
	page := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, `action="/settings/plugins/tv/adult"`, `name="hide_adult" value="1"`, "Hide adult items")
	mustNotContain(t, page, `name="hide_adult" value="1" checked`)
	mustNotContain(t, e.get("/settings/plugins/book").Body.String(), "hide_adult", "Hide adult items")
	if rec := e.do("POST", "/settings/plugins/book/adult", url.Values{"hide_adult": {"1"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("book: %d", rec.Code)
	}
	// CSRF: a cross-site post changes nothing
	req := httptest.NewRequest("POST", "/settings/plugins/tv/adult", strings.NewReader("hide_adult=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", rec.Code)
	}
	if v, ok, _ := e.set.Get(bg, "plugin.tv.hide_adult"); ok {
		t.Fatalf("cross-site stored %q", v)
	}

	// on
	rec = e.do("POST", "/settings/plugins/tv/adult", url.Values{"hide_adult": {"1"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins/tv#adult" {
		t.Fatalf("on: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if v, _, _ := e.set.Get(bg, "plugin.tv.hide_adult"); v != "1" {
		t.Fatalf("stored %q", v)
	}
	mustContain(t, e.get("/settings/plugins/tv").Body.String(), `name="hide_adult" value="1" checked`)
	for name, p := range pages {
		b := e.get(p).Body.String()
		switch name {
		case "recs":
			mustContain(t, b, "Plain Rec")
			mustNotContain(t, b, "Flagged Rec")
		case "search":
			mustContain(t, b, "Plain Hit")
			mustNotContain(t, b, "Flagged Hit")
		default:
			if strings.Contains(b, "Flagged Show") {
				t.Errorf("on: %s shows the flagged item", name)
			}
			if !strings.Contains(b, "Plain Show") {
				t.Errorf("on: %s misses the plain item", name)
			}
		}
	}
	if got := strings.Join(pickTitles(), ","); strings.Contains(got, "Flagged Show") || !strings.Contains(got, "Unknown Show") {
		t.Errorf("on: picks %s", got)
	}
	// the scroll endpoint and the bulk "every matching entry" count
	more := e.getWith("/tv?page=1&view=cards", moreHeaders).Body.String()
	mustNotContain(t, more, "Flagged Show")
	mustContain(t, more, "Plain Show")
	bulk := e.do("POST", "/tv/bulk", url.Values{"scope": {"filter"}, "list": {"status=all&view=table"}, "action": {"status"}, "to_status": {"dropped"}}, false)
	mustContain(t, bulk.Body.String(), "Change 2 entries?")
	// the item page still opens; statistics still count it
	if rec := e.get("/tv/tvdata/2"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Flagged Show") {
		t.Fatalf("item page: %d", rec.Code)
	}
	if got := e.get("/stats").Body.String(); got != stats {
		t.Error("statistics changed with the setting")
	}

	// off again
	if rec := e.do("POST", "/settings/plugins/tv/adult", url.Values{}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("off: %d", rec.Code)
	}
	mustContain(t, e.get("/tv?view=table").Body.String(), "Flagged Show")
}
