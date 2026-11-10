package yamtrack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

// ---- fakes ----

type enq struct {
	Kind    string
	Payload any
}

// fakeQueue records enqueued jobs and also writes them to the jobs table
// (inner), because the importer reconciles imports against that table.
type fakeQueue struct {
	mu    sync.Mutex
	jobs  []enq
	err   error
	inner *jobs.Queue
}

func (q *fakeQueue) Enqueue(ctx context.Context, kind string, payload any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.jobs = append(q.jobs, enq{kind, payload})
	if q.inner != nil {
		return q.inner.Enqueue(ctx, kind, payload)
	}
	return nil
}

func (q *fakeQueue) ofKind(kind string) []enq {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []enq
	for _, j := range q.jobs {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}

// fakeResolver answers AnimeByMALID from fn and records every call.
type fakeResolver struct {
	mu    sync.Mutex
	calls []string
	fn    func(malID string, n int) (provider.Anime, error)
}

func (f *fakeResolver) AnimeByMALID(_ context.Context, malID string) (provider.Anime, error) {
	f.mu.Lock()
	f.calls = append(f.calls, malID)
	n := 0
	for _, c := range f.calls {
		if c == malID {
			n++
		}
	}
	f.mu.Unlock()
	return f.fn(malID, n)
}

func (f *fakeResolver) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// ani is the AniList anime for MAL id mal; AniList id is mal+100000.
func ani(mal string, episodes int) provider.Anime {
	n, _ := strconv.Atoi(mal)
	eps := episodes
	a := provider.Anime{
		SearchResult: provider.SearchResult{
			Source: provider.AniList, ID: strconv.Itoa(n + 100000), CoverURL: "https://cdn.example/" + mal + ".jpg",
			Titles: []provider.Title{{Lang: "en", Text: "Anime " + mal}},
		},
		MALID: mal,
	}
	if episodes > 0 {
		a.Episodes = &eps
	}
	return a
}

func found(malID string, _ int) (provider.Anime, error) { return ani(malID, 12), nil }

// clock is a fake clock whose sleep advances time.
type clock struct {
	onSleep func() // called (unlocked) on every sleep, may be nil
	mu      sync.Mutex
	t       time.Time
	sleeps  []time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	hook := c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (c *clock) slept() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var s time.Duration
	for _, d := range c.sleeps {
		s += d
	}
	return s
}

type env struct {
	t   *testing.T
	d   *sql.DB
	st  *store.Store
	q   *fakeQueue
	res *fakeResolver
	clk *clock
	im  *Importer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &env{t: t, d: d, st: store.New(d), q: &fakeQueue{inner: jobs.NewQueue(d)}, res: &fakeResolver{fn: found},
		clk: &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	e.im = New(d, e.st, e.res, e.q, WithClock(e.clk.now, e.clk.sleep))
	return e
}

const csvHeader = "media_id,source,media_type,title,image,score,status,notes,start_date,end_date,progress\n"

// csvOf builds an export; each arg is "id|title|status|score|progress" or a full line starting with a quote.
func csvOf(rows ...string) string {
	var b strings.Builder
	b.WriteString(csvHeader)
	for _, r := range rows {
		f := strings.Split(r, "|")
		for len(f) < 5 {
			f = append(f, "")
		}
		fmt.Fprintf(&b, "%s,mal,anime,%q,https://img.example/%s.jpg,%s,%s,,,,%s\n", f[0], f[1], f[0], f[3], f[2], f[4])
	}
	return b.String()
}

func (e *env) submit(content string) int64 {
	e.t.Helper()
	id, err := e.im.Submit(context.Background(), "export.csv", strings.NewReader(content))
	if err != nil {
		e.t.Fatalf("submit: %v", err)
	}
	return id
}

func (e *env) run(id int64) error {
	return e.runCtx(context.Background(), id)
}

func (e *env) runCtx(ctx context.Context, id int64) error {
	payload, _ := json.Marshal(Payload{ImportID: id})
	return e.im.Handler()(ctx, payload)
}

func (e *env) report() *Report {
	e.t.Helper()
	r, err := e.im.Latest(context.Background())
	if err != nil || r == nil {
		e.t.Fatalf("latest: %v %v", r, err)
	}
	return r
}

func (e *env) animeCount() int {
	var n int
	if err := e.d.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) entryByMAL(mal string) (*store.Saved, *store.ListEntry) {
	e.t.Helper()
	s, err := e.st.FindByMALID(context.Background(), mal)
	if err != nil || s == nil {
		e.t.Fatalf("mal %s not saved: %v", mal, err)
	}
	le, err := e.st.GetListEntry(context.Background(), s.ID)
	if err != nil || le == nil {
		e.t.Fatalf("no list entry for %s: %v", mal, err)
	}
	return s, le
}

func (e *env) outcomes(id int64) map[string]string {
	e.t.Helper()
	rows, err := e.d.Query(`SELECT external_id, outcome FROM import_rows WHERE import_id=?`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		m[k] = v
	}
	return m
}

// ---- tests ----

func TestImportSavesEntriesAndEnqueuesSync(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvOf("1|One|Completed|8.0|12", "2|Two|Planning||0", "3|Three|In progress|7.0|4"))
	if jobsq := e.q.ofKind(KindImport); len(jobsq) != 1 || jobsq[0].Payload.(Payload) != (Payload{ImportID: id, PluginID: "anime"}) {
		t.Fatalf("import job not enqueued: %+v", e.q.jobs)
	}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	s, le := e.entryByMAL("1")
	if s.Source != provider.AniList || s.ID == 0 || le.Status != store.Completed || le.Rating == nil || *le.Rating != 8 || le.Progress != 12 {
		t.Errorf("entry 1: %+v %+v", s, le)
	}
	if s.SyncState != "pending" {
		t.Errorf("sync state = %q", s.SyncState)
	}
	if _, le3 := e.entryByMAL("3"); le3.Status != store.Watching || le3.Progress != 4 {
		t.Errorf("entry 3: %+v", le3)
	}
	syncs := e.q.ofKind(jobs.KindSyncItem)
	if len(syncs) != 3 {
		t.Fatalf("sync jobs = %d", len(syncs))
	}
	if p, ok := syncs[0].Payload.(jobs.SyncPayload); !ok || p.ItemID != s.ID || p.PluginID != "anime" {
		t.Errorf("sync payload = %#v", syncs[0].Payload)
	}
	r := e.report()
	if r.State != "done" || r.Total != 3 || r.Imported != 3 || r.Processed != 3 || r.Remaining != 0 || r.Active {
		t.Errorf("report: %+v", r)
	}
}

func TestImportTwiceCreatesNoDuplicates(t *testing.T) {
	e := newEnv(t)
	content := csvOf("1|One|Completed|8.0|12", "2|Two|Planning||0")
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" {
			return provider.Anime{}, provider.ErrNotFound
		}
		return found(mal, n)
	}
	e.run(e.submit(content))
	if e.animeCount() != 2 {
		t.Fatalf("count %d", e.animeCount())
	}
	id2 := e.submit(content)
	if err := e.run(id2); err != nil {
		t.Fatal(err)
	}
	if e.animeCount() != 2 {
		t.Fatalf("duplicates created: %d", e.animeCount())
	}
	if len(e.res.called()) != 2 {
		t.Errorf("second import made requests: %v", e.res.called())
	}
	if r := e.report(); r.ID != id2 || r.Skipped != 2 || r.Imported != 0 {
		t.Errorf("second report: %+v", r)
	}
}

func TestImportResumesAfterInterruption(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvOf("1|One|Completed|8.0|1", "2|Two|Planning||0", "3|Three|Planning||0"))
	ctx, cancel := context.WithCancel(context.Background())
	interrupted := false
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" && !interrupted {
			interrupted = true
			cancel()
			return provider.Anime{}, fmt.Errorf("anilist: request: %w", ctx.Err())
		}
		return found(mal, n)
	}
	err := e.runCtx(ctx, id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := e.outcomes(id); got["1"] != "imported" || got["2"] != "pending" || got["3"] != "pending" {
		t.Fatalf("after interruption: %v", got)
	}
	e.res.mu.Lock()
	e.res.calls = nil
	e.res.mu.Unlock()
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.res.called(), ","); got != "2,3" {
		t.Fatalf("second run fetched %q, want only the unfinished rows 2,3", got)
	}
	if r := e.report(); r.State != "done" || r.Imported != 3 {
		t.Errorf("report: %+v", r)
	}
}

func TestImportSkipsAlreadySavedByMALID(t *testing.T) {
	e := newEnv(t)
	pre := ani("1", 12)
	pre.ID = "777" // saved earlier under another AniList id
	if _, err := e.st.AddToList(context.Background(), pre, store.ListEntry{Status: store.Completed}); err != nil {
		t.Fatal(err)
	}
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if got := e.res.called(); len(got) != 1 || got[0] != "2" {
		t.Fatalf("requests = %v, want only 2", got)
	}
	if _, le := e.entryByMAL("1"); le.Status != store.Completed {
		t.Errorf("existing entry overwritten: %+v", le)
	}
	r := e.report()
	if r.Skipped != 1 || r.Imported != 1 || len(r.SkippedRows) != 1 || r.SkippedRows[0].Message != "already in your list" {
		t.Errorf("report: %+v", r)
	}
}

func TestImportSkipsSameAniListIDUnderAnotherMALID(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, _ int) (provider.Anime, error) {
		a := ani("1", 12) // every MAL id resolves to the same AniList anime
		return a, nil
	}
	id := e.submit(csvOf("1|One|Planning||0", "9|Nine|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if e.animeCount() != 1 {
		t.Fatalf("count %d", e.animeCount())
	}
	r := e.report()
	if r.Imported != 1 || r.Skipped != 1 || r.Failed != 0 {
		t.Errorf("report: %+v", r)
	}
}

func TestImportSavesUnmatchedAsManualEntry(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(string, int) (provider.Anime, error) { return provider.Anime{}, provider.ErrNotFound }
	id := e.submit(csvOf("55|Lost Show|Dropped|6.0|3"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	s, le := e.entryByMAL("55")
	if s.Source != provider.Manual || s.Anime.ID == "" || s.MALID != "55" {
		t.Errorf("saved: %+v", s)
	}
	if len(s.Titles) != 1 || s.Titles[0] != (provider.Title{Lang: "ja-latn", Text: "Lost Show"}) {
		t.Errorf("titles = %+v", s.Titles)
	}
	if s.CoverPath != "https://img.example/55.jpg" {
		t.Errorf("cover = %q", s.CoverPath)
	}
	if s.SyncState != "pending" {
		t.Errorf("sync state = %q", s.SyncState)
	}
	if le.Status != store.Dropped || le.Rating == nil || *le.Rating != 6 || le.Progress != 3 {
		t.Errorf("entry: %+v", le)
	}
	syncs := e.q.ofKind(jobs.KindSyncItem)
	if len(syncs) != 1 || syncs[0].Payload.(jobs.SyncPayload).ItemID != s.ID {
		t.Errorf("sync jobs: %+v", syncs)
	}
}

func TestImportUnmatchedWithoutHTTPImageHasNoCover(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(string, int) (provider.Anime, error) { return provider.Anime{}, provider.ErrNotFound }
	content := csvHeader + `55,mal,anime,Lost,javascript:alert(1),,Planning,,,,0` + "\n"
	e.run(e.submit(content))
	if s, _ := e.entryByMAL("55"); s.CoverPath != "" {
		t.Errorf("cover = %q", s.CoverPath)
	}
}

func TestImportUnmatchedStillCountedAndListed(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "55" {
			return provider.Anime{}, provider.ErrNotFound
		}
		return found(mal, n)
	}
	content := csvOf("1|One|Completed||1", "55|Lost Show|Planning||0")
	e.run(e.submit(content))
	r := e.report()
	if r.Unmatched != 1 || r.Imported != 1 || len(r.UnmatchedRows) != 1 {
		t.Fatalf("report: %+v", r)
	}
	u := r.UnmatchedRows[0]
	s, _ := e.entryByMAL("55")
	if u.Title != "Lost Show" || u.AnimeID != s.ID || u.SourceID != s.Anime.ID {
		t.Errorf("item = %+v, saved %+v", u, s)
	}
	e.run(e.submit(content))
	if e.animeCount() != 2 {
		t.Errorf("re-run duplicated: %d", e.animeCount())
	}
}

func TestImportContinuesAfterOneRowFails(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" {
			return provider.Anime{}, errors.New("graphql error \"boom\"")
		}
		return found(mal, n)
	}
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	r := e.report()
	if r.State != "done" || r.Imported != 2 || r.Failed != 1 || len(r.FailedRows) != 1 ||
		r.FailedRows[0].Title != "Two" || !strings.Contains(r.FailedRows[0].Message, "boom") {
		t.Fatalf("report: %+v", r)
	}
}

func TestImportAdjustsRowsInsteadOfLosingThem(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, _ int) (provider.Anime, error) { return ani(mal, 12), nil }
	content := csvHeader +
		`1,mal,anime,Over,,,Completed,,2024-01-01 10:00:00+00:00,2024-02-01 10:00:00+00:00,20` + "\n" +
		`2,mal,anime,Backwards,,,Completed,,2024-03-01 10:00:00+00:00,2024-02-01 10:00:00+00:00,12` + "\n" +
		`3,mal,anime,Zero,,,Completed,,,,0` + "\n"
	id := e.submit(content)
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if _, le := e.entryByMAL("1"); le.Progress != 12 || le.StartedOn == nil || le.FinishedOn == nil {
		t.Errorf("clamp: %+v", le)
	}
	if _, le := e.entryByMAL("2"); le.StartedOn != nil || le.FinishedOn != nil || le.Progress != 12 {
		t.Errorf("dates not dropped: %+v", le)
	}
	if _, le := e.entryByMAL("3"); le.Status != store.Completed || le.Progress != 0 {
		t.Errorf("completed with 0 progress changed: %+v", le)
	}
	r := e.report()
	if r.Imported != 3 || r.Failed != 0 {
		t.Fatalf("report: %+v", r)
	}
	var msgs []string
	rows, _ := e.d.Query(`SELECT message FROM import_rows WHERE import_id=? ORDER BY seq`, id)
	for rows.Next() {
		var m string
		rows.Scan(&m)
		msgs = append(msgs, m)
	}
	rows.Close()
	if !strings.Contains(msgs[0], "progress clamped to 12") || !strings.Contains(msgs[1], "dates dropped") || msgs[2] != "" {
		t.Errorf("messages = %q", msgs)
	}
}

func TestImportKeepsWrittenCalendarDayInStore(t *testing.T) {
	e := newEnv(t)
	content := csvHeader + `1,mal,anime,Tokyo,,,Completed,,2024-03-05 00:30:00+09:00,2024-03-05 08:00:00+09:00,12` + "\n"
	e.run(e.submit(content))
	_, le := e.entryByMAL("1")
	if le.StartedOn == nil || le.StartedOn.Format("2006-01-02") != "2024-03-05" {
		t.Errorf("started = %v", le.StartedOn)
	}
}

func TestImportPacesRequestsNotSkippedRows(t *testing.T) {
	e := newEnv(t)
	pre := ani("1", 12)
	if _, err := e.st.AddToList(context.Background(), pre, store.ListEntry{Status: store.Completed}); err != nil {
		t.Fatal(err)
	}
	id := e.submit(csvOf("1|Saved|Planning||0", "2|A|Planning||0", "3|B|Planning||0", "4|C|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	want := 2 * (time.Minute / anilistRequestsPerMinute) // two gaps between three requests
	if got := e.clk.slept(); got != want {
		t.Fatalf("slept %v, want %v (3 requests need 2 intervals; the saved row must not wait)", got, want)
	}
	if got := len(e.clk.sleeps); got != 2 {
		t.Errorf("sleeps = %v", e.clk.sleeps)
	}
	if interval := time.Minute / anilistRequestsPerMinute; interval != 2*time.Second {
		t.Errorf("interval = %v", interval)
	}
}

func TestImportRetriesRateLimitThenSucceeds(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if n <= 2 {
			return provider.Anime{}, fmt.Errorf("anilist: http 429: %w", provider.ErrRateLimited)
		}
		return found(mal, n)
	}
	id := e.submit(csvOf("1|One|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.Imported != 1 || r.Failed != 0 {
		t.Fatalf("report: %+v", r)
	}
	if got := len(e.res.called()); got != 3 {
		t.Errorf("calls = %d", got)
	}
	if e.clk.slept() < 2*time.Minute {
		t.Errorf("slept only %v for two rate limit waits", e.clk.slept())
	}
}

func TestImportRateLimitExhaustedRetriesJobLater(t *testing.T) {
	e := newEnv(t)
	limited := true
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" && limited {
			return provider.Anime{}, fmt.Errorf("anilist: request failed: %w", provider.ErrUnavailable)
		}
		return found(mal, n)
	}
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0"))
	err := e.run(id)
	if !errors.Is(err, jobs.ErrRetryLater) || !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrRetryLater wrapping ErrUnavailable", err)
	}
	row2 := 0
	for _, c := range e.res.called() {
		if c == "2" {
			row2++
		}
	}
	if row2 != 1+maxRetries {
		t.Errorf("row 2 tried %d times, want %d", row2, 1+maxRetries)
	}
	if got := e.outcomes(id); got["1"] != "imported" || got["2"] != "pending" || got["3"] != "pending" {
		t.Fatalf("outcomes = %v", got)
	}
	if r := e.report(); r.State != "running" || !r.Active || r.Failed != 0 {
		t.Errorf("report: %+v", r)
	}
	limited = false
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if r := e.report(); r.State != "done" || r.Imported != 3 {
		t.Errorf("report after retry: %+v", r)
	}
}

func TestImportCancelStopsAtNextRow(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0"))
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "1" {
			if err := e.im.Cancel(context.Background()); err != nil {
				t.Error(err)
			}
		}
		return found(mal, n)
	}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.res.called(), ","); got != "1" {
		t.Errorf("calls = %q, want only the in-flight row", got)
	}
	r := e.report()
	if r.State != "cancelled" || r.Active || r.Imported != 1 || r.Remaining != 2 {
		t.Errorf("report: %+v", r)
	}
	// the cancelled job, run again, does nothing
	if err := e.run(id); err != nil || len(e.res.called()) != 1 {
		t.Errorf("rerun after cancel: %v %v", err, e.res.called())
	}
}

func TestImportContextCancelStopsPromptlyAndKeepsRows(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0"))
	ctx, cancel := context.WithCancel(context.Background())
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		a, err := found(mal, n)
		if mal == "2" {
			cancel() // shutdown arrives while row 2 is being saved
		}
		return a, err
	}
	err := e.runCtx(ctx, id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := e.res.called(); len(got) != 2 {
		t.Errorf("calls = %v, row 3 must not be requested", got)
	}
	got := e.outcomes(id)
	if got["1"] != "imported" || got["2"] != "imported" || got["3"] != "pending" {
		t.Errorf("outcomes = %v (the in-flight row must still be recorded)", got)
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

func TestSubmitAllowsOnlyOneActiveImport(t *testing.T) {
	e := newEnv(t)
	first := e.submit(csvOf("1|One|Planning||0"))
	_, err := e.im.Submit(context.Background(), "again.csv", strings.NewReader(csvOf("2|Two|Planning||0")))
	var active *ActiveError
	if !errors.Is(err, ErrActive) || !errors.As(err, &active) || active.ID != first {
		t.Fatalf("err = %v", err)
	}
	var n int
	e.d.QueryRow(`SELECT COUNT(*) FROM imports`).Scan(&n)
	if n != 1 {
		t.Errorf("imports = %d", n)
	}
	if len(e.q.ofKind(KindImport)) != 1 {
		t.Errorf("second job enqueued")
	}
	// once finished (or cancelled) a new upload is fine
	if err := e.im.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.im.Submit(context.Background(), "again.csv", strings.NewReader(csvOf("2|Two|Planning||0"))); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
}

func TestSubmitRejectsBadFilesAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	cases := map[string]struct {
		in   string
		want error
	}{
		"missing columns": {"foo,bar\n1,2\n", ErrBadFormat},
		"empty":           {"", ErrBadFormat},
		"no anime rows":   {csvHeader + `1,igdb,game,G,,,Completed,,,,0` + "\n", ErrNoAnimeRows},
		"header only":     {csvHeader, ErrNoAnimeRows},
	}
	for name, c := range cases {
		_, err := e.im.Submit(context.Background(), "x.csv", strings.NewReader(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	var n, m int
	e.d.QueryRow(`SELECT COUNT(*) FROM imports`).Scan(&n)
	e.d.QueryRow(`SELECT COUNT(*) FROM import_rows`).Scan(&m)
	if n != 0 || m != 0 || len(e.q.jobs) != 0 {
		t.Errorf("stored imports=%d rows=%d jobs=%d", n, m, len(e.q.jobs))
	}
}

func TestSubmitEnqueueFailureMarksImportFailed(t *testing.T) {
	e := newEnv(t)
	e.q.err = errors.New("queue down")
	if _, err := e.im.Submit(context.Background(), "x.csv", strings.NewReader(csvOf("1|One|Planning||0"))); err == nil {
		t.Fatal("expected error")
	}
	if r := e.report(); r.State != "failed" || r.Active {
		t.Errorf("report: %+v", r)
	}
	e.q.err = nil
	if _, err := e.im.Submit(context.Background(), "x.csv", strings.NewReader(csvOf("1|One|Planning||0"))); err != nil {
		t.Fatalf("a failed import must not block the next: %v", err)
	}
}

func TestSubmitStoresParserSkipsAndCountsIgnored(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvHeader +
		`1,mal,anime,Good,,,Planning,,,,0` + "\n" +
		`2,mal,anime,Weird,,,Maybe,,,,0` + "\n" +
		`3,igdb,game,Game,,,Completed,,,,0` + "\n")
	r := e.report()
	if r.ID != id || r.Total != 2 || r.Ignored != 1 || r.Skipped != 1 || r.Remaining != 1 || r.Processed != 1 {
		t.Fatalf("report: %+v", r)
	}
	if len(r.SkippedRows) != 1 || !strings.Contains(r.SkippedRows[0].Message, "unknown status") {
		t.Errorf("skipped rows: %+v", r.SkippedRows)
	}
	if r.ETA != time.Duration(r.Remaining)*time.Minute/anilistRequestsPerMinute {
		t.Errorf("eta = %v", r.ETA)
	}
}

func TestReportCapsListsAt200(t *testing.T) {
	e := newEnv(t)
	var rows []string
	for i := 1; i <= 250; i++ {
		rows = append(rows, fmt.Sprintf("%d|Show %d|Planning||0", i, i))
	}
	e.res.fn = func(string, int) (provider.Anime, error) { return provider.Anime{}, errors.New("nope") }
	id := e.submit(csvOf(rows...))
	if err := e.run(id); err != nil {
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

func TestHandlerIgnoresMissingImportAndRejectsBadPayload(t *testing.T) {
	e := newEnv(t)
	if err := e.run(999); err != nil {
		t.Errorf("missing import: %v", err)
	}
	if err := e.im.Handler()(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Error("bad payload accepted")
	}
}

func TestLatestWithoutImportIsNil(t *testing.T) {
	e := newEnv(t)
	if r, err := e.im.Latest(context.Background()); r != nil || err != nil {
		t.Fatalf("%v %v", r, err)
	}
}

// ---- fix round 1 ----

// jobState sets the state of the import job(s) of an import in the jobs table.
func (e *env) setJobState(id int64, state, lastErr string) {
	e.t.Helper()
	_, err := e.d.Exec(`UPDATE jobs SET state=?, last_error=? WHERE kind=? AND json_extract(payload_json,'$.import_id')=?`,
		state, lastErr, KindImport, id)
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestStuckImportIsReconciledToFailed(t *testing.T) {
	e := newEnv(t)
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0"))
	e.setJobState(id, "failed", "gave up after 10 partial runs: AniList is not answering")
	r := e.report()
	if r.State != "failed" || r.Active || !strings.Contains(r.Note, "gave up after 10 partial runs") || !r.CanResume {
		t.Fatalf("report: %+v", r)
	}
	// the one-active-import rule no longer blocks an upload
	if _, err := e.im.Submit(context.Background(), "n.csv", strings.NewReader(csvOf("3|Three|Planning||0"))); err != nil {
		t.Fatalf("submit after stall: %v", err)
	}
}

func TestReconcileLeavesImportsWithLiveJobAlone(t *testing.T) {
	for _, state := range []string{"pending", "running"} {
		e := newEnv(t)
		id := e.submit(csvOf("1|One|Planning||0"))
		e.setJobState(id, state, "")
		if r := e.report(); r.State != "pending" || !r.Active || r.Note != "" {
			t.Errorf("job %s: report %+v", state, r)
		}
	}
}

func TestSubmitAfterReconcileIsRaceSafe(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.im.Submit(context.Background(), "x.csv", strings.NewReader(csvOf(fmt.Sprintf("%d|S|Planning||0", i+1))))
		}()
	}
	// readers reconciling at the same time must not fail a freshly submitted import
	for i := 0; i < 20; i++ {
		e.im.Latest(context.Background())
	}
	wg.Wait()
	ok, active := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrActive):
			active++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || active != len(errs)-1 {
		t.Fatalf("ok=%d active=%d", ok, active)
	}
	var n int
	e.d.QueryRow(`SELECT COUNT(*) FROM imports WHERE state IN ('pending','running')`).Scan(&n)
	if n != 1 {
		t.Fatalf("active imports = %d", n)
	}
}

func TestResumeEnqueuesNewJobAndProcessesOnlyPendingRows(t *testing.T) {
	e := newEnv(t)
	limited := true
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" && limited {
			return provider.Anime{}, fmt.Errorf("x: %w", provider.ErrUnavailable)
		}
		return found(mal, n)
	}
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0"))
	if err := e.run(id); !errors.Is(err, jobs.ErrRetryLater) {
		t.Fatal(err)
	}
	e.setJobState(id, "failed", "gave up")
	if r := e.report(); r.State != "failed" || !r.CanResume {
		t.Fatalf("report: %+v", r)
	}
	limited = false
	e.res.mu.Lock()
	e.res.calls = nil
	e.res.mu.Unlock()
	rid, err := e.im.Resume(context.Background())
	if err != nil || rid != id {
		t.Fatalf("resume: %d %v", rid, err)
	}
	if n := len(e.q.ofKind(KindImport)); n != 2 {
		t.Fatalf("import jobs enqueued = %d, want 2", n)
	}
	if r := e.report(); r.State != "pending" || !r.Active || r.Note != "" || r.CanResume {
		t.Fatalf("after resume: %+v", r)
	}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.res.called(), ","); got != "2,3" {
		t.Fatalf("fetched %q, want only the pending rows", got)
	}
	if r := e.report(); r.State != "done" || r.Imported != 3 || r.CanResume {
		t.Errorf("final: %+v", r)
	}
}

func TestResumeCancelledImportAndRefusals(t *testing.T) {
	e := newEnv(t)
	if _, err := e.im.Resume(context.Background()); !errors.Is(err, ErrNothingToResume) {
		t.Fatalf("no import: %v", err)
	}
	id := e.submit(csvOf("1|One|Planning||0"))
	// active: refused with the active import's id
	_, err := e.im.Resume(context.Background())
	var active *ActiveError
	if !errors.As(err, &active) || active.ID != id {
		t.Fatalf("active: %v", err)
	}
	e.im.Cancel(context.Background())
	if !e.report().CanResume {
		t.Fatal("cancelled import with pending rows must be resumable")
	}
	if _, err := e.im.Resume(context.Background()); err != nil {
		t.Fatalf("resume cancelled: %v", err)
	}
	e.run(id)
	// finished: nothing to resume
	if _, err := e.im.Resume(context.Background()); !errors.Is(err, ErrNothingToResume) {
		t.Fatalf("done import: %v", err)
	}
	// only the latest import can be resumed, and not while a newer one is active
	e.submit(csvOf("2|Two|Planning||0"))
	if _, err := e.im.Resume(context.Background()); !errors.Is(err, ErrActive) {
		t.Fatalf("newer active: %v", err)
	}
}

func TestRetryNoteIsSetWhileWaitingAndClearedAfter(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if n <= 2 {
			return provider.Anime{}, fmt.Errorf("x: %w", provider.ErrRateLimited)
		}
		return found(mal, n)
	}
	var notes []string
	e.clk.onSleep = func() {
		if r, _ := e.im.Latest(context.Background()); r != nil && r.Note != "" {
			notes = append(notes, r.Note)
		}
	}
	id := e.submit(csvOf("1|One|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "Waiting for AniList (retry 1/5)") || !strings.Contains(notes[1], "retry 2/5") {
		t.Fatalf("notes during retry = %q", notes)
	}
	if r := e.report(); r.Note != "" || r.State != "done" {
		t.Errorf("note not cleared: %+v", r)
	}
}

func TestCancelDuringRetryLoopStopsBeforeNextAttempt(t *testing.T) {
	e := newEnv(t)
	e.res.fn = func(string, int) (provider.Anime, error) {
		return provider.Anime{}, fmt.Errorf("x: %w", provider.ErrRateLimited)
	}
	e.clk.onSleep = func() { e.im.Cancel(context.Background()) }
	id := e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0"))
	if err := e.run(id); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := e.res.called(); len(got) != 1 {
		t.Fatalf("calls = %v, want one (no retry after cancel)", got)
	}
	if r := e.report(); r.State != "cancelled" || r.Note != "" {
		t.Errorf("report: %+v", r)
	}
}

func savedNoEntry(t *testing.T, e *env, a provider.Anime, state string) int64 {
	t.Helper()
	id, err := e.st.SaveAnime(context.Background(), a, store.WithSyncState(state))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSavedButUnlistedRowGetsListEntry(t *testing.T) {
	e := newEnv(t)
	// path 1: found by MAL id
	pre := ani("1", 12)
	pre.ID = "777"
	id1 := savedNoEntry(t, e, pre, "done")
	// path 2: saved without MAL id, resolved to the same AniList id (ErrAlreadySaved)
	pre2 := ani("2", 12)
	pre2.MALID = ""
	id2 := savedNoEntry(t, e, pre2, "done")
	id := e.submit(csvOf("1|One|Completed|8.0|5", "2|Two|In progress||20"))
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	le1, _ := e.st.GetListEntry(context.Background(), id1)
	le2, _ := e.st.GetListEntry(context.Background(), id2)
	if le1 == nil || le1.Status != store.Completed || le1.Rating == nil || *le1.Rating != 8 || le1.Progress != 5 {
		t.Errorf("entry 1: %+v", le1)
	}
	if le2 == nil || le2.Status != store.Watching || le2.Progress != 12 {
		t.Errorf("entry 2 (progress clamped): %+v", le2)
	}
	r := e.report()
	if r.Imported != 2 || r.Skipped != 0 || r.Failed != 0 {
		t.Fatalf("report: %+v", r)
	}
	var msg string
	e.d.QueryRow(`SELECT message FROM import_rows WHERE import_id=? AND external_id='2'`, id).Scan(&msg)
	if !strings.Contains(msg, "added to your list (anime was already saved)") || !strings.Contains(msg, "progress clamped to 12") {
		t.Errorf("message = %q", msg)
	}
	if e.animeCount() != 2 {
		t.Errorf("count %d", e.animeCount())
	}
}

func TestSavedAndListedRowStaysSkipped(t *testing.T) {
	e := newEnv(t)
	pre := ani("1", 12)
	if _, err := e.st.AddToList(context.Background(), pre, store.ListEntry{Status: store.Dropped}, store.WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	id := e.submit(csvOf("1|One|Completed|8.0|5"))
	e.run(id)
	r := e.report()
	if r.Skipped != 1 || r.Imported != 0 || r.SkippedRows[0].Message != "already in your list" {
		t.Fatalf("report: %+v", r)
	}
	if _, le := e.entryByMAL("1"); le.Status != store.Dropped {
		t.Errorf("entry overwritten: %+v", le)
	}
}

func TestSavedRowWithPendingSyncGetsOneSyncJob(t *testing.T) {
	e := newEnv(t)
	// listed (skipped) with sync still pending, e.g. a crash before the job was enqueued
	if _, err := e.st.AddToList(context.Background(), ani("1", 12), store.ListEntry{Status: store.Dropped}); err != nil {
		t.Fatal(err)
	}
	// unlisted with sync pending
	pre := ani("2", 12)
	savedNoEntry(t, e, pre, "pending")
	// listed with sync done: no job
	if _, err := e.st.AddToList(context.Background(), ani("3", 12), store.ListEntry{Status: store.Dropped}, store.WithSyncState("done")); err != nil {
		t.Fatal(err)
	}
	e.run(e.submit(csvOf("1|One|Planning||0", "2|Two|Planning||0", "3|Three|Planning||0")))
	syncs := e.q.ofKind(jobs.KindSyncItem)
	if len(syncs) != 2 {
		t.Fatalf("sync jobs = %d, want 2 (rows 1 and 2 only)", len(syncs))
	}
}

func TestImperfectFieldsKeepEntryAndAppendNotes(t *testing.T) {
	e := newEnv(t)
	content := csvHeader +
		`1,mal,anime,One,,abc,Completed,,junk,,zzz` + "\n" +
		`2,mal,anime,"",,,Planning,,,,0` + "\n"
	e.res.fn = func(mal string, n int) (provider.Anime, error) {
		if mal == "2" {
			return provider.Anime{}, provider.ErrNotFound
		}
		return found(mal, n)
	}
	id := e.submit(content)
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	_, le := e.entryByMAL("1")
	if le.Rating != nil || le.Progress != 0 || le.StartedOn != nil {
		t.Errorf("entry 1: %+v", le)
	}
	s2, _ := e.entryByMAL("2")
	if len(s2.Titles) != 1 || s2.Titles[0].Text != "MAL #2" {
		t.Errorf("titles = %+v", s2.Titles)
	}
	var m1, m2 string
	e.d.QueryRow(`SELECT message FROM import_rows WHERE import_id=? AND external_id='1'`, id).Scan(&m1)
	e.d.QueryRow(`SELECT message FROM import_rows WHERE import_id=? AND external_id='2'`, id).Scan(&m2)
	for _, w := range []string{"score", "progress", "start date"} {
		if !strings.Contains(m1, w) {
			t.Errorf("message 1 %q lacks %q", m1, w)
		}
	}
	if !strings.Contains(m2, "title") || !strings.Contains(m2, "not found on AniList") {
		t.Errorf("message 2 = %q", m2)
	}
	if r := e.report(); r.Imported != 1 || r.Unmatched != 1 || r.Failed != 0 {
		t.Errorf("report: %+v", r)
	}
}
