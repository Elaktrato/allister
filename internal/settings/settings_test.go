package settings

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/allister/allister/internal/db"
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
	exec(`INSERT INTO anime(id, source, source_id) VALUES(1,'x','1')`)
	exec(`INSERT INTO anime(id, source, source_id) VALUES(2,'x','2')`)
	exec(`INSERT INTO anime_titles(anime_id, lang, text) VALUES(1,'fr','a'),(1,'en','b'),(2,'fr','c'),(2,'de','d')`)
	got, err := s.KnownLanguages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"de", "en", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
