package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
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

func TestVersionEndpoint(t *testing.T) {
	rec := get(t, "/version")
	if rec.Code != 200 {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %q", err, rec.Body.String())
	}
	want := map[string]string{"version": "dev", "commit": "unknown", "date": "unknown", "go": runtime.Version()}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// /healthz stays exactly "ok" for monitors
	if rec := get(t, "/healthz"); rec.Body.String() != "ok" {
		t.Errorf("healthz %q", rec.Body.String())
	}
}

func TestSettingsShowsVersion(t *testing.T) {
	body := get(t, "/settings").Body.String()
	if !strings.Contains(body, `>Allister dev</p>`) {
		t.Errorf("settings page lacks the version: %q", body)
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
