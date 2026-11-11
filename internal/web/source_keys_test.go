package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// withSourceKey serves e through a Deps.SourceKey resolver (what production
// wires to answer with the stored key or one from the environment).
func (e *env) withSourceKey(fn func(context.Context, string) (string, error)) {
	deps := e.srv.deps
	deps.SourceKey = fn
	e.srv, e.h = newServer(deps, func() time.Time { return *e.now })
}

// sourceKeyBlock is the card block of source id on the plugins page.
func sourceKeyBlock(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `data-source-key="`+id+`"`)
	if i < 0 {
		t.Fatalf("no key card block for %s in %s", id, body)
	}
	start := strings.LastIndex(body[:i], "<li")
	if start < 0 {
		t.Fatalf("no <li> before the key card block for %s", id)
	}
	end := strings.Index(body[start:], "</li>")
	if end < 0 {
		t.Fatalf("unterminated key card block for %s", id)
	}
	return body[start : start+end]
}

// sourceKeyForm is the key form of source id within a card block.
func sourceKeyForm(t *testing.T, block, id string) string {
	t.Helper()
	start := strings.Index(block, `<form method="post" action="/settings/plugins/keys/`+id+`"`)
	if start < 0 {
		t.Fatalf("no key form for %s in %s", id, block)
	}
	end := strings.Index(block[start:], "</form>")
	if end < 0 {
		t.Fatalf("unterminated key form for %s", id)
	}
	return block[start : start+end]
}

// TestSourceKeysCardSavesAndClearsWithoutEchoing: the plugins page offers the
// API key of every source that declares one, saves it, then shows only that a
// key is configured (the key itself is never rendered) and can remove it.
func TestSourceKeysCardSavesAndClearsWithoutEchoing(t *testing.T) {
	e := newFixtureEnv(t)
	ctx := context.Background()
	page := e.get("/settings/plugins").Body.String()
	block := sourceKeyBlock(t, page, "tvdata")
	mustContain(t, block, "Fake TV", "API key", "Get one at example.test.",
		`action="/settings/plugins/keys/tvdata"`, `name="key"`, `type="password"`,
		`autocomplete="new-password"`, `spellcheck="false"`)
	mustNotContain(t, block, "A key is configured.", `name="remove"`)
	f := sourceKeyForm(t, block, "tvdata")
	mustNotContain(t, f, "value=") // a key input never carries a value

	rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {"secret"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins#source-keys" {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if got, err := e.set.SourceKey(ctx, "tvdata"); err != nil || got != "secret" {
		t.Fatalf("stored %q %v", got, err)
	}

	page = e.get("/settings/plugins").Body.String()
	mustNotContain(t, page, "secret")
	block = sourceKeyBlock(t, page, "tvdata")
	mustContain(t, block, "A key is configured.", `name="remove"`, `value="1"`)

	if rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"remove": {"1"}}, false); rec.Code != http.StatusSeeOther ||
		rec.Header().Get("Location") != "/settings/plugins#source-keys" {
		t.Fatalf("remove: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, err := e.set.SourceKey(ctx, "tvdata"); err != nil || got != "" {
		t.Fatalf("stored after remove %q %v", got, err)
	}
	mustNotContain(t, e.get("/settings/plugins").Body.String(), `name="remove"`)
}

// TestSourceKeyValidation: only 1-500 printable ASCII characters are keys;
// a rejected form re-renders the page with status 400 and the message, stores
// nothing and never echoes what was sent. Unknown and unkeyed sources 404.
func TestSourceKeyValidation(t *testing.T) {
	e := newFixtureEnv(t)
	ctx := context.Background()
	for _, c := range []struct{ name, key, want string }{
		{"empty", "", "Please enter the key."},
		{"space", "a b", "printable ASCII"},
		{"control", "a\nb", "printable ASCII"},
		{"non-ascii", "schéma", "printable ASCII"},
		{"long", strings.Repeat("k", 501), "500 characters"},
	} {
		rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {c.key}}, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d", c.name, rec.Code)
		}
		body := rec.Body.String()
		mustContain(t, body, c.want, `role="alert"`, `action="/settings/plugins/keys/tvdata"`, "<h1")
		if c.key != "" {
			mustNotContain(t, body, c.key)
		}
	}
	if got, err := e.set.SourceKey(ctx, "tvdata"); err != nil || got != "" {
		t.Fatalf("invalid key stored %q %v", got, err)
	}
	// surrounding white space is trimmed from an otherwise valid key
	if rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {"  padded  "}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("padded: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := e.set.SourceKey(ctx, "tvdata"); got != "padded" {
		t.Fatalf("stored %q", got)
	}
	// exactly the maximum length is accepted
	longest := strings.Repeat("k", 500)
	if rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {longest}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("500 chars: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := e.set.SourceKey(ctx, "tvdata"); got != longest {
		t.Fatalf("500 chars stored %d characters", len(got))
	}
	// an unknown source, and one that declares no key label, answer 404
	for _, src := range []string{"nope", "books"} {
		if rec := e.do("POST", "/settings/plugins/keys/"+src, url.Values{"key": {"x"}}, false); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", src, rec.Code)
		}
	}
	if got, _ := e.set.SourceKey(ctx, "books"); got != "" {
		t.Fatalf("unkeyed source stored %q", got)
	}
}

// TestSourceKeysCardHiddenWithoutKeyedSources: no installed source declares a
// key label (the real anime plugin), so the card is absent.
func TestSourceKeysCardHiddenWithoutKeyedSources(t *testing.T) {
	e := newEnv(t)
	b := e.get("/settings/plugins").Body.String()
	mustContain(t, b, "<h1", "Plugins")
	mustNotContain(t, b, `id="source-keys"`, `name="key"`, "/settings/plugins/keys/")
}

// TestSourceKeyFromEnvironmentShowsConfiguredWithoutRemove: a key the
// environment supplies counts as configured but is not stored, so it cannot
// be removed from the page; a stored key adds Remove.
func TestSourceKeyFromEnvironmentShowsConfiguredWithoutRemove(t *testing.T) {
	e := newFixtureEnv(t)
	e.withSourceKey(func(ctx context.Context, id string) (string, error) {
		if id == "tvdata" {
			return "from-env", nil
		}
		return "", nil
	})
	block := sourceKeyBlock(t, e.get("/settings/plugins").Body.String(), "tvdata")
	mustContain(t, block, "A key is configured.", `name="key"`)
	mustNotContain(t, block, `name="remove"`, "from-env")
	if got, err := e.set.SourceKey(context.Background(), "tvdata"); err != nil || got != "" {
		t.Fatalf("env key stored %q %v", got, err)
	}
	if rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {"stored"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	block = sourceKeyBlock(t, e.get("/settings/plugins").Body.String(), "tvdata")
	mustContain(t, block, "A key is configured.", `name="remove"`)
}

// TestSourceKeysCardDeduplicatesSharedSource: two plugins declaring source id
// tvdata (TV and Movies) share one key and one form.
func TestSourceKeysCardDeduplicatesSharedSource(t *testing.T) {
	book, tv := plugintest.BookPlugin(), plugintest.TVPlugin()
	movies := plugintest.TVPlugin(plugintest.WithID("movies"))
	e := newEnvWith(t, withPlugins(book, tv, movies))
	b := e.get("/settings/plugins").Body.String()
	if n := strings.Count(b, `action="/settings/plugins/keys/tvdata"`); n != 1 {
		t.Fatalf("%d key forms for tvdata: %s", n, b)
	}
	if n := strings.Count(b, `data-source-key="tvdata"`); n != 1 {
		t.Fatalf("%d key blocks for tvdata", n)
	}
	sourceKeyBlock(t, b, "tvdata")
}

// TestPluginSettingsNoticeForMissingKey: a plugin whose source declares a key
// label links to the card while no key is configured; it also says the rest
// still works when another source needs no key, and the notice goes away once
// the key is saved.
func TestPluginSettingsNoticeForMissingKey(t *testing.T) {
	e := newFixtureEnv(t, func(book, tv *plugintest.Plugin) {
		tv.M.Sources = append(tv.M.Sources, plugin.SourceInfo{ID: "tvfree", Name: "Fake TV Free", RequestsPerMinute: 60})
	})
	b := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, b, `href="/settings/plugins#source-keys"`, "Fake TV", "work without a key")
	// the book plugin has only a source without a key label: no notice
	mustNotContain(t, e.get("/settings/plugins/book").Body.String(), "/settings/plugins#source-keys")

	if rec := e.do("POST", "/settings/plugins/keys/tvdata", url.Values{"key": {"secret"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	b = e.get("/settings/plugins/tv").Body.String()
	mustNotContain(t, b, `href="/settings/plugins#source-keys"`, "secret")
}
