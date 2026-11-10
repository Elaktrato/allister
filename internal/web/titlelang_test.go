package web

import (
	"context"
	"net/url"
	"strconv"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// titles builds titles from lang, text pairs.
func titles(pairs ...string) []plugin.Title {
	var out []plugin.Title
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, plugin.Title{Lang: pairs[i], Text: pairs[i+1]})
	}
	return out
}

// TestTitleLanguageIsPerPluginEverywhere: the tv fixture declares the
// fallback order ["original"] (data, not core code) and its title language
// is chosen on its own settings page. Every place that shows a tv title
// (list, +1 row, overview, source search, recommendations, person page,
// match dialog) follows the tv choice and the tv fallback; the anime and
// book plugins keep their own (default) language, and the book plugin,
// which declares no fallback, shows the item's first stored title. The
// list's text search still matches every language.
func TestTitleLanguageIsPerPluginEverywhere(t *testing.T) {
	e := newFixtureEnv(t, func(_, tv *plugintest.Plugin) { tv.M.Kinds[0].TitleFallback = []string{"original"} })
	if rec := e.do("POST", "/settings/plugins/tv/title-language", url.Values{"title_lang": {"en"}}, false); rec.Code != 303 {
		t.Fatalf("set tv language: %d", rec.Code)
	}
	active := library.Entry{Status: library.Active, Progress: map[string]float64{"episode": 1}}
	one := tvShow("1", "")
	one.Titles = titles("de", "Deutsch Eins", "original", "Original Eins") // no en: falls back to original
	oneID := e.addFixture(t, "tv", "show", one, active)
	two := tvShow("2", "")
	two.Titles = titles("original", "Original Zwei", "en", "English Zwei")
	e.addFixture(t, "tv", "show", two, active)

	list := e.listGet(t, "/tv")
	mustContain(t, list, "Original Eins", "English Zwei")
	mustNotContain(t, list, "Deutsch Eins", "Original Zwei")
	// text search matches any language, the row shows the chosen one
	found := e.listGet(t, "/tv?q=deutsch")
	mustContain(t, found, "Original Eins")
	mustNotContain(t, found, "English Zwei")
	// the +1 row
	inc := e.do("POST", "/list/"+strconv.FormatInt(oneID, 10)+"/increment", url.Values{"return": {"/tv"}}, true)
	mustContain(t, inc.Body.String(), "Original Eins")
	// overview (saved) and its titles block
	mustContain(t, e.get("/tv/tvdata/1").Body.String(), "Original Eins</h1>")
	// source search
	e.tv.Summaries = []plugin.ItemSummary{{Source: "tvdata", ID: "9", Kind: "show", Titles: titles("de", "Gesucht", "original", "Searched Original")}}
	s := e.get("/tv/search?q=x").Body.String()
	mustContain(t, s, "Searched Original")
	mustNotContain(t, s, "Gesucht")
	// recommendations
	e.tv.Recs = []plugin.ItemSummary{{Source: "tvdata", ID: "8", Kind: "show", Titles: titles("de", "Empfohlen", "original", "Recommended Original")}}
	r := e.htmxGet("/tv/tvdata/2/recommendations").Body.String()
	mustContain(t, r, "Recommended Original")
	mustNotContain(t, r, "Empfohlen")
	// person page
	if err := e.lib.SaveCredits(context.Background(), oneID, []plugin.Credit{{Role: "actor", Person: plugin.PersonRef{ID: "p1", Name: "Pat Player"}}}); err != nil {
		t.Fatal(err)
	}
	ps, err := e.lib.ItemPeople(context.Background(), oneID)
	if err != nil || len(ps) != 1 {
		t.Fatalf("people %+v %v", ps, err)
	}
	p := e.get("/people/" + idStr(ps[0].ID)).Body.String()
	mustContain(t, p, "Original Eins")
	mustNotContain(t, p, "Deutsch Eins")
	// match dialog of a manual item
	man := plugin.ItemData{ItemSummary: plugin.ItemSummary{Source: "manual", ID: "m1", Kind: "show",
		Titles: titles("de", "Handarbeit", "original", "Manual Original"), Fields: map[string]plugin.FieldValue{"type": {Text: "series"}}}}
	e.addFixture(t, "tv", "show", man, library.Entry{Status: library.Planned})
	md := e.do("GET", "/tv/manual/m1/match", nil, true)
	if md.Code != 200 {
		t.Fatalf("match dialog: %d", md.Code)
	}
	mustContain(t, md.Body.String(), "Manual Original")
	mustNotContain(t, md.Body.String(), "Handarbeit")

	// switching tv to original changes tv only
	if rec := e.do("POST", "/settings/plugins/tv/title-language", url.Values{"title_lang": {"original"}}, false); rec.Code != 303 {
		t.Fatalf("set tv original: %d", rec.Code)
	}
	mustContain(t, e.listGet(t, "/tv"), "Original Zwei")

	// the book plugin declares no fallback: the first stored title
	book := bookItem("1", "")
	book.Titles = titles("de", "Buch Deutsch", "fr", "Livre")
	e.addFixture(t, "book", "book", book, library.Entry{Status: library.Active})
	bl := e.listGet(t, "/book")
	mustContain(t, bl, "Buch Deutsch")
	mustNotContain(t, bl, "Livre")
	if got := e.set.TitleLang(context.Background(), "book"); got != "" {
		t.Fatalf("book language changed: %q", got)
	}
}
