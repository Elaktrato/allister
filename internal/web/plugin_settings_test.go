package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
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
	if err := e.lib.StopRefreshRun(bg, run2.ID, library.RefreshStoppedCredential); err != nil {
		t.Fatal(err)
	}
	page = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, "Updated 0, nothing new 0, failed 2", "Stopped: the credential of the source is missing or was refused.")
}

func TestPluginSettingsRefreshNothingToAsk(t *testing.T) {
	tv := plugintest.TVPlugin()
	e := newEnvWith(t, withPlugins(tv))
	rec := e.do("POST", "/settings/plugins/tv/refresh", url.Values{}, false)
	mustContain(t, rec.Body.String(), "There are no items from a source to update.")
	mustNotContain(t, rec.Body.String(), `name="confirm" value="1"`)
}
