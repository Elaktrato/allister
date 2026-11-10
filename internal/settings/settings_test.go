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

func TestTitleLangDefault(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if got := s.TitleLang(ctx); got != "ja" {
		t.Fatalf("got %q", got)
	}
	if err := s.Set(ctx, KeyTitleLang, "en"); err != nil {
		t.Fatal(err)
	}
	if got := s.TitleLang(ctx); got != "en" {
		t.Fatalf("got %q", got)
	}
}

func TestKnownLanguages(t *testing.T) {
	s, exec := newStore(t)
	exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(1,'anime','anime','x','1')`)
	exec(`INSERT INTO items(id, plugin_id, kind, source, source_id) VALUES(2,'anime','anime','x','2')`)
	exec(`INSERT INTO item_titles(item_id, lang, text) VALUES(1,'fr','a'),(1,'en','b'),(2,'fr','c'),(2,'de','d')`)
	got, err := s.KnownLanguages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"de", "en", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
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
