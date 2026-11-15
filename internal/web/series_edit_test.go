package web

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// withSeriesData gives the book plugin's book kind series fields and a link
// to the comic kind (whose isbn fills the description and pages the expected
// parts).
func withSeriesData(book, tv *plugintest.Plugin) {
	withComicKind(book, tv)
	k := &book.M.Kinds[0]
	k.SeriesFields = []plugin.FieldDef{
		{Key: "publisher", Label: "Publisher", Type: plugin.FieldText, Show: true, Editable: true},
		{Key: "volumes", Label: "Volumes", Type: plugin.FieldNumber, Min: num(1), Max: num(100), Show: true, Editable: true},
		{Key: "site", Label: "Website", Type: plugin.FieldURL, Show: true, Editable: true},
	}
	k.SeriesLink = &plugin.SeriesLinkDef{Kind: "comic", DescriptionField: "isbn", PartsField: "pages"}
}

// bookSeries is a series of two books of the book kind.
func (fe *fixtureEnv) bookSeries(t *testing.T, title string) (*library.Series, []int64) {
	t.Helper()
	a := fe.addFixture(t, "book", "book", bookItem("1"+title, title+" One"), library.Entry{Status: library.Completed})
	b := fe.addFixture(t, "book", "book", bookItem("2"+title, title+" Two"), library.Entry{Status: library.Planned})
	ser, err := fe.lib.CreateSeries(bg, a, title)
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.AddPart(bg, ser.ID, b, ""); err != nil {
		t.Fatal(err)
	}
	return &ser, []int64{a, b}
}

func (fe *fixtureEnv) comic(t *testing.T, id, title string) int64 {
	t.Helper()
	d := bookItem(id, title)
	d.Kind = "comic"
	d.Fields["isbn"] = plugin.FieldValue{Text: "Linked blurb"}
	d.Fields["pages"] = plugin.FieldValue{Num: num(7)}
	return fe.addFixture(t, "book", "comic", d, library.Entry{Status: library.Planned})
}

func bookKind(t *testing.T, fe *fixtureEnv) plugin.KindDef {
	t.Helper()
	m, _ := fe.reg.Manifest("book")
	k, _ := kindDef(m, "book")
	return k
}

func TestSeriesPageHeader(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	k := bookKind(t, fe)
	err := fe.lib.UpdateSeriesDetails(bg, ser.ID, k, library.SeriesDetails{Title: "Saga", Description: "An epic\nin two parts",
		StartedOn: sday(2001, 2, 3), EndedOn: sday(2010, 12, 31), Finished: true, PartsTotal: 5, CoverPath: "https://img.example/c.jpg",
		Fields: map[string]plugin.FieldValue{"volumes": {Num: num(9)}, "publisher": {Text: "Acme"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := fe.get(seriesURL("book", ser.UID)).Body.String()
	mustContain(t, body, "An epic\nin two parts", "2001-02-03 - 2010-12-31", "Expected parts", ">5<", `src="https://img.example/c.jpg"`, "1/5", "Edit details")
	// the series fields in the order the kind declares them
	pub, vol := strings.Index(body, "Publisher"), strings.Index(body, "Volumes")
	if pub < 0 || vol < pub {
		t.Fatalf("series fields order: publisher %d volumes %d", pub, vol)
	}
	mustNotContain(t, body, "Website", "Spin-off of", "Spin-offs")
}

func TestSeriesPageSpinOffs(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	parent, ids := fe.bookSeries(t, "Saga")
	kid, _ := fe.bookSeries(t, "Side")
	k := bookKind(t, fe)
	if err := fe.lib.UpdateSeriesDetails(bg, kid.ID, k, library.SeriesDetails{Title: "Side", ParentID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	if err := fe.lib.SetPartSpinOff(bg, parent.ID, ids[1], true); err != nil {
		t.Fatal(err)
	}
	pb := fe.get(seriesURL("book", parent.UID)).Body.String()
	mustContain(t, pb, "Spin-offs", "Spin-off 1", "Part 1", "Mark as main part", "Mark as spin-off", "Side", "1/2 completed", seriesURL("book", kid.UID))
	// the spin-off part is not counted: 1 of 1 main parts
	mustContain(t, pb, "1/1")
	kb := fe.get(seriesURL("book", kid.UID)).Body.String()
	mustContain(t, kb, "Spin-off of", ">Saga</a>", seriesURL("book", parent.UID))
	mustNotContain(t, kb, "Spin-offs")
}

func TestSeriesSpinOffToggle(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, ids := fe.bookSeries(t, "Saga")
	base := seriesURL("book", ser.UID)
	rec := fe.do("POST", base+"/parts/"+idStr(ids[0])+"/spinoff", url.Values{"spin_off": {"1"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("flip: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if _, n, spin, _ := fe.lib.SeriesOfItem(bg, ids[0]); !spin || n != 1 {
		t.Fatalf("a = %d %v", n, spin)
	}
	if _, n, spin, _ := fe.lib.SeriesOfItem(bg, ids[1]); spin || n != 1 {
		t.Fatalf("b renumbered = %d %v", n, spin)
	}
	fe.do("POST", base+"/parts/"+idStr(ids[0])+"/spinoff", url.Values{"spin_off": {"0"}}, false)
	if _, n, spin, _ := fe.lib.SeriesOfItem(bg, ids[0]); spin || n != 2 {
		t.Fatalf("back = %d %v", n, spin)
	}
	for _, c := range []struct {
		path string
		v    url.Values
		code int
	}{
		{"/parts/" + idStr(ids[0]) + "/spinoff", url.Values{"spin_off": {"x"}}, 422},
		{"/parts/" + idStr(ids[0]) + "/spinoff", url.Values{}, 422},
		{"/parts/99999/spinoff", url.Values{"spin_off": {"1"}}, 404},
		{"/parts/abc/spinoff", url.Values{"spin_off": {"1"}}, 404},
	} {
		if rec := fe.do("POST", base+c.path, c.v, false); rec.Code != c.code {
			t.Errorf("%s %v: %d", c.path, c.v, rec.Code)
		}
	}
}

func TestSeriesPartLabels(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, ids := fe.bookSeries(t, "Saga")
	if err := fe.lib.SetPartSpinOff(bg, ser.ID, ids[1], true); err != nil {
		t.Fatal(err)
	}
	// the overview of the spin-off part and of the main part
	spin := fe.get("/book/books/2Saga").Body.String()
	mustContain(t, spin, "Spin-off 1 of", "Saga")
	mustNotContain(t, spin, "Part 1 of")
	main := fe.get("/book/books/1Saga").Body.String()
	mustContain(t, main, "Part 1 of")
	mustNotContain(t, main, "Next part", "Spin-off 1 of")
	// the list badge
	list := fe.get("/book?kind=book").Body.String()
	mustContain(t, list, "Spin-off · Saga", "1 · Saga")
}

func TestSeriesEditDialog(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	fe.comic(t, "c1", "Comic One")
	fe.comic(t, "c2", "Another")
	other, _ := fe.bookSeries(t, "Other")
	base := seriesURL("book", ser.UID)
	rec := fe.do("GET", base+"/edit", nil, true)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, `data-dialog`, `action="`+base+`/edit"`, `enctype="multipart/form-data"`, `name="title" value="Saga"`, `name="description"`,
		`name="started_on"`, `name="ended_on"`, `name="finished"`, `name="parts_total"`, `name="field.publisher"`, `name="field.volumes"`, `name="field.site"`,
		`name="cover"`, `name="cover_url"`, `name="linked"`, `name="parent"`, base+"/link-candidates", base+"/parent-candidates", "Comic One", "Another", ">Other<")
	mustNotContain(t, b, "<html")
	// without htmx: the series page with the dialog open
	full := fe.get(base + "/edit")
	mustContain(t, full.Body.String(), "<html", `id="dialog-slot"`, `data-dialog`, "<h1")
	// pickers
	pr := fe.get(base + "/link-candidates?q=another").Body.String()
	mustContain(t, pr, "Another", `name="linked"`)
	mustNotContain(t, pr, "Comic One")
	parents := fe.get(base + "/parent-candidates").Body.String()
	mustContain(t, parents, "Other", `value="`+idStr(other.ID)+`"`)
	mustNotContain(t, parents, ">Saga<")
}

func TestSeriesEditRoundTrip(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	linked := fe.comic(t, "c1", "Comic One")
	parent, _ := fe.bookSeries(t, "Parent")
	base := seriesURL("book", ser.UID)
	form := url.Values{"title": {"  Saga Reloaded "}, "description": {"Own text"}, "started_on": {"2001-02-03"}, "ended_on": {"2010-12-31"},
		"finished": {"1"}, "parts_total": {"12"}, "field.publisher": {"Acme"}, "field.volumes": {"9"}, "field.site": {"https://acme.example/"},
		"linked": {idStr(linked)}, "parent": {idStr(parent.ID)}}
	// an uploaded cover
	rec := fe.postMultipart(t, base+"/edit", form, &upload{"cover", "c.png", "image/png", png1("series")})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := mustSeriesWeb(t, fe, ser.UID)
	if got.Title != "Saga Reloaded" || got.Description != "Own text" || got.StartedOn == nil || got.EndedOn == nil || !got.Finished || got.PartsTotal != 12 ||
		got.LinkedItemID != linked || got.ParentID != parent.ID || got.Fields["publisher"].Text != "Acme" || got.Fields["volumes"].Num == nil ||
		got.Fields["site"].Text != "https://acme.example/" || !hashedPNG.MatchString(got.CoverPath) || !hasFile(fe.env, got.CoverPath) {
		t.Fatalf("stored: %+v", got)
	}
	uploaded := got.CoverPath
	// a cover URL replaces the upload; the file goes
	form.Set("cover_url", "https://img.example/series.jpg")
	form.Set("linked", "")
	form.Del("parent") // absent keeps
	if rec := fe.do("POST", base+"/edit", form, true); rec.Code != 204 || rec.Header().Get("HX-Redirect") != base {
		t.Fatalf("htmx save: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	got = mustSeriesWeb(t, fe, ser.UID)
	if got.CoverPath != "https://img.example/series.jpg" || got.LinkedItemID != 0 || got.ParentID != parent.ID || hasFile(fe.env, uploaded) {
		t.Fatalf("url cover: %+v file %v", got, hasFile(fe.env, uploaded))
	}
	// clearing a field clears the own value
	clear := url.Values{"title": {"Saga"}, "cover_url": {""}, "parent": {"0"}}
	if rec := fe.do("POST", base+"/edit", clear, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d", rec.Code)
	}
	got = mustSeriesWeb(t, fe, ser.UID)
	if got.Description != "" || got.CoverPath != "" || got.PartsTotal != 0 || got.ParentID != 0 || got.Finished || len(got.Fields) != 0 || got.StartedOn != nil {
		t.Fatalf("cleared: %+v", got)
	}
	// the edit page shows the linked value once the link is set and own is empty
	if rec := fe.do("POST", base+"/edit", url.Values{"title": {"Saga"}, "linked": {idStr(linked)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("link: %d", rec.Code)
	}
	body := fe.get(base).Body.String()
	mustContain(t, body, "Linked blurb", "Expected parts", ">7<")
	// the dialog shows the OWN (empty) description, not the linked one
	mustNotContain(t, fe.get(base+"/edit").Body.String(), "Linked blurb</textarea>")
}

func mustSeriesWeb(t *testing.T, fe *fixtureEnv, uid string) *library.Series {
	t.Helper()
	s, err := fe.lib.SeriesByUID(bg, "book", uid)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var alertRe = regexp.MustCompile(`(?s)role="alert"><p>(.*?)</p>`)

func TestSeriesEditRefusesInvalid(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	unlisted := fe.saveFixture(t, "book", "comic", func() plugin.ItemData { d := bookItem("u", "Unlisted"); d.Kind = "comic"; return d }())
	book := fe.addFixture(t, "book", "book", bookItem("9", "Not a comic"), library.Entry{Status: library.Planned})
	base := seriesURL("book", ser.UID)
	valid := func() url.Values { return url.Values{"title": {"Saga"}, "description": {"keep out"}} }
	cases := map[string]struct {
		set  func(url.Values)
		want string
	}{
		"http cover url":   {func(v url.Values) { v.Set("cover_url", "http://img.example/c.jpg") }, "Cover URL"},
		"javascript cover": {func(v url.Values) { v.Set("cover_url", "javascript:alert(1)") }, "Cover URL"},
		"bad date":         {func(v url.Values) { v.Set("started_on", "2026-02-30") }, "Started"},
		"bad end date":     {func(v url.Values) { v.Set("ended_on", "soon") }, "Ended"},
		"number field":     {func(v url.Values) { v.Set("field.volumes", "abc") }, "Volumes"},
		"number range":     {func(v url.Values) { v.Set("field.volumes", "1000") }, "Volumes"},
		"url field":        {func(v url.Values) { v.Set("field.site", "ftp://x") }, "Website"},
		"unknown field":    {func(v url.Values) { v.Set("field.nope", "x") }, "nope"},
		"parts":            {func(v url.Values) { v.Set("parts_total", "many") }, "Expected parts"},
		"parts huge":       {func(v url.Values) { v.Set("parts_total", "999999") }, "expected parts"},
		"description":      {func(v url.Values) { v.Set("description", strings.Repeat("é", 20001)) }, "Description"},
		"empty title":      {func(v url.Values) { v.Set("title", " ") }, "title"},
		"link not listed":  {func(v url.Values) { v.Set("linked", idStr(unlisted)) }, "not in your list"},
		"link wrong kind":  {func(v url.Values) { v.Set("linked", idStr(book)) }, "link"},
		"link garbage":     {func(v url.Values) { v.Set("linked", "x") }, "linked entry"},
		"parent self":      {func(v url.Values) { v.Set("parent", idStr(ser.ID)) }, "parent"},
		"parent unknown":   {func(v url.Values) { v.Set("parent", "99999") }, "parent"},
	}
	for name, c := range cases {
		v := valid()
		c.set(v)
		for _, htmx := range []bool{false, true} {
			rec := fe.do("POST", base+"/edit", v, htmx)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s htmx=%v: %d", name, htmx, rec.Code)
				continue
			}
			body := rec.Body.String()
			alert := alertRe.FindStringSubmatch(body)
			if alert == nil || !strings.Contains(strings.ToLower(alert[1]), strings.ToLower(c.want)) {
				t.Errorf("%s: message %q does not name %q", name, alert, c.want)
			}
			// the dialog keeps what was typed
			if v.Get("description") != "" && name != "description" && !strings.Contains(body, "keep out") {
				t.Errorf("%s: input not echoed", name)
			}
		}
	}
	got := mustSeriesWeb(t, fe, ser.UID)
	if got.Description != "" || got.Title != "Saga" || got.CoverPath != "" || got.LinkedItemID != 0 || got.ParentID != 0 || len(got.Fields) != 0 {
		t.Fatalf("stored after refusal: %+v", got)
	}
	// an upload that is not an image is refused and leaves no file
	rec := fe.postMultipart(t, base+"/edit", valid(), &upload{"cover", "x.png", "image/png", []byte("<html></html>")})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad upload: %d", rec.Code)
	}
	// a valid form with a bad field does not keep the uploaded file
	v := valid()
	v.Set("field.volumes", "abc")
	rec = fe.postMultipart(t, base+"/edit", v, &upload{"cover", "c.png", "image/png", png1("x")})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "choose the cover file again") {
		t.Fatalf("upload + invalid: %d", rec.Code)
	}
	if n := countFiles(t, fe.env); n != 0 {
		t.Fatalf("%d cover files left", n)
	}
	// unknown series
	if rec := fe.do("POST", seriesURL("book", "nope")+"/edit", valid(), false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if rec := fe.get(seriesURL("book", "nope") + "/edit"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown get: %d", rec.Code)
	}
}

func TestSeriesPickersListOnlyValidCandidates(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	fe.comic(t, "c1", "Listed Comic")
	d := bookItem("u", "Unlisted Comic")
	d.Kind = "comic"
	fe.saveFixture(t, "book", "comic", d)
	fe.addFixture(t, "book", "book", bookItem("b", "A Book"), library.Entry{Status: library.Planned})
	mid, _ := fe.bookSeries(t, "Mid")
	kid, _ := fe.bookSeries(t, "Kid")
	k := bookKind(t, fe)
	if err := fe.lib.UpdateSeriesDetails(bg, kid.ID, k, library.SeriesDetails{Title: "Kid", ParentID: mid.ID}); err != nil {
		t.Fatal(err)
	}
	base := seriesURL("book", ser.UID)
	lp := fe.get(base + "/link-candidates").Body.String()
	mustContain(t, lp, "Listed Comic")
	mustNotContain(t, lp, "Unlisted Comic", "A Book")
	pp := fe.get(base + "/parent-candidates").Body.String()
	mustContain(t, pp, "Mid")
	mustNotContain(t, pp, ">Saga<", ">Kid<")
	// a series with spin-offs cannot become one
	mp := fe.get(seriesURL("book", mid.UID) + "/parent-candidates").Body.String()
	mustContain(t, mp, "has spin-offs")
	mustNotContain(t, mp, `name="parent"`)
	// a kind without a link has no candidates
	fe2 := newFixtureEnv(t)
	s2, _ := fe2.tvSeries(t, "Tv")
	if b := fe2.get(seriesURL("tv", s2.UID) + "/link-candidates").Body.String(); strings.Contains(b, `type="radio"`) {
		t.Fatalf("link candidates without a link: %s", b)
	}
}

func sday(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// countFiles is the number of files in the covers directory.
func countFiles(t *testing.T, e *env) int {
	t.Helper()
	ents, err := os.ReadDir(e.covers)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// TestSeriesCoverSharedBetweenSeries: two series with the same image share
// the file; replacing one's cover keeps the file for the other, and a failed
// save keeps it too.
func TestSeriesCoverSharedBetweenSeries(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	a, _ := fe.bookSeries(t, "A")
	b, _ := fe.bookSeries(t, "B")
	up := func(ser *library.Series) {
		t.Helper()
		rec := fe.postMultipart(t, seriesURL("book", ser.UID)+"/edit", url.Values{"title": {ser.Title}}, &upload{"cover", "c.png", "image/png", png1("same")})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("upload: %d", rec.Code)
		}
	}
	up(a)
	up(b)
	name := mustSeriesWeb(t, fe, a.UID).CoverPath
	if name == "" || mustSeriesWeb(t, fe, b.UID).CoverPath != name {
		t.Fatalf("not shared: %q", name)
	}
	// B's failed save with the same image (invalid field) must not delete it
	bad := url.Values{"title": {"B"}, "field.volumes": {"abc"}}
	if rec := fe.postMultipart(t, seriesURL("book", b.UID)+"/edit", bad, &upload{"cover", "c.png", "image/png", png1("same")}); rec.Code != 422 {
		t.Fatalf("bad: %d", rec.Code)
	}
	if !hasFile(fe.env, name) {
		t.Fatal("failed save removed the shared file")
	}
	// A replaces its cover with a URL: B keeps the file
	if rec := fe.do("POST", seriesURL("book", a.UID)+"/edit", url.Values{"title": {"A"}, "cover_url": {"https://img.example/a.jpg"}}, false); rec.Code != 303 {
		t.Fatalf("replace: %d", rec.Code)
	}
	if !hasFile(fe.env, name) {
		t.Fatal("A replacing its cover deleted B's file")
	}
	// B removes it: now nothing references the file
	if rec := fe.do("POST", seriesURL("book", b.UID)+"/edit", url.Values{"title": {"B"}, "remove_cover": {"1"}}, false); rec.Code != 303 {
		t.Fatalf("remove: %d", rec.Code)
	}
	if hasFile(fe.env, name) {
		t.Fatal("unreferenced file kept")
	}
}

func TestSeriesPickersKeepCurrentChoice(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	linked := fe.comic(t, "c1", "Comic One")
	fe.comic(t, "c2", "Another")
	parent, _ := fe.bookSeries(t, "Parent")
	k := bookKind(t, fe)
	if err := fe.lib.UpdateSeriesDetails(bg, ser.ID, k, library.SeriesDetails{Title: "Saga", LinkedItemID: linked, ParentID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	base := seriesURL("book", ser.UID)
	for _, c := range []struct{ path, name, id, title string }{
		{"/link-candidates?q=another", "linked", idStr(linked), "Comic One"},
		{"/parent-candidates?q=zzz", "parent", idStr(parent.ID), "Parent"},
	} {
		b := fe.get(base + c.path).Body.String()
		mustContain(t, b, `name="`+c.name+`" value="`+c.id+`" checked`, c.title)
	}
	// the dialog itself shows the current choice once
	d := fe.get(base + "/edit").Body.String()
	if n := strings.Count(d, `value="`+idStr(linked)+`" checked`); n != 1 {
		t.Fatalf("current link shown %d times", n)
	}
}

func TestSeriesEditLinkedTitleUsesFallback(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData, func(book, _ *plugintest.Plugin) {
		book.M.Kinds[1].TitleLanguages = []string{"en", "de", "fr"}
		book.M.Kinds[1].TitleFallback = []string{"fr"}
	})
	ser, _ := fe.bookSeries(t, "Saga")
	d := bookItem("c9", "x")
	d.Kind = "comic"
	d.Titles = []plugin.Title{{Lang: "de", Text: "Deutsch"}, {Lang: "fr", Text: "Francais"}}
	id := fe.addFixture(t, "book", "comic", d, library.Entry{Status: library.Planned})
	k := bookKind(t, fe)
	if err := fe.lib.UpdateSeriesDetails(bg, ser.ID, k, library.SeriesDetails{Title: "Saga", LinkedItemID: id}); err != nil {
		t.Fatal(err)
	}
	b := fe.get(seriesURL("book", ser.UID) + "/edit").Body.String()
	mustContain(t, b, "Francais")
	mustNotContain(t, b, "Deutsch")
}

func TestSeriesSpinOffToggleOtherSeriesPart(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	a, _ := fe.bookSeries(t, "A")
	_, bIDs := fe.bookSeries(t, "B")
	rec := fe.do("POST", seriesURL("book", a.UID)+"/parts/"+idStr(bIDs[0])+"/spinoff", url.Values{"spin_off": {"1"}}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("%d", rec.Code)
	}
	if _, _, spin, _ := fe.lib.SeriesOfItem(bg, bIDs[0]); spin {
		t.Fatal("part of another series changed")
	}
}

func TestSeriesInfoShowsSetExpectedParts(t *testing.T) {
	fe := newFixtureEnv(t, withSeriesData)
	ser, _ := fe.bookSeries(t, "Saga")
	k := bookKind(t, fe)
	if err := fe.lib.UpdateSeriesDetails(bg, ser.ID, k, library.SeriesDetails{Title: "Saga", PartsTotal: 2}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, fe.get(seriesURL("book", ser.UID)).Body.String(), "Expected parts")
}
