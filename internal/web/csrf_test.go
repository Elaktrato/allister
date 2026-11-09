package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCrossOriginProtection(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int // 0 = anything but 403
	}{
		{"same-origin fetch metadata", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "same-origin"}, 0},
		{"none (typed/bookmark)", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "none"}, 0},
		{"cross-site", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same-site", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"evil origin", "POST", "/settings", map[string]string{"Origin": "http://evil.example"}, 403},
		{"null origin", "POST", "/settings", map[string]string{"Origin": "null"}, 403},
		{"unparsable origin", "POST", "/settings", map[string]string{"Origin": "::bad"}, 403},
		{"matching origin", "POST", "/settings", map[string]string{"Origin": "http://allister.test"}, 0},
		{"matching origin other scheme", "POST", "/settings", map[string]string{"Origin": "https://allister.test"}, 0},
		{"origin same host other port", "POST", "/settings", map[string]string{"Origin": "http://allister.test:9"}, 403},
		{"no headers", "POST", "/settings", nil, 0},
		{"fetch metadata wins over matching origin", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://allister.test"}, 403},
		{"get cross-site still ok", "GET", "/healthz", map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"head cross-site still ok", "HEAD", "/healthz", map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"proxy host matches origin", "POST", "/settings", map[string]string{"Origin": "https://anime.home", "X-Forwarded-Host": "anime.home"}, 0},
		{"proxy host mismatch", "POST", "/settings", map[string]string{"Origin": "https://evil.example", "X-Forwarded-Host": "anime.home"}, 403},
		{"forwarded host does not excuse fetch metadata", "POST", "/settings", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://anime.home", "X-Forwarded-Host": "anime.home"}, 403},
		{"delete cross-site", "DELETE", "/settings", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _, _ := newTestServer(t)
			req := httptest.NewRequest(c.method, "http://allister.test"+c.path,
				strings.NewReader(url.Values{"title_lang": {"en"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if c.want == 403 {
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cross-origin request refused") {
					t.Fatalf("got %d %q", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code == http.StatusForbidden {
				t.Fatalf("unexpected 403 %q", rec.Body.String())
			}
			if c.want != 0 && rec.Code != c.want {
				t.Fatalf("got %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestRefusedMutationHasNoEffect(t *testing.T) {
	h, st, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "http://allister.test/settings", strings.NewReader("title_lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("got %d", rec.Code)
	}
	if _, ok, _ := st.Get(req.Context(), "title_lang"); ok {
		t.Fatal("refused request changed settings")
	}
}

func TestSameOriginPostKeepsRedirectAndVary(t *testing.T) {
	h, _, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "http://allister.test/settings", strings.NewReader("title_lang=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://allister.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	if !strings.Contains(rec.Header().Get("Vary"), "HX-Request") {
		t.Fatalf("Vary lost: %q", rec.Header().Get("Vary"))
	}
}
