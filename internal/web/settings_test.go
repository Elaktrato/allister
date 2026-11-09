package web

import (
	"context"
	"database/sql"
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
	return NewServer(Deps{Settings: st}), st, d
}

func postForm(h http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPostSettingsPersists(t *testing.T) {
	h, st, _ := newTestServer(t)
	rec := postForm(h, "/settings", url.Values{"title_lang": {"en"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := st.TitleLang(context.Background()); got != "en" {
		t.Fatalf("got %q", got)
	}
}

func TestPostSettingsInvalid(t *testing.T) {
	h, st, _ := newTestServer(t)
	rec := postForm(h, "/settings", url.Values{"title_lang": {"xx"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
	if _, ok, _ := st.Get(context.Background(), settings.KeyTitleLang); ok {
		t.Fatal("invalid value persisted")
	}
}

func TestPostSettingsStoredLanguage(t *testing.T) {
	h, st, d := newTestServer(t)
	d.Exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'de','T')`)
	if rec := postForm(h, "/settings", url.Values{"title_lang": {"de"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
	if got := st.TitleLang(context.Background()); got != "de" {
		t.Fatalf("got %q", got)
	}
}

func TestGetSettingsShowsSelected(t *testing.T) {
	h, st, d := newTestServer(t)
	d.Exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	d.Exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'fr','T'),(1,'pt-br','U')`)
	st.Set(context.Background(), settings.KeyTitleLang, "ja-latn")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="title_lang"`) {
		t.Fatalf("got %d %q", rec.Code, body)
	}
	if !strings.Contains(body, `value="ja-latn" selected`) || strings.Contains(body, `value="ja" selected`) {
		t.Fatalf("selection wrong: %s", body)
	}
	for _, want := range []string{`value="en"`, `value="fr"`, `>Français<`, `value="pt-br"`, `>pt-br<`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}
