package web

// The web says what to do when a source cannot answer because its credential
// is missing or refused (plugin.ErrNotConfigured): search, the live overview,
// the add dialog and a match answer 503 with a message pointing at Settings,
// recommendations stay 200 with the same message, and a saved item keeps
// rendering from the database. Source switches mark such a source with
// " (needs key)" until a key is configured.

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// The fixture TV source declares KeyLabel "API key"; sourceMessage keeps the
// acronym and html/template escapes ">", so this is the rendered sentence.
const needsKeyMessage = "Fake TV needs a valid API key. Add or replace it in Settings &gt; Plugins."

// TestSearchNeedsConfiguration: a refused search is a 503 (not a 200 with the
// generic outage text) naming the source, the key label and where to fix it.
func TestSearchNeedsConfiguration(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpSearch: plugin.ErrNotConfigured}
	rec := e.get("/tv/search?q=dark")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), needsKeyMessage, "Try again")
	mustNotContain(t, rec.Body.String(), "Search is unavailable right now")

	// the htmx results partial answers 200 (htmx swaps no 5xx outside the
	// dialog slot) with the same message and no page chrome
	rec = e.do("GET", "/tv/search?q=dark", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("htmx: got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), needsKeyMessage, "Try again")
	mustNotContain(t, rec.Body.String(), "<html")
}

// TestLiveOverviewNeedsConfiguration: an unsaved item needs the source, so a
// missing credential is a 503 "Not configured" page; a source that declares no
// key label gets the neutral wording instead of a made-up label.
func TestLiveOverviewNeedsConfiguration(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec := e.get("/tv/tvdata/1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Not configured", needsKeyMessage, "Try again")

	book := newFixtureEnv(t)
	book.book.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec = book.get("/book/books/1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("book: got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Fake Books needs a valid credential. Add or replace it in Settings &gt; Plugins.")
}

// TestAddDialogNeedsConfiguration: the htmx add dialog comes back as a 503
// partial with the message and the whole form, so the user can retry after
// saving a key.
func TestAddDialogNeedsConfiguration(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec := e.do("GET", "/tv/tvdata/1/add", nil, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	b := rec.Body.String()
	mustContain(t, b, "data-dialog", needsKeyMessage, `action="/tv/tvdata/1/add"`,
		`name="status"`, `name="progress.episode"`, `name="comment"`)
	mustNotContain(t, b, "<html")
}

// TestMatchNeedsConfiguration: a match cannot fetch the source item without a
// credential; it answers 503 saying nothing was changed and leaves the manual
// item alone.
func TestMatchNeedsConfiguration(t *testing.T) {
	e := newFixtureEnv(t)
	id := tvManual(t, e.env)
	e.tv.Errs = map[string]error{plugintest.OpDetails: plugin.ErrNotConfigured}
	rec := e.do("POST", "/tv/manual/m1/match", url.Values{"pick": {"77"}}, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), needsKeyMessage, "Nothing was changed.")
	if e.tv.Calls(plugintest.OpDetails) != 1 {
		t.Fatalf("details calls %d", e.tv.Calls(plugintest.OpDetails))
	}
	if it, _ := e.snapshot(t, id); it.Source != "manual" {
		t.Fatalf("item converted despite the missing key: %+v", it)
	}
}

// TestRecommendationsNeedConfiguration: the late-loaded block stays a 200
// fragment, but the notice names the missing key instead of the generic
// "unavailable" text.
func TestRecommendationsNeedConfiguration(t *testing.T) {
	e := newFixtureEnv(t)
	e.tv.Errs = map[string]error{plugintest.OpRecommendations: plugin.ErrNotConfigured}
	part := e.htmxGet(recsURL)
	if part.Code != http.StatusOK {
		t.Fatalf("htmx: got %d", part.Code)
	}
	mustContain(t, part.Body.String(), needsKeyMessage, "Try again")
	mustNotContain(t, part.Body.String(), "Recommendations are unavailable right now")

	full := e.get(recsURL)
	if full.Code != http.StatusOK {
		t.Fatalf("full: got %d", full.Code)
	}
	mustContain(t, full.Body.String(), "<html", needsKeyMessage)
}

// TestSourceChoiceLabelsNeedsKey: a source switch marks a source whose key is
// missing, in search and in the match dialog, and only until a key is saved.
func TestSourceChoiceLabelsNeedsKey(t *testing.T) {
	other := &plugintest.FakeSource{}
	e := newFixtureEnv(t, func(book, _ *plugintest.Plugin) {
		book.M.Sources = append(book.M.Sources, plugin.SourceInfo{ID: "openlib", Name: "Open Library",
			RequestsPerMinute: 60, KeyLabel: "API key"})
		book.Sources["openlib"] = other
	})
	e.book.Summaries = []plugin.ItemSummary{bookSummary("42", "Some Book")}

	b := e.get("/book/search?q=x").Body.String()
	mustContain(t, b, "Open Library (needs key)", `value="openlib"`)
	mustNotContain(t, b, "Fake Books (needs key)") // the books source declares no key

	if err := e.set.SetSourceKey(context.Background(), "openlib", "secret"); err != nil {
		t.Fatal(err)
	}
	b = e.get("/book/search?q=x").Body.String()
	mustContain(t, b, "Open Library")
	mustNotContain(t, b, "(needs key)")

	// the match dialog lists the same labels
	if err := e.set.ClearSourceKey(context.Background(), "openlib"); err != nil {
		t.Fatal(err)
	}
	e.saveFixture(t, "book", "book", plugin.ItemData{ItemSummary: plugin.ItemSummary{
		Source: "manual", ID: "m1", Kind: "book", Titles: ttl("Manual Book")}})
	b = e.do("GET", "/book/manual/m1/match", nil, true).Body.String()
	mustContain(t, b, "Open Library (needs key)", "Fake Books")
	mustNotContain(t, b, "Fake Books (needs key)")
}

// TestSavedItemStillRendersWithoutKey: only live calls need the credential; a
// saved item is served from the database without asking the source.
func TestSavedItemStillRendersWithoutKey(t *testing.T) {
	e := newFixtureEnv(t)
	e.saveFixture(t, "book", "book", bookItem("saved1", "Saved Book"))
	rec := e.get("/book/books/saved1")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Saved Book</h1>")
	if n := e.book.Calls(plugintest.OpDetails) + e.book.Calls(plugintest.OpPeople); n != 0 {
		t.Fatalf("saved item asked the source %d times", n)
	}
}
