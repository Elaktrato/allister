package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer(Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestBasePageHasNav(t *testing.T) {
	rec := get(t, "/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Anime") ||
		!strings.Contains(body, `href="/settings"`) || !strings.Contains(body, `href="/anime"`) {
		t.Fatalf("got %d %q", rec.Code, body)
	}
}

func TestStaticHtmx(t *testing.T) {
	if rec := get(t, "/static/htmx.min.js"); rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
}
