package settings

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestSourceKeyRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if got, err := s.SourceKey(ctx, "tmdb"); got != "" || err != nil {
		t.Fatalf("unset: %q %v", got, err)
	}
	if err := s.SetSourceKey(ctx, "tmdb", "  secret-key  "); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get(ctx, "source.tmdb.key"); !ok || v != "secret-key" {
		t.Fatalf("stored %q %v", v, ok)
	}
	if got, _ := s.SourceKey(ctx, "tmdb"); got != "secret-key" {
		t.Fatalf("got %q", got)
	}
	if got, _ := s.SourceKey(ctx, "other"); got != "" {
		t.Fatalf("source ids share a key: %q", got)
	}
	if err := s.ClearSourceKey(ctx, "tmdb"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SourceKey(ctx, "tmdb"); got != "" {
		t.Fatalf("after clear: %q", got)
	}
}

func TestSourceKeyFields(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if got, err := s.SourceKeyField(ctx, "igdb", "client_id"); got != "" || err != nil {
		t.Fatalf("unset: %q %v", got, err)
	}
	if err := s.SetSourceKeyField(ctx, "igdb", "client_id", " id "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceKeyField(ctx, "igdb", "client_secret", "secret"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get(ctx, "source.igdb.client_id"); !ok || v != "id" {
		t.Fatalf("stored %q %v", v, ok)
	}
	if err := s.SetSourceKeyField(ctx, "steam", "key", "steam-key"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceKeyField(ctx, "tmdb", "key", "tmdb-key"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSourceKeys(ctx, "igdb"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"client_id", "client_secret"} {
		if got, _ := s.SourceKeyField(ctx, "igdb", f); got != "" {
			t.Fatalf("%s survived: %q", f, got)
		}
	}
	if got, _ := s.SourceKeyField(ctx, "steam", "key"); got != "steam-key" {
		t.Fatalf("other source: %q", got)
	}
	// the single-key API still reads and writes field "key"
	if got, _ := s.SourceKey(ctx, "tmdb"); got != "tmdb-key" {
		t.Fatalf("single: %q", got)
	}
}

// TestClearSourceKeysScope: ClearSourceKeys removes the single-key field
// "key" too, only the named source's own fields (never a field of a source
// whose id merely starts with "<id>."), and refuses an id that is empty or
// dotted, which no source can have.
func TestClearSourceKeysScope(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for _, k := range []struct{ id, field string }{
		{"tmdb", "key"}, {"igdb", "client_id"}, {"igdb.x", "key"}, {"igdbx", "key"},
	} {
		if err := s.SetSourceKeyField(ctx, k.id, k.field, "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearSourceKeys(ctx, "tmdb"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SourceKey(ctx, "tmdb"); got != "" {
		t.Fatalf("single key survived: %q", got)
	}
	if err := s.ClearSourceKeys(ctx, "igdb"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SourceKeyField(ctx, "igdb", "client_id"); got != "" {
		t.Fatalf("igdb field survived: %q", got)
	}
	for _, id := range []string{"igdb.x", "igdbx"} {
		if got, _ := s.SourceKey(ctx, id); got != "v" {
			t.Fatalf("clearing igdb removed %s: %q", id, got)
		}
	}
	for _, id := range []string{"", "igdb.x"} {
		if err := s.ClearSourceKeys(ctx, id); err == nil {
			t.Fatalf("ClearSourceKeys(%q): want error", id)
		}
	}
	if got, _ := s.SourceKey(ctx, "igdb.x"); got != "v" {
		t.Fatalf("refused clear removed igdb.x: %q", got)
	}
}

// TestSetSourceKeyFieldsIsAllOrNothing: every field of a source is saved in
// one transaction, trimmed; when one write fails none is stored and the old
// values stay.
func TestSetSourceKeyFieldsIsAllOrNothing(t *testing.T) {
	s, exec := newStore(t)
	ctx := context.Background()
	if err := s.SetSourceKeyFields(ctx, "igdb", map[string]string{"client_id": " id ", "client_secret": "secret"}); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"client_id": "id", "client_secret": "secret"} {
		if got, err := s.SourceKeyField(ctx, "igdb", f); err != nil || got != want {
			t.Fatalf("%s = %q %v, want %q", f, got, err, want)
		}
	}

	// the second write fails: neither field changes
	exec(`CREATE TRIGGER refuse_secret BEFORE UPDATE ON settings
		WHEN NEW.key = 'source.igdb.client_secret' BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	if err := s.SetSourceKeyFields(ctx, "igdb", map[string]string{"client_id": "new-id", "client_secret": "new-secret"}); err == nil {
		t.Fatal("want error")
	}
	for f, want := range map[string]string{"client_id": "id", "client_secret": "secret"} {
		if got, err := s.SourceKeyField(ctx, "igdb", f); err != nil || got != want {
			t.Fatalf("after a failed save %s = %q %v, want %q", f, got, err, want)
		}
	}
}

func TestSourceKeyFieldsReportDBErrors(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(d)
	d.Close()
	if _, err := s.SourceKeyField(context.Background(), "igdb", "client_id"); err == nil {
		t.Fatal("want error")
	}
	if err := s.SetSourceKeyField(context.Background(), "igdb", "client_id", "x"); err == nil {
		t.Fatal("want error")
	}
	if err := s.ClearSourceKeys(context.Background(), "igdb"); err == nil {
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

func TestTimeZoneValidation(t *testing.T) {
	for _, ok := range []string{"UTC", "Europe/Berlin", "America/New_York"} {
		if !ValidTimeZone(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus", "../etc/passwd", "Europe/Berlin\n"} {
		if ValidTimeZone(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestTimeZoneDefaultFollowsTZ(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	t.Setenv("TZ", "Europe/Berlin")
	if got := s.TimeZoneName(ctx); got != "Europe/Berlin" {
		t.Errorf("TZ default: %q", got)
	}
	t.Setenv("TZ", "Not/AZone")
	if got := s.TimeZoneName(ctx); got != "UTC" {
		t.Errorf("invalid TZ: %q", got)
	}
	t.Setenv("TZ", "")
	if got := s.TimeZoneName(ctx); got != "UTC" {
		t.Errorf("no TZ: %q", got)
	}
}

func TestTimeZoneStoredWinsAndEmptyResets(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	t.Setenv("TZ", "Asia/Tokyo")
	if err := s.SetTimeZone(ctx, "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	if got := s.TimeZoneName(ctx); got != "Europe/Berlin" {
		t.Errorf("stored: %q", got)
	}
	if loc := s.TimeLocation(ctx); loc.String() != "Europe/Berlin" {
		t.Errorf("location: %v", loc)
	}
	if err := s.SetTimeZone(ctx, "Mars/Olympus"); err == nil {
		t.Error("invalid zone stored")
	}
	if err := s.SetTimeZone(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if stored, _ := s.StoredTimeZone(ctx); stored != "" {
		t.Errorf("still stored: %q", stored)
	}
	if got := s.TimeZoneName(ctx); got != "Asia/Tokyo" {
		t.Errorf("after reset: %q", got)
	}
}

func TestValidTimeZoneIsStrict(t *testing.T) {
	for _, bad := range []string{"Local", "/etc/localtime", "../x", "Europe/../x", "a\x00/b", "Europe/Ber\tlin", "Europe/Berlin ", "Europe/" + strings.Repeat("a", 60), "Berlin", "Europe/Bérlin", "utc", "EST", "Japan"} {
		if ValidTimeZone(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	for _, ok := range []string{"UTC", "Europe/Berlin", "America/Argentina/Buenos_Aires", "Etc/GMT+5", "America/Port-au-Prince"} {
		if !ValidTimeZone(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
}

// TestTimeZoneSyntax pins the name check that runs before the zone database
// is asked: the length cap and every edge of the allowed characters.
func TestTimeZoneSyntax(t *testing.T) {
	ok := []string{"UTC", "a/b", "z/z", "A/A", "Z/Z", "0/9", "a_b/c+d-e", "x/" + strings.Repeat("y", 62)}
	for _, name := range ok {
		if !timeZoneSyntax(name) {
			t.Errorf("%q should pass the syntax check", name)
		}
	}
	bad := []string{"", "x/" + strings.Repeat("y", 63), "Berlin", "/a/b", "a/../b", "a/b..c",
		"`/a", "{/a", "@/a", "[/a", ":/a", "a/b c", "a/b*", "a/b.c", "a/\u00e9"}
	for _, name := range bad {
		if timeZoneSyntax(name) {
			t.Errorf("%q should fail the syntax check", name)
		}
	}
}

// TestTimeZoneNameLogsAnUnreadableSetting: a read error is logged once and the
// default is used; a normal read logs nothing.
func TestTimeZoneNameLogsAnUnreadableSetting(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	t.Setenv("TZ", "")
	s, exec := newStore(t)
	ctx := context.Background()
	if got := s.TimeZoneName(ctx); got != "UTC" || buf.Len() != 0 {
		t.Fatalf("normal read: %q, log %q", got, buf.String())
	}
	exec(`DROP TABLE settings`)
	if got := s.TimeZoneName(ctx); got != "UTC" {
		t.Errorf("unreadable: %q", got)
	}
	if !strings.Contains(buf.String(), "using the default zone") {
		t.Errorf("no log line: %q", buf.String())
	}
}
