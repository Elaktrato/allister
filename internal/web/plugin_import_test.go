package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// TestPluginSettingsImportSection: the section renders the account importer
// with its name field and repeat select and the file importer with a file
// input; saving the account form stores the rule, the name and the schedule,
// starts an import with that rule and enqueues the repeat job.
func TestPluginSettingsImportSection(t *testing.T) {
	var tvp *plugintest.Plugin
	e := newFixtureEnv(t, func(book, tv *plugintest.Plugin) { tvp = tv })
	page := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, ">Import</h2>", `action="/settings/plugins/tv/import/tv-account"`,
		`name="account"`, ">Username<", `name="mode"`, `name="schedule"`,
		"Skip (leave them alone)", "Update when the source is newer", ">Daily<")
	mustContain(t, page, `action="/settings/plugins/tv/import/tv-json"`, `type="file"`, `enctype="multipart/form-data"`)

	acc := tvp.Importers["tv-account"].(*plugintest.FakeImporter)
	acc.AccountRows = []plugin.ParsedEntry{{Kind: "show", Title: "From Account", Status: plugin.Active,
		ExternalIDs: map[string]string{"tvdata": "77"}}}
	rec := e.do("POST", "/settings/plugins/tv/import/tv-account",
		url.Values{"account": {"someone"}, "mode": {"newer"}, "schedule": {"daily"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/import" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	ctx := context.Background()
	if got := e.set.ImportAccount(ctx, "tv", "tv-account"); got != "someone" {
		t.Fatalf("account = %q", got)
	}
	if got := e.set.ImportMode(ctx, "tv", "tv-account"); got != "newer" {
		t.Fatalf("mode = %q", got)
	}
	if got := e.set.ImportSchedule(ctx, "tv", "tv-account"); got != "daily" {
		t.Fatalf("schedule = %q", got)
	}
	var mode string
	if err := e.d.QueryRow(`SELECT mode FROM imports ORDER BY id DESC LIMIT 1`).Scan(&mode); err != nil || mode != "newer" {
		t.Fatalf("import mode = %q %v", mode, err)
	}
	found := false
	for _, j := range e.jobs.got {
		if j.Kind == "scheduled_import" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no repeat job enqueued: %+v", e.jobs.got)
	}
	// the page shows the stored choices
	page = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, `value="someone"`, `<option value="newer" selected>`, `<option value="daily" selected>`)
	// an unknown importer is refused; an unknown mode is a bad request
	if rec := e.do("POST", "/settings/plugins/tv/import/nope", url.Values{"account": {"x"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown importer: %d", rec.Code)
	}
	if rec := e.do("POST", "/settings/plugins/tv/import/tv-account", url.Values{"account": {"x"}, "mode": {"weird"}}, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: %d", rec.Code)
	}
}
