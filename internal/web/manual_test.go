package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/settings"
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

// postMultipart posts v (and the optional upload) as multipart to target.
func (e *env) postMultipart(t testing.TB, target string, v url.Values, up *upload) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, v, up)
	req := httptest.NewRequest("POST", target, body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) postManual(t testing.TB, v url.Values, up *upload) *httptest.ResponseRecorder {
	t.Helper()
	return e.postMultipart(t, "/anime/manual/new", v, up)
}

// validManualValues is a valid anime manual form. ADAPTED: the schema's
// fields are posted as field.<key> and the status is a core status class.
func validManualValues() url.Values {
	return url.Values{"title": {"My Show"}, "field.type": {"TV"}, "status": {"planned"}}
}

func png1(extra string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00"), extra...)
}

// itemCount counts every saved item of every plugin.
func (e *env) itemCount(t testing.TB) int {
	t.Helper()
	var n int
	if err := e.d.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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

// mustRejectAt asserts a 422 from target with nothing saved and no new cover file.
func (e *env) mustRejectAt(t *testing.T, target string, v url.Values, up *upload, want string) string {
	t.Helper()
	items, files := e.itemCount(t), e.coverFiles(t)
	rec := e.postMultipart(t, target, v, up)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422; body %s", rec.Code, rec.Body.String())
	}
	if want != "" {
		mustContain(t, rec.Body.String(), want)
	}
	if n := e.itemCount(t); n != items {
		t.Fatalf("%d items saved", n-items)
	}
	if f := e.coverFiles(t); !reflect.DeepEqual(f, files) {
		t.Fatalf("cover files left: %v", f)
	}
	return rec.Body.String()
}

func (e *env) mustRejectManual(t *testing.T, v url.Values, up *upload, want string) {
	t.Helper()
	e.mustRejectAt(t, "/anime/manual/new", v, up, want)
}

var manualLoc = regexp.MustCompile(`^/([a-z]+)/manual/([0-9a-f-]{36})(\?kind=[a-z]+)?$`)

// createAt posts a manual form to target and returns the new item.
func (e *env) createAt(t *testing.T, target string, v url.Values, up *upload) library.Item {
	t.Helper()
	rec := e.postMultipart(t, target, v, up)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	m := manualLoc.FindStringSubmatch(rec.Header().Get("Location"))
	if m == nil {
		t.Fatalf("location %q", rec.Header().Get("Location"))
	}
	items := e.savedItems(t, m[1], "manual", m[2])
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	return items[0]
}

func (e *env) createManual(t *testing.T, v url.Values, up *upload) string {
	t.Helper()
	return e.createAt(t, "/anime/manual/new", v, up).SourceID
}

// manualAnime returns the saved manual anime id with its entry.
func (e *env) manualAnime(t *testing.T, id string) (library.Item, *library.Entry) {
	t.Helper()
	items := e.savedItems(t, "anime", "manual", id)
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	en, err := e.lib.Entry(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return items[0], en
}

func TestManualFormRouteBeatsOverview(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/anime/manual/new")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `action="/anime/manual/new"`, `enctype="multipart/form-data"`,
		`name="title"`, `name="field.type"`, `name="cover"`, `name="status"`)
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
	it, _ := e.manualAnime(t, id)
	if !reflect.DeepEqual(it.Titles, []plugin.Title{{Lang: "en", Text: "My Show"}}) {
		t.Fatalf("titles %+v", it.Titles)
	}
	// a preferred language the kind does not offer falls back to the first
	if err := e.set.Set(context.Background(), settings.KeyTitleLang, "de"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/anime/manual/new").Body.String(), `<option value="ja" selected>`)
}

func TestManualRequiresTitleAndType(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", "   ")
	e.mustRejectManual(t, v, nil, "Please enter a title")
	v = validManualValues()
	v.Set("field.type", "")
	e.mustRejectManual(t, v, nil, "Please choose a type")
	v.Set("field.type", "MANGA")
	e.mustRejectManual(t, v, nil, "Please choose a type")
	v.Del("field.type")
	e.mustRejectManual(t, v, nil, "Please choose a type")
}

func TestManualSavesWithOptionalFieldsEmpty(t *testing.T) {
	e := newEnv(t)
	id := e.createManual(t, validManualValues(), nil)
	it, en := e.manualAnime(t, id)
	if len(it.Fields) != 1 || it.Fields["type"].Text != "TV" || it.CoverPath != "" || len(it.Genres) != 0 || len(it.Tags) != 0 {
		t.Fatalf("%+v", it)
	}
	if it.SyncState != "done" {
		t.Fatalf("sync_state %q", it.SyncState)
	}
	if en == nil || en.Status != library.Planned || en.Rating != nil || en.Progress["episode"] != 0 {
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
	v.Set("field.type", "MOVIE")
	v.Set("field.episodes", "12")
	v.Set("field.year", "1999")
	v.Set("field.studio", "Studio X")
	v.Set("field.synopsis", "Para one.\r\n\r\nPara <b>two</b>.")
	v.Set("field.started_on", "1999-04-01")
	v.Set("genres", "Drama\nMystery")
	v.Set("tags", "Time Travel:80!\nTwist")
	v.Set("status", "active")
	v.Set("rating", "0")
	v.Set("progress", "5")
	v.Set("started_on", "2024-01-02")
	v.Set("finished_on", "2024-02-03")
	v.Set("comment", "fun")
	id := e.createManual(t, v, nil)
	it, en := e.manualAnime(t, id)
	f := it.Fields
	if f["type"].Text != "MOVIE" || *f["episodes"].Num != 12 || *f["year"].Num != 1999 || f["studio"].Text != "Studio X" ||
		f["synopsis"].Text != "Para one.\r\n\r\nPara <b>two</b>." || it.Titles[0].Lang != "ja-latn" || fmtDay(f["started_on"].Date) != "1999-04-01" {
		t.Fatalf("%+v", it)
	}
	if !reflect.DeepEqual(it.Genres, []string{"Drama", "Mystery"}) || len(it.Tags) != 2 || *it.Tags[0].Rank != 80 || !it.Tags[0].Spoiler {
		t.Fatalf("terms %+v %+v", it.Genres, it.Tags)
	}
	if en.Status != library.Active || en.Rating == nil || *en.Rating != 0 || en.Progress["episode"] != 5 ||
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
	_, en := e.manualAnime(t, e.createManual(t, v, nil))
	if en.Rating != nil {
		t.Fatalf("rating %v", *en.Rating)
	}
}

// ADAPTED: the generic limits are 1000 characters for a one-line text field
// (was 200 for the studio) and 20000 for a text-section field (was 5000 for
// the synopsis, now the library's limit).
func TestManualInvalidFieldsRejected(t *testing.T) {
	long := func(n int) string { return strings.Repeat("あ", n) }
	cases := map[string]func(url.Values){
		"title_lang":     func(v url.Values) { v.Set("title_lang", "xx") },
		"type":           func(v url.Values) { v.Set("field.type", "BOGUS") },
		"episodes nan":   func(v url.Values) { v.Set("field.episodes", "abc") },
		"episodes zero":  func(v url.Values) { v.Set("field.episodes", "0") },
		"episodes big":   func(v url.Values) { v.Set("field.episodes", "10001") },
		"episodes hex":   func(v url.Values) { v.Set("field.episodes", "0x10") },
		"episodes inf":   func(v url.Values) { v.Set("field.episodes", "Inf") },
		"year low":       func(v url.Values) { v.Set("field.year", "1899") },
		"year high":      func(v url.Values) { v.Set("field.year", "2101") },
		"date":           func(v url.Values) { v.Set("field.started_on", "1999-13-01") },
		"title long":     func(v url.Values) { v.Set("title", long(301)) },
		"title control":  func(v url.Values) { v.Set("title", "a\x07b") },
		"studio long":    func(v url.Values) { v.Set("field.studio", long(1001)) },
		"studio newline": func(v url.Values) { v.Set("field.studio", "a\nb") },
		"synopsis long":  func(v url.Values) { v.Set("field.synopsis", long(20001)) },
		"synopsis ctrl":  func(v url.Values) { v.Set("field.synopsis", "a\x00b") },
		"unknown field":  func(v url.Values) { v.Set("field.nope", "x") },
		"genre control":  func(v url.Values) { v.Set("genres", "a\x07") },
		"tag rank":       func(v url.Values) { v.Set("tags", "x:101") },
		"status":         func(v url.Values) { v.Set("status", "nope") },
		"rating":         func(v url.Values) { v.Set("rating", "11") },
		"entry date":     func(v url.Values) { v.Set("started_on", "yesterday") },
		"progress>eps":   func(v url.Values) { v.Set("field.episodes", "12"); v.Set("progress", "13") },
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
	v.Set("field.episodes", "10000")
	v.Set("field.year", "2100")
	v.Set("field.studio", long(1000))
	e.createManual(t, v, nil)
}

func TestManualInvalidKeepsInput(t *testing.T) {
	e := newEnv(t)
	v := validManualValues()
	v.Set("title", `Ti"tle <x>`)
	v.Set("field.type", "OVA")
	v.Set("field.year", "1800")
	v.Set("field.studio", "Studio Q")
	v.Set("field.synopsis", "Some text")
	v.Set("genres", "Gen <g>")
	v.Set("tags", "Tag <t>:5!")
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
		"choose the cover file again", "Gen &lt;g&gt;", "Tag &lt;t&gt;:5!")
	mustNotContain(t, b, "<x>", "<g>", "<t>")
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
	it, _ := e.manualAnime(t, id)
	if !regexp.MustCompile(`^[0-9a-f]{64}\.png$`).MatchString(it.CoverPath) {
		t.Fatalf("cover_path %q", it.CoverPath)
	}
	if f := e.coverFiles(t); len(f) != 1 || f[0] != it.CoverPath {
		t.Fatalf("files %v", f)
	}
	mustContain(t, e.get("/anime/manual/"+id).Body.String(), `src="/covers/`+it.CoverPath+`"`)
	if rec := e.get("/covers/" + it.CoverPath); rec.Code != 200 {
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
	if e.itemCount(t) != 0 {
		t.Fatal("saved")
	}
	mustNotContain(t, e.get("/anime/manual/new").Body.String(), `name="cover"`)
}

func TestManualOversizeRequest(t *testing.T) {
	e := newEnv(t)
	big := png1(strings.Repeat("a", 13<<20))
	rec := e.postManual(t, validManualValues(), &upload{"cover", "c.png", "image/png", big})
	if rec.Code != 413 {
		t.Fatalf("status %d", rec.Code)
	}
	if e.itemCount(t) != 0 || len(e.coverFiles(t)) != 0 {
		t.Fatal("something saved")
	}
}

func TestManualCoverOver10MiBRejected(t *testing.T) {
	e := newEnv(t)
	big := png1(strings.Repeat("a", 10<<20)) // within the request cap, over the image cap
	e.mustRejectManual(t, validManualValues(), &upload{"cover", "c.png", "image/png", big}, "10 MiB")
}

// A database failure after the cover file was written removes the file
// unless another row references the same (content-hash) name.
func TestManualCoverCleanupWhenSaveFails(t *testing.T) {
	e := newEnv(t)
	up := &upload{"cover", "c.png", "image/png", png1("shared")}
	if _, err := e.d.Exec(`CREATE TRIGGER boom BEFORE INSERT ON items BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := e.postManual(t, validManualValues(), up); rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	if f := e.coverFiles(t); len(f) != 0 || e.itemCount(t) != 0 {
		t.Fatalf("orphan file %v", f)
	}
	if _, err := e.d.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	id := e.createManual(t, validManualValues(), up)
	it, _ := e.manualAnime(t, id)
	if _, err := e.d.Exec(`CREATE TRIGGER boom BEFORE INSERT ON items BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := e.postManual(t, validManualValues(), up); rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	if f := e.coverFiles(t); len(f) != 1 || f[0] != it.CoverPath {
		t.Fatalf("shared file removed: %v", f)
	}
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
	if e.itemCount(t) != 2 {
		t.Fatalf("count %d", e.itemCount(t))
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
	s := &server{deps: Deps{Library: e.lib, CoversDir: e.covers}}
	os.MkdirAll(e.covers, 0o755)
	shared, orphan := "shared.png", "orphan.png"
	for _, n := range []string{shared, orphan} {
		os.WriteFile(e.covers+"/"+n, []byte("x"), 0o644)
	}
	d := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "m", CoverURL: shared, Titles: ttl("Shared")}}
	e.saveFixture(t, "anime", "anime", d)
	s.cleanupCover(context.Background(), shared)
	s.cleanupCover(context.Background(), orphan)
	s.cleanupCover(context.Background(), "")
	if f := e.coverFiles(t); len(f) != 1 || f[0] != shared {
		t.Fatalf("files %v", f)
	}
}

func TestManualFormOffersGenresAndTags(t *testing.T) {
	e := newFixtureEnv(t)
	b := e.get("/anime/manual/new").Body.String()
	mustContain(t, b, `name="genres"`, `name="tags"`, "Time Travel:80!", "One per line")
	b = e.get("/book/manual/new").Body.String()
	mustContain(t, b, `name="genres"`)
	mustNotContain(t, b, `name="tags"`)
	// genres enabled but not editable: no input
	e2 := newFixtureEnv(t, func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Genres.Editable = false })
	mustNotContain(t, e2.get("/book/manual/new").Body.String(), `name="genres"`)
}

func TestManualBookFixture(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Fields[1].Unit = "pages" })
	b := e.get("/book/manual/new").Body.String()
	mustContain(t, b, "Add book manually", `action="/book/manual/new"`, `name="field.author"`, `name="field.isbn"`,
		`type="number" name="field.pages"`, `min="1"`, ">pages<", `name="genres"`, `name="progress.pages"`,
		"Want to read", `<option value="en" selected>`)
	mustNotContain(t, b, "episode", "Episode", "Authors", `name="tags"`) // the nav names other plugins
	v := url.Values{"title": {"Earthsea"}, "field.author": {"Ursula K. Le Guin"}, "field.pages": {"183"},
		"field.isbn": {"978-0"}, "genres": {"Fantasy\nYoung adult"}, "status": {"active"}, "progress.pages": {"20"}}
	it := e.createAt(t, "/book/manual/new", v, nil)
	if it.PluginID != "book" || it.Kind != "book" || it.Fields["author"].Text != "Ursula K. Le Guin" || *it.Fields["pages"].Num != 183 ||
		it.Fields["isbn"].Text != "978-0" || !reflect.DeepEqual(it.Genres, []string{"Fantasy", "Young adult"}) || it.SyncState != "done" {
		t.Fatalf("%+v", it)
	}
	en, _ := e.lib.Entry(context.Background(), it.ID)
	if en.Status != library.Active || en.Progress["pages"] != 20 {
		t.Fatalf("entry %+v", en)
	}
	if len(e.jobs.got) != 0 {
		t.Fatalf("job for a fixture: %+v", e.jobs.got)
	}
	// page above the typed total, page count below the field minimum
	v.Set("progress.pages", "200")
	e.mustRejectAt(t, "/book/manual/new", withTitle(v, "Other"), nil, "")
	v.Set("progress.pages", "1")
	v.Set("field.pages", "0")
	e.mustRejectAt(t, "/book/manual/new", withTitle(v, "Other"), nil, "Pages must be")
}

// withTitle returns a copy of v with another title (mustRejectAt checks that
// nothing new was saved, so these tests run on an empty library).
func withTitle(v url.Values, title string) url.Values {
	out := url.Values{}
	for k, x := range v {
		out[k] = append([]string(nil), x...)
	}
	out.Set("title", title)
	return out
}

func tvWithNetworks(_, tv *plugintest.Plugin) {
	k := &tv.M.Kinds[0]
	k.Fields = append(k.Fields, plugin.FieldDef{Key: "networks", Label: "Networks", Type: plugin.FieldList, Editable: true})
}

func TestManualTVFixture(t *testing.T) {
	e := newFixtureEnv(t, tvWithNetworks)
	b := e.get("/tv/manual/new").Body.String()
	mustContain(t, b, `<select name="field.type"`, `<option value="series">`, `<option value="miniseries">`,
		`<textarea name="field.networks"`, "one per line", `name="tags"`, `name="progress.episode"`,
		`<option value="original">`)
	v := url.Values{"title": {"Dark"}, "title_lang": {"original"}, "field.type": {"series"}, "status": {"planned"},
		"field.networks": {"Netflix\r\nNetflix\n  ZDF  \n\nnetflix"},
		"genres":         {"Drama\r\nSci-Fi, Fantasy\n\nThriller"},
		"tags":           {"Time Travel:80!\nTwist\nRe:Zero\nSpoiler!\n  Plot:0  "}}
	it := e.createAt(t, "/tv/manual/new", v, nil)
	if !reflect.DeepEqual(it.Fields["networks"].List, []string{"Netflix", "ZDF", "netflix"}) {
		t.Fatalf("list %q", it.Fields["networks"].List)
	}
	if !reflect.DeepEqual(it.Genres, []string{"Drama", "Sci-Fi, Fantasy", "Thriller"}) || it.Titles[0].Lang != "original" {
		t.Fatalf("%+v", it)
	}
	want := []plugin.Tag{{Name: "Time Travel", Rank: ip(80), Spoiler: true}, {Name: "Twist"}, {Name: "Re:Zero"},
		{Name: "Spoiler", Spoiler: true}, {Name: "Plot", Rank: ip(0)}}
	if !reflect.DeepEqual(it.Tags, want) {
		t.Fatalf("tags %+v", it.Tags)
	}
	e2 := newFixtureEnv(t, tvWithNetworks)
	many := make([]string, 51)
	for i := range many {
		many[i] = "n" + idStr(int64(i))
	}
	for name, mod := range map[string]func(url.Values){
		"51 values": func(v url.Values) { v.Set("field.networks", strings.Join(many, "\n")) },
		"enum":      func(v url.Values) { v.Set("field.type", "movie") },
		"rank":      func(v url.Values) { v.Set("tags", "x:101") },
		"rank sign": func(v url.Values) { v.Set("tags", "x:-1") },
		"lang":      func(v url.Values) { v.Set("title_lang", "ja") },
	} {
		w := withTitle(v, "Other")
		mod(w)
		rec := e2.postMultipart(t, "/tv/manual/new", w, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if e2.itemCount(t) != 0 {
		t.Fatal("saved")
	}
}

func TestManualRequiredFieldsEnforced(t *testing.T) {
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) { book.M.Kinds[0].Fields[0].Required = true })
	mustContain(t, e.get("/book/manual/new").Body.String(), `name="field.author" value="" required`)
	e.mustRejectAt(t, "/book/manual/new", url.Values{"title": {"X"}, "status": {"planned"}}, nil, "Please enter author")
	e.mustRejectAt(t, "/book/manual/new", url.Values{"title": {"X"}, "status": {"planned"}, "field.author": {"   "}}, nil, "Please enter author")
}

func TestManualUnknownKind404(t *testing.T) {
	e := newFixtureEnv(t)
	if c := e.get("/book/manual/new?kind=comic").Code; c != 404 {
		t.Fatalf("%d", c)
	}
	if c := e.postMultipart(t, "/book/manual/new?kind=comic", url.Values{"title": {"x"}}, nil).Code; c != 404 {
		t.Fatalf("post %d", c)
	}
}

func TestManualKindSwitch(t *testing.T) {
	e := newFixtureEnv(t, withComicKind)
	b := e.get("/book/manual/new").Body.String()
	mustContain(t, b, `href="/book/manual/new?kind=comic"`)
	b = e.get("/book/manual/new?kind=comic").Body.String()
	mustContain(t, b, `action="/book/manual/new?kind=comic"`)
	it := e.createAt(t, "/book/manual/new?kind=comic", url.Values{"title": {"Comic"}, "status": {"planned"}}, nil)
	if it.Kind != "comic" {
		t.Fatalf("kind %q", it.Kind)
	}
}
