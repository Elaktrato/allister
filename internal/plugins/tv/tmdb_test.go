package tv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/tmdb"
)

func tmdbServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("api_key") != "key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func keyFunc(key string) tmdb.KeyFunc {
	return func(context.Context) (string, error) { return key, nil }
}

func TestTMDBDetailsCastRecommendations(t *testing.T) {
	body := `{"id":1,"name":"Dark","original_name":"Dark Original","overview":"Time travel.","status":"Ended","type":"Scripted","first_air_date":"2017-12-01","last_air_date":"2020-06-27","number_of_seasons":3,"number_of_episodes":26,"episode_run_time":[60],"genres":[{"name":"Mystery"}],"networks":[{"name":"Netflix"}],"origin_country":["DE"],"poster_path":"/p.jpg","credits":{"cast":[{"id":2,"name":"Louis Actor","character":"Jonas","credit_id":"c1","profile_path":"/a.jpg","order":0}]},"recommendations":{"results":[{"id":2,"name":"1899","poster_path":"/r.jpg"}]},"keywords":{"results":[{"name":"Time Travel"}]},"external_ids":{"tvdb_id":123,"imdb_id":"tt1"}}`
	srv := tmdbServer(t, body)
	s := &tmdbSource{c: tmdb.New(srv.URL, keyFunc("key"), srv.Client())}
	d, err := s.Details(context.Background(), KindShow, "1")
	if err != nil || d.Fields["seasons"].Num == nil || *d.Fields["seasons"].Num != 3 ||
		d.Fields["episodes"].Num == nil || *d.Fields["episodes"].Num != 26 ||
		d.Fields["type"].Text != "series" || d.ExternalIDs["tvdb"] != "123" ||
		len(d.Titles) != 2 || d.Titles[1].Lang != "original" || len(d.Tags) != 1 || d.Tags[0].Name != "Time Travel" {
		t.Fatalf("%+v %v", d, err)
	}
	people, err := s.People(context.Background(), KindShow, "1")
	if err != nil || len(people) != 1 || people[0].Person.ID != "character-c1" || people[0].With.ID != "person-2" {
		t.Fatalf("%+v %v", people, err)
	}
	recs, err := s.Recommendations(context.Background(), KindShow, "1")
	if err != nil || len(recs) != 1 || recs[0].ID != "2" {
		t.Fatalf("%+v %v", recs, err)
	}
}

func TestTMDBKeyRedactedAndErrors(t *testing.T) {
	srv := tmdbServer(t, `{}`)
	c := tmdb.New(srv.URL, keyFunc("wrong"), srv.Client())
	if _, err := (&tmdbSource{c: c}).Details(context.Background(), KindShow, "1"); !errors.Is(err, plugin.ErrNotConfigured) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("err = %v", err)
	}
}

func TestTMDBLookupFindsExternalID(t *testing.T) {
	var findSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/find/tt1":
			findSource = r.URL.Query().Get("external_source")
			_, _ = w.Write([]byte(`{"tv_results":[{"id":7}]}`))
		case "/tv/7":
			_, _ = w.Write([]byte(`{"id":7,"name":"Dark","external_ids":{"imdb_id":"tt1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := &tmdbSource{c: tmdb.New(srv.URL, keyFunc("key"), srv.Client())}
	d, err := s.Lookup(context.Background(), KindShow, "imdb", "tt1")
	if err != nil || findSource != "imdb_id" || d.ExternalIDs["tmdb"] != "7" {
		t.Fatalf("%+v %v (external_source = %q)", d, err, findSource)
	}
	if _, err := s.Lookup(context.Background(), KindShow, "tvdb", "404"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("missing external id err = %v, want ErrNotFound", err)
	}
}
