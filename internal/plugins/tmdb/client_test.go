package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// requestRecorder keeps what the last request looked like. Each test makes one
// request and reads the fields only after Get returned.
type requestRecorder struct {
	count  int
	path   string
	query  url.Values
	auth   string
	accept string
	ua     string
}

func (r *requestRecorder) record(req *http.Request) {
	r.count++
	r.path = req.URL.Path
	r.query = req.URL.Query()
	r.auth = req.Header.Get("Authorization")
	r.accept = req.Header.Get("Accept")
	r.ua = req.Header.Get("User-Agent")
}

func staticKey(key string) KeyFunc {
	return func(context.Context) (string, error) { return key, nil }
}

func TestConstants(t *testing.T) {
	if DefaultBase != "https://api.themoviedb.org/3" {
		t.Errorf("DefaultBase = %q", DefaultBase)
	}
	if MaxBody != 8<<20 {
		t.Errorf("MaxBody = %d", MaxBody)
	}
}

func TestGetSendsV3KeyAndV4Bearer(t *testing.T) {
	t.Run("v3 key goes into the query", func(t *testing.T) {
		rec := &requestRecorder{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			io.WriteString(w, `{"ok":true}`)
		}))
		defer srv.Close()

		var out struct {
			OK bool `json:"ok"`
		}
		values := url.Values{"language": {"en-US"}}
		c := New(srv.URL, staticKey("abc"), srv.Client())
		if err := c.Get(context.Background(), "/movie/1", values, &out); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !out.OK {
			t.Error("response was not decoded")
		}
		if got := rec.query.Get("api_key"); got != "abc" {
			t.Errorf("api_key = %q, want abc", got)
		}
		if rec.auth != "" {
			t.Errorf("Authorization = %q, want empty for a v3 key", rec.auth)
		}
		if got := rec.query.Get("language"); got != "en-US" {
			t.Errorf("language = %q, want en-US", got)
		}
		if rec.accept != "application/json" {
			t.Errorf("Accept = %q", rec.accept)
		}
		if rec.ua == "" {
			t.Error("User-Agent is empty")
		}
	})

	t.Run("v4 token goes into the Authorization header", func(t *testing.T) {
		rec := &requestRecorder{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			io.WriteString(w, `{}`)
		}))
		defer srv.Close()

		const key = "eyJhbGciOiJIUzI1NiJ9.e30.signature"
		c := New(srv.URL, staticKey(key), srv.Client())
		if err := c.Get(context.Background(), "/movie/1", nil, &struct{}{}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got, want := rec.auth, "Bearer "+key; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		if _, ok := rec.query["api_key"]; ok {
			t.Errorf("api_key = %q, want no api_key for a v4 token", rec.query.Get("api_key"))
		}
	})
}

func TestGetWithoutKeyNeedsConfiguration(t *testing.T) {
	cases := map[string]KeyFunc{
		"empty": staticKey(""),
		"nil":   nil,
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				io.WriteString(w, `{}`)
			}))
			defer srv.Close()

			c := New(srv.URL, key, srv.Client())
			err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
			if !errors.Is(err, plugin.ErrNotConfigured) {
				t.Fatalf("err = %v, want ErrNotConfigured", err)
			}
			if requests != 0 {
				t.Errorf("%d requests, want 0", requests)
			}
		})
	}
}

func TestGetKeyResolverErrorIsUnavailable(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	key := func(context.Context) (string, error) { return "", errors.New("settings unavailable") }
	c := New(srv.URL, key, srv.Client())
	err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
	if !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if requests != 0 {
		t.Errorf("%d requests, want 0", requests)
	}
}

func TestGetMapsStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, plugin.ErrNotConfigured},
		{http.StatusForbidden, plugin.ErrNotConfigured},
		{http.StatusNotFound, plugin.ErrNotFound},
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
		{http.StatusBadGateway, plugin.ErrUnavailable},
		{http.StatusRequestTimeout, plugin.ErrUnavailable},
		{http.StatusBadRequest, plugin.ErrBadResponse},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := New(srv.URL, staticKey("abc"), srv.Client())
			err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
			}
			if errors.Is(err, plugin.ErrNotFound) && tc.want != plugin.ErrNotFound {
				t.Errorf("status %d: err = %v, must not be ErrNotFound", tc.status, err)
			}
		})
	}
}

func TestGetDoesNotLeakKey(t *testing.T) {
	t.Run("error body", func(t *testing.T) {
		const key = "secret-v3-key"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"status_message":"secret-v3-key is invalid"}`)
		}))
		defer srv.Close()

		c := New(srv.URL, staticKey(key), srv.Client())
		err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
		if err == nil {
			t.Fatal("err = nil, want a failure")
		}
		if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "secret") {
			t.Errorf("err %q leaks the key", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		const key = "secret-v3-key"
		hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// The URL carries the key; a wrapped error would leak it.
			return nil, fmt.Errorf("dial failed for %s", r.URL)
		})}
		c := New("https://api.example.test", staticKey(key), hc)
		err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
		if !errors.Is(err, plugin.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("err %q leaks the key", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFindReturnsMovieAndTVIds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/find/tt0133093"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("external_source"), "imdb_id"; got != want {
			t.Errorf("external_source = %q, want %q", got, want)
		}
		io.WriteString(w, `{"movie_results":[{"id":603}],"tv_results":[{"id":1234}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, staticKey("abc"), srv.Client())
	got, err := c.Find(context.Background(), "tt0133093", "imdb_id")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got.MovieResults) != 1 || got.MovieResults[0].ID != 603 {
		t.Errorf("MovieResults = %+v, want id 603", got.MovieResults)
	}
	if len(got.TVResults) != 1 || got.TVResults[0].ID != 1234 {
		t.Errorf("TVResults = %+v, want id 1234", got.TVResults)
	}
}

func TestFindEscapesID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.EscapedPath(), "/find/a%2Fb"; got != want {
			t.Errorf("escaped path = %q, want %q", got, want)
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := New(srv.URL, staticKey("abc"), srv.Client())
	if _, err := c.Find(context.Background(), "a/b", "imdb_id"); err != nil {
		t.Fatalf("Find: %v", err)
	}
}

func TestGetBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, io.LimitReader(strings.NewReader(strings.Repeat("a", MaxBody+1)), int64(MaxBody+1)))
	}))
	defer srv.Close()

	c := New(srv.URL, staticKey("abc"), srv.Client())
	err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
	if !errors.Is(err, plugin.ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse for an oversized body", err)
	}
}

func TestGetClonesValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	values := url.Values{"language": {"en"}}
	c := New(srv.URL, staticKey("abc"), srv.Client())
	if err := c.Get(context.Background(), "/movie/1", values, &struct{}{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := values.Get("api_key"); got != "" {
		t.Errorf("caller values gained api_key = %q", got)
	}
	if _, ok := values["api_key"]; ok {
		t.Error("caller values were mutated")
	}
}

func TestGetMalformedJSONIsBadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	defer srv.Close()

	c := New(srv.URL, staticKey("abc"), srv.Client())
	err := c.Get(context.Background(), "/movie/1", nil, &struct{}{})
	if !errors.Is(err, plugin.ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse", err)
	}
}

func TestNewTrimsBaseAndFillsDefaults(t *testing.T) {
	rec := &requestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := New(srv.URL+"/", staticKey("abc"), nil)
	if c.hc != http.DefaultClient {
		t.Error("nil http.Client was not replaced by http.DefaultClient")
	}
	if got, want := c.base, srv.URL; got != want {
		t.Errorf("base = %q, want %q", got, want)
	}
	if err := c.Get(context.Background(), "/movie/1", nil, &struct{}{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got, want := rec.path, "/movie/1"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestImage(t *testing.T) {
	cases := []struct {
		path, size, want string
	}{
		{"", "w500", ""},
		{"/p.jpg", "w500", "https://image.tmdb.org/t/p/w500/p.jpg"},
		{"/p.jpg", "", "https://image.tmdb.org/t/p/w500/p.jpg"},
		{"p.jpg", "original", "https://image.tmdb.org/t/p/original/p.jpg"},
	}
	for _, tc := range cases {
		if got := Image(tc.path, tc.size); got != tc.want {
			t.Errorf("Image(%q, %q) = %q, want %q", tc.path, tc.size, got, tc.want)
		}
	}
}

func TestGetDecodesIntoOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":603,"title":"The Matrix"}`)
	}))
	defer srv.Close()

	var out struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	c := New(srv.URL, staticKey("abc"), srv.Client())
	if err := c.Get(context.Background(), "/movie/603", nil, &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.ID != 603 || out.Title != "The Matrix" {
		t.Errorf("out = %+v", out)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"id":603,"title":"The Matrix"}` {
		t.Errorf("round trip = %s", raw)
	}
}
