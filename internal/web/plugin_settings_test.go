package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// sourceless is the book fixture under id "nosrc" without any source.
func sourceless() *plugintest.Plugin {
	p := plugintest.BookPlugin(plugintest.WithID("nosrc"))
	p.M.Sources, p.Sources, p.M.Importers = nil, nil, nil
	return p
}

func TestPluginSettingsRefresh(t *testing.T) {
	book, tv := plugintest.BookPlugin(), plugintest.TVPlugin()
	e := newEnvWith(t, withPlugins(book, tv, sourceless()))
	if _, ok := e.reg.Manifest("nosrc"); !ok {
		t.Fatalf("fixture refused: %v", e.reg.Problems())
	}
	e.addFixture(t, "tv", "show", tvShow("1", "Show A"), library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("2", "Show B"), library.Entry{Status: library.Active})
	manual := tvShow("m1", "Manual Show")
	manual.Source = "manual"
	e.addFixture(t, "tv", "show", manual, library.Entry{Status: library.Active})

	// the button only for plugins with a source
	mustNotContain(t, e.get("/settings/plugins/nosrc").Body.String(), "Update from source")
	if rec := e.do("POST", "/settings/plugins/nosrc/refresh", url.Values{"confirm": {"1"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("sourceless post: %d", rec.Code)
	}
	page := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, `action="/settings/plugins/tv/refresh"`, ">Update from source</button>")

	// the confirmation names the count and starts nothing
	rec := e.do("POST", "/settings/plugins/tv/refresh", url.Values{}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Update 2 items from source? Only empty values are filled in and missing list entries added; nothing you set is changed.",
		`name="confirm" value="1"`)
	if r, err := e.lib.LatestRefreshRun(bg, "tv"); err != nil || r != nil || len(e.jobs.got) != 0 {
		t.Fatalf("started on the confirmation: %+v %v %v", r, err, e.jobs.got)
	}

	// confirmed: the run starts with one job per item
	rec = e.do("POST", "/settings/plugins/tv/refresh", url.Values{"confirm": {"1"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins/tv#refresh" {
		t.Fatalf("start: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	run, err := e.lib.LatestRefreshRun(bg, "tv")
	if err != nil || run == nil || run.Total != 2 {
		t.Fatalf("run %+v %v", run, err)
	}
	if len(e.jobs.got) != 2 {
		t.Fatalf("jobs %v", e.jobs.got)
	}
	for _, j := range e.jobs.got {
		p, ok := j.Payload.(jobs.RefreshPayload)
		if j.Kind != jobs.KindRefreshItem || !ok || p.RunID != run.ID || p.PluginID != "tv" {
			t.Fatalf("job %+v", j)
		}
	}

	// progress, also as the polled partial
	page = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, "0 of 2", `hx-get="/settings/plugins/tv/refresh"`)
	mustNotContain(t, page, ">Update from source</button>")
	part := e.do("GET", "/settings/plugins/tv/refresh", nil, true).Body.String()
	mustContain(t, part, `id="refresh"`, "0 of 2")
	mustNotContain(t, part, "<html")
	if rec := e.get("/settings/plugins/tv/refresh"); rec.Code != http.StatusSeeOther {
		t.Fatalf("plain get: %d", rec.Code)
	}

	// a second press while it runs is refused
	rec = e.do("POST", "/settings/plugins/tv/refresh", url.Values{"confirm": {"1"}}, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second start: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "already running")
	if len(e.jobs.got) != 2 {
		t.Fatalf("second start enqueued: %v", e.jobs.got)
	}

	// the final summary with the failed titles
	if err := e.lib.RecordRefresh(bg, run.ID, library.RefreshUpdated, ""); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.get("/settings/plugins/tv").Body.String(), "1 of 2")
	if err := e.lib.RecordRefresh(bg, run.ID, library.RefreshFailed, "Show <B>"); err != nil {
		t.Fatal(err)
	}
	page = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, "Updated 1, nothing new 0, failed 1", "Show &lt;B&gt;", ">Update from source</button>")
	mustNotContain(t, page, `hx-get="/settings/plugins/tv/refresh"`)

	// a run stopped by a missing credential says so
	rec = e.do("POST", "/settings/plugins/tv/refresh", url.Values{"confirm": {"1"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("restart: %d", rec.Code)
	}
	run2, _ := e.lib.LatestRefreshRun(bg, "tv")
	// its jobs are still queued: the stop shows at once all the same
	for _, j := range e.jobs.got[2:] {
		b, _ := json.Marshal(j.Payload)
		if _, err := e.d.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES (?, ?, '2000-01-01T00:00:00Z')`, j.Kind, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.lib.StopRefreshRun(bg, run2.ID, library.RefreshStoppedCredential); err != nil {
		t.Fatal(err)
	}
	page = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, "Updated 0, nothing new 0, failed 2", "Stopped: the credential of the source is missing or was refused.", ">Update from source</button>")
	mustNotContain(t, page, `hx-get="/settings/plugins/tv/refresh"`)
	// and a new press is accepted
	if rec := e.do("POST", "/settings/plugins/tv/refresh", url.Values{"confirm": {"1"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("start after the stop: %d", rec.Code)
	}
}

func TestPluginSettingsRefreshNothingToAsk(t *testing.T) {
	tv := plugintest.TVPlugin()
	e := newEnvWith(t, withPlugins(tv))
	rec := e.do("POST", "/settings/plugins/tv/refresh", url.Values{}, false)
	mustContain(t, rec.Body.String(), "There are no items from a source to update.")
	mustNotContain(t, rec.Body.String(), `name="confirm" value="1"`)
}

// withoutPeople is the tv fixture under id "tvnp" whose kind has no people
// section: a refresh job asks its source once (Details only).
func withoutPeople() *plugintest.Plugin {
	p := plugintest.TVPlugin(plugintest.WithID("tvnp"))
	k := &p.M.Kinds[0]
	k.Sections = slices.DeleteFunc(k.Sections, func(s plugin.SectionDef) bool { return s.Type == plugin.SectionPeople })
	return p
}

// The run's jobs are staggered to the source's declared request rate: job
// i may run at start + i * calls per item * 1 min / RequestsPerMinute (the
// tv source declares 30 per minute; a kind with a people section costs two
// calls per item, one without costs one).
func TestPluginSettingsRefreshPacedToSource(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.TVPlugin(), withoutPeople()))
	if _, ok := e.reg.Manifest("tvnp"); !ok {
		t.Fatalf("fixture refused: %v", e.reg.Problems())
	}
	for _, pid := range []string{"tv", "tvnp"} {
		for _, id := range []string{"1", "2", "3"} {
			e.addFixture(t, pid, "show", tvShow(id, "Show "+id), library.Entry{Status: library.Active})
		}
	}
	start := *e.now
	for _, c := range []struct {
		plugin string
		gap    time.Duration
	}{{"tv", 4 * time.Second}, {"tvnp", 2 * time.Second}} {
		e.jobs.got, e.jobs.at = nil, nil
		if rec := e.do("POST", "/settings/plugins/"+c.plugin+"/refresh", url.Values{"confirm": {"1"}}, false); rec.Code != http.StatusSeeOther {
			t.Fatalf("%s start: %d", c.plugin, rec.Code)
		}
		want := []time.Time{start, start.Add(c.gap), start.Add(2 * c.gap)}
		if len(e.jobs.at) != 3 {
			t.Fatalf("%s jobs %v", c.plugin, e.jobs.got)
		}
		for i, at := range e.jobs.at {
			if !at.Equal(want[i]) {
				t.Fatalf("%s run_after %v, want %v", c.plugin, e.jobs.at, want)
			}
		}
	}
}

// lockedClock is a clock a running queue may read while the test moves it.
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockedClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *lockedClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// A job enqueued after a run started (a new item's sync) runs before the
// run's later jobs instead of waiting behind the whole run.
func TestPluginSettingsRefreshLetsOtherJobsIn(t *testing.T) {
	e := newEnvWith(t, withPlugins(plugintest.TVPlugin()))
	for _, id := range []string{"1", "2", "3"} {
		e.addFixture(t, "tv", "show", tvShow(id, "Show "+id), library.Entry{Status: library.Active})
	}
	clk := &lockedClock{t: *e.now}
	q := jobs.NewQueue(e.d, jobs.WithClock(clk.now), jobs.WithPollInterval(time.Millisecond))
	e.srv.deps.Jobs = dbJobs{e.jobs, q}
	var (
		mu  sync.Mutex
		ran []string
	)
	note := func(s string) {
		mu.Lock()
		ran = append(ran, s)
		mu.Unlock()
	}
	q.Register(jobs.KindRefreshItem, func(_ context.Context, raw json.RawMessage) error {
		var p jobs.RefreshPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		note(fmt.Sprint("refresh ", p.ItemID))
		return nil
	})
	q.Register("other", func(context.Context, json.RawMessage) error { note("other"); return nil })

	if rec := e.do("POST", "/settings/plugins/tv/refresh", url.Values{"confirm": {"1"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", rec.Code)
	}
	if err := q.Enqueue(bg, "other", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	wait := func(n int) []string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			got := slices.Clone(ran)
			mu.Unlock()
			if len(got) >= n || time.Now().After(deadline) {
				return got
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got := wait(2); !slices.Equal(got, []string{"refresh 1", "other"}) {
		t.Fatalf("before the run's later jobs: %v", got)
	}
	clk.add(time.Minute)
	if got := wait(4); !slices.Equal(got, []string{"refresh 1", "other", "refresh 2", "refresh 3"}) {
		t.Fatalf("order %v", got)
	}
}
