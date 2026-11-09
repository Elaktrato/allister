package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func coversServer(t *testing.T, coversDir string) http.Handler {
	t.Helper()
	_, st, _ := newTestServer(t)
	return NewServer(Deps{Settings: st, CoversDir: coversDir})
}

func serve(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestCoversServedWithCacheHeader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "covers")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "abc.jpg"), []byte("jpegdata"), 0o644)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o644)
	h := coversServer(t, dir)

	rec := serve(h, "/covers/abc.jpg")
	if rec.Code != 200 || rec.Body.String() != "jpegdata" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	for _, p := range []string{"/covers/", "/covers", "/covers/sub", "/covers/sub/", "/covers/missing.jpg",
		"/covers/../secret.txt", "/covers/%2e%2e/secret.txt", "/covers/..%2fsecret.txt",
		"/covers/%2e%2e%2fsecret.txt", "/covers/sub/../../secret.txt", "/covers//secret.txt"} {
		rec := serve(h, p)
		// The mux may answer 301 to the cleaned path; following it must 404.
		for i := 0; rec.Code == http.StatusMovedPermanently && i < 3; i++ {
			rec = serve(h, rec.Header().Get("Location"))
		}
		if rec.Code != http.StatusNotFound || rec.Body.String() == "secret" {
			t.Errorf("%s: got %d %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestCoversRouteAbsentWithoutDir(t *testing.T) {
	h := coversServer(t, "")
	if rec := serve(h, "/covers/abc.jpg"); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}
