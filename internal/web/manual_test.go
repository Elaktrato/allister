package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
)

type upload struct {
	field, filename, ctype string
	data                   []byte
}

func multipartBody(t testing.TB, v url.Values, up *upload) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range v {
		for _, x := range vs {
			if err := mw.WriteField(k, x); err != nil {
				t.Fatal(err)
			}
		}
	}
	if up != nil {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="` + up.field + `"; filename="` + up.filename + `"`}
		h["Content-Type"] = []string{up.ctype}
		pw, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		pw.Write(up.data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func (e *env) postManual(t testing.TB, v url.Values, up *upload) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, v, up)
	req := httptest.NewRequest("POST", "/anime/manual/new", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func validManualValues() url.Values {
	return url.Values{"title": {"My Show"}, "type": {"TV"}, "status": {"plan_to_watch"}}
}

func png1(extra string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00"), extra...)
}

func (e *env) animeCount(t testing.TB) int {
	t.Helper()
	rows, err := e.st.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func (e *env) coverFiles(t testing.TB) []string {
	t.Helper()
	ents, err := os.ReadDir(e.covers)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range ents {
		out = append(out, x.Name())
	}
	return out
}

// mustRejectManual asserts a 422 with nothing saved and no cover file.
func (e *env) mustRejectManual(t *testing.T, v url.Values, up *upload, want string) {
	t.Helper()
	rec := e.postManual(t, v, up)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422; body %s", rec.Code, rec.Body.String())
	}
	if want != "" {
		mustContain(t, rec.Body.String(), want)
	}
	if n := e.animeCount(t); n != 0 {
		t.Fatalf("%d anime saved", n)
	}
	if f := e.coverFiles(t); len(f) != 0 {
		t.Fatalf("cover files left: %v", f)
	}
}

var manualLoc = regexp.MustCompile(`^/anime/manual/([0-9a-f-]{36})$`)

func (e *env) createManual(t *testing.T, v url.Values, up *upload) string {
	t.Helper()
	rec := e.postManual(t, v, up)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	m := manualLoc.FindStringSubmatch(rec.Header().Get("Location"))
	if m == nil {
		t.Fatalf("location %q", rec.Header().Get("Location"))
	}
	return m[1]
}

func TestManualFormRouteBeatsOverview(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/anime/manual/new")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `action="/anime/manual/new"`, `enctype="multipart/form-data"`,
		`name="title"`, `name="type"`, `name="cover"`, `name="status"`)
}

func TestManualFormVaryHeader(t *testing.T) {
	e := newEnv(t)
	if got := e.get("/anime/manual/new").Header().Get("Vary"); got != "HX-Request" {
		t.Fatalf("Vary %q", got)
	}
}

func TestManualFormDefaultsToPreferredLanguage(t *testing.T) {
	e := newEnv(t)
	if err := e.set.Set(context.Background(), settings.KeyTitleLang, "en"); err != nil {
		t.Fatal(err)
	}
	b := e.get("/anime/manual/new").Body.String()
	mustContain(t, b, `<option value="en" selected>`)
	mustNotContain(t, b, `<option value="ja" selected>`)
	id := e.createManual(t, validManualValues(), nil) // no title_lang sent: preferred
	sv, _ := e.st.FindBySource(context.Background(), provider.Manual, id)
	if sv == nil || len(sv.Titles) != 1 || sv.Titles[0].Lang != "en" || sv.Titles[0].Text != "My Show" {
		t.Fatalf("titles %+v", sv)
	}
}

func TestManualRequiresTitleAndType(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", "   ")
	e.mustRejectManual(t, v, nil, "Please enter a title")
	v = validManualValues()
	v.Set("type", "")
	e.mustRejectManual(t, v, nil, "Please choose a type")
	v.Set("type", "MANGA")
	e.mustRejectManual(t, v, nil, "Please choose a type")
}

func TestManualSavesWithOptionalFieldsEmpty(t *testing.T) {
	e := newEnv(t)
	id := e.createManual(t, validManualValues(), nil)
	sv, err := e.st.FindBySource(context.Background(), provider.Manual, id)
	if err != nil || sv == nil {
		t.Fatalf("%v %v", sv, err)
	}
	if sv.Episodes != nil || sv.Year != nil || sv.Studio != "" || sv.Synopsis != "" || sv.CoverPath != "" || sv.Type != "TV" {
		t.Fatalf("%+v", sv)
	}
	if sv.SyncState != "done" {
		t.Fatalf("sync_state %q", sv.SyncState)
	}
	en, _ := e.st.GetListEntry(context.Background(), sv.ID)
	if en == nil || en.Status != store.PlanToWatch || en.Rating != nil || en.Progress != 0 {
		t.Fatalf("entry %+v", en)
	}
	if len(e.jobs.got) != 0 {
		t.Fatalf("jobs enqueued: %v", e.jobs.got)
	}
	if rec := e.get("/anime/manual/" + id); rec.Code != 200 {
		t.Fatalf("overview %d", rec.Code)
	}
}

func TestManualSavesAllFields(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title_lang", "ja-latn")
	v.Set("type", "MOVIE")
	v.Set("episodes", "12")
	v.Set("year", "1999")
	v.Set("studio", "Studio X")
	v.Set("synopsis", "Para one.\r\n\r\nPara <b>two</b>.")
	v.Set("status", "watching")
	v.Set("rating", "0")
	v.Set("progress", "5")
	v.Set("started_on", "2024-01-02")
	v.Set("finished_on", "2024-02-03")
	v.Set("comment", "fun")
	id := e.createManual(t, v, nil)
	sv, _ := e.st.FindBySource(context.Background(), provider.Manual, id)
	if sv.Type != "MOVIE" || *sv.Episodes != 12 || *sv.Year != 1999 || sv.Studio != "Studio X" ||
		sv.Synopsis != "Para one.\r\n\r\nPara <b>two</b>." || sv.Titles[0].Lang != "ja-latn" {
		t.Fatalf("%+v", sv)
	}
	en, _ := e.st.GetListEntry(context.Background(), sv.ID)
	if en.Status != store.Watching || en.Rating == nil || *en.Rating != 0 || en.Progress != 5 ||
		fmtDay(en.StartedOn) != "2024-01-02" || fmtDay(en.FinishedOn) != "2024-02-03" || en.Comment != "fun" {
		t.Fatalf("entry %+v", en)
	}
	b := e.get("/anime/manual/" + id).Body.String()
	mustContain(t, b, "Para one.", "Para &lt;b&gt;two&lt;/b&gt;.", "Studio X")
	mustNotContain(t, b, "<b>two</b>")
}

func TestManualRatingBlankIsNil(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("rating", "")
	id := e.createManual(t, v, nil)
	sv, _ := e.st.FindBySource(context.Background(), provider.Manual, id)
	en, _ := e.st.GetListEntry(context.Background(), sv.ID)
	if en.Rating != nil {
		t.Fatalf("rating %v", *en.Rating)
	}
}

func TestManualInvalidFieldsRejected(t *testing.T) {
	long := func(n int) string { return strings.Repeat("あ", n) }
	cases := map[string]func(url.Values){
		"title_lang":    func(v url.Values) { v.Set("title_lang", "xx") },
		"type":          func(v url.Values) { v.Set("type", "BOGUS") },
		"episodes nan":  func(v url.Values) { v.Set("episodes", "abc") },
		"episodes zero": func(v url.Values) { v.Set("episodes", "0") },
		"episodes big":  func(v url.Values) { v.Set("episodes", "10001") },
		"year low":      func(v url.Values) { v.Set("year", "1899") },
		"year high":     func(v url.Values) { v.Set("year", "2101") },
		"title long":    func(v url.Values) { v.Set("title", long(301)) },
		"studio long":   func(v url.Values) { v.Set("studio", long(201)) },
		"synopsis long": func(v url.Values) { v.Set("synopsis", long(5001)) },
		"status":        func(v url.Values) { v.Set("status", "nope") },
		"rating":        func(v url.Values) { v.Set("rating", "11") },
		"date":          func(v url.Values) { v.Set("started_on", "yesterday") },
		"progress>eps":  func(v url.Values) { v.Set("episodes", "12"); v.Set("progress", "13") },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			v := validManualValues()
			mod(v)
			e.mustRejectManual(t, v, nil, "")
		})
	}
	// boundaries are accepted
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", long(300))
	v.Set("episodes", "10000")
	v.Set("year", "2100")
	e.createManual(t, v, nil)
}

func TestManualInvalidKeepsInput(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", `Ti"tle <x>`)
	v.Set("type", "OVA")
	v.Set("year", "1800")
	v.Set("studio", "Studio Q")
	v.Set("synopsis", "Some text")
	v.Set("comment", "my comment")
	v.Set("rating", "7")
	v.Set("status", "dropped")
	rec := e.postManual(t, v, &upload{"cover", "c.png", "image/png", png1("")})
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "Ti&#34;tle &lt;x&gt;", "Studio Q", "Some text", "my comment", `value="7"`,
		`<option value="OVA" selected>`, `<option value="dropped" selected>`, "Year must be",
		"choose the cover file again")
	mustNotContain(t, b, "<x>")
	if f := e.coverFiles(t); len(f) != 0 {
		t.Fatalf("files %v", f)
	}
}

func TestManualCoverRejectsNonImage(t *testing.T) {
	cases := map[string]upload{
		"html as png": {"cover", "x.png", "image/png", []byte("<html><script>alert(1)</script></html>")},
		"svg":         {"cover", "x.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"svg as png":  {"cover", "x.png", "image/png", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"binary":      {"cover", "x.jpg", "image/jpeg", []byte{0, 1, 2, 3}},
	}
	for name, up := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			up := up
			e.mustRejectManual(t, validManualValues(), &up, "image")
		})
	}
}

func TestManualCoverSavedAndShown(t *testing.T) {
	e := newEnv(t)
	// client lies about type and name; sniffing decides
	id := e.createManual(t, validManualValues(), &upload{"cover", "evil.html", "text/html", png1("a")})
	sv, _ := e.st.FindBySource(context.Background(), provider.Manual, id)
	if !regexp.MustCompile(`^[0-9a-f]{64}\.png$`).MatchString(sv.CoverPath) {
		t.Fatalf("cover_path %q", sv.CoverPath)
	}
	if f := e.coverFiles(t); len(f) != 1 || f[0] != sv.CoverPath {
		t.Fatalf("files %v", f)
	}
	mustContain(t, e.get("/anime/manual/"+id).Body.String(), `src="/covers/`+sv.CoverPath+`"`)
	if rec := e.get("/covers/" + sv.CoverPath); rec.Code != 200 {
		t.Fatalf("serve %d", rec.Code)
	}
}

func TestManualCoverWithoutCoversDir(t *testing.T) {
	e := newEnv(t)
	e.covers = ""
	_, e.h = newServer(Deps{Settings: e.set, Store: e.st, Jobs: e.jobs}, time.Now)
	rec := e.postManual(t, validManualValues(), &upload{"cover", "c.png", "image/png", png1("")})
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "not available")
	if e.animeCount(t) != 0 {
		t.Fatal("saved")
	}
}

func TestManualOversizeRequest(t *testing.T) {
	e := newEnv(t)
	big := png1(strings.Repeat("a", 13<<20))
	rec := e.postManual(t, validManualValues(), &upload{"cover", "c.png", "image/png", big})
	if rec.Code != 413 && rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	if e.animeCount(t) != 0 || len(e.coverFiles(t)) != 0 {
		t.Fatal("something saved")
	}
}

func TestManualCoverOver10MiBRejected(t *testing.T) {
	e := newEnv(t)
	big := png1(strings.Repeat("a", 10<<20)) // within the request cap, over the image cap
	e.mustRejectManual(t, validManualValues(), &upload{"cover", "c.png", "image/png", big}, "10 MiB")
}

func TestManualEntryAppearsInList(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", "Home Made Show")
	v.Set("title_lang", "en")
	id := e.createManual(t, v, nil)
	b := e.get("/anime").Body.String()
	mustContain(t, b, "Home Made Show", `href="/anime/manual/`+id+`"`)
}

func TestManualOverviewHasNoCast(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", "Cast Free")
	id := e.createManual(t, v, nil)
	b := e.get("/anime/manual/" + id).Body.String()
	mustContain(t, b, "Cast Free")
	mustNotContain(t, b, ">Cast<", "Recommendations", "Cast unavailable")
	if e.fp.animeCalls != 0 || e.fp.castCall != 0 {
		t.Fatal("provider called for manual entry")
	}
}

func TestManualIdenticalTitlesAllowed(t *testing.T) {
	e := newEnv(t)
	a := e.createManual(t, validManualValues(), nil)
	b := e.createManual(t, validManualValues(), nil)
	if a == b {
		t.Fatal("same id")
	}
	if e.animeCount(t) != 2 {
		t.Fatalf("count %d", e.animeCount(t))
	}
}

func TestManualUrlencodedPostAlsoWorks(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/anime/manual/new", validManualValues(), false)
	if rec.Code != 303 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestListEmptyStateLinksToManual(t *testing.T) {
	e := newEnv(t)
	mustContain(t, e.get("/anime").Body.String(), `href="/anime/manual/new"`)
}

func TestCleanupCoverKeepsSharedFiles(t *testing.T) {
	e := newEnv(t)
	s := &server{deps: Deps{Store: e.st, CoversDir: e.covers}}
	os.MkdirAll(e.covers, 0o755)
	shared, orphan := "shared.png", "orphan.png"
	for _, n := range []string{shared, orphan} {
		os.WriteFile(e.covers+"/"+n, []byte("x"), 0o644)
	}
	a := provider.Anime{SearchResult: provider.SearchResult{Source: provider.Manual, ID: "m", CoverURL: shared}}
	if _, err := e.st.SaveAnime(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	s.cleanupCover(req, shared)
	s.cleanupCover(req, orphan)
	if f := e.coverFiles(t); len(f) != 1 || f[0] != shared {
		t.Fatalf("files %v", f)
	}
}
