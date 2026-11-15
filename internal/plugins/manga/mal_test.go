package manga

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	if f.Key == "" || f.Label != want.Label || f.Type != want.Type || !f.Show || !f.Editable || !f.Filterable {
		t.Fatalf("magazine field = %+v", f)
	}
	for _, c := range m.Kinds[0].Columns {
		if c.Key == "magazine" {
			if c.Default {
				t.Fatal("magazine column is on by default")
			}
			goto col
		}
	}
	t.Fatal("no magazine column")
col:
	s := m.Sources[0]
	if !s.KeyOptional || len(s.KeyFields) != 1 || s.KeyFields[0].Name != MALClientIDField ||
		!strings.Contains(s.KeyHelp, "https://myanimelist.net/apiconfig") || !strings.Contains(s.KeyHelp, "MyAnimeList") {
		t.Fatalf("source key = %+v", s)
	}
	if err := plugin.Validate(m); err != nil {
		t.Fatal(err)
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
