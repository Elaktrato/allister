package settings

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/plugin"
)

func newStore(t *testing.T) (*Store, func(string, ...any)) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d), func(q string, a ...any) {
		if _, err := d.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetSetRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, ok, err := s.Get(ctx, "k"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := s.Set(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Get(ctx, "k"); v != "v1" || !ok || err != nil {
		t.Fatalf("v=%q ok=%v err=%v", v, ok, err)
	}
	if err := s.Set(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Get(ctx, "k"); v != "v2" {
		t.Fatalf("overwrite: %q", v)
	}
}

// TestTitleLangPerPlugin: the title language is stored per plugin
// (plugin.<id>.title_lang); nothing stored is "" (the web layer picks the
// plugin's default), and plugins never see each other's choice.
func TestTitleLangPerPlugin(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if got := s.TitleLang(ctx, "anime"); got != "" {
		t.Fatalf("unset: got %q", got)
	}
	if err := s.SetTitleLang(ctx, "anime", "en"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get(ctx, "plugin.anime.title_lang"); !ok || v != "en" {
		t.Fatalf("stored %q %v", v, ok)
	}
	if got := s.TitleLang(ctx, "anime"); got != "en" {
		t.Fatalf("got %q", got)
	}
	if got := s.TitleLang(ctx, "book"); got != "" {
		t.Fatalf("book sees anime's language: %q", got)
	}
	if _, ok, _ := s.Get(ctx, "title_lang"); ok {
		t.Fatal("the global key must not be written")
	}
}

func TestKnownLanguagesOfOnePlugin(t *testing.T) {
	s, exec := newStore(t)
	exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(2,'anime','anime','x','2')`)
	exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(3,'book','book','x','3')`)
	exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'fr','a'),(1,'en','b'),(2,'fr','c'),(2,'de','d'),(3,'es','e')`)
	got, err := s.KnownLanguages(context.Background(), "anime")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"de", "en", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got, _ := s.KnownLanguages(context.Background(), "book"); !reflect.DeepEqual(got, []string{"es"}) {
		t.Fatalf("book: %v", got)
	}
}

func TestEnabledStoreDefaultsAndRoundTrip(t *testing.T) {
	s, exec := newStore(t)
	ctx := context.Background()
	for _, def := range []bool{true, false} {
		if got, err := s.IsEnabled(ctx, "anime", def); err != nil || got != def {
			t.Fatalf("missing key def=%v: got %v, %v", def, got, err)
		}
	}
	if err := s.SetEnabled(ctx, "anime", false); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get(ctx, "plugin.anime.enabled"); !ok || v != "0" {
		t.Fatalf("stored %q %v", v, ok)
	}
	if got, _ := s.IsEnabled(ctx, "anime", true); got {
		t.Fatal("stored 0 must beat default true")
	}
	if err := s.SetEnabled(ctx, "anime", true); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Get(ctx, "plugin.anime.enabled"); v != "1" {
		t.Fatalf("stored %q", v)
	}
	if got, _ := s.IsEnabled(ctx, "anime", false); !got {
		t.Fatal("stored 1 must beat default false")
	}
	// other plugins are independent; garbage falls back to the default
	if got, _ := s.IsEnabled(ctx, "book", false); got {
		t.Fatal("book leaked from anime")
	}
	exec(`INSERT INTO settings(key, value) VALUES('plugin.book.enabled','maybe')`)
	if got, err := s.IsEnabled(ctx, "book", true); err != nil || !got {
		t.Fatalf("garbage value: %v %v", got, err)
	}
}

func TestEnabledStoreReportsDBErrors(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(d)
	d.Close()
	if _, err := s.IsEnabled(context.Background(), "anime", true); err == nil {
		t.Fatal("want error")
	}
	if err := s.SetEnabled(context.Background(), "anime", true); err == nil {
		t.Fatal("want error")
	}
}

var _ plugin.EnabledStore = (*Store)(nil)

// TestColumnsRoundTrip: the visible columns of a plugin's kind are stored as
// a JSON list under plugin.<id>.columns.<kind>; nothing stored (or an
// unreadable value) means the kind's defaults; Reset removes the choice;
// plugins and kinds never share it.
func TestColumnsRoundTrip(t *testing.T) {
	s, exec := newStore(t)
	ctx := context.Background()
	if got, ok := s.Columns(ctx, "anime", "anime"); ok || got != nil {
		t.Fatalf("unset: %v %v", got, ok)
	}
	if err := s.SetColumns(ctx, "anime", "anime", []string{"studio", "rating"}); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get(ctx, "plugin.anime.columns.anime"); !ok || v != `["studio","rating"]` {
		t.Fatalf("stored %q %v", v, ok)
	}
	if got, ok := s.Columns(ctx, "anime", "anime"); !ok || !reflect.DeepEqual(got, []string{"studio", "rating"}) {
		t.Fatalf("got %v %v", got, ok)
	}
	for _, other := range [][2]string{{"book", "book"}, {"anime", "other"}} {
		if got, ok := s.Columns(ctx, other[0], other[1]); ok {
			t.Fatalf("%v sees anime's choice: %v", other, got)
		}
	}
	// an empty choice (title only) is a choice
	if err := s.SetColumns(ctx, "book", "book", []string{}); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Columns(ctx, "book", "book"); !ok || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, ok)
	}
	if err := s.ResetColumns(ctx, "anime", "anime"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Columns(ctx, "anime", "anime"); ok {
		t.Fatal("reset kept the choice")
	}
	if _, ok := s.Columns(ctx, "book", "book"); !ok {
		t.Fatal("reset of anime removed book's choice")
	}
	// garbage is ignored (the defaults apply)
	for _, bad := range []string{`not json`, `{"a":1}`, `[1,2]`, `null`} {
		exec(`INSERT INTO settings(key, value) VALUES('plugin.tv.columns.show', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, bad)
		if got, ok := s.Columns(ctx, "tv", "show"); ok {
			t.Errorf("%s: %v %v", bad, got, ok)
		}
	}
}
