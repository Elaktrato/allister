package web

// A source may declare named credential fields instead of one key
// (SourceInfo.KeyFields): the plugins page shows one input per field, saves
// them all or nothing, and clears every field on Remove. The source labels,
// the plugin notice and the missing-credential message treat such a source as
// configured only when every field is set.

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// newFieldFixtureEnv is newFixtureEnv with the TV source needing two named
// credentials (Client ID and Client Secret, the latter masked) instead of one
// API key.
func newFieldFixtureEnv(t *testing.T, mod ...func(book, tv *plugintest.Plugin)) *fixtureEnv {
	t.Helper()
	book, tv := plugintest.BookPlugin(), plugintest.TVPlugin(plugintest.WithSourceKeyFields(
		plugin.KeyField{Name: "client_id", Label: "Client ID"},
		plugin.KeyField{Name: "client_secret", Label: "Client Secret", Secret: true},
	))
	for _, m := range mod {
		m(book, tv)
	}
	fe := &fixtureEnv{}
	fe.book, _ = book.Sources["books"].(*plugintest.FakeSource)
	fe.tv, _ = tv.Sources["tvdata"].(*plugintest.FakeSource)
	fe.env = newEnvWith(t, withPlugins(book, tv))
	return fe
}

// withSourceKeyField serves e through a Deps.SourceKeyField resolver (what
// production wires to answer with the stored field or one from the
// environment).
func (e *env) withSourceKeyField(fn func(context.Context, string, string) (string, error)) {
	deps := e.srv.deps
	deps.SourceKeyField = fn
	e.srv, e.h = newServer(deps, func() time.Time { return *e.now })
}

// TestSourceKeyFieldsCardSavesAllOrNothing: the card renders one input per
// named field (masked only when the field is secret), refuses a form missing a
// field with 400 and stores nothing, saves every field together without
// echoing them, and Remove clears them all.
func TestSourceKeyFieldsCardSavesAllOrNothing(t *testing.T) {
	e := newFieldFixtureEnv(t)
	ctx := context.Background()
	page := e.get("/settings/plugins").Body.String()
	block := sourceKeyBlock(t, page, "tvdata")
	mustContain(t, block, "Fake TV", "Client ID", "Client Secret",
		`action="/settings/plugins/tvdata/key"`,
		`name="field.client_id"`, `name="field.client_secret"`,
		`autocomplete="new-password"`)
	mustNotContain(t, block, "A key is configured.", `name="remove"`)
	f := sourceKeyForm(t, block, "tvdata")
	mustContain(t, f, `type="text" name="field.client_id"`,
		`type="password" name="field.client_secret"`)
	mustNotContain(t, f, "value=") // no input carries a value

	// only one field: 400, no echo, nothing stored
	rec := e.do("POST", "/settings/plugins/tvdata/key", url.Values{"field.client_id": {"id-only"}}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("partial: got %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Please enter Client Secret.", `role="alert"`,
		`action="/settings/plugins/tvdata/key"`, "<h1")
	mustNotContain(t, rec.Body.String(), "id-only", "A key is configured.")
	for _, field := range []string{"client_id", "client_secret"} {
		if got, err := e.set.SourceKeyField(ctx, "tvdata", field); err != nil || got != "" {
			t.Fatalf("partial form stored %s=%q %v", field, got, err)
		}
	}

	// a field with a space is refused with the field's name
	rec = e.do("POST", "/settings/plugins/tvdata/key", url.Values{
		"field.client_id": {"the-id"}, "field.client_secret": {"two words"}}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad field: got %d %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "Client Secret must be printable ASCII characters without spaces.")
	mustNotContain(t, rec.Body.String(), "two words", "the-id")
	for _, field := range []string{"client_id", "client_secret"} {
		if got, err := e.set.SourceKeyField(ctx, "tvdata", field); err != nil || got != "" {
			t.Fatalf("bad field stored %s=%q %v", field, got, err)
		}
	}

	// both fields save together
	rec = e.do("POST", "/settings/plugins/tvdata/key", url.Values{
		"field.client_id": {"the-id"}, "field.client_secret": {"the-secret"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins#source-keys" {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if got, err := e.set.SourceKeyField(ctx, "tvdata", "client_id"); err != nil || got != "the-id" {
		t.Fatalf("stored client id %q %v", got, err)
	}
	if got, err := e.set.SourceKeyField(ctx, "tvdata", "client_secret"); err != nil || got != "the-secret" {
		t.Fatalf("stored client secret %q %v", got, err)
	}

	page = e.get("/settings/plugins").Body.String()
	mustNotContain(t, page, "the-id", "the-secret")
	block = sourceKeyBlock(t, page, "tvdata")
	mustContain(t, block, "A key is configured.", `name="remove"`, `value="1"`)
	f = sourceKeyForm(t, block, "tvdata")
	mustNotContain(t, f, "the-id", "the-secret")

	// remove clears every field
	rec = e.do("POST", "/settings/plugins/tvdata/key", url.Values{"remove": {"1"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins#source-keys" {
		t.Fatalf("remove: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, field := range []string{"client_id", "client_secret"} {
		if got, err := e.set.SourceKeyField(ctx, "tvdata", field); err != nil || got != "" {
			t.Fatalf("remove left %s=%q %v", field, got, err)
		}
	}
	block = sourceKeyBlock(t, e.get("/settings/plugins").Body.String(), "tvdata")
	mustNotContain(t, block, "A key is configured.", `name="remove"`)
}

// TestSourceKeyFieldsEnvironmentAndSingleCompatibility: the environment can
// supply both fields (configured, but not stored, so no Remove), one stored
// field alone offers Remove, and a single-key source keeps its one-key card
// and handler.
func TestSourceKeyFieldsEnvironmentAndSingleCompatibility(t *testing.T) {
	e := newFieldFixtureEnv(t)
	e.withSourceKeyField(func(_ context.Context, id, field string) (string, error) {
		if id == "tvdata" {
			return "from-env-" + field, nil
		}
		return "", nil
	})
	block := sourceKeyBlock(t, e.get("/settings/plugins").Body.String(), "tvdata")
	mustContain(t, block, "A key is configured.")
	mustNotContain(t, block, `name="remove"`, "from-env")
	if got, err := e.set.SourceKeyField(context.Background(), "tvdata", "client_id"); err != nil || got != "" {
		t.Fatalf("env field stored %q %v", got, err)
	}

	// one stored field is enough to offer Remove; the values are never echoed
	if err := e.set.SetSourceKeyField(context.Background(), "tvdata", "client_id", "stored-id"); err != nil {
		t.Fatal(err)
	}
	block = sourceKeyBlock(t, e.get("/settings/plugins").Body.String(), "tvdata")
	mustContain(t, block, `name="remove"`)
	mustNotContain(t, block, "stored-id")

	// the single-key fixture keeps the one-key form and handler
	single := newFixtureEnv(t)
	block = sourceKeyBlock(t, single.get("/settings/plugins").Body.String(), "tvdata")
	mustContain(t, block, `name="key"`, `type="password"`)
	mustNotContain(t, block, `name="field.`)
	rec := single.do("POST", "/settings/plugins/tvdata/key", url.Values{"key": {"secret"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("single save: %d %s", rec.Code, rec.Body.String())
	}
	if got, err := single.set.SourceKey(context.Background(), "tvdata"); err != nil || got != "secret" {
		t.Fatalf("single stored %q %v", got, err)
	}
}

// TestSourceKeyFieldsLabelsAndNotice: a source switch marks the source until
// every field is configured, and the plugin settings notice links to the card
// while a field is missing, both going away once all fields are set.
func TestSourceKeyFieldsLabelsAndNotice(t *testing.T) {
	e := newFieldFixtureEnv(t, func(_, tv *plugintest.Plugin) {
		tv.M.Sources = append(tv.M.Sources, plugin.SourceInfo{ID: "tvfree", Name: "Fake TV Free", RequestsPerMinute: 60})
	})
	ctx := context.Background()
	if err := e.set.SetSourceKeyField(ctx, "tvdata", "client_id", "only-id"); err != nil {
		t.Fatal(err)
	}
	b := e.get("/tv/search?q=x").Body.String()
	mustContain(t, b, "Fake TV (needs key)", `value="tvdata"`, "Fake TV Free")
	mustNotContain(t, b, "Fake TV Free (needs key)")

	b = e.get("/settings/plugins/tv").Body.String()
	mustContain(t, b, `href="/settings/plugins#source-keys"`, "Fake TV", "work without a key")

	if err := e.set.SetSourceKeyField(ctx, "tvdata", "client_secret", "secret"); err != nil {
		t.Fatal(err)
	}
	b = e.get("/tv/search?q=x").Body.String()
	mustContain(t, b, "Fake TV", "Fake TV Free")
	mustNotContain(t, b, "(needs key)")

	b = e.get("/settings/plugins/tv").Body.String()
	mustNotContain(t, b, `href="/settings/plugins#source-keys"`, "needs key")
}

// TestSourceKeyFieldsMessage: a multi-field source's missing credential names
// the group label with the plural wording (there is no single key to replace).
func TestSourceKeyFieldsMessage(t *testing.T) {
	e := newFieldFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec := e.get("/tv/tvdata/1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(),
		"Fake TV needs valid API key. Add or replace them in Settings &gt; Plugins.")
	mustNotContain(t, rec.Body.String(), "needs a valid")
}
