package appearance

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/allister/allister/internal/db"
)

// woff2File is a real font: one of the bundled files.
func woff2File(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../web/static/fonts/jetbrains-mono-latin-ext-wght-v5.3.0.woff2")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeFont is n bytes starting with magic (only the magic is ever read).
func fakeFont(magic string, n int) []byte {
	b := bytes.Repeat([]byte{0x5a}, n)
	copy(b, magic)
	return b
}

func newFontStore(t *testing.T) (*FontStore, *sql.DB, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	dir := filepath.Join(t.TempDir(), "fonts")
	return NewFontStore(d, dir), d, dir
}

func TestSniffFont(t *testing.T) {
	for magic, want := range map[string]string{
		"wOF2": "woff2", "wOFF": "woff", "\x00\x01\x00\x00": "truetype", "true": "truetype", "OTTO": "opentype",
	} {
		if got, ok := SniffFont(fakeFont(magic, 64)); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", magic, got, ok, want)
		}
	}
	for name, head := range map[string][]byte{
		"empty": nil, "short": []byte("wOF"), "html": []byte("<!DOCTYPE html><html>"), "svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><font>`),
		"svg font with bom": []byte("\xef\xbb\xbf<?xml version=\"1.0\"?><svg>"), "collection": fakeFont("ttcf", 64), "eot": fakeFont("\x00\x00\x01\x00", 64),
		"lowercase": fakeFont("wof2", 64), "zip": fakeFont("PK\x03\x04", 64), "png": fakeFont("\x89PNG", 64), "script": []byte("<script>alert(1)</script>"),
		"magic later": append([]byte("    "), fakeFont("wOF2", 60)...),
	} {
		if f, ok := SniffFont(head); ok {
			t.Errorf("%s accepted as %s", name, f)
		}
	}
}

func TestCheckFontName(t *testing.T) {
	for in, want := range map[string]string{"My Font": "My Font", "  Trimmed  ": "Trimmed", "Ünïcødé 字体": "Ünïcødé 字体", strings.Repeat("x", 40): strings.Repeat("x", 40)} {
		if got, err := CheckFontName(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 41), "a\nb", "tab\there", "nul\x00", "bidi‮evil", "\xff\xfe", "zero​width"} {
		if got, err := CheckFontName(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestFontStoreAddEveryFormat(t *testing.T) {
	ctx := context.Background()
	fs, _, dir := newFontStore(t)
	files := map[string][]byte{
		"woff2": woff2File(t), "woff": fakeFont("wOFF", 300), "truetype": fakeFont("\x00\x01\x00\x00", 400), "opentype": fakeFont("OTTO", 500),
	}
	for format, content := range files {
		f, existing, err := fs.Add(ctx, "Font "+format, bytes.NewReader(content))
		if err != nil || existing {
			t.Fatalf("%s: %v %v", format, existing, err)
		}
		if f.Format != format || f.Size != int64(len(content)) || !ValidFontFile(f.File, format) || f.ID <= 0 || f.Name != "Font "+format {
			t.Errorf("%s: %+v", format, f)
		}
		got, err := os.ReadFile(filepath.Join(dir, f.File))
		if err != nil || !bytes.Equal(got, content) {
			t.Errorf("%s: stored file %v", format, err)
		}
	}
	list, err := fs.List(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("list %v %v", list, err)
	}
	// no temp file is left behind
	ents, _ := os.ReadDir(dir)
	if len(ents) != 4 {
		t.Errorf("%d files in the fonts dir", len(ents))
	}
}

func TestFontStoreRefuses(t *testing.T) {
	ctx := context.Background()
	fs, _, dir := newFontStore(t)
	cases := map[string]struct {
		name    string
		content []byte
		want    error
	}{
		"html disguised": {"x", []byte("<html><body>hello</body></html>"), ErrNotFont},
		"svg font":       {"x", []byte(`<?xml version="1.0"?><svg><defs><font id="f"></font></defs></svg>`), ErrNotFont},
		"empty":          {"x", nil, ErrNotFont},
		"wrong magic":    {"x", fakeFont("ttcf", 100), ErrNotFont},
		"oversize":       {"x", fakeFont("wOF2", MaxFontBytes+1), ErrFontTooLarge},
		"name empty":     {"", woff2File(t), ErrInvalid},
		"name too long":  {strings.Repeat("n", 41), woff2File(t), ErrInvalid},
		"name control":   {"a\x07b", woff2File(t), ErrInvalid},
		"name bidi":      {"a\u202eb", fakeFont("wOF2", 10), ErrInvalid},
	}
	for name, c := range cases {
		if _, _, err := fs.Add(ctx, c.name, bytes.NewReader(c.content)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if list, _ := fs.List(ctx); len(list) != 0 {
		t.Errorf("refused fonts listed: %v", list)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("refused uploads left %d files", len(ents))
	}
	// exactly the limit is fine
	if _, _, err := fs.Add(ctx, "max", bytes.NewReader(fakeFont("wOF2", MaxFontBytes))); err != nil {
		t.Errorf("a font of exactly %d bytes: %v", MaxFontBytes, err)
	}
}

func TestFontStoreDedupesAndCaps(t *testing.T) {
	ctx := context.Background()
	fs, _, _ := newFontStore(t)
	a, _, err := fs.Add(ctx, "First", bytes.NewReader(woff2File(t)))
	if err != nil {
		t.Fatal(err)
	}
	b, existing, err := fs.Add(ctx, "Second name", bytes.NewReader(woff2File(t)))
	if err != nil || !existing || b.ID != a.ID || b.Name != "First" {
		t.Fatalf("same content: %+v %v %v", b, existing, err)
	}
	for i := 1; i < MaxFonts; i++ {
		if _, _, err := fs.Add(ctx, "F", bytes.NewReader(fakeFont("wOFF", 100+i))); err != nil {
			t.Fatalf("font %d: %v", i+1, err)
		}
	}
	if _, _, err := fs.Add(ctx, "One too many", bytes.NewReader(fakeFont("OTTO", 99))); !errors.Is(err, ErrTooManyFonts) {
		t.Fatalf("over the cap: %v", err)
	}
	// a duplicate is still answered at the cap
	if _, existing, err := fs.Add(ctx, "dup", bytes.NewReader(woff2File(t))); err != nil || !existing {
		t.Errorf("duplicate at the cap: %v %v", existing, err)
	}
	if list, _ := fs.List(ctx); len(list) != MaxFonts {
		t.Errorf("%d fonts", len(list))
	}
}

func TestFontStoreDeleteAndLookup(t *testing.T) {
	ctx := context.Background()
	fs, _, dir := newFontStore(t)
	f, _, err := fs.Add(ctx, "Gone soon", bytes.NewReader(woff2File(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := fs.ByFile(ctx, f.File); err != nil || !ok || got.ID != f.ID {
		t.Fatalf("ByFile %v %v %v", got, ok, err)
	}
	if _, ok, _ := fs.ByFile(ctx, "../"+f.File); ok {
		t.Error("ByFile accepted a path")
	}
	if err := fs.Delete(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, f.File)); !os.IsNotExist(err) {
		t.Errorf("file kept: %v", err)
	}
	if _, ok, _ := fs.ByFile(ctx, f.File); ok {
		t.Error("deleted font still found")
	}
	if err := fs.Delete(ctx, f.ID); !errors.Is(err, ErrNoFont) {
		t.Errorf("second delete: %v", err)
	}
}

// TestServiceDeleteActiveFontFallsBack: deleting the font in use returns
// the app font to the default; deleting another keeps the choice.
func TestServiceDeleteActiveFontFallsBack(t *testing.T) {
	ctx := context.Background()
	fs, _, _ := newFontStore(t)
	st := &mapStore{m: map[string]string{}}
	s := NewService(st, fs)
	a, _, err := s.AddFont(ctx, "A", bytes.NewReader(woff2File(t)))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.AddFont(ctx, "B", bytes.NewReader(fakeFont("wOFF", 50)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetFont(ctx, "upload-"+itoa(a.ID)); err != nil {
		t.Fatal(err)
	}
	css, _ := s.Stylesheet(ctx)
	mustHave(t, string(css), `--al-font: "Allister Font `+itoa(a.ID)+`", `, `src: url("/fonts/`+a.File+`") format("woff2");`, `src: url("/fonts/`+b.File+`") format("woff");`)
	if err := s.DeleteFont(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if st.m[KeyFont] != "upload-"+itoa(a.ID) {
		t.Error("deleting another font changed the choice")
	}
	if err := s.DeleteFont(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st.m[KeyFont] != "" {
		t.Errorf("font choice %q kept after its font was deleted", st.m[KeyFont])
	}
	css, _ = s.Stylesheet(ctx)
	if string(css) != string(CSS(Config{}, nil)) {
		t.Error("stylesheet not back to the defaults")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
