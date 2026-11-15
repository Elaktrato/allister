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
	if rec := rm(ids[0]); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv/?tab=series" {
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
		rec := fe.do("POST", seriesURL("tv", s2.UID)+"/delete", url.Values{"entries": {mode}}, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/tv/?tab=series" {
			t.Fatalf("delete %s: %d %s", mode, rec.Code, rec.Header().Get("Location"))
		}
		it, err := fe.lib.Item(bg, ids2[0])
		if mode == "keep" && (err != nil || it == nil) {
			t.Fatalf("keep removed the entry: %v", err)
		}
		if mode == "remove" && err == nil && it != nil {
			t.Fatal("remove kept the entry")
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
	taken, _ := fe.lib.CreateSeries(bg, mk("5", "Other", ""), "Else")
	_ = taken
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
