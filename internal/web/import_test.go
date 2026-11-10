package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/plugins/anime/anilist"
	"github.com/allister/allister/internal/settings"
)

// fnResolver is an AniList-like provider for the anime plugin: its MAL id
// lookup is the function, everything else is unavailable.
type fnResolver func(malID string) (anilist.Anime, error)

func (f fnResolver) AnimeByMALID(_ context.Context, malID string) (anilist.Anime, error) {
	return f(malID)
}
func (f fnResolver) Name() anilist.Source { return anilist.AniList }
func (f fnResolver) Search(context.Context, string) ([]anilist.SearchResult, error) {
	return nil, anilist.ErrUnavailable
}
func (f fnResolver) Anime(context.Context, string) (anilist.Anime, error) {
	return anilist.Anime{}, anilist.ErrUnavailable
}
func (f fnResolver) Cast(context.Context, string) ([]anilist.CastMember, error) {
	return nil, anilist.ErrUnavailable
}

// dbJobs records jobs in the fake and also writes them to the jobs table: the
// runner reconciles imports against that table.
type dbJobs struct {
	*fakeJobs
	q *jobs.Queue
}

func (d dbJobs) Enqueue(ctx context.Context, kind string, payload any) error {
	if err := d.fakeJobs.Enqueue(ctx, kind, payload); err != nil {
		return err
	}
	return d.q.Enqueue(ctx, kind, payload)
}

type impEnv struct {
	d    *sql.DB
	h    http.Handler
	jobs *fakeJobs
	im   *importer.Runner
	lib  *library.Library
	reg  *plugin.Registry
	set  *settings.Store
}

// newImpEnv is the legacy import env: the real anime plugin (Yamtrack
// importer, AniList source over res) is the only plugin.
func newImpEnv(t *testing.T, res fnResolver) *impEnv {
	t.Helper()
	return newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		if err := r.Register(anime.New(anime.Options{Provider: res})); err != nil {
			t.Fatal(err)
		}
		return r
	})
}

func newImpEnvWith(t *testing.T, mk func(plugin.EnabledStore) *plugin.Registry) *impEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &impEnv{d: d, jobs: &fakeJobs{}, set: settings.New(d)}
	e.reg = mk(e.set)
	e.lib = library.New(d, e.reg)
	syncJobs := map[string]func(int64) (string, any){}
	for _, m := range e.reg.Manifests() {
		syncJobs[m.ID] = jobs.SyncJobFor(m.ID)
	}
	// A fake clock whose sleep returns at once keeps the pacing out of the tests.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.im = importer.New(importer.Deps{DB: d, Library: e.lib, Registry: e.reg, Queue: dbJobs{e.jobs, jobs.NewQueue(d)},
		SyncJobs: syncJobs,
		Now:      func() time.Time { now = now.Add(time.Hour); return now },
		Sleep:    func(ctx context.Context, _ time.Duration) error { return ctx.Err() }})
	e.h = NewServer(Deps{Settings: e.set, Jobs: e.jobs, Imports: e.im, Registry: e.reg, Library: e.lib, SyncJobs: syncJobs})
	return e
}

// bookImport is the book fixture with a programmable importer and source.
type bookImport struct {
	p   *plugintest.Plugin
	src *plugintest.LookupSource
	imp *plugintest.FakeImporter
}

func newBookImport() *bookImport {
	b := &bookImport{p: plugintest.BookPlugin(), imp: &plugintest.FakeImporter{}}
	b.src = &plugintest.LookupSource{Fn: func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		return plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "books", ID: "w" + v, Kind: "book",
			Titles: []plugin.Title{{Lang: "en", Text: "Book " + v}}}}, nil
	}}
	b.p.Sources = map[string]plugin.Source{"books": b.src}
	b.p.Importers = map[string]plugin.Importer{"books-csv": b.imp}
	return b
}

func bookRow(isbn, title string) plugin.ParsedEntry {
	return plugin.ParsedEntry{Kind: "book", Title: title, ExternalIDs: map[string]string{"isbn": isbn}, Status: plugin.Planned}
}

const impHeader = "media_id,source,media_type,title,image,score,status,notes,start_date,end_date,progress\n"

func impRow(id int, title, status string) string {
	return fmt.Sprintf("%d,mal,anime,%q,,,%s,,,,0\n", id, title, status)
}

// upload posts only the file (no importer field), as the legacy form did.
func (e *impEnv) upload(t testing.TB, field, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	// several importers exist now: name the first file importer of the env's
	// enabled plugins, like a user choosing from the select
	v := url.Values{}
	for _, m := range e.reg.Manifests() {
		if !e.reg.IsEnabled(m.ID) {
			continue
		}
		for _, im := range m.Importers {
			if im.AccountLabel == "" {
				v.Set("importer", m.ID+"/"+im.ID)
				break
			}
		}
		if len(v) > 0 {
			break
		}
	}
	return e.uploadTo(t, v, field, name, data)
}

func (e *impEnv) uploadTo(t testing.TB, v url.Values, field, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, v, &upload{field: field, filename: name, ctype: "text/csv", data: data})
	req := httptest.NewRequest("POST", "/settings/import", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *impEnv) get(path string, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *impEnv) postPath(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// runJob runs the enqueued import job to completion.
func (e *impEnv) runJob(t *testing.T) {
	t.Helper()
	for _, j := range e.jobs.got {
		if j.Kind == importer.KindRun {
			raw, _ := json.Marshal(j.Payload)
			if err := e.im.Handler()(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no import job enqueued")
}

func notFoundExcept(ok ...string) fnResolver {
	return func(mal string) (anilist.Anime, error) {
		for _, o := range ok {
			if o == mal {
				return anilist.Anime{SearchResult: anilist.SearchResult{Source: anilist.AniList, ID: "9" + mal,
					Titles: []anilist.Title{{Lang: "en", Text: "T" + mal}}}, MALID: mal}, nil
			}
		}
		return anilist.Anime{}, anilist.ErrNotFound
	}
}

// TestImportPageShowsProgress: the legacy test without its failed row (the
// anime source maps a plain provider error to "unavailable", which is
// retried, not failed); the failed row is TestImportPageListsFailedRows.
func TestImportPageShowsProgress(t *testing.T) {
	e := newImpEnv(t, notFoundExcept("1"))
	data := impHeader + impRow(1, "Found One", "Completed") + impRow(2, `Lost <b>Show</b>`, "Planning") +
		impRow(4, "Weird", "Nope") + "5,igdb,game,G,,,Completed,,,,0\n"
	if rec := e.upload(t, "file", "yamtrack.csv", []byte(data)); rec.Code != 303 || rec.Header().Get("Location") != "/settings/import" {
		t.Fatalf("upload: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	e.runJob(t)
	rec := e.get("/settings/import", false)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "<html", "yamtrack.csv", "Done", `role="progressbar"`, "aria-valuenow=\"100\"",
		"unknown status", "Lost &lt;b&gt;Show&lt;/b&gt;", "data-ignored",
		"Not found on AniList: added as manual entries with the data from Yamtrack CSV.")
	mustNotContain(t, body, "<b>Show</b>", "hx-trigger", "Cancel import", "and 0 more")
	s, err := findByMAL(context.Background(), e.lib, "2")
	if err != nil || s == nil {
		t.Fatal(err)
	}
	mustContain(t, body, `href="/anime/manual/`+url.PathEscape(s.SourceID)+`"`)
	// counts: total 3 (game ignored), imported 1, unmatched 1, skipped 1
	for _, want := range []string{"Total", "Imported", "Unmatched", "Failed", "Skipped"} {
		mustContain(t, body, want)
	}
	mustContain(t, body, `data-count="total">3<`, `data-count="imported">1<`, `data-count="unmatched">1<`,
		`data-count="failed">0<`, `data-count="skipped">1<`)
}

// TestImportPageListsFailedRows is the failed row of the legacy
// TestImportPageShowsProgress, on the book fixture whose source returns a
// plain error.
func TestImportPageListsFailedRows(t *testing.T) {
	b := newBookImport()
	b.src.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		if v == "3" {
			return plugin.ItemData{}, errors.New("graphql exploded")
		}
		return plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "books", ID: "w" + v, Titles: []plugin.Title{{Lang: "en", Text: "B"}}}}, nil
	}
	b.imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookRow("1", "Found One"), bookRow("3", "Broken One")}}
	e := newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(b.p)
		return r
	})
	if rec := e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "books.csv", []byte("x")); rec.Code != 303 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	e.runJob(t)
	body := e.get("/settings/import", false).Body.String()
	mustContain(t, body, "Done", "Broken One", "graphql exploded", `data-count="failed">1<`, `data-count="imported">1<`)
	mustNotContain(t, body, "and 0 more")
}

func TestImportPagePollsOnlyWhileActive(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	mustNotContain(t, e.get("/settings/import", false).Body.String(), "hx-trigger")
	e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")))
	full := e.get("/settings/import", false).Body.String()
	mustContain(t, full, `hx-get="/settings/import"`, `hx-trigger="every 3s"`, `hx-swap="outerHTML"`,
		`action="/settings/import/cancel"`, "Cancel import", "Pending")
	frag := e.get("/settings/import", true)
	fb := frag.Body.String()
	if strings.Contains(fb, "<html") || strings.Contains(fb, `enctype="multipart/form-data"`) {
		t.Errorf("htmx request must get only the fragment: %s", fb)
	}
	mustContain(t, fb, `hx-trigger="every 3s"`, `id="import-progress"`)
	if v := frag.Header().Get("Vary"); !strings.Contains(v, "HX-Request") {
		t.Errorf("Vary = %q", v)
	}
	e.runJob(t)
	done := e.get("/settings/import", true).Body.String()
	mustContain(t, done, `id="import-progress"`)
	mustNotContain(t, done, "hx-trigger", "hx-get")
}

func TestImportPageHasUploadFormAndSettingsLink(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	page := e.get("/settings/import", false).Body.String()
	mustContain(t, page, `action="/settings/import"`, `enctype="multipart/form-data"`, `name="file"`, `type="file"`)
	// several file importers: the form asks which one
	mustContain(t, page, `<select name="importer"`, "Yamtrack CSV (Anime)", "MyAnimeList XML export (Anime)",
		"Export your list from Yamtrack", "Rows are matched on AniList")
	mustNotContain(t, page, "AniList account (Anime)") // account importers live on the plugin page
	mustContain(t, settingsTabs(t, e.get("/settings", false).Body.String()), `href="/settings/import"`, "<span>Import</span>")
}

func TestImportUploadRejectsBadFilesAndStoresNothing(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	cases := map[string]struct {
		field string
		data  string
		want  string
	}{
		"missing columns": {"file", "foo,bar\n1,2\n", "missing column"},
		"no anime rows":   {"file", impHeader + "1,igdb,game,G,,,Completed,,,,0\n", "no importable anime rows"},
		"empty":           {"file", "", "empty"},
		"wrong field":     {"other", impHeader + impRow(1, "x", "Planning"), "choose"},
	}
	for name, c := range cases {
		rec := e.upload(t, c.field, "x.csv", []byte(c.data))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d", name, rec.Code)
			continue
		}
		mustContain(t, rec.Body.String(), c.want, `role="alert"`)
	}
	if len(e.jobs.got) != 0 {
		t.Errorf("jobs enqueued: %+v", e.jobs.got)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil {
		t.Errorf("import stored: %+v", r)
	}
	// no file at all (urlencoded body)
	rec := e.postPath("/settings/import")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no file: status %d", rec.Code)
	}
}

func TestImportUploadOversizeIs413(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	big := []byte(impHeader + strings.Repeat("x", 21<<20))
	rec := e.upload(t, "file", "big.csv", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.jobs.got) != 0 {
		t.Errorf("stored: %v %v", r, e.jobs.got)
	}
}

// The cap holds whatever the importer: the fixture importer never reads its
// input, so only the web layer can refuse the body.
func TestImportUploadCapAppliesToEveryImporter(t *testing.T) {
	b := newBookImport()
	b.imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookRow("1", "One")}}
	e := newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(b.p)
		return r
	})
	rec := e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "big.csv", make([]byte, maxImportBody+1))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil {
		t.Errorf("stored: %+v", r)
	}
	// just under the cap is fine
	if rec := e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "ok.csv", make([]byte, maxImportBody-4096)); rec.Code != 303 {
		t.Fatalf("under the cap: %d", rec.Code)
	}
}

func TestImportSecondUploadWhileActiveIs409(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	good := []byte(impHeader + impRow(1, "One", "Planning"))
	if rec := e.upload(t, "file", "a.csv", good); rec.Code != 303 {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := e.upload(t, "file", "b.csv", good)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `href="/settings/import"`, "already")
	if n := len(e.jobs.got); n != 1 {
		t.Errorf("jobs = %d", n)
	}
}

func TestImportCancelEndpoint(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")))
	rec := e.postPath("/settings/import/cancel")
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings/import" {
		t.Fatalf("cancel: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body := e.get("/settings/import", false).Body.String()
	mustContain(t, body, "Cancelled")
	mustNotContain(t, body, "hx-trigger", "Cancel import")
	// the job started later does nothing
	e.runJob(t)
	if s, _ := findByMAL(context.Background(), e.lib, "1"); s != nil {
		t.Error("cancelled import saved a row")
	}
	// cancelling again, with nothing active, is harmless
	if rec := e.postPath("/settings/import/cancel"); rec.Code != 303 {
		t.Errorf("second cancel: %d", rec.Code)
	}
	// and a new upload is possible
	if rec := e.upload(t, "file", "b.csv", []byte(impHeader+impRow(2, "Two", "Planning"))); rec.Code != 303 {
		t.Errorf("upload after cancel: %d", rec.Code)
	}
}

// TestImportListsAreCappedAt200 is the legacy test on the book fixture (a
// plain source error fails a row only with a generic source).
func TestImportListsAreCappedAt200(t *testing.T) {
	b := newBookImport()
	b.src.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, errors.New("nope")
	}
	for i := 1; i <= 230; i++ {
		b.imp.Result.Entries = append(b.imp.Result.Entries, bookRow(fmt.Sprint(i), fmt.Sprintf("Show %d", i)))
	}
	e := newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(b.p)
		return r
	})
	e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "a.csv", []byte("x"))
	e.runJob(t)
	body := e.get("/settings/import", false).Body.String()
	mustContain(t, body, "Show 200", "and 30 more")
	mustNotContain(t, body, "Show 201")
}

func TestImportRoutesNeedAnImporter(t *testing.T) {
	h, _, _ := newTestServer(t)
	for _, c := range [][2]string{{"GET", "/settings/import"}, {"POST", "/settings/import"}, {"POST", "/settings/import/cancel"}, {"POST", "/settings/import/resume"}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c[0], c[1], nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d", c[0], c[1], rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	mustNotContain(t, rec.Body.String(), `href="/settings/import"`)
}

func TestImportStalledShowsNoteAndResumeButton(t *testing.T) {
	e := newImpEnv(t, notFoundExcept("1", "2"))
	e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")+impRow(2, "Two", "Planning")))
	// the queue gave up on the job
	if _, err := e.d.Exec(`UPDATE jobs SET state='failed', last_error='gave up after 10 partial runs'`); err != nil {
		t.Fatal(err)
	}
	body := e.get("/settings/import", false).Body.String()
	mustContain(t, body, "Failed", "gave up after 10 partial runs", `action="/settings/import/resume"`, "Resume import")
	mustNotContain(t, body, "hx-trigger", "Cancel import")

	rec := e.postPath("/settings/import/resume")
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings/import" {
		t.Fatalf("resume: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	jobsN := 0
	for _, j := range e.jobs.got {
		if j.Kind == importer.KindRun {
			jobsN++
		}
	}
	if jobsN != 2 {
		t.Errorf("import jobs = %d, want 2", jobsN)
	}
	body = e.get("/settings/import", false).Body.String()
	mustContain(t, body, `hx-trigger="every 3s"`, "Cancel import")
	mustNotContain(t, body, "Resume import")
}

func TestImportResumeRefusals(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	if rec := e.postPath("/settings/import/resume"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("nothing to resume: %d", rec.Code)
	}
	e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")))
	rec := e.postPath("/settings/import/resume")
	if rec.Code != http.StatusConflict {
		t.Errorf("active: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `href="/settings/import"`)
}

func TestImportPageShowsRetryNote(t *testing.T) {
	e := newImpEnv(t, notFoundExcept())
	e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")))
	if _, err := e.d.Exec(`UPDATE imports SET state='running', note='Waiting for AniList (retry 2/5)'`); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/settings/import", true).Body.String(), "Waiting for AniList (retry 2/5)", `hx-trigger="every 3s"`)
}

// newTwoImporterEnv has the anime plugin and the book fixture, each with an importer.
func newTwoImporterEnv(t *testing.T) (*impEnv, *bookImport) {
	t.Helper()
	b := newBookImport()
	b.imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookRow("1", "One")}}
	e := newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		r.Register(anime.New(anime.Options{Provider: notFoundExcept()}))
		r.Register(b.p)
		return r
	})
	return e, b
}

func TestImportPageOffersImportersOfEnabledPlugins(t *testing.T) {
	e, _ := newTwoImporterEnv(t)
	page := e.get("/settings/import", false).Body.String()
	mustContain(t, page, `<select name="importer"`, `<option value="anime/yamtrack">Yamtrack CSV (Anime)</option>`,
		`<option value="book/books-csv">Books CSV (Books)</option>`, "CSV export", "Rows are matched on Fake Books")
	mustContain(t, page, "<h1 class=\"mb-6 text-2xl font-semibold\">Import</h1>")
	mustContain(t, settingsTabs(t, e.get("/settings", false).Body.String()), `href="/settings/import"`, "<span>Import</span>")

	// with several importers the field is required
	rec := e.uploadTo(t, nil, "file", "x.csv", []byte("x"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no importer: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "choose what to import from")
	for _, v := range []string{"book/nope", "nope/books-csv", "book", "../book/books-csv", "anime/books-csv"} {
		rec := e.uploadTo(t, url.Values{"importer": {v}}, "file", "x.csv", []byte("x"))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d", v, rec.Code)
		}
		mustContain(t, rec.Body.String(), "This importer is not available.")
		// (the sidebar's icon names, such as menu_book, are not the echo)
		main := rec.Body.String()[strings.Index(rec.Body.String(), "</header>"):]
		mustNotContain(t, main, v+"<")
	}
	if r, _ := e.im.Latest(context.Background()); r != nil {
		t.Fatalf("stored: %+v", r)
	}
	// the chosen importer runs, and stays selected on a later error
	rec = e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "b.csv", []byte("x"))
	if rec.Code != 303 {
		t.Fatalf("book upload: %d %s", rec.Code, rec.Body.String())
	}
	if r, _ := e.im.Latest(context.Background()); r == nil || r.PluginID != "book" || r.ImporterID != "books-csv" {
		t.Fatalf("report: %+v", r)
	}
	rec = e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "b.csv", []byte("x"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `<option value="book/books-csv" selected>`)
}

func TestImportOfDisabledPluginIsNotOfferedAndRefused(t *testing.T) {
	e, _ := newTwoImporterEnv(t)
	if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
		t.Fatal(err)
	}
	page := e.get("/settings/import", false).Body.String()
	mustNotContain(t, page, "book/books-csv", "Books CSV")
	mustContain(t, page, `<select name="importer"`, `<option value="anime/yamtrack">`)
	rec := e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "b.csv", []byte("x"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled: %d", rec.Code)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.jobs.got) != 0 {
		t.Fatalf("stored: %+v %+v", r, e.jobs.got)
	}
	// with every importer disabled the page says so and Settings has no link
	if err := e.reg.SetEnabled(context.Background(), "anime", false); err != nil {
		t.Fatal(err)
	}
	page = e.get("/settings/import", false).Body.String()
	mustContain(t, page, "data-no-importers")
	mustNotContain(t, page, `enctype="multipart/form-data"`)
	mustNotContain(t, e.get("/settings", false).Body.String(), `href="/settings/import"`)
	if rec := e.upload(t, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning"))); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no importer at all: %d", rec.Code)
	}
}

// An import whose plugin is disabled waits in the job queue; the page says
// why while it is active, and stops saying so once the plugin is enabled.
func TestImportPageExplainsImportPausedByDisabledPlugin(t *testing.T) {
	e, _ := newTwoImporterEnv(t)
	if rec := e.uploadTo(t, url.Values{"importer": {"book/books-csv"}}, "file", "b.csv", []byte("x")); rec.Code != 303 {
		t.Fatalf("upload: %d", rec.Code)
	}
	const paused = "This import is paused because the Books plugin is disabled. Enable it on the Plugins page of Settings to continue, or cancel the import."
	mustNotContain(t, e.get("/settings/import", false).Body.String(), paused)
	if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/settings/import", true).Body.String(), `data-state="pending"`, paused, "Cancel import")
	rec := e.uploadTo(t, url.Values{"importer": {"anime/yamtrack"}}, "file", "a.csv", []byte(impHeader+impRow(1, "One", "Planning")))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second import: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), paused)
	if err := e.reg.SetEnabled(context.Background(), "book", true); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, e.get("/settings/import", false).Body.String(), "is paused because")
	// a finished import of a disabled plugin is not "paused"
	e.runJob(t)
	e.reg.SetEnabled(context.Background(), "book", false)
	b := e.get("/settings/import", false).Body.String()
	mustContain(t, b, `data-state="done"`)
	mustNotContain(t, b, "is paused because")
}

// The unmatched link of a plugin with several kinds names the kind.
func TestImportUnmatchedLinkOfMultiKindPlugin(t *testing.T) {
	b := newBookImport()
	k := b.p.M.Kinds[0]
	k.Key, k.Label, k.LabelPlural = "comic", "Comic", "Comics"
	b.p.M.Kinds = append(b.p.M.Kinds, k)
	b.src.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	row := bookRow("1", "Lost Comic")
	row.Kind = "comic"
	b.imp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{row}}
	e := newImpEnvWith(t, func(es plugin.EnabledStore) *plugin.Registry {
		r := plugin.NewRegistry(es)
		if err := r.Register(b.p); err != nil {
			t.Fatal(err)
		}
		return r
	})
	e.upload(t, "file", "c.csv", []byte("x"))
	e.runJob(t)
	it, err := e.lib.ItemByExternalID(context.Background(), "book", "comic", "isbn", "1")
	if err != nil || it == nil {
		t.Fatalf("%v %v", it, err)
	}
	mustContain(t, e.get("/settings/import", false).Body.String(),
		`href="/book/manual/`+url.PathEscape(it.SourceID)+`?kind=comic"`, "Not found on Fake Books")
}
