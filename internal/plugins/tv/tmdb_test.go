package tv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
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

func TestTMDBDetailsCastRecommendations(t *testing.T) {
	body := `{"id":1,"name":"Dark","original_name":"Dark Original","overview":"Time travel.","status":"Ended","type":"Scripted","first_air_date":"2017-12-01","last_air_date":"2020-06-27","number_of_seasons":3,"number_of_episodes":26,"episode_run_time":[60],"genres":[{"name":"Mystery"}],"networks":[{"name":"Netflix"}],"origin_country":["DE"],"poster_path":"/p.jpg","credits":{"cast":[{"id":2,"name":"Louis Actor","character":"Jonas","credit_id":"c1","profile_path":"/a.jpg","order":0}]},"recommendations":{"results":[{"id":2,"name":"1899","poster_path":"/r.jpg"}]},"keywords":{"keywords":[{"name":"Time Travel"}]},"external_ids":{"tvdb_id":123,"imdb_id":"tt1"}}`
	srv := tmdbServer(t, body)
	c := newTMDBClient(srv.URL, "key", srv.Client())
	s := &tmdbSource{c: c}
	d, err := s.Details(context.Background(), KindShow, "1")
	if err != nil || d.Fields["seasons"].Num == nil || *d.Fields["seasons"].Num != 3 ||
		d.Fields["episodes"].Num == nil || *d.Fields["episodes"].Num != 26 ||
		d.Fields["type"].Text != "series" || d.ExternalIDs["tvdb"] != "123" ||
		len(d.Titles) != 2 || d.Titles[1].Lang != "original" {
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
	c := newTMDBClient(srv.URL, "wrong", srv.Client())
	if _, err := (&tmdbSource{c: c}).Details(context.Background(), KindShow, "1"); !errors.Is(err, plugin.ErrBadResponse) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("err = %v", err)
	}
}
