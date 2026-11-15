package web

import (
	"errors"
	"net/http/httptest"
	"testing"
)

// logSafe keeps a logged value on one line: CR and LF from a request (an id,
// a query value, an error that quotes one) cannot forge a log entry.
func TestLogSafe(t *testing.T) {
	for in, want := range map[any]string{
		"plain":                       "plain",
		"a\nfake: entry":              `a\nfake: entry`,
		"a\r\nb":                      `a\r\nb`,
		errors.New("bad id \"x\ny\""): `bad id "x\ny"`,
		42:                            "42",
	} {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// A missing font answers with nosniff (http.Error sets it), but with
// neither the font type nor the immutable cache.
func TestStaticFont404HasNosniff(t *testing.T) {
	rec := httptest.NewRecorder()
	staticHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/static/missing-v1.woff2", nil))
	if rec.Code != 404 || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("Content-Type") == "font/woff2" || rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}
