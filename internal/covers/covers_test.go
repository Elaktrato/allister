package covers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func imageServer(t *testing.T, ctype string, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestDownloadStoresFileByHash(t *testing.T) {
	body := []byte("fake-jpeg-bytes")
	srv := imageServer(t, "image/jpeg", 200, body)
	dir := filepath.Join(t.TempDir(), "sub", "covers")
	rel, err := Download(context.Background(), nil, dir, srv.URL+"/a.png")
	if err != nil {
		t.Fatal(err)
	}
	// sha256("fake-jpeg-bytes") hex + extension from Content-Type, not the URL
	if !strings.HasSuffix(rel, ".jpg") || len(rel) != 64+4 || strings.ContainsAny(rel, "/\\") {
		t.Fatalf("rel = %q", rel)
	}
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("read: %v %q", err, got)
	}
	rel2, err := Download(context.Background(), nil, dir, srv.URL+"/other")
	if err != nil || rel2 != rel {
		t.Fatalf("second: %q %v", rel2, err)
	}
	if f := files(t, dir); len(f) != 1 {
		t.Fatalf("files = %v", f)
	}
}

func TestDownloadExtensions(t *testing.T) {
	for ctype, ext := range map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp",
		"image/gif": ".gif", "image/avif": ".avif", "image/png; charset=x": ".png",
	} {
		srv := imageServer(t, ctype, 200, []byte("x"+ctype))
		rel, err := Download(context.Background(), nil, t.TempDir(), srv.URL)
		if err != nil || !strings.HasSuffix(rel, ext) {
			t.Errorf("%s: %q %v", ctype, rel, err)
		}
	}
	srv := imageServer(t, "image/svg+xml", 200, []byte("<svg/>"))
	if _, err := Download(context.Background(), nil, t.TempDir(), srv.URL); err == nil {
		t.Error("svg accepted")
	}
}

func TestDownloadRejectsNonImage(t *testing.T) {
	srv := imageServer(t, "text/html", 200, []byte("<html>"))
	dir := t.TempDir()
	if _, err := Download(context.Background(), nil, dir, srv.URL); err == nil {
		t.Fatal("expected error")
	}
	if f := files(t, dir); len(f) != 0 {
		t.Fatalf("files = %v", f)
	}
}

func TestDownloadRejectsNon2xx(t *testing.T) {
	srv := imageServer(t, "image/png", 404, []byte("nope"))
	dir := t.TempDir()
	if _, err := Download(context.Background(), nil, dir, srv.URL); err == nil {
		t.Fatal("expected error")
	}
	if f := files(t, dir); len(f) != 0 {
		t.Fatalf("files = %v", f)
	}
}

// TestDownloadRejectsOver2MiB: a downloaded image may be at most
// MaxImageBytes (2 MiB); one byte more is refused and leaves no file.
func TestDownloadRejectsOver2MiB(t *testing.T) {
	if MaxImageBytes != 2<<20 {
		t.Fatalf("MaxImageBytes = %d, want 2 MiB", MaxImageBytes)
	}
	dir := t.TempDir()
	over := imageServer(t, "image/png", 200, bytes.Repeat([]byte("a"), MaxImageBytes+1))
	if _, err := Download(context.Background(), nil, dir, over.URL); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if f := files(t, dir); len(f) != 0 {
		t.Fatalf("files = %v", f)
	}
	exact := imageServer(t, "image/png", 200, bytes.Repeat([]byte("b"), MaxImageBytes))
	name, err := Download(context.Background(), nil, dir, exact.URL)
	if err != nil {
		t.Fatalf("exactly 2 MiB: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || fi.Size() != MaxImageBytes {
		t.Fatalf("stored %v %v", fi, err)
	}
	if f := files(t, dir); len(f) != 1 {
		t.Fatalf("files = %v", f)
	}
}

func TestDownloadRejectsSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://x/y.png", "/local/path.png", ""} {
		if _, err := Download(context.Background(), nil, t.TempDir(), u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestDownloadFollowsRedirect(t *testing.T) {
	img := imageServer(t, "image/webp", 200, []byte("w"))
	redir := httptest.NewServer(http.RedirectHandler(img.URL, http.StatusFound))
	defer redir.Close()
	rel, err := Download(context.Background(), nil, t.TempDir(), redir.URL)
	if err != nil || !strings.HasSuffix(rel, ".webp") {
		t.Fatalf("%q %v", rel, err)
	}
}

func TestSrc(t *testing.T) {
	for in, want := range map[string]string{
		"":                    "",
		"http://x/a.jpg":      "http://x/a.jpg",
		"https://x/a.jpg?q=1": "https://x/a.jpg?q=1",
		"abc.jpg":             "/covers/abc.jpg",
		"a b#c?.jpg":          "/covers/a%20b%23c%3F.jpg",
	} {
		if got := Src(in); got != want {
			t.Errorf("Src(%q) = %q, want %q", in, got, want)
		}
	}
}

func pngBytes(extra string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00"), extra...)
}

func TestSaveAcceptsSniffedImages(t *testing.T) {
	cases := map[string][]byte{
		".png":  pngBytes("a"),
		".jpg":  append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF"), "a"...),
		".gif":  []byte("GIF89a\x01\x00\x01\x00"),
		".webp": []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00"),
	}
	for ext, body := range cases {
		dir := t.TempDir()
		rel, err := Save(dir, bytes.NewReader(body))
		if err != nil || !strings.HasSuffix(rel, ext) || len(rel) != 64+len(ext) {
			t.Errorf("%s: %q %v", ext, rel, err)
			continue
		}
		if got, _ := os.ReadFile(filepath.Join(dir, rel)); !bytes.Equal(got, body) {
			t.Errorf("%s: content differs", ext)
		}
	}
}

func TestSaveRejectsOtherContent(t *testing.T) {
	cases := map[string][]byte{
		"html as png": []byte("<!DOCTYPE html><html><script>alert(1)</script>"),
		"svg":         []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"svg bare":    []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"empty":       nil,
		"binary":      {0, 1, 2, 3, 4, 5},
		"text":        []byte("hello"),
	}
	for name, body := range cases {
		dir := t.TempDir()
		if rel, err := Save(dir, bytes.NewReader(body)); err == nil {
			t.Errorf("%s accepted as %q", name, rel)
		}
		if f := files(t, dir); len(f) != 0 {
			t.Errorf("%s left files %v", name, f)
		}
	}
}

func TestSaveRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	// user uploads keep their own larger cap (MaxUploadBytes, 10 MiB)
	if MaxUploadBytes != 10<<20 {
		t.Fatalf("MaxUploadBytes = %d", MaxUploadBytes)
	}
	body := pngBytes(strings.Repeat("a", MaxUploadBytes))
	if _, err := Save(dir, bytes.NewReader(body)); err == nil {
		t.Fatal("expected error")
	}
	if f := files(t, dir); len(f) != 0 {
		t.Fatalf("files = %v", f)
	}
	// exactly the limit is fine
	ok := pngBytes(strings.Repeat("a", MaxUploadBytes-len(pngBytes(""))))
	if _, err := Save(dir, bytes.NewReader(ok)); err != nil {
		t.Fatalf("at limit: %v", err)
	}
}

func TestSaveSameBytesOneFile(t *testing.T) {
	dir := t.TempDir()
	a, err1 := Save(dir, bytes.NewReader(pngBytes("x")))
	b, err2 := Save(dir, bytes.NewReader(pngBytes("x")))
	if err1 != nil || err2 != nil || a != b {
		t.Fatalf("%q %q %v %v", a, b, err1, err2)
	}
	if f := files(t, dir); len(f) != 1 {
		t.Fatalf("files = %v", f)
	}
}

// A cover a backup restores may be any type Download stores, AVIF included
// (http.DetectContentType does not know AVIF, so Save alone would refuse it
// and the restored item would lose its only copy of the cover).
func TestSaveRestoredAcceptsDownloadTypes(t *testing.T) {
	cases := map[string][]byte{
		".png":   pngBytes("a"),
		".jpg":   append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF"), "a"...),
		".avif":  []byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"),
		".avif ": []byte("\x00\x00\x00\x1cftypavis\x00\x00\x00\x00avisavifmif1"), // image sequence brand
	}
	for name, body := range cases {
		ext := strings.TrimSpace(name)
		dir := t.TempDir()
		rel, err := SaveRestored(dir, bytes.NewReader(body))
		if err != nil || !strings.HasSuffix(rel, ext) || len(rel) != 64+len(ext) {
			t.Errorf("%q: %q %v", name, rel, err)
			continue
		}
		if got, _ := os.ReadFile(filepath.Join(dir, rel)); !bytes.Equal(got, body) {
			t.Errorf("%q: content differs", name)
		}
	}
	for name, body := range map[string][]byte{
		"heic brand":   []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"),
		"short ftyp":   []byte("\x00\x00\x00\x08ftyp"),
		"svg":          []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"text":         []byte("hello"),
		"avif as text": []byte("ftypavif"),
	} {
		dir := t.TempDir()
		if rel, err := SaveRestored(dir, bytes.NewReader(body)); !errors.Is(err, ErrNotImage) {
			t.Errorf("%s: %q %v", name, rel, err)
		}
		if f := files(t, dir); len(f) != 0 {
			t.Errorf("%s left files %v", name, f)
		}
	}
	// user uploads stay limited to the sniffable types
	if _, err := Save(t.TempDir(), bytes.NewReader(cases[".avif"])); !errors.Is(err, ErrNotImage) {
		t.Fatalf("Save accepted AVIF: %v", err)
	}
}
