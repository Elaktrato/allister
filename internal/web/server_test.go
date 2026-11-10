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
	h, _, _ := newTestServer(t)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestBasePageHasNav(t *testing.T) {
	rec := get(t, "/settings")
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

func TestHomeRedirectsToList(t *testing.T) {
	rec := get(t, "/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/anime" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestIsASCIIDigits(t *testing.T) {
	for _, c := range []struct {
		s    string
		max  int
		want bool
	}{
		{"", 0, false}, {"0", 0, true}, {"123", 3, true}, {"1234", 3, false},
		{"99999999999999999999999", 0, true}, {"12a", 0, false}, {"-1", 0, false},
		{"+1", 0, false}, {"١٢", 0, false}, {" 1", 0, false},
	} {
		if got := isASCIIDigits(c.s, c.max); got != c.want {
			t.Errorf("isASCIIDigits(%q, %d) = %v", c.s, c.max, got)
		}
	}
}
