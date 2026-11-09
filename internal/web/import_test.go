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
	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
)

type fnResolver func(malID string) (provider.Anime, error)

func (f fnResolver) AnimeByMALID(_ context.Context, malID string) (provider.Anime, error) {
	return f(malID)
}

// dbJobs records jobs in the fake and also writes them to the jobs table: the
// importer reconciles imports against that table.
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
	im   *yamtrack.Importer
	st   *store.Store
	res  fnResolver
}

func newImpEnv(t *testing.T, res fnResolver) *impEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &impEnv{d: d, jobs: &fakeJobs{}, st: store.New(d)}
	// A fake clock whose sleep returns at once keeps the pacing out of the tests.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.im = yamtrack.New(d, e.st, res, dbJobs{e.jobs, jobs.NewQueue(d)}, yamtrack.WithClock(
		func() time.Time { now = now.Add(time.Hour); return now },
		func(ctx context.Context, _ time.Duration) error { return ctx.Err() }))
	e.h = NewServer(Deps{Settings: settings.New(d), Store: e.st, Jobs: e.jobs, Imports: e.im})
	return e
}

const impHeader = "media_id,source,media_type,title,image,score,status,notes,start_date,end_date,progress\n"

func impRow(id int, title, status string) string {
	return fmt.Sprintf("%d,mal,anime,%q,,,%s,,,,0\n", id, title, status)
}

func (e *impEnv) upload(t testing.TB, field, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, nil, &upload{field: field, filename: name, ctype: "text/csv", data: data})
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
		if j.Kind == yamtrack.KindImport {
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
	return func(mal string) (provider.Anime, error) {
		for _, o := range ok {
			if o == mal {
				return provider.Anime{SearchResult: provider.SearchResult{Source: provider.AniList, ID: "9" + mal,
					Titles: []provider.Title{{Lang: "en", Text: "T" + mal}}}, MALID: mal}, nil
			}
		}
		return provider.Anime{}, provider.ErrNotFound
	}
}

func TestImportPageShowsProgress(t *testing.T) {
	e := newImpEnv(t, func(mal string) (provider.Anime, error) {
		switch mal {
		case "1":
			return notFoundExcept("1")(mal)
		case "3":
			return provider.Anime{}, errors.New("graphql exploded")
		}
		return provider.Anime{}, provider.ErrNotFound
	})
	data := impHeader + impRow(1, "Found One", "Completed") + impRow(2, `Lost <b>Show</b>`, "Planning") +
		impRow(3, "Broken One", "Planning") + impRow(4, "Weird", "Nope") + "5,igdb,game,G,,,Completed,,,,0\n"
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
		"Broken One", "graphql exploded", "unknown status", "Lost &lt;b&gt;Show&lt;/b&gt;")
	mustNotContain(t, body, "<b>Show</b>", "hx-trigger", "Cancel import", "and 0 more")
	s, err := e.st.FindByMALID(context.Background(), "2")
	if err != nil || s == nil {
		t.Fatal(err)
	}
	mustContain(t, body, `href="/anime/manual/`+url.PathEscape(s.Anime.ID)+`"`)
	// counts: total 4 (game ignored), imported 1, unmatched 1, failed 1, skipped 1
	for _, want := range []string{"Total", "Imported", "Unmatched", "Failed", "Skipped"} {
		mustContain(t, body, want)
	}
	mustContain(t, body, `data-count="total">4<`, `data-count="imported">1<`, `data-count="unmatched">1<`,
		`data-count="failed">1<`, `data-count="skipped">1<`)
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
	mustContain(t, e.get("/settings/import", false).Body.String(),
		`action="/settings/import"`, `enctype="multipart/form-data"`, `name="file"`, `type="file"`)
	mustContain(t, e.get("/settings", false).Body.String(), `href="/settings/import"`, "Import from Yamtrack")
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
	if s, _ := e.st.FindByMALID(context.Background(), "1"); s != nil {
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

func TestImportListsAreCappedAt200(t *testing.T) {
	e := newImpEnv(t, func(string) (provider.Anime, error) { return provider.Anime{}, errors.New("nope") })
	var b strings.Builder
	b.WriteString(impHeader)
	for i := 1; i <= 230; i++ {
		b.WriteString(impRow(i, fmt.Sprintf("Show %d", i), "Planning"))
	}
	e.upload(t, "file", "a.csv", []byte(b.String()))
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
	mustNotContain(t, rec.Body.String(), "Import from Yamtrack")
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
		if j.Kind == yamtrack.KindImport {
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
