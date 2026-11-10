package web

import (
	"context"
	"database/sql"
	"github.com/allister/allister/internal/library"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/settings"
)

// newTestServer builds a server on a temp DB and returns it with its settings store and DB.
func newTestServer(t *testing.T) (http.Handler, *settings.Store, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := settings.New(d)
	reg := animeRegistry(t, st, nil)
	return NewServer(Deps{Settings: st, Registry: reg, Library: library.New(d, reg)}), st, d
}

func postForm(h http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The title language is a per-plugin setting, edited on the plugin's own
// settings page (/settings/plugins/{id}); the General section no longer has
// it.

func TestPostTitleLanguagePersists(t *testing.T) {
	h, st, _ := newTestServer(t)
	rec := postForm(h, "/settings/plugins/anime/title-language", url.Values{"title_lang": {"en"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/plugins/anime" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := st.TitleLang(context.Background(), "anime"); got != "en" {
		t.Fatalf("got %q", got)
	}
	if _, ok, _ := st.Get(context.Background(), "title_lang"); ok {
		t.Fatal("global key written")
	}
}

func TestPostTitleLanguageInvalid(t *testing.T) {
	h, st, _ := newTestServer(t)
	for _, bad := range []string{"xx", "", "EN", "en\n"} {
		rec := postForm(h, "/settings/plugins/anime/title-language", url.Values{"title_lang": {bad}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: got %d", bad, rec.Code)
		}
	}
	if got := st.TitleLang(context.Background(), "anime"); got != "" {
		t.Fatalf("invalid value persisted: %q", got)
	}
	// unknown plugins answer 404 and store nothing
	if rec := postForm(h, "/settings/plugins/nope/title-language", url.Values{"title_lang": {"en"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin: %d", rec.Code)
	}
	if got := st.TitleLang(context.Background(), "nope"); got != "" {
		t.Fatalf("unknown plugin stored %q", got)
	}
}

// TestPostTitleLanguageStoredLanguage: a language found in the plugin's own
// stored titles is offered and accepted; one found only in another plugin's
// titles is refused.
func TestPostTitleLanguageStoredLanguage(t *testing.T) {
	h, st, d := newTestServer(t)
	d.Exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'de','T')`)
	d.Exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(2,'book','book','x','2')`)
	d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(2,'es','T')`)
	if rec := postForm(h, "/settings/plugins/anime/title-language", url.Values{"title_lang": {"de"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
	if got := st.TitleLang(context.Background(), "anime"); got != "de" {
		t.Fatalf("got %q", got)
	}
	if rec := postForm(h, "/settings/plugins/anime/title-language", url.Values{"title_lang": {"es"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("other plugin's language: got %d", rec.Code)
	}
}

func TestPluginSettingsShowTitleLanguage(t *testing.T) {
	h, st, d := newTestServer(t)
	d.Exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'fr','T'),(1,'pt-br','U')`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/plugins/anime", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="title_lang"`) {
		t.Fatalf("got %d %q", rec.Code, body)
	}
	// nothing stored: the plugin's first title language
	mustContain(t, body, `action="/settings/plugins/anime/title-language"`, `value="ja" selected`, "<h1", "Anime")
	st.SetTitleLang(context.Background(), "anime", "ja-latn")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/plugins/anime", nil))
	body = rec.Body.String()
	if !strings.Contains(body, `value="ja-latn" selected`) || strings.Contains(body, `value="ja" selected`) {
		t.Fatalf("selection wrong: %s", body)
	}
	for _, want := range []string{`value="en"`, `>English<`, `>Romaji<`, `value="fr"`, `>Français<`, `value="pt-br"`, `>pt-br<`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	// the Plugins tab is the current one, and the plugins page links here
	mustContain(t, body, `href="/settings/plugins" aria-current="true"`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/plugins", nil))
	mustContain(t, rec.Body.String(), `href="/settings/plugins/anime"`)
	// unknown plugin
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/plugins/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin: %d", rec.Code)
	}
}

// TestGeneralSettingsHasNoTitleLanguage: the global control is gone; General
// points at the per-plugin settings, and the old POST stores nothing.
func TestGeneralSettingsHasNoTitleLanguage(t *testing.T) {
	h, st, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	mustNotContain(t, rec.Body.String(), `name="title_lang"`, `method="post" action="/settings"`)
	mustContain(t, rec.Body.String(), `href="/settings/plugins/anime"`)
	if rec := postForm(h, "/settings", url.Values{"title_lang": {"en"}}); rec.Code == http.StatusSeeOther {
		t.Fatalf("old POST still answers %d", rec.Code)
	}
	if _, ok, _ := st.Get(context.Background(), "title_lang"); ok {
		t.Fatal("old POST stored the global key")
	}
}

// TestPluginSettingsAreIsolated: each plugin offers only its own
// languages; choosing one for a plugin leaves every other plugin alone.
func TestPluginSettingsAreIsolated(t *testing.T) {
	e := newFixtureEnv(t)
	b := e.get("/settings/plugins/tv").Body.String()
	mustContain(t, b, `value="en" selected`, `value="original"`)
	mustNotContain(t, b, `value="ja"`, `value="ja-latn"`)
	if rec := e.do("POST", "/settings/plugins/tv/title-language", url.Values{"title_lang": {"ja"}}, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("anime language for tv: %d", rec.Code)
	}
	if rec := e.do("POST", "/settings/plugins/tv/title-language", url.Values{"title_lang": {"original"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("tv original: %d", rec.Code)
	}
	ctx := context.Background()
	if e.set.TitleLang(ctx, "tv") != "original" || e.set.TitleLang(ctx, "anime") != "" || e.set.TitleLang(ctx, "book") != "" {
		t.Fatalf("tv=%q anime=%q book=%q", e.set.TitleLang(ctx, "tv"), e.set.TitleLang(ctx, "anime"), e.set.TitleLang(ctx, "book"))
	}
	mustContain(t, e.get("/settings/plugins/anime").Body.String(), `value="ja" selected`)
}

// TestStoredTitleLanguageIsShownSelected: a stored language the plugin no
// longer offers (an old value, a removed title) is still what titles use, so
// the select shows it, selected, instead of silently showing another.
func TestStoredTitleLanguageIsShownSelected(t *testing.T) {
	h, st, _ := newTestServer(t)
	st.SetTitleLang(context.Background(), "anime", "pt-br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/plugins/anime", nil))
	b := rec.Body.String()
	mustContain(t, b, `<option value="pt-br" selected>pt-br</option>`, `value="ja"`)
	// exactly one selected option in the language select (the page's other
	// selects, the home settings, have their own)
	i := strings.Index(b, `id="title_lang"`)
	if i < 0 {
		t.Fatal("no title language form")
	}
	if n := strings.Count(b[i:strings.Index(b[i:], "</form>")+i], " selected>"); n != 1 {
		t.Fatalf("%d selected options in the language form", n)
	}
	// saving the page unchanged keeps it
	if rec := postForm(h, "/settings/plugins/anime/title-language", url.Values{"title_lang": {"pt-br"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resave: %d", rec.Code)
	}
}
