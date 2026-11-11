package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// igdbTestRecorder records the token and data requests the fake servers see.
type igdbTestRecorder struct {
	mu            sync.Mutex
	tokenCount    int
	tokenMethod   string
	tokenQuery    url.Values
	tokenAccept   string
	dataRequests  []igdbTestRequest
	dataRequestNo int
}

type igdbTestRequest struct {
	method      string
	path        string
	body        string
	clientID    string
	auth        string
	accept      string
	contentType string
}

func (r *igdbTestRecorder) recordToken(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokenCount++
	r.tokenMethod = req.Method
	r.tokenQuery = req.URL.Query()
	r.tokenAccept = req.Header.Get("Accept")
}

func (r *igdbTestRecorder) recordData(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dataRequestNo++
	r.dataRequests = append(r.dataRequests, igdbTestRequest{
		method:      req.Method,
		path:        req.URL.Path,
		body:        string(body),
		clientID:    req.Header.Get("Client-ID"),
		auth:        req.Header.Get("Authorization"),
		accept:      req.Header.Get("Accept"),
		contentType: req.Header.Get("Content-Type"),
	})
}

func (r *igdbTestRecorder) tokenRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokenCount
}

func (r *igdbTestRecorder) tokenRequest() (method string, query url.Values, accept string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokenMethod, r.tokenQuery, r.tokenAccept
}

func (r *igdbTestRecorder) requests() []igdbTestRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]igdbTestRequest(nil), r.dataRequests...)
}

func (r *igdbTestRecorder) dataCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dataRequestNo
}

func igdbKey(key string) igdbKeyFunc {
	return func(context.Context) (string, error) { return key, nil }
}

func igdbOKToken(token string, expires int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d}`, token, expires)
	}
}

// newIGDBTestServers starts the fake token and API servers. Both are recorded
// before the test's handler runs.
func newIGDBTestServers(t *testing.T, rec *igdbTestRecorder, token, data http.HandlerFunc) (tokenSrv, dataSrv *httptest.Server) {
	t.Helper()
	tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.recordToken(r)
		token(w, r)
	}))
	t.Cleanup(tokenSrv.Close)
	dataSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.recordData(r)
		data(w, r)
	}))
	t.Cleanup(dataSrv.Close)
	return tokenSrv, dataSrv
}

func newIGDBTestClient(t *testing.T, rec *igdbTestRecorder, token, data http.HandlerFunc) *igdbClient {
	t.Helper()
	tokenSrv, dataSrv := newIGDBTestServers(t, rec, token, data)
	return newIGDBClient(dataSrv.URL, tokenSrv.URL, igdbKey("client-id"), igdbKey("client-secret"), dataSrv.Client())
}

func TestIGDBDefaults(t *testing.T) {
	if DefaultIGDBBase != "https://api.igdb.com/v4" {
		t.Errorf("DefaultIGDBBase = %q", DefaultIGDBBase)
	}
	if DefaultIGDBTokenURL != "https://id.twitch.tv/oauth2/token" {
		t.Errorf("DefaultIGDBTokenURL = %q", DefaultIGDBTokenURL)
	}
	c := newIGDBClient("", "", nil, nil, nil)
	if c.base != DefaultIGDBBase || c.tokenURL != DefaultIGDBTokenURL {
		t.Errorf("defaults = %q %q", c.base, c.tokenURL)
	}
	if c.hc != http.DefaultClient {
		t.Error("nil http.Client was not replaced by http.DefaultClient")
	}
	c = newIGDBClient("https://api.example.test/v4/", "https://id.example.test/token/", nil, nil, nil)
	if c.base != "https://api.example.test/v4" || c.tokenURL != "https://id.example.test/token/" {
		t.Errorf("trimmed = %q %q", c.base, c.tokenURL)
	}
}

func TestIGDBTokenRequestAndCaching(t *testing.T) {
	rec := &igdbTestRecorder{}
	token := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, rec.tokenRequests())
	}
	c := newIGDBTestClient(t, rec, token, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})

	for i := 0; i < 2; i++ {
		var out []json.RawMessage
		if err := c.Post(context.Background(), "games", "fields name; limit 1;", &out); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}

	if got := rec.tokenRequests(); got != 1 {
		t.Errorf("token requests = %d, want 1 (the token is cached)", got)
	}
	method, query, accept := rec.tokenRequest()
	if method != http.MethodPost {
		t.Errorf("token method = %q, want POST", method)
	}
	for key, want := range map[string]string{
		"client_id":     "client-id",
		"client_secret": "client-secret",
		"grant_type":    "client_credentials",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("token query %s = %q, want %q", key, got, want)
		}
	}
	if accept != "application/json" {
		t.Errorf("token Accept = %q", accept)
	}

	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d data requests, want 2", len(reqs))
	}
	for i, req := range reqs {
		if req.method != http.MethodPost || req.path != "/games" {
			t.Errorf("request %d = %s %s", i, req.method, req.path)
		}
		if req.body != "fields name; limit 1;" {
			t.Errorf("request %d body = %q", i, req.body)
		}
		if req.clientID != "client-id" {
			t.Errorf("request %d Client-ID = %q", i, req.clientID)
		}
		if req.auth != "Bearer token-1" {
			t.Errorf("request %d Authorization = %q", i, req.auth)
		}
		if req.accept != "application/json" {
			t.Errorf("request %d Accept = %q", i, req.accept)
		}
	}
}

func TestIGDBTokenCacheKeyedByClientID(t *testing.T) {
	rec := &igdbTestRecorder{}
	tokenSrv, dataSrv := newIGDBTestServers(t, rec, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, rec.tokenRequests())
	}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})
	id := "client-a"
	c := newIGDBClient(dataSrv.URL, tokenSrv.URL, func(context.Context) (string, error) { return id, nil }, igdbKey("client-secret"), dataSrv.Client())

	var out []json.RawMessage
	if err := c.Post(context.Background(), "games", "fields name;", &out); err != nil {
		t.Fatalf("Post a: %v", err)
	}
	id = "client-b"
	if err := c.Post(context.Background(), "games", "fields name;", &out); err != nil {
		t.Fatalf("Post b: %v", err)
	}
	if got := rec.tokenRequests(); got != 2 {
		t.Errorf("token requests = %d, want 2 for a changed client ID", got)
	}
	reqs := rec.requests()
	if len(reqs) != 2 || reqs[0].clientID != "client-a" || reqs[1].clientID != "client-b" {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[1].auth != "Bearer token-2" {
		t.Errorf("second Authorization = %q, want the token of client-b", reqs[1].auth)
	}
}

func TestIGDBTokenRefreshedBeforeExpiry(t *testing.T) {
	rec := &igdbTestRecorder{}
	token := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":30}`, rec.tokenRequests())
	}
	c := newIGDBTestClient(t, rec, token, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})

	for i := 0; i < 2; i++ {
		var out []json.RawMessage
		if err := c.Post(context.Background(), "games", "fields name;", &out); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	if got := rec.tokenRequests(); got != 2 {
		t.Errorf("token requests = %d, want 2: a token expiring inside 60s is refreshed", got)
	}
	reqs := rec.requests()
	if reqs[1].auth != "Bearer token-2" {
		t.Errorf("second Authorization = %q", reqs[1].auth)
	}
}

func TestIGDBDataUnauthorizedRefreshesOnce(t *testing.T) {
	t.Run("refresh then retry", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		token := func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, rec.tokenRequests())
		}
		c := newIGDBTestClient(t, rec, token, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer token-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `[]`)
		})

		var out []json.RawMessage
		if err := c.Post(context.Background(), "games", "fields name;", &out); err != nil {
			t.Fatalf("Post: %v", err)
		}
		if got := rec.tokenRequests(); got != 2 {
			t.Errorf("token requests = %d, want 2", got)
		}
		reqs := rec.requests()
		if len(reqs) != 2 {
			t.Fatalf("%d data requests, want 2", len(reqs))
		}
		if reqs[0].auth != "Bearer token-1" || reqs[1].auth != "Bearer token-2" {
			t.Errorf("authorizations = %q %q", reqs[0].auth, reqs[1].auth)
		}
	})

	t.Run("second 401 is not configured", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		token := func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, rec.tokenRequests())
		}
		c := newIGDBTestClient(t, rec, token, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})

		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if !errors.Is(err, plugin.ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
		if got := rec.dataCount(); got != 2 {
			t.Errorf("%d data requests, want 2 (one retry only)", got)
		}
		if got := rec.tokenRequests(); got != 2 {
			t.Errorf("token requests = %d, want 2", got)
		}
	})

	t.Run("refresh failure is not configured", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		token := func(w http.ResponseWriter, r *http.Request) {
			if rec.tokenRequests() == 1 {
				io.WriteString(w, `{"access_token":"token-1","expires_in":3600}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		}
		c := newIGDBTestClient(t, rec, token, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})

		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if !errors.Is(err, plugin.ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
		if got := rec.dataCount(); got != 1 {
			t.Errorf("%d data requests, want 1", got)
		}
	})
}

func TestIGDBMapsStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusBadRequest, plugin.ErrBadResponse},
		{http.StatusUnprocessableEntity, plugin.ErrBadResponse},
		{http.StatusNotFound, plugin.ErrNotFound},
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
		{http.StatusBadGateway, plugin.ErrUnavailable},
		{http.StatusServiceUnavailable, plugin.ErrUnavailable},
		{http.StatusRequestTimeout, plugin.ErrUnavailable},
		{http.StatusForbidden, plugin.ErrBadResponse},
		{http.StatusTeapot, plugin.ErrBadResponse},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			rec := &igdbTestRecorder{}
			c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			})
			err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
			}
			if errors.Is(err, plugin.ErrNotFound) && tc.want != plugin.ErrNotFound {
				t.Errorf("status %d: err = %v, must not be ErrNotFound", tc.status, err)
			}
			if got := rec.tokenRequests(); got != 1 {
				t.Errorf("status %d: %d token requests, want 1", tc.status, got)
			}
		})
	}
}

func TestIGDBMissingCredentials(t *testing.T) {
	cases := []struct {
		name   string
		id     igdbKeyFunc
		secret igdbKeyFunc
		want   error
	}{
		{"nil resolvers", nil, nil, plugin.ErrNotConfigured},
		{"empty id", igdbKey(""), igdbKey("secret"), plugin.ErrNotConfigured},
		{"empty secret", igdbKey("id"), igdbKey(""), plugin.ErrNotConfigured},
		{"blank id", igdbKey("  "), igdbKey("secret"), plugin.ErrNotConfigured},
		{"blank secret", igdbKey("id"), igdbKey("  "), plugin.ErrNotConfigured},
		{"id resolver error", func(context.Context) (string, error) { return "", errors.New("settings unavailable") }, igdbKey("secret"), plugin.ErrUnavailable},
		{"secret resolver error", igdbKey("id"), func(context.Context) (string, error) { return "", errors.New("settings unavailable") }, plugin.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &igdbTestRecorder{}
			tokenSrv, dataSrv := newIGDBTestServers(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `[]`)
			})
			c := newIGDBClient(dataSrv.URL, tokenSrv.URL, tc.id, tc.secret, dataSrv.Client())
			err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := rec.tokenRequests(); got != 0 {
				t.Errorf("%d token requests, want 0", got)
			}
			if got := rec.dataCount(); got != 0 {
				t.Errorf("%d data requests, want 0", got)
			}
		})
	}
}

func TestIGDBTokenFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"bad request", http.StatusBadRequest, plugin.ErrNotConfigured},
		{"unauthorized", http.StatusUnauthorized, plugin.ErrNotConfigured},
		{"forbidden", http.StatusForbidden, plugin.ErrNotConfigured},
		{"rate limited", http.StatusTooManyRequests, plugin.ErrRateLimited},
		{"server error", http.StatusInternalServerError, plugin.ErrUnavailable},
		{"timeout", http.StatusRequestTimeout, plugin.ErrUnavailable},
		{"not found", http.StatusNotFound, plugin.ErrBadResponse},
		{"teapot", http.StatusTeapot, plugin.ErrBadResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &igdbTestRecorder{}
			c := newIGDBTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `[]`)
			})
			err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := rec.dataCount(); got != 0 {
				t.Errorf("%d data requests, want 0", got)
			}
		})
	}

	t.Run("malformed token response", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{not json`)
		}, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		if err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{}); !errors.Is(err, plugin.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
	})

	t.Run("empty access token", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"access_token":"","expires_in":3600}`)
		}, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		if err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{}); !errors.Is(err, plugin.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
	})
}

func TestIGDBDoesNotLeakCredentials(t *testing.T) {
	const secret = "super-secret-client-secret"
	const token = "token-1"

	t.Run("token error body", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"message":"%s is invalid"}`, secret)
		}, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		c.clientSecret = igdbKey(secret)
		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if err == nil {
			t.Fatal("err = nil, want a failure")
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("err %q leaks the secret", err)
		}
	})

	t.Run("data error body", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken(token, 3600), func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"message":"%s is invalid"}`, token)
		})
		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if err == nil {
			t.Fatal("err = nil, want a failure")
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("err %q leaks the token", err)
		}
	})

	t.Run("transport error keeps the token URL out", func(t *testing.T) {
		hc := &http.Client{Transport: igdbRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed for %s", r.URL)
		})}
		c := newIGDBClient("https://api.example.test/v4", "https://id.example.test/token?keep=1", igdbKey("id"), igdbKey(secret), hc)
		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if !errors.Is(err, plugin.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "client_secret") {
			t.Errorf("err %q leaks the secret", err)
		}
	})
}

type igdbRoundTripFunc func(*http.Request) (*http.Response, error)

func (f igdbRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIGDBSearch(t *testing.T) {
	t.Run("query and summaries", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/games" {
				t.Errorf("path = %q", r.URL.Path)
			}
			const want = `search "doom \"eternal\""; fields name,slug,first_release_date,cover.url; where version_parent = null; limit 20;`
			if body, _ := io.ReadAll(r.Body); string(body) != want {
				t.Errorf("body = %q, want %q", body, want)
			}
			io.WriteString(w, `[
				{"id":9606,"name":"The Elder Scrolls V: Skyrim","slug":"the-elder-scrolls-v-skyrim","first_release_date":1320969600,"cover":{"url":"//images.igdb.com/igdb/image/upload/t_thumb/co1u2d.jpg"}},
				{"id":1029,"name":"","first_release_date":0},
				{"id":0,"name":"no id"}
			]`)
		})

		out, err := (&igdbSource{c: c}).Search(context.Background(), KindGame, `doom "eternal"`)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("results = %+v", out)
		}
		got := out[0]
		if got.Source != SourceIGDB || got.ID != "9606" || got.Kind != KindGame {
			t.Errorf("summary = %+v", got)
		}
		if len(got.Titles) != 1 || got.Titles[0].Lang != "en" || got.Titles[0].Text != "The Elder Scrolls V: Skyrim" {
			t.Errorf("titles = %+v", got.Titles)
		}
		if got.Fields["year"].Num == nil || *got.Fields["year"].Num != 2011 {
			t.Errorf("year = %+v", got.Fields["year"])
		}
		if want := "https://images.igdb.com/igdb/image/upload/t_cover_big/co1u2d.jpg"; got.CoverURL != want {
			t.Errorf("cover = %q, want %q", got.CoverURL, want)
		}
	})

	t.Run("empty query makes no request", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		out, err := (&igdbSource{c: c}).Search(context.Background(), KindGame, "  ")
		if err != nil || len(out) != 0 {
			t.Fatalf("Search = %+v, %v", out, err)
		}
		if got := rec.dataCount() + rec.tokenRequests(); got != 0 {
			t.Errorf("%d requests, want 0", got)
		}
	})
}

const igdbTestGameFields = "fields name,slug,summary,first_release_date,platforms.name,genres.name,themes.name,keywords.name,involved_companies.company.name,involved_companies.developer,involved_companies.publisher,cover.url,similar_games.id,similar_games.name,similar_games.cover.url,external_games.uid,external_games.external_game_source;"

const igdbSkyrimJSON = `[{
	"id": 9606,
	"name": "The Elder Scrolls V: Skyrim",
	"slug": "the-elder-scrolls-v-skyrim",
	"summary": "Dragons.\n\nShouts.",
	"first_release_date": 1320969600,
	"platforms": [{"name": "PC"}, {"name": "PlayStation 3"}],
	"genres": [{"name": "Role-playing (RPG)"}, {"name": "Adventure"}],
	"themes": [{"name": "Fantasy"}, {"name": "Open world"}],
	"keywords": [{"name": "dragons"}, {"name": "Fantasy"}],
	"involved_companies": [
		{"developer": true, "publisher": false, "company": {"name": "Bethesda Game Studios"}},
		{"developer": true, "publisher": false, "company": {"name": "Bethesda Softworks"}},
		{"developer": false, "publisher": true, "company": {"name": "Bethesda Softworks"}},
		{"developer": false, "publisher": true, "company": {"name": "Zenimax"}},
		{"developer": false, "publisher": true, "company": {"name": "Fourth Publisher"}}
	],
	"cover": {"url": "//images.igdb.com/igdb/image/upload/t_thumb/co1u2d.jpg"},
	"similar_games": [
		{"id": 1029, "name": "The Elder Scrolls V: Skyrim", "first_release_date": 1320969600, "cover": {"url": "//images.igdb.com/igdb/image/upload/t_thumb/co1u2d.jpg"}}
	],
	"external_games": [
		{"uid": "72850", "external_game_source": 1},
		{"uid": "ign-123", "external_game_source": 5}
	]
}]`

func TestIGDBDetails(t *testing.T) {
	rec := &igdbTestRecorder{}
	c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/games":
			body, _ := io.ReadAll(r.Body)
			if want := igdbTestGameFields + " where id = 9606; limit 1;"; string(body) != want {
				t.Errorf("details body = %q, want %q", body, want)
			}
			io.WriteString(w, igdbSkyrimJSON)
		case "/game_time_to_beats":
			body, _ := io.ReadAll(r.Body)
			if want := "fields hastily,normally,completely,count; where game_id = 9606; limit 1;"; string(body) != want {
				t.Errorf("time-to-beat body = %q, want %q", body, want)
			}
			io.WriteString(w, `[{"hastily":36000,"normally":72000,"completely":108000,"count":100}]`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	d, err := (&igdbSource{c: c}).Details(context.Background(), KindGame, "9606")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if d.Source != SourceIGDB || d.ID != "9606" || d.Kind != KindGame {
		t.Fatalf("summary = %+v", d.ItemSummary)
	}
	if len(d.Titles) != 1 || d.Titles[0].Lang != "en" || d.Titles[0].Text != "The Elder Scrolls V: Skyrim" {
		t.Errorf("titles = %+v", d.Titles)
	}
	if d.Fields["summary"].Text != "Dragons.\n\nShouts." {
		t.Errorf("summary = %q", d.Fields["summary"].Text)
	}
	if d.Fields["platforms"].Text != "PC, PlayStation 3" {
		t.Errorf("platforms = %q", d.Fields["platforms"].Text)
	}
	if d.Fields["developer"].Text != "Bethesda Game Studios, Bethesda Softworks" {
		t.Errorf("developer = %q", d.Fields["developer"].Text)
	}
	if d.Fields["publisher"].Text != "Bethesda Softworks, Zenimax, Fourth Publisher" {
		t.Errorf("publisher = %q", d.Fields["publisher"].Text)
	}
	if d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2011 {
		t.Errorf("year = %+v", d.Fields["year"])
	}
	day := time.Date(2011, 11, 11, 0, 0, 0, 0, time.UTC)
	if d.Fields["released_on"].Date == nil || !d.Fields["released_on"].Date.Equal(day) {
		t.Errorf("released_on = %+v", d.Fields["released_on"])
	}
	for key, want := range map[string]float64{"time_main": 600, "time_extra": 1200, "time_complete": 1800} {
		if d.Fields[key].Num == nil || *d.Fields[key].Num != want {
			t.Errorf("%s = %+v, want %v", key, d.Fields[key], want)
		}
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Role-playing (RPG)" || d.Genres[1] != "Adventure" {
		t.Errorf("genres = %+v", d.Genres)
	}
	if len(d.Tags) != 3 || d.Tags[0].Name != "Fantasy" || d.Tags[1].Name != "Open world" || d.Tags[2].Name != "dragons" {
		t.Errorf("tags = %+v", d.Tags)
	}
	if len(d.ExternalIDs) != 2 || d.ExternalIDs["igdb"] != "9606" || d.ExternalIDs["steam"] != "72850" {
		t.Errorf("external ids = %+v", d.ExternalIDs)
	}
	if want := "https://www.igdb.com/games/the-elder-scrolls-v-skyrim"; d.ExternalURL != want {
		t.Errorf("external URL = %q, want %q", d.ExternalURL, want)
	}
	if want := "https://images.igdb.com/igdb/image/upload/t_cover_big/co1u2d.jpg"; d.CoverURL != want {
		t.Errorf("cover = %q, want %q", d.CoverURL, want)
	}
}

func TestIGDBDetailsNotFound(t *testing.T) {
	rec := &igdbTestRecorder{}
	c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})
	_, err := (&igdbSource{c: c}).Details(context.Background(), KindGame, "999")
	if !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := rec.dataCount(); got != 1 {
		t.Errorf("%d data requests, want 1 (no time-to-beat call without a game)", got)
	}
}

func TestIGDBTimeToBeatBestEffort(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"rate limited": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) },
		"bad request":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) },
		"no data":      func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `[]`) },
	}
	for name, timeHandler := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &igdbTestRecorder{}
			c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/games" {
					io.WriteString(w, igdbSkyrimJSON)
					return
				}
				timeHandler(w, r)
			})
			d, err := (&igdbSource{c: c}).Details(context.Background(), KindGame, "9606")
			if err != nil {
				t.Fatalf("Details: %v", err)
			}
			if d.ID != "9606" {
				t.Errorf("ID = %q", d.ID)
			}
			for _, key := range []string{"time_main", "time_extra", "time_complete"} {
				if _, ok := d.Fields[key]; ok {
					t.Errorf("time-to-beat failure kept %s = %+v", key, d.Fields[key])
				}
			}
		})
	}
}

func TestIGDBLookupSteam(t *testing.T) {
	t.Run("external game then details", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/external_games":
				body, _ := io.ReadAll(r.Body)
				if want := `fields game; where uid = "72850" & external_game_source = 1; limit 1;`; string(body) != want {
					t.Errorf("external body = %q, want %q", body, want)
				}
				io.WriteString(w, `[{"game":9606}]`)
			case "/games":
				// No external_games: the lookup itself must set the steam id.
				io.WriteString(w, `[{"id":9606,"name":"The Elder Scrolls V: Skyrim","slug":"the-elder-scrolls-v-skyrim"}]`)
			case "/game_time_to_beats":
				io.WriteString(w, `[]`)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		})

		d, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "steam", "72850")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if d.ID != "9606" || d.ExternalIDs["igdb"] != "9606" || d.ExternalIDs["steam"] != "72850" {
			t.Errorf("data = %+v", d.ItemSummary)
		}
	})

	t.Run("no external game", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		_, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "steam", "72850")
		if !errors.Is(err, plugin.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got := rec.dataCount(); got != 1 {
			t.Errorf("%d data requests, want 1", got)
		}
	})
}

func TestIGDBLookupIGDBIDAndSlug(t *testing.T) {
	t.Run("numeric id", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/games":
				body, _ := io.ReadAll(r.Body)
				if want := igdbTestGameFields + " where id = 9606; limit 1;"; string(body) != want {
					t.Errorf("body = %q, want %q", body, want)
				}
				io.WriteString(w, igdbSkyrimJSON)
			case "/game_time_to_beats":
				io.WriteString(w, `[]`)
			}
		})
		d, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "igdb", "9606")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if d.ID != "9606" || d.ExternalIDs["igdb"] != "9606" {
			t.Errorf("data = %+v", d.ItemSummary)
		}
	})

	t.Run("slug canonicalises to the numeric id", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/games":
				body, _ := io.ReadAll(r.Body)
				if want := igdbTestGameFields + ` where slug = "the-elder-scrolls-v-skyrim"; limit 1;`; string(body) != want {
					t.Errorf("body = %q, want %q", body, want)
				}
				io.WriteString(w, igdbSkyrimJSON)
			case "/game_time_to_beats":
				io.WriteString(w, `[{"hastily":3600}]`)
			}
		})
		d, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "igdb", "the-elder-scrolls-v-skyrim")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if d.ID != "9606" || d.ExternalIDs["igdb"] != "9606" {
			t.Errorf("data = %+v", d.ItemSummary)
		}
		if want := "https://www.igdb.com/games/the-elder-scrolls-v-skyrim"; d.ExternalURL != want {
			t.Errorf("external URL = %q, want %q", d.ExternalURL, want)
		}
		if d.Fields["time_main"].Num == nil || *d.Fields["time_main"].Num != 60 {
			t.Errorf("time_main = %+v", d.Fields["time_main"])
		}
	})

	t.Run("unknown id type", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		_, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "mobygames", "1")
		if !errors.Is(err, plugin.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got := rec.dataCount() + rec.tokenRequests(); got != 0 {
			t.Errorf("%d requests, want 0", got)
		}
	})

	t.Run("empty slug", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[]`)
		})
		_, err := (&igdbSource{c: c}).Lookup(context.Background(), KindGame, "igdb", "  ")
		if !errors.Is(err, plugin.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got := rec.dataCount() + rec.tokenRequests(); got != 0 {
			t.Errorf("%d requests, want 0", got)
		}
	})
}

func TestIGDBPeople(t *testing.T) {
	cs, err := (&igdbSource{}).People(context.Background(), KindGame, "9606")
	if err != nil || cs != nil {
		t.Fatalf("People = %+v, %v; want nil, nil", cs, err)
	}
	if _, err := (&igdbSource{}).People(context.Background(), "movie", "9606"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestIGDBRecommendations(t *testing.T) {
	var similar []string
	for i := 0; i < 25; i++ {
		similar = append(similar, fmt.Sprintf(`{"id":%d,"name":"Similar %02d","first_release_date":1320969600,"cover":{"url":"//images.igdb.com/igdb/image/upload/t_thumb/rec.jpg"}}`, 1000+i, i))
	}
	body := `[{"id":9606,"name":"The Elder Scrolls V: Skyrim","similar_games":[` + strings.Join(similar, ",") + `]}]`

	rec := &igdbTestRecorder{}
	c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})
	recs, err := (&igdbSource{c: c}).Recommendations(context.Background(), KindGame, "9606")
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}
	if len(recs) != 20 {
		t.Fatalf("%d recommendations, want the cap of 20", len(recs))
	}
	got := recs[0]
	if got.Source != SourceIGDB || got.ID != "1000" || got.Kind != KindGame {
		t.Errorf("recommendation = %+v", got)
	}
	if len(got.Titles) != 1 || got.Titles[0].Text != "Similar 00" {
		t.Errorf("titles = %+v", got.Titles)
	}
	if got.Fields["year"].Num == nil || *got.Fields["year"].Num != 2011 {
		t.Errorf("year = %+v", got.Fields["year"])
	}
	if want := "https://images.igdb.com/igdb/image/upload/t_cover_big/rec.jpg"; got.CoverURL != want {
		t.Errorf("cover = %q, want %q", got.CoverURL, want)
	}
}

func TestIGDBKindGuards(t *testing.T) {
	s := &igdbSource{}
	ctx := context.Background()
	if _, err := s.Search(ctx, "movie", "doom"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Search err = %v", err)
	}
	if _, err := s.Details(ctx, "movie", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Details err = %v", err)
	}
	if _, err := s.Recommendations(ctx, "movie", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Recommendations err = %v", err)
	}
	if _, err := s.Lookup(ctx, "movie", "igdb", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Lookup err = %v", err)
	}
	// A non-numeric details id is not a lookup and makes no request; the nil
	// client would panic if one were attempted.
	if _, err := s.Details(ctx, KindGame, "the-elder-scrolls-v-skyrim"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Details slug err = %v", err)
	}
	if _, err := s.Recommendations(ctx, KindGame, "not-a-number"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Recommendations id err = %v", err)
	}
}

func TestIGDBExternalUIDAcceptsStringAndNumber(t *testing.T) {
	for _, in := range []string{`"72850"`, `72850`} {
		var e struct {
			UID igdbUID `json:"uid"`
		}
		if err := json.Unmarshal([]byte(`{"uid":`+in+`}`), &e); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if string(e.UID) != "72850" {
			t.Errorf("uid %s = %q", in, e.UID)
		}
	}
}

func TestIGDBBodyLimitAndMalformedJSON(t *testing.T) {
	t.Run("oversized body", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.Copy(w, io.LimitReader(strings.NewReader(strings.Repeat("a", (8<<20)+1)), (8<<20)+1))
		})
		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if !errors.Is(err, plugin.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		rec := &igdbTestRecorder{}
		c := newIGDBTestClient(t, rec, igdbOKToken("token-1", 3600), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{not json`)
		})
		err := c.Post(context.Background(), "games", "fields name;", &[]json.RawMessage{})
		if !errors.Is(err, plugin.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
	})
}
