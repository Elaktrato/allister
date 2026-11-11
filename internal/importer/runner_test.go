package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// genEnv runs the generic runner over the book and tv fixture plugins. The
// book source answers 5 requests per minute and looks up ISBNs; the tv
// importer declares no fallback title language.
type genEnv struct {
	t        *testing.T
	lib      *library.Library
	reg      *plugin.Registry
	enabled  *plugintest.MemEnabled
	q        *fakeQueue
	clk      *clock
	im       *Runner
	bookSrc  *plugintest.LookupSource
	bookSrc2 *plugintest.LookupSource
	bookImp  *plugintest.FakeImporter
	tvSrc    *plugintest.LookupSource
	tvImp    *plugintest.FakeImporter
	pctImp   *plugintest.FakeImporter
	pctSrc   *plugintest.LookupSource
	dbh      *sql.DB
	// bookPlugin is the registered book fixture (tests change its sources).
	bookPlugin *plugintest.Plugin
}

func pf(f float64) *float64 { return &f }
func ip(i int) *int         { return &i }
func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// book is valid book data for isbn with the given page total (0: none).
func book(isbn string, pages float64) plugin.ItemData {
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: "books", ID: "w" + isbn, Kind: "book",
			Titles:   []plugin.Title{{Lang: "en", Text: "Book " + isbn}},
			Fields:   map[string]plugin.FieldValue{"author": {Text: "A. Author"}},
			CoverURL: "https://books.example/c/" + isbn + ".jpg"},
		ExternalURL: "https://books.example/work/w" + isbn,
	}
	if pages > 0 {
		d.Fields["pages"] = plugin.FieldValue{Num: pf(pages)}
	}
	return d
}

func bookEntry(isbn, title string, status plugin.StatusClass, pages float64) plugin.ParsedEntry {
	return plugin.ParsedEntry{Kind: "book", Title: title, ExternalIDs: map[string]string{"isbn": isbn},
		Status: status, Progress: map[string]float64{"pages": pages}, CoverURL: "https://covers.example/" + isbn + ".jpg"}
}

func newGenEnv(t *testing.T) *genEnv { return newGenEnvWith(t, false, false) }

func newGenEnvWithFallback(t *testing.T) *genEnv { return newGenEnvWith(t, true, false) }

func newGenEnvManual(t *testing.T) *genEnv { return newGenEnvWith(t, false, true) }

func newGenEnvWith(t *testing.T, fallback, manual bool) *genEnv {
	t.Helper()
	d := openDB(t)
	e := &genEnv{t: t, q: &fakeQueue{inner: jobs.NewQueue(d)}, clk: &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		enabled: plugintest.NewMemEnabled()}
	bk := plugintest.BookPlugin()
	bk.M.Sources[0].RequestsPerMinute = 5
	e.bookSrc = &plugintest.LookupSource{Fn: func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		return book(v, 300), nil
	}}
	e.bookImp = &plugintest.FakeImporter{}
	bk.Sources = map[string]plugin.Source{"books": e.bookSrc}
	if fallback {
		e.bookSrc2 = &plugintest.LookupSource{Fn: func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
			d := book(v, 300)
			d.Source, d.ID, d.ExternalURL = "books2", "w2"+v, "https://books2.example/work/w2"+v
			return d, nil
		}}
		bk.Sources["books2"] = e.bookSrc2
		bk.M.Sources = append(bk.M.Sources, plugin.SourceInfo{ID: "books2", Name: "Second Books", RequestsPerMinute: 5,
			ExternalIDTypes: []string{"isbn"}, SupportsLookup: true})
		bk.M.Importers[0].ResolveViaFallback = []string{"books2"}
	}
	if manual {
		bk.M.Importers[0].ManualRows = true
	}
	bk.Importers = map[string]plugin.Importer{"books-csv": e.bookImp}

	tv := plugintest.TVPlugin()
	tv.M.Importers[0].FallbackTitleLang = ""
	e.tvSrc = &plugintest.LookupSource{Fn: func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}}
	e.tvImp = &plugintest.FakeImporter{}
	tv.Sources = map[string]plugin.Source{"tvdata": e.tvSrc}
	tv.Importers = map[string]plugin.Importer{"tv-json": e.tvImp}

	// ebook: the book fixture tracking percent by default
	eb := plugintest.BookPlugin(plugintest.WithID("ebook"))
	eb.M.Kinds[0].Progress.Defaults = []string{"percent"}
	e.pctSrc = &plugintest.LookupSource{Fn: func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		return book(v, 300), nil
	}}
	e.pctImp = &plugintest.FakeImporter{}
	eb.Sources = map[string]plugin.Source{"books": e.pctSrc}
	eb.Importers = map[string]plugin.Importer{"books-csv": e.pctImp}

	e.reg = plugin.NewRegistry(e.enabled)
	for _, p := range []plugin.Plugin{bk, tv, eb} {
		if err := e.reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	e.lib = library.New(d, e.reg)
	e.im = New(Deps{DB: d, Library: e.lib, Registry: e.reg, Queue: e.q,
		SyncJobs: map[string]func(int64) (string, any){
			"book": jobs.SyncJobFor("book"), "tv": jobs.SyncJobFor("tv"), "ebook": jobs.SyncJobFor("ebook")},
		Now: e.clk.now, Sleep: e.clk.sleep})
	e.dbh = d
	e.bookPlugin = bk
	return e
}

func (e *genEnv) submit(pluginID, importerID string) int64 {
	e.t.Helper()
	id, err := e.im.Submit(context.Background(), pluginID, importerID, "file.csv", strings.NewReader("ignored"))
	if err != nil {
		e.t.Fatalf("submit: %v", err)
	}
	return id
}

func (e *genEnv) run(pluginID string, id int64) error {
	payload, _ := json.Marshal(Payload{ImportID: id, PluginID: pluginID})
	return e.im.Handler()(context.Background(), payload)
}

func (e *genEnv) report() *Report {
	e.t.Helper()
	r, err := e.im.Latest(context.Background())
	if err != nil || r == nil {
		e.t.Fatalf("latest: %v %v", r, err)
	}
	return r
}

func (e *genEnv) item(pluginID, kind, idType, value string) (*library.Item, *library.Entry) {
	e.t.Helper()
	it, err := e.lib.ItemByExternalID(context.Background(), pluginID, kind, idType, value)
	if err != nil || it == nil {
		e.t.Fatalf("%s %s not saved: %v", idType, value, err)
	}
	en, err := e.lib.Entry(context.Background(), it.ID)
	if err != nil || en == nil {
		e.t.Fatalf("no entry for %s: %v", value, err)
	}
	return it, en
}

func (e *genEnv) message(id int64, seq int) string {
	e.t.Helper()
	var m string
	if err := e.dbh.QueryRow(`SELECT message FROM import_rows WHERE import_id=? AND seq=?`, id, seq).Scan(&m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

// TestRunnerWorksForBookFixture: a fixture importer with another external id
// type (isbn) whose source allows 5 requests per minute.
func TestRunnerWorksForBookFixture(t *testing.T) {
	e := newGenEnv(t)
	pre, err := e.lib.AddToList(context.Background(), "book", "book", func() plugin.ItemData {
		d := book("9", 0)
		d.ExternalIDs = map[string]string{"isbn": "9"}
		return d
	}(), library.Entry{Status: library.Completed}, library.WithSyncState("done"))
	if err != nil {
		t.Fatal(err)
	}
	e.bookSrc.Fn = func(_ context.Context, kind, idType, v string, _ int) (plugin.ItemData, error) {
		if kind != "book" || idType != "isbn" {
			t.Errorf("lookup %s %s", kind, idType)
		}
		if v == "3" {
			return plugin.ItemData{}, fmt.Errorf("no such isbn: %w", plugin.ErrNotFound)
		}
		return book(v, 300), nil
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("9", "Saved", plugin.Planned, 0),
		bookEntry("1", "One", plugin.Completed, 300),
		bookEntry("2", "Two", plugin.Active, 42),
		bookEntry("3", "Three", plugin.Paused, 7),
	}}
	id := e.submit("book", "books-csv")
	if j := e.q.ofKind(KindRun); len(j) != 1 || j[0].Payload.(Payload) != (Payload{ImportID: id, PluginID: "book"}) {
		t.Fatalf("import job: %+v", e.q.jobs)
	}
	// four pending rows that may need a request, at 12 s each
	if r := e.report(); r.ETA != 4*12*time.Second || r.Total != 4 || r.Remaining != 4 {
		t.Fatalf("before run: %+v", r)
	}
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	// 3 requests (the saved row needs none): 2 gaps of a minute / 5
	if got := e.bookSrc.Looked(); strings.Join(got, ",") != "1,2,3" {
		t.Fatalf("looked up %v", got)
	}
	if e.clk.slept() != 2*12*time.Second || len(e.clk.sleeps) != 2 {
		t.Fatalf("sleeps = %v", e.clk.sleeps)
	}
	it1, en1 := e.item("book", "book", "isbn", "1")
	if it1.Source != "books" || it1.SourceID != "w1" || en1.Status != library.Completed || en1.Progress["pages"] != 300 {
		t.Errorf("1: %+v %+v", it1, en1)
	}
	if _, en2 := e.item("book", "book", "isbn", "2"); en2.Status != library.Active || en2.Progress["pages"] != 42 {
		t.Errorf("2: %+v", en2)
	}
	it3, en3 := e.item("book", "book", "isbn", "3")
	if it3.Source != "manual" || it3.SourceID == "" || len(it3.Titles) != 1 || it3.Titles[0] != (plugin.Title{Lang: "en", Text: "Three"}) ||
		it3.CoverPath != "https://covers.example/3.jpg" || it3.SyncState != "pending" || en3.Status != library.Paused || en3.Progress["pages"] != 7 {
		t.Errorf("3: %+v %+v", it3, en3)
	}
	if en, _ := e.lib.Entry(context.Background(), pre); en.Status != library.Completed {
		t.Errorf("saved entry changed: %+v", en)
	}
	r := e.report()
	if r.State != "done" || r.Imported != 2 || r.Unmatched != 1 || r.Skipped != 1 || r.PluginID != "book" || r.ImporterID != "books-csv" {
		t.Fatalf("report: %+v", r)
	}
	if u := r.UnmatchedRows[0]; u.Title != "Three" || u.ItemID != it3.ID || u.SourceID != it3.SourceID || u.Kind != "book" ||
		!strings.Contains(u.Message, "not found on Fake Books") {
		t.Errorf("unmatched: %+v", u)
	}
	syncs := e.q.ofKind(jobs.KindSyncItem)
	if len(syncs) != 3 {
		t.Fatalf("sync jobs: %+v", syncs)
	}
	for _, s := range syncs {
		if s.Payload.(jobs.SyncPayload).PluginID != "book" {
			t.Errorf("sync payload %+v", s.Payload)
		}
	}
	// a second run of the same file looks nothing up and creates nothing
	e.bookSrc.ResetLooked()
	id2 := e.submit("book", "books-csv")
	if err := e.run("book", id2); err != nil {
		t.Fatal(err)
	}
	if len(e.bookSrc.Looked()) != 0 {
		t.Errorf("second import looked up %v", e.bookSrc.Looked())
	}
	if r := e.report(); r.Skipped != 4 {
		t.Errorf("second report: %+v", r)
	}
}

func TestRunnerAdjustsProgressRatingAndDates(t *testing.T) {
	e := newGenEnv(t)
	over := bookEntry("1", "Over", plugin.Completed, 500)
	over.Progress["percent"] = 50 // not an active key of the book kind
	over.Rating = ip(11)
	over.StartedOn, over.FinishedOn = day(2024, 3, 2), day(2024, 3, 1)
	frac := bookEntry("2", "Fraction", plugin.Active, 12.7)
	frac.Rating = ip(0)
	bad := bookEntry("3", "Negative", plugin.Active, -4)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{over, frac, bad}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	_, en := e.item("book", "book", "isbn", "1")
	if en.Progress["pages"] != 300 || len(en.Progress) != 1 || en.Rating != nil || en.StartedOn != nil || en.FinishedOn != nil {
		t.Errorf("over: %+v", en)
	}
	m := e.message(id, 1)
	for _, w := range []string{"progress clamped to 300", `progress "percent"`, "rating", "dates dropped (finish date was before start date)"} {
		if !strings.Contains(m, w) {
			t.Errorf("message %q lacks %q", m, w)
		}
	}
	if _, en := e.item("book", "book", "isbn", "2"); en.Progress["pages"] != 12 || en.Rating == nil || *en.Rating != 0 {
		t.Errorf("fraction: %+v", en)
	}
	if m := e.message(id, 2); !strings.Contains(m, "rounded down to 12") {
		t.Errorf("message 2 = %q", m)
	}
	if _, en := e.item("book", "book", "isbn", "3"); en.Progress["pages"] != 0 {
		t.Errorf("negative: %+v", en)
	}
	if m := e.message(id, 3); !strings.Contains(m, "invalid progress") {
		t.Errorf("message 3 = %q", m)
	}
	if r := e.report(); r.Imported != 3 || r.Failed != 0 {
		t.Errorf("report: %+v", r)
	}
}

func TestRunnerClampsPercentTo100(t *testing.T) {
	e := newGenEnv(t)
	en := plugin.ParsedEntry{Kind: "book", Title: "P", ExternalIDs: map[string]string{"isbn": "1"}, Status: plugin.Active,
		Progress: map[string]float64{"percent": 140, "pages": 10}}
	e.pctImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{en}}
	id := e.submit("ebook", "books-csv")
	if err := e.run("ebook", id); err != nil {
		t.Fatal(err)
	}
	_, got := e.item("ebook", "book", "isbn", "1")
	if got.Progress["percent"] != 100 || len(got.Progress) != 1 {
		t.Errorf("entry: %+v", got)
	}
	if m := e.message(id, 1); !strings.Contains(m, "progress clamped to 100") || !strings.Contains(m, `progress "pages"`) {
		t.Errorf("message = %q", m)
	}
}

func TestRunnerSkipsRowsOfUnknownKindOrWithoutExternalID(t *testing.T) {
	e := newGenEnv(t)
	noID := bookEntry("", "No Id", plugin.Planned, 0)
	noID.ExternalIDs = nil
	e.bookImp.Result = plugin.ParseResult{
		Entries: []plugin.ParsedEntry{bookEntry("1", "Good", plugin.Planned, 0), {Kind: "show", Title: "Wrong Kind", ExternalIDs: map[string]string{"isbn": "2"}, Status: plugin.Planned}, noID},
		Skipped: []plugin.SkippedRow{{Line: 7, Title: "Bad Row", Reason: "unknown status"}},
		Ignored: 4,
	}
	id := e.submit("book", "books-csv")
	r := e.report()
	// ADAPTED (Task 13): the row without an isbn is skipped at submit time,
	// not left pending until the job runs (was Skipped 2, Remaining 2).
	if r.Total != 4 || r.Ignored != 4 || r.Skipped != 3 || r.Remaining != 1 {
		t.Fatalf("after submit: %+v", r)
	}
	if !strings.Contains(e.message(id, 3), "no isbn") {
		t.Errorf("row 3 message at submit = %q", e.message(id, 3))
	}
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	r = e.report()
	if r.Imported != 1 || r.Skipped != 3 || r.Failed != 0 {
		t.Fatalf("report: %+v", r)
	}
	msgs := map[string]string{}
	for _, s := range r.SkippedRows {
		msgs[s.Title] = s.Message
	}
	if !strings.Contains(msgs["Wrong Kind"], `unknown kind "show"`) || !strings.Contains(msgs["No Id"], "no isbn") || msgs["Bad Row"] != "unknown status" {
		t.Errorf("skipped: %+v", r.SkippedRows)
	}
	if got := e.bookSrc.Looked(); len(got) != 1 {
		t.Errorf("looked up %v", got)
	}
}

func TestSubmitWithOnlyUnusableRowsIsErrNoRows(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{
		Entries: []plugin.ParsedEntry{{Kind: "show", Title: "Wrong Kind", ExternalIDs: map[string]string{"isbn": "2"}}},
		Skipped: []plugin.SkippedRow{{Line: 2, Reason: "bad"}},
	}
	_, err := e.im.Submit(context.Background(), "book", "books-csv", "f.csv", strings.NewReader(""))
	if !errors.Is(err, ErrNoRows) || !strings.Contains(err.Error(), "no importable books rows") {
		t.Fatalf("err = %v", err)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.q.jobs) != 0 {
		t.Fatalf("stored %+v %+v", r, e.q.jobs)
	}
}

func TestRunnerLibraryRejectionFailsRowAndContinues(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		d := book(v, 300)
		if v == "1" {
			d.Fields["pages"] = plugin.FieldValue{Num: pf(-5)} // below the field's minimum
		}
		return d, nil
	}
	bad := bookEntry("2", "Bad Status", "sometimes", 1)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("1", "Rejected", plugin.Planned, 0), bad, bookEntry("3", "Fine", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.State != "done" || r.Failed != 2 || r.Imported != 1 {
		t.Fatalf("report: %+v", r)
	}
	if !strings.Contains(r.FailedRows[0].Message, "invalid") || !strings.Contains(r.FailedRows[1].Message, "status") {
		t.Errorf("failed rows: %+v", r.FailedRows)
	}
}

// TestImportContinuesAfterOneRowFails is the legacy test on the book
// fixture: a lookup that returns a plain error fails that row only (see
// TestImportBadResponseFailsOnlyThatRow for the anime source).
func TestImportContinuesAfterOneRowFails(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		if v == "2" {
			return plugin.ItemData{}, errors.New("graphql error \"boom\"")
		}
		return book(v, 300), nil
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("1", "One", plugin.Planned, 0), bookEntry("2", "Two", plugin.Planned, 0), bookEntry("3", "Three", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.State != "done" || r.Imported != 2 || r.Failed != 1 || len(r.FailedRows) != 1 ||
		r.FailedRows[0].Title != "Two" || !strings.Contains(r.FailedRows[0].Message, "boom") {
		t.Fatalf("report: %+v", r)
	}
}

// TestReportCapsListsAt200 is the legacy test on the book fixture (a
// generic source makes 250 failing rows easy to produce).
func TestReportCapsListsAt200(t *testing.T) {
	e := newGenEnv(t)
	var rows []plugin.ParsedEntry
	for i := 1; i <= 250; i++ {
		rows = append(rows, bookEntry(fmt.Sprint(i), fmt.Sprintf("Show %d", i), plugin.Planned, 0))
	}
	e.bookImp.Result = plugin.ParseResult{Entries: rows}
	e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, errors.New("nope")
	}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.Failed != 250 || len(r.FailedRows) != 200 || r.MoreFailed != 50 {
		t.Fatalf("failed=%d listed=%d more=%d", r.Failed, len(r.FailedRows), r.MoreFailed)
	}
	if r.FailedRows[0].Title != "Show 1" {
		t.Errorf("order: %+v", r.FailedRows[0])
	}
}

func TestRunnerFallbackTitleLanguageAndEmptyTitle(t *testing.T) {
	e := newGenEnv(t)
	e.tvImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		{Kind: "show", Title: "", ExternalIDs: map[string]string{"tvdata": "7"}, Status: plugin.Planned},
	}}
	id := e.submit("tv", "tv-json")
	if err := e.run("tv", id); err != nil {
		t.Fatal(err)
	}
	it, _ := e.item("tv", "show", "tvdata", "7")
	// no fallback language declared: the kind's first title language
	if len(it.Titles) != 1 || it.Titles[0] != (plugin.Title{Lang: "en", Text: "tvdata #7"}) || it.Source != "manual" {
		t.Errorf("item: %+v", it)
	}
	if m := e.message(id, 1); !strings.Contains(m, "no title") || !strings.Contains(m, "not found on Fake TV") {
		t.Errorf("message = %q", m)
	}
	if r := e.report(); r.Unmatched != 1 || r.UnmatchedRows[0].Title != "tvdata #7" {
		t.Errorf("report: %+v", r)
	}
}

func TestSubmitRefusesUnknownOrDisabledImporter(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
	if err := e.reg.SetEnabled(context.Background(), "tv", false); err != nil {
		t.Fatal(err)
	}
	e.tvImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{{Kind: "show", Title: "S", ExternalIDs: map[string]string{"tvdata": "1"}}}}
	for _, c := range [][2]string{{"nope", "books-csv"}, {"book", "nope"}, {"book", "tv-json"}, {"tv", "tv-json"}, {"", ""}} {
		_, err := e.im.Submit(context.Background(), c[0], c[1], "f.csv", strings.NewReader(""))
		if !errors.Is(err, ErrUnknownImporter) {
			t.Errorf("%v: err = %v", c, err)
		}
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.q.jobs) != 0 {
		t.Fatalf("stored %+v %+v", r, e.q.jobs)
	}
}

func TestSubmitBadInputIsErrBadFile(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Err = fmt.Errorf("%w: missing column isbn", plugin.ErrBadInput)
	_, err := e.im.Submit(context.Background(), "book", "books-csv", "f.csv", strings.NewReader(""))
	var bad *BadFileError
	if !errors.Is(err, ErrBadFile) || !errors.Is(err, plugin.ErrBadInput) || !errors.As(err, &bad) || bad.Reason != "missing column isbn" {
		t.Fatalf("err = %v", err)
	}
	// any other importer error is not a bad file
	e.bookImp.Err = errors.New("disk on fire")
	if _, err := e.im.Submit(context.Background(), "book", "books-csv", "f.csv", strings.NewReader("")); err == nil || errors.Is(err, ErrBadFile) {
		t.Fatalf("err = %v", err)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil {
		t.Fatalf("stored %+v", r)
	}
}

func TestSubmitKeepsOnlyTheFileName(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
	if _, err := e.im.Submit(context.Background(), "book", "books-csv", `C:\Users\me\..\export `+strings.Repeat("x", 300)+".csv", strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); strings.ContainsAny(r.Filename, `\/`) || !strings.HasPrefix(r.Filename, "export ") || len([]rune(r.Filename)) != 200 {
		t.Errorf("filename = %q", r.Filename)
	}
}

// A legacy import (importer_id ”, anime-shaped rows) cannot be run or
// resumed by the generic runner: its job fails it with a clear note.
func TestLegacyImportIsFailedAndNotResumable(t *testing.T) {
	e := newGenEnv(t)
	e.dbh.Exec(`INSERT INTO imports(id, filename, state, total, plugin_id, importer_id) VALUES(1,'old.csv','pending',1,'book','')`)
	e.dbh.Exec(`INSERT INTO import_rows(import_id, seq, external_id, title, outcome, data) VALUES(1,1,'5','Old','pending','{"MALID":"5"}')`)
	if err := e.run("book", 1); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.State != "failed" || !strings.Contains(r.Note, "created by an older version") || r.CanResume {
		t.Fatalf("report: %+v", r)
	}
	if _, err := e.im.Resume(context.Background()); !errors.Is(err, ErrNothingToResume) {
		t.Fatalf("resume: %v", err)
	}
	if len(e.bookSrc.Looked()) != 0 {
		t.Errorf("looked up %v", e.bookSrc.Looked())
	}
}

// An import whose importer disappeared (plugin uninstalled) fails instead of
// retrying forever.
func TestImportOfMissingImporterFails(t *testing.T) {
	e := newGenEnv(t)
	e.dbh.Exec(`INSERT INTO imports(id, filename, state, total, plugin_id, importer_id) VALUES(1,'x.csv','pending',1,'gone','csv')`)
	e.dbh.Exec(`INSERT INTO import_rows(import_id, seq, external_id, title, outcome, data) VALUES(1,1,'5','Old','pending','{}')`)
	if err := e.run("gone", 1); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.State != "failed" || !strings.Contains(r.Note, "no longer installed") {
		t.Fatalf("report: %+v", r)
	}
}

func TestSleepCtxReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerIgnoresMissingImportAndRejectsBadPayload(t *testing.T) {
	e := newGenEnv(t)
	if err := e.run("book", 999); err != nil {
		t.Errorf("missing import: %v", err)
	}
	if err := e.im.Handler()(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Error("bad payload accepted")
	}
}

func TestLatestWithoutImportIsNil(t *testing.T) {
	e := newGenEnv(t)
	if r, err := e.im.Latest(context.Background()); r != nil || err != nil {
		t.Fatalf("%v %v", r, err)
	}
}

func TestReportOfImport(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	r, err := e.im.Report(context.Background(), id)
	if err != nil || r == nil || r.ID != id || r.Total != 1 || r.Percent() != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := e.im.Report(context.Background(), id+1); r != nil || err != nil {
		t.Fatalf("missing: %+v %v", r, err)
	}
}

// A file whose entries all lack the importer's external id holds nothing
// importable: Submit refuses it and stores nothing.
func TestSubmitWithOnlyRowsWithoutExternalIDIsErrNoRows(t *testing.T) {
	e := newGenEnv(t)
	a, b := bookEntry("", "No Id", plugin.Planned, 0), bookEntry("", "Blank Id", plugin.Planned, 0)
	a.ExternalIDs = nil
	b.ExternalIDs = map[string]string{"isbn": " \n "}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{a, b}}
	_, err := e.im.Submit(context.Background(), "book", "books-csv", "f.csv", strings.NewReader(""))
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("err = %v, want ErrNoRows", err)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.q.jobs) != 0 {
		t.Fatalf("stored %+v %+v", r, e.q.jobs)
	}
}

// An importer that names a ResolveVia source the plugin does not provide is
// refused at Submit (its rows would otherwise all become manual entries);
// only an empty ResolveVia means manual-only.
func TestSubmitRefusesImporterWhoseSourceIsMissing(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
	e.bookPlugin.Sources = map[string]plugin.Source{} // Source("books") is nil
	_, err := e.im.Submit(context.Background(), "book", "books-csv", "f.csv", strings.NewReader(""))
	if !errors.Is(err, ErrSourceMissing) || !errors.Is(err, ErrUnknownImporter) {
		t.Fatalf("err = %v, want ErrSourceMissing (an ErrUnknownImporter)", err)
	}
	if r, _ := e.im.Latest(context.Background()); r != nil || len(e.q.jobs) != 0 {
		t.Fatalf("stored %+v %+v", r, e.q.jobs)
	}
}

// When the source disappears after Submit (a plugin update), the job fails
// the import with a note instead of saving every row as a manual entry.
func TestRunFailsWhenTheSourceIsGone(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	e.bookPlugin.Sources = map[string]plugin.Source{}
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.State != "failed" || r.Imported != 0 || !strings.Contains(r.Note, "source") {
		t.Fatalf("report: %+v", r)
	}
	if it, _ := e.lib.ItemByExternalID(context.Background(), "book", "book", "isbn", "1"); it != nil {
		t.Fatalf("saved %+v", it)
	}
}

// An importer without ResolveVia is manual-only: its rows become manual
// entries that keep their external id.
func TestManualOnlyImporterSavesManualEntries(t *testing.T) {
	e := newGenEnv(t)
	mb := plugintest.BookPlugin(plugintest.WithID("mbook"))
	mb.M.Importers[0].ResolveVia = ""
	src := &plugintest.LookupSource{Fn: func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		return book(v, 300), nil
	}}
	mb.Sources = map[string]plugin.Source{"books": src}
	mb.Importers = map[string]plugin.Importer{"books-csv": &plugintest.FakeImporter{
		Result: plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Planned, 0)}}}}
	if err := e.reg.Register(mb); err != nil {
		t.Fatal(err)
	}
	id := e.submit("mbook", "books-csv")
	if err := e.run("mbook", id); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.State != "done" || r.Unmatched != 1 {
		t.Fatalf("report: %+v", r)
	}
	it, _ := e.item("mbook", "book", "isbn", "1")
	if it.Source != "manual" {
		t.Errorf("item = %+v", it)
	}
	if got := src.Looked(); len(got) != 0 {
		t.Errorf("looked up %v", got)
	}
}

// plugin.ErrBadResponse fails the row at once even when the source also
// wraps a retryable sentinel: retrying a bad answer does not help.
func TestRunnerBadResponseFailsRowWithoutRetry(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		if v == "2" {
			return plugin.ItemData{}, fmt.Errorf("books: %w (%w)", plugin.ErrBadResponse, plugin.ErrUnavailable)
		}
		return book(v, 300), nil
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("1", "One", plugin.Planned, 0), bookEntry("2", "Two", plugin.Planned, 0), bookEntry("3", "Three", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatalf("the job must complete: %v", err)
	}
	r := e.report()
	if r.State != "done" || r.Imported != 2 || r.Failed != 1 || r.FailedRows[0].Title != "Two" ||
		!strings.Contains(r.FailedRows[0].Message, "bad response") {
		t.Fatalf("report: %+v", r)
	}
	n := 0
	for _, v := range e.bookSrc.Looked() {
		if v == "2" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("row 2 looked up %d times, want once", n)
	}
}

// usable double-checks the resolved importer: a declared ResolveVia source
// must be provided by the plugin and support lookup (Validate already
// refuses a manifest without lookup; this guards the runner on its own).
func TestImporterRefUsable(t *testing.T) {
	src := &plugintest.LookupSource{}
	for name, c := range map[string]struct {
		ref  importerRef
		want error
	}{
		"manual-only": {importerRef{info: plugin.ImporterInfo{ID: "i"}}, nil},
		"resolvable": {importerRef{info: plugin.ImporterInfo{ResolveVia: "s"}, source: src,
			srcInfo: plugin.SourceInfo{ID: "s", Name: "S", SupportsLookup: true}}, nil},
		"source missing": {importerRef{info: plugin.ImporterInfo{ResolveVia: "s"},
			srcInfo: plugin.SourceInfo{ID: "s", SupportsLookup: true}}, ErrSourceMissing},
		"no lookup": {importerRef{info: plugin.ImporterInfo{ResolveVia: "s"}, source: src,
			srcInfo: plugin.SourceInfo{ID: "s", Name: "S"}}, ErrNoLookup},
	} {
		err := c.ref.usable()
		if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
		if err != nil && !errors.Is(err, ErrUnknownImporter) {
			t.Errorf("%s: %v must match ErrUnknownImporter", name, err)
		}
	}
}

// TestDisablingThePluginPausesARunningImport: the plugin is switched off
// while its import runs (after the second lookup). The run stops before the
// next row with jobs.ErrPaused (the queue keeps the job without counting a
// partial or waiting a backoff; its gate holds it while the plugin is off),
// the import stays running and nothing more is looked up or saved; enabling
// the plugin and running the job again imports the remaining rows exactly
// once.
func TestDisablingThePluginPausesARunningImport(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		if v == "2" {
			if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
				t.Fatal(err)
			}
		}
		return book(v, 300), nil
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("1", "One", plugin.Completed, 300), bookEntry("2", "Two", plugin.Active, 1),
		bookEntry("3", "Three", plugin.Active, 2), bookEntry("4", "Four", plugin.Planned, 0)}}
	id := e.submit("book", "books-csv")
	err := e.run("book", id)
	if !errors.Is(err, jobs.ErrPaused) {
		t.Fatalf("run with the plugin switched off: %v, want ErrPaused", err)
	}
	if got := strings.Join(e.bookSrc.Looked(), ","); got != "1,2" {
		t.Fatalf("looked up %s after the plugin was switched off", got)
	}
	if r := e.report(); r.State != "running" || r.Imported != 2 || r.Remaining != 2 || !r.Active {
		t.Fatalf("paused report: %+v", r)
	}
	var items int
	e.dbh.QueryRow(`SELECT count(*) FROM items`).Scan(&items)
	if items != 2 {
		t.Fatalf("%d items saved", items)
	}

	if err := e.reg.SetEnabled(context.Background(), "book", true); err != nil {
		t.Fatal(err)
	}
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.bookSrc.Looked(), ","); got != "1,2,3,4" {
		t.Fatalf("looked up %s", got)
	}
	if r := e.report(); r.State != "done" || r.Imported != 4 || r.Remaining != 0 || r.Skipped != 0 || r.Failed != 0 {
		t.Fatalf("final report: %+v", r)
	}
	e.dbh.QueryRow(`SELECT count(*) FROM items`).Scan(&items)
	if items != 4 {
		t.Fatalf("%d items after resume", items)
	}
}

// TestDisablingThePluginAfterTheLastRowStillFinishes: the plugin is switched
// off while the import's last row is being processed. Nothing is left to do,
// so the import finishes done right away instead of staying paused (running)
// until the plugin is enabled again.
func TestDisablingThePluginAfterTheLastRowStillFinishes(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(_ context.Context, _, _, v string, _ int) (plugin.ItemData, error) {
		if v == "2" {
			if err := e.reg.SetEnabled(context.Background(), "book", false); err != nil {
				t.Fatal(err)
			}
		}
		return book(v, 300), nil
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		bookEntry("1", "One", plugin.Completed, 300), bookEntry("2", "Two", plugin.Active, 1)}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := e.report(); r.State != "done" || r.Imported != 2 || r.Remaining != 0 || r.Failed != 0 {
		t.Fatalf("report: %+v", r)
	}
}

// TestMalformedJobPayloadDoesNotBlockImports: an import_run job row whose
// payload is not JSON must not break the check for an active import (which
// matches jobs to imports by the payload's import_id).
func TestMalformedJobPayloadDoesNotBlockImports(t *testing.T) {
	e := newGenEnv(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("1", "One", plugin.Completed, 300)}}
	if _, err := e.dbh.Exec(`INSERT INTO jobs(kind, payload_json) VALUES (?, 'not json {')`, KindRun); err != nil {
		t.Fatal(err)
	}
	id := e.submit("book", "books-csv")
	var active *ActiveError
	if _, err := e.im.Submit(context.Background(), "book", "books-csv", "again.csv", strings.NewReader("x")); !errors.As(err, &active) {
		t.Fatalf("second submit while one is pending: %v, want *ActiveError", err)
	}
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.State != "done" || r.Imported != 1 {
		t.Fatalf("report: %+v", r)
	}
}

func TestImportFallbackUsesNextSourceOnNotFound(t *testing.T) {
	e := newGenEnvWithFallback(t)
	e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	e.bookImp.AccountRows = []plugin.ParsedEntry{bookEntry("9781", "Fallback Book", plugin.Planned, 0)}
	id := e.submitAccount("books-csv", "someone", ModeSkip)
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	it, _ := e.item("book", "book", "isbn", "9781")
	if it.Source != "books2" || it.SourceID != "w29781" {
		t.Fatalf("item = %+v", it)
	}
	if e.bookSrc2.Looked() == nil || len(e.bookSrc2.Looked()) != 1 || e.bookSrc2.Looked()[0] != "9781" {
		t.Fatalf("fallback lookups = %v", e.bookSrc2.Looked())
	}
	if r := e.report(); r.Imported != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestImportFallbackDoesNotHideRateLimit(t *testing.T) {
	e := newGenEnvWithFallback(t)
	e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrRateLimited
	}
	e.bookImp.AccountRows = []plugin.ParsedEntry{bookEntry("9782", "Rate Limited", plugin.Planned, 0)}
	id := e.submitAccount("books-csv", "someone", ModeSkip)
	err := e.run("book", id)
	if !errors.Is(err, jobs.ErrRetryLater) {
		t.Fatalf("run err = %v, want ErrRetryLater", err)
	}
	if len(e.bookSrc2.Looked()) != 0 {
		t.Fatalf("fallback was called: %v", e.bookSrc2.Looked())
	}
}

func TestImportFallbackAllNotFoundSavesUnmatched(t *testing.T) {
	e := newGenEnvWithFallback(t)
	e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	e.bookSrc2.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	e.bookImp.AccountRows = []plugin.ParsedEntry{bookEntry("9783", "Nowhere", plugin.Planned, 0)}
	id := e.submitAccount("books-csv", "someone", ModeSkip)
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	it, _ := e.item("book", "book", "isbn", "9783")
	if it.Source != "manual" {
		t.Fatalf("item = %+v", it)
	}
	if r := e.report(); r.Unmatched != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestManualRowsImportWithoutExternalID(t *testing.T) {
	e := newGenEnvManual(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{{
		Kind: "book", Title: "Manual Only", Status: plugin.Completed,
		Fields:   map[string]plugin.FieldValue{"author": {Text: "A. Author"}, "pages": {Num: pf(120)}, "isbn": {Text: "978"}},
		Progress: map[string]float64{"pages": 120},
	}}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	pg, err := e.lib.List(context.Background(), library.Query{PluginID: "book", Kind: "book"}, "en")
	if err != nil || len(pg.Rows) != 1 {
		t.Fatalf("rows = %+v %v", pg.Rows, err)
	}
	row := pg.Rows[0]
	if row.Item.Source != "manual" || row.Item.Titles[0].Text != "Manual Only" ||
		row.Item.Fields["author"].Text != "A. Author" || row.Item.Fields["pages"].Num == nil || *row.Item.Fields["pages"].Num != 120 ||
		row.Entry.Status != library.Completed || row.Entry.Progress["pages"] != 120 {
		t.Fatalf("item = %+v entry = %+v", row.Item, row.Entry)
	}
	if r := e.report(); r.Imported != 1 || r.Unmatched != 0 || r.Skipped != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestManualRowsNeedTitle(t *testing.T) {
	e := newGenEnvManual(t)
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{
		{Kind: "book", Title: "Has Title", Status: plugin.Planned, Fields: map[string]plugin.FieldValue{"pages": {Num: pf(120)}}},
		{Kind: "book", Fields: map[string]plugin.FieldValue{"pages": {Num: pf(120)}}},
	}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.Imported != 1 || r.Skipped != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSourceMissKeepsManualFields(t *testing.T) {
	e := newGenEnv(t)
	e.bookSrc.Fn = func(context.Context, string, string, string, int) (plugin.ItemData, error) {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	e.bookImp.Result = plugin.ParseResult{Entries: []plugin.ParsedEntry{bookEntry("9784", "Missing", plugin.Planned, 0)}}
	e.bookImp.Result.Entries[0].Fields = map[string]plugin.FieldValue{"author": {Text: "A. Author"}, "pages": {Num: pf(200)}}
	id := e.submit("book", "books-csv")
	if err := e.run("book", id); err != nil {
		t.Fatal(err)
	}
	it, _ := e.item("book", "book", "isbn", "9784")
	if it.Source != "manual" || it.Fields["author"].Text != "A. Author" || it.Fields["pages"].Num == nil || *it.Fields["pages"].Num != 200 {
		t.Fatalf("item = %+v", it)
	}
	if r := e.report(); r.Unmatched != 1 {
		t.Fatalf("report = %+v", r)
	}
}
