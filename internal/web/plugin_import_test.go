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

// TestPluginSettingsIdleDays: a plugin with an IdlePause importer gets the
// idle cutoff field (default 30, labelled generically); saving stores it,
// invalid input answers the page with a message and 422; a plugin without
// such an importer shows no field and refuses the route.
func TestPluginSettingsIdleDays(t *testing.T) {
	e := newFixtureEnv(t, func(book, tv *plugintest.Plugin) { tv.M.Importers[1].IdlePause = true })
	page := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, `action="/settings/plugins/tv/idle"`, `name="idle_days" value="30"`,
		"Pause entries without activity for", "(0 = off)", `min="0"`, `max="3650"`)

	rec := e.do("POST", "/settings/plugins/tv/idle", url.Values{"idle_days": {" 45 "}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins/tv#idle" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	ctx := context.Background()
	if got := e.set.IdleDays(ctx, "tv"); got != 45 {
		t.Fatalf("idle days = %d", got)
	}
	mustContain(t, e.get("/settings/plugins/tv").Body.String(), `name="idle_days" value="45"`)
	if rec := e.do("POST", "/settings/plugins/tv/idle", url.Values{"idle_days": {"0"}}, false); rec.Code != http.StatusSeeOther || e.set.IdleDays(ctx, "tv") != 0 {
		t.Fatalf("off: %d %d", rec.Code, e.set.IdleDays(ctx, "tv"))
	}
	for _, bad := range []string{"", "-1", "3651", "abc", "1.5", "<b>9</b>"} {
		rec := e.do("POST", "/settings/plugins/tv/idle", url.Values{"idle_days": {bad}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q: %d", bad, rec.Code)
		}
		mustContain(t, rec.Body.String(), "whole number of days from 0 to 3650", `name="idle_days"`)
		mustNotContain(t, rec.Body.String(), "<b>9</b>")
		if got := e.set.IdleDays(ctx, "tv"); got != 0 {
			t.Fatalf("%q changed the setting to %d", bad, got)
		}
	}

	// the book fixture has no IdlePause importer: no field, no route
	mustNotContain(t, e.get("/settings/plugins/book").Body.String(), `name="idle_days"`)
	if rec := e.do("POST", "/settings/plugins/book/idle", url.Values{"idle_days": {"10"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("book: %d", rec.Code)
	}
	if rec := e.do("POST", "/settings/plugins/nope/idle", url.Values{"idle_days": {"10"}}, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin: %d", rec.Code)
	}
}

// TestPluginSettingsNoIdleFieldWithoutIdlePause: without the opt-in the
// account importer's plugin shows no idle field.
func TestPluginSettingsNoIdleFieldWithoutIdlePause(t *testing.T) {
	e := newFixtureEnv(t)
	mustNotContain(t, e.get("/settings/plugins/tv").Body.String(), `name="idle_days"`, "/idle")
}

// TestIdleFormLabelsFromTargetedKind: the help text names the statuses of
// the kind the IdlePause importer resolves into (its ResolveVia source's
// kinds); when that is not one kind, the neutral core names are used.
func TestIdleFormLabelsFromTargetedKind(t *testing.T) {
	first := plugin.KindDef{Key: "a", Statuses: plugin.StatusLabels{Planned: "PA", Active: "AA", Paused: "PsA", Completed: "CA", Dropped: "DA"}}
	second := plugin.KindDef{Key: "b", Statuses: plugin.StatusLabels{Planned: "PB", Active: "AB", Paused: "PsB", Completed: "CB", Dropped: "DB"}}
	m := plugin.Manifest{ID: "p", Kinds: []plugin.KindDef{first, second},
		Sources:   []plugin.SourceInfo{{ID: "s", Kinds: []string{"b"}}},
		Importers: []plugin.ImporterInfo{{ID: "i", Name: "I", AccountLabel: "U", IdlePause: true, ResolveVia: "s"}}}
	f := idleFormFor(m, "30", "")
	if f.Active != "AB" || f.Paused != "PsB" || f.Kept != "CB and DB" {
		t.Fatalf("targeted kind: %+v", f)
	}
	m.Sources[0].Kinds = nil
	f = idleFormFor(m, "30", "")
	if f.Active != "Active" || f.Paused != "Paused" || f.Kept != "Completed and Dropped" {
		t.Fatalf("several kinds: %+v", f)
	}
	m.Kinds = m.Kinds[:1]
	if f = idleFormFor(m, "30", ""); f.Active != "AA" {
		t.Fatalf("single kind: %+v", f)
	}
}

// TestIdleFormHelpWhileOn: the "keep their status" line is shown only while
// the rule is on.
func TestIdleFormHelpWhileOn(t *testing.T) {
	e := newFixtureEnv(t, func(book, tv *plugintest.Plugin) { tv.M.Importers[1].IdlePause = true })
	mustContain(t, e.get("/settings/plugins/tv").Body.String(), "entries keep their status while this is on")
	if err := e.set.SetIdleDays(context.Background(), "tv", 0); err != nil {
		t.Fatal(err)
	}
	page := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, page, "The rule is off.")
	mustNotContain(t, page, "keep their status")
}
