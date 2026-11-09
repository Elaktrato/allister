package covers

import (
	"bytes"
	"context"
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

func TestDownloadRejectsOversize(t *testing.T) {
	srv := imageServer(t, "image/png", 200, bytes.Repeat([]byte("a"), maxBytes+1))
	dir := t.TempDir()
	if _, err := Download(context.Background(), nil, dir, srv.URL); err == nil {
		t.Fatal("expected error")
	}
	if f := files(t, dir); len(f) != 0 {
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
