package web

// Choosing a source when the request names none: the first configured source
// serving the kind wins (preferring one that supports lookup); only when none
// is configured does the manifest's first source stay the default. The search
// and the match dialog, its search and the match POST all follow it, so a user
// with only the second source's credentials lands on that source.

import (
	"context"
	"net/url"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// keyedDefaultSources gives the tv fixture, before its single-key "tvdata", an
// IGDB-like first source "tvmeta" that needs two named credentials and serves
// the show kind with lookup support.
func keyedDefaultSources(meta *plugintest.FakeSource) func(_, tv *plugintest.Plugin) {
	return func(_, tv *plugintest.Plugin) {
		tv.M.Sources = append([]plugin.SourceInfo{{
			ID: "tvmeta", Name: "TV Meta", RequestsPerMinute: 60, Kinds: []string{"show"},
			SupportsLookup: true, KeyLabel: "API key",
			KeyFields: []plugin.KeyField{
				{Name: "client_id", Label: "Client ID"},
				{Name: "client_secret", Label: "Client Secret", Secret: true},
			},
			URLPatterns: []string{"tvmeta.example/show/{id}"},
		}}, tv.M.Sources...)
		tv.Sources["tvmeta"] = meta
	}
}

func TestDefaultSourcePrefersAConfiguredOne(t *testing.T) {
	// fixture is the tv plugin with the multi-field "tvmeta" first and the
	// single-key "tvdata" second; each source answers its own search.
	fixture := func(t *testing.T) (*fixtureEnv, *plugintest.FakeSource) {
		t.Helper()
		meta := &plugintest.FakeSource{Summaries: []plugin.ItemSummary{{
			Source: "tvmeta", ID: "9", Kind: "show", Titles: ttl("Meta Show")}}}
		e := newFixtureEnv(t, keyedDefaultSources(meta))
		e.tv.Summaries = []plugin.ItemSummary{{
			Source: "tvdata", ID: "77", Kind: "show", Titles: ttl("TV Show")}}
		e.tv.Data = tvShow("77", "TV Show")
		e.saveFixture(t, "tv", "show", plugin.ItemData{ItemSummary: plugin.ItemSummary{
			Source: "manual", ID: "m1", Kind: "show", Titles: ttl("My Show")}}, library.WithSyncState("done"))
		return e, meta
	}

	t.Run("only the second configured", func(t *testing.T) {
		e, meta := fixture(t)
		if err := e.set.SetSourceKey(context.Background(), "tvdata", "steam-key"); err != nil {
			t.Fatal(err)
		}
		b := e.get("/tv/search?q=x").Body.String()
		mustContain(t, b, "Search Fake TV…", `value="tvdata" selected`, "TV Meta (needs key)",
			"TV Show", `href="/tv/tvdata/77"`)
		mustNotContain(t, b, "Meta Show", `value="tvmeta" selected`)
		if e.tv.Calls(plugintest.OpSearch) != 1 || meta.Calls(plugintest.OpSearch) != 0 {
			t.Fatalf("searches: tvdata %d tvmeta %d",
				e.tv.Calls(plugintest.OpSearch), meta.Calls(plugintest.OpSearch))
		}

		// the match dialog, its search and the match POST pick the same source
		b = e.do("GET", "/tv/manual/m1/match", nil, true).Body.String()
		mustContain(t, b, "Match to Fake TV", `name="source" value="tvdata"`,
			`hx-get="/tv/manual/m1/match/search?source=tvdata"`)
		b = e.do("GET", "/tv/manual/m1/match/search?q=x", nil, true).Body.String()
		mustContain(t, b, "TV Show", `name="pick" value="77"`)
		if meta.Calls(plugintest.OpSearch) != 0 {
			t.Fatalf("match search asked the unconfigured source")
		}
		rec := e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"77"}}, true)
		if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/tv/tvdata/77" {
			t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
		}
	})

	t.Run("both configured", func(t *testing.T) {
		e, _ := fixture(t)
		ctx := context.Background()
		if err := e.set.SetSourceKey(ctx, "tvdata", "steam-key"); err != nil {
			t.Fatal(err)
		}
		for field, value := range map[string]string{"client_id": "igdb-id", "client_secret": "igdb-secret"} {
			if err := e.set.SetSourceKeyField(ctx, "tvmeta", field, value); err != nil {
				t.Fatal(err)
			}
		}
		b := e.get("/tv/search?q=x").Body.String()
		mustContain(t, b, "Search TV Meta…", `value="tvmeta" selected`, "Meta Show", `href="/tv/tvmeta/9"`)
		mustNotContain(t, b, "TV Show", `href="/tv/tvdata/77"`, `value="tvdata" selected`, "(needs key)")
	})

	t.Run("neither configured", func(t *testing.T) {
		e, _ := fixture(t)
		b := e.get("/tv/search?q=x").Body.String()
		mustContain(t, b, "Search TV Meta…", `value="tvmeta" selected`, "TV Meta (needs key)", "Meta Show")
		mustNotContain(t, b, `value="tvdata" selected`, `href="/tv/tvdata/77"`)
	})
}
