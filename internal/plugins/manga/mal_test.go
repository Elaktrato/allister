package manga

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

func idFn(id string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return id, nil }
}

func TestMALSerialization(t *testing.T) {
	var gotPath, gotQuery, gotHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotHdr = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-MAL-CLIENT-ID")
		w.Write([]byte(`{"id":2,"serialization":[{"node":{"id":1,"name":"Young Animal"}},{"node":{"id":2,"name":"Other Mag"}}]}`))
	}))
	defer srv.Close()
	c := newMALClient(srv.URL+"/v2", srv.Client(), idFn("id1"), time.Nanosecond)
	got, err := c.Serialization(context.Background(), "2")
	if err != nil || len(got) != 2 || got[0] != "Young Animal" || got[1] != "Other Mag" {
		t.Fatalf("%v %v", got, err)
	}
	if gotPath != "/v2/manga/2" || gotQuery != "fields=serialization" || gotHdr != "id1" {
		t.Fatalf("path %q query %q header %q", gotPath, gotQuery, gotHdr)
	}
}

func TestMALNeverInURL(t *testing.T) {
	const secret = "sekrit-client-id"
	var urls []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		urls = append(urls, r.URL.String())
		mu.Unlock()
		switch r.URL.Path {
		case "/v2/manga/404":
			http.NotFound(w, r)
		case "/v2/manga/500":
			http.Error(w, "boom "+secret, 500)
		default:
			io.WriteString(w, "{not json")
		}
	}))
	defer srv.Close()
	c := newMALClient(srv.URL+"/v2", srv.Client(), idFn(secret), time.Nanosecond)
	for _, id := range []string{"404", "500", "7"} {
		_, err := c.Serialization(context.Background(), id)
		if err == nil {
			t.Fatalf("%s: no error", id)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: error leaks the id: %v", id, err)
		}
	}
	for _, u := range urls {
		if strings.Contains(u, secret) {
			t.Fatalf("url leaks the id: %s", u)
		}
	}
	if len(urls) != 3 {
		t.Fatalf("requests = %d", len(urls))
	}
	// a transport error never names the id either
	srv.Close()
	_, err := c.Serialization(context.Background(), "7")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error: %v", err)
	}
}

func TestMALErrorKinds(t *testing.T) {
	status := 404
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := newMALClient(srv.URL+"/v2", srv.Client(), idFn("x"), time.Nanosecond)
	for st, want := range map[int]error{404: plugin.ErrNotFound, 429: plugin.ErrRateLimited, 503: plugin.ErrUnavailable, 401: plugin.ErrNotConfigured} {
		status = st
		if _, err := c.Serialization(context.Background(), "1"); !errors.Is(err, want) {
			t.Fatalf("%d: %v", st, err)
		}
	}
	if _, err := newMALClient(srv.URL, srv.Client(), idFn(""), time.Nanosecond).Serialization(context.Background(), "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := c.Serialization(context.Background(), "../x"); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestMALPacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"serialization":[]}`))
	}))
	defer srv.Close()
	c := newMALClient(srv.URL+"/v2", srv.Client(), idFn("x"), 150*time.Millisecond)
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.Serialization(context.Background(), "1"); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 280*time.Millisecond {
		t.Fatalf("3 requests took %v, want paced", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Serialization(ctx, "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestMagazineOnDetails(t *testing.T) {
	anilist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "manga.json"))
	}))
	defer anilist.Close()
	mode := "ok"
	mal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/manga/13" {
			t.Errorf("path %q", r.URL.Path)
		}
		switch mode {
		case "404":
			http.NotFound(w, r)
		case "500":
			http.Error(w, "x", 500)
		case "bad":
			io.WriteString(w, "{nope")
		default:
			io.WriteString(w, `{"serialization":[{"node":{"name":"Weekly Shonen Jump"}}]}`)
		}
	}))
	defer mal.Close()
	build := func(id func(context.Context) (string, error)) plugin.Source {
		return New(Options{AniListURL: anilist.URL, HTTP: anilist.Client(), MALURL: mal.URL + "/v2", MALClientID: id, MALInterval: time.Nanosecond}).Source("anilist")
	}
	ctx := context.Background()
	d, err := build(idFn("id1")).Details(ctx, KindManga, "30013")
	if err != nil || d.Fields["magazine"].Text != "Weekly Shonen Jump" {
		t.Fatalf("%+v %v", d.Fields["magazine"], err)
	}
	for _, m := range []string{"404", "500", "bad"} {
		mode = m
		d, err := build(idFn("id1")).Details(ctx, KindManga, "30013")
		if err != nil || d.ID != "30013" {
			t.Fatalf("%s: %v", m, err)
		}
		if _, ok := d.Fields["magazine"]; ok {
			t.Fatalf("%s: magazine set", m)
		}
	}
	mode = "ok"
	for name, id := range map[string]func(context.Context) (string, error){
		"nil": nil, "empty": idFn(""), "error": func(context.Context) (string, error) { return "", errors.New("store down") },
	} {
		d, err := build(id).Details(ctx, KindManga, "30013")
		if err != nil || d.ID != "30013" {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := d.Fields["magazine"]; ok {
			t.Fatalf("%s: magazine set", name)
		}
	}
}

func TestMagazineManifest(t *testing.T) {
	m := manifest()
	var f plugin.FieldDef
	for _, x := range m.Kinds[0].Fields {
		if x.Key == "magazine" {
			f = x
		}
	}
	want := plugin.FieldDef{Key: "magazine", Label: "Magazine", Type: plugin.FieldText, Show: true, Editable: true, Filterable: true}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("magazine field = %+v, want %+v", f, want)
	}
	var col plugin.ColumnDef
	for _, c := range m.Kinds[0].Columns {
		if c.Key == "magazine" {
			col = c
		}
	}
	if wantCol := (plugin.ColumnDef{Key: "magazine", Hide: "lg"}); col != wantCol {
		t.Fatalf("magazine column = %+v, want %+v", col, wantCol)
	}
	s := m.Sources[0]
	if !s.KeyOptional || s.KeyLabel != "MyAnimeList" ||
		!reflect.DeepEqual(s.KeyFields, []plugin.KeyField{{Name: MALClientIDField, Label: "MyAnimeList Client ID", Secret: true}}) {
		t.Fatalf("source key = %+v", s)
	}
	if !strings.HasPrefix(s.KeyHelp, "Fills the manga Magazine field from MyAnimeList (data courtesy of MyAnimeList)") ||
		strings.Contains(s.KeyHelp, "Optional") || !strings.Contains(s.KeyHelp, "https://myanimelist.net/apiconfig") {
		t.Fatalf("key help = %q", s.KeyHelp)
	}
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
	}
}

// TestRefusedClientIDIsLogged: a refused Client ID (401) and a failing
// credential lookup are logged without the id; the details still succeed
// and carry no magazine. 404 and an unset id stay silent.
func TestRefusedClientIDIsLogged(t *testing.T) {
	const secret = "sekrit-client-id"
	anilist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "manga.json"))
	}))
	defer anilist.Close()
	status := 401
	mal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer mal.Close()
	cases := []struct {
		name    string
		id      func(context.Context) (string, error)
		status  int
		wantLog bool
	}{
		{"refused", idFn(secret), 401, true},
		{"forbidden", idFn(secret), 403, true},
		{"lookup fails", func(context.Context) (string, error) { return "", errors.New("store down") }, 200, true},
		{"not found", idFn(secret), 404, false},
		{"unset", idFn(""), 200, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)
			status = c.status
			s := New(Options{AniListURL: anilist.URL, HTTP: anilist.Client(), MALURL: mal.URL, MALClientID: c.id, MALInterval: time.Nanosecond}).Source("anilist")
			d, err := s.Details(context.Background(), KindManga, "30013")
			if err != nil || d.ID != "30013" {
				t.Fatalf("details: %v", err)
			}
			if _, ok := d.Fields["magazine"]; ok {
				t.Fatal("magazine set")
			}
			out := buf.String()
			if (out != "") != c.wantLog {
				t.Fatalf("log = %q, want log %v", out, c.wantLog)
			}
			if strings.Contains(out, secret) {
				t.Fatalf("log leaks the id: %q", out)
			}
		})
	}
}

// TestMALPacingCancelWhileQueued: a waiter whose context is cancelled leaves
// the queue promptly even though earlier waiters still hold their slots.
func TestMALPacingCancelWhileQueued(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"serialization":[]}`))
	}))
	defer srv.Close()
	c := newMALClient(srv.URL+"/v2", srv.Client(), idFn("x"), 600*time.Millisecond)
	if _, err := c.Serialization(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	go c.Serialization(context.Background(), "1") // queued behind the first
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Serialization(ctx, "1"); done <- err }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || time.Since(start) > 200*time.Millisecond {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter still blocked")
	}
}

func TestCredentialResolverSuppliesClientID(t *testing.T) {
	var gotSrc, gotField string
	mal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"serialization":[{"node":{"name":"M"}}]}`)
	}))
	defer mal.Close()
	anilist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "manga.json"))
	}))
	defer anilist.Close()
	s := New(Options{AniListURL: anilist.URL, HTTP: anilist.Client(), MALURL: mal.URL, MALInterval: time.Nanosecond,
		Credential: func(_ context.Context, src, field string) (string, error) {
			gotSrc, gotField = src, field
			return "k", nil
		}}).Source("anilist")
	d, err := s.Details(context.Background(), KindManga, "30013")
	if err != nil || d.Fields["magazine"].Text != "M" || gotSrc != "anilist" || gotField != MALClientIDField {
		t.Fatalf("%v %v %q %q", d.Fields["magazine"], err, gotSrc, gotField)
	}
}
