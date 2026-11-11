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

func tvServer(t *testing.T) *httptest.Server {
	t.Helper()
	show := `{"id":1,"name":"Dark","type":"Scripted","language":"German","status":"Ended","runtime":60,"premiered":"2017-12-01","ended":"2020-06-27","genres":["Mystery","Drama"],"summary":"<p>Time travel.</p>","image":{"medium":"https://img/m.jpg","original":"https://img/o.jpg"},"network":{"name":"Netflix","country":{"name":"Germany"}},"externals":{"thetvdb":123,"imdb":"tt456"}}`
	cast := `{"id":1,"name":"Dark","type":"Scripted","status":"Ended","image":{"medium":"","original":""},"externals":{},"_embedded":{"cast":[{"person":{"id":2,"name":"Louis Actor","url":"https://tvmaze.com/people/2","image":{"medium":"https://img/p.jpg","original":""}},"character":{"id":3,"name":"Jonas","url":"https://tvmaze.com/characters/3","image":{"medium":"https://img/c.jpg","original":""}}}]}}`
	eps := `[{"season":1,"number":1,"airstamp":"2020-01-02T20:00:00+00:00"},{"season":2,"number":3,"airstamp":"2020-01-09T20:00:00+00:00"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search/shows":
			_, _ = w.Write([]byte(`[{"show":` + show + `}]`))
		case "/shows/1/episodes":
			_, _ = w.Write([]byte(eps))
		case "/shows/1":
			if strings.Contains(r.URL.RawQuery, "embed") {
				_, _ = w.Write([]byte(cast))
			} else {
				_, _ = w.Write([]byte(show))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTVmazeSearchDetailsCastAndSchedule(t *testing.T) {
	srv := tvServer(t)
	s := &tvmazeSource{c: newTVmazeClient(srv.URL, srv.Client())}
	res, err := s.Search(context.Background(), KindShow, "dark")
	if err != nil || len(res) != 1 || res[0].ID != "1" || res[0].Titles[0].Text != "Dark" {
		t.Fatalf("%+v %v", res, err)
	}
	d, err := s.Details(context.Background(), KindShow, "1")
	if err != nil || d.Fields["seasons"].Num == nil || *d.Fields["seasons"].Num != 2 ||
		d.Fields["episodes"].Num == nil || *d.Fields["episodes"].Num != 2 ||
		d.Fields["type"].Text != "series" || d.Fields["network"].Text != "Netflix" ||
		d.Fields["country"].Text != "Germany" || d.ExternalIDs["tvdb"] != "123" || d.ExternalIDs["imdb"] != "tt456" ||
		!strings.Contains(d.Fields["synopsis"].Text, "Time travel.") {
		t.Fatalf("%+v %v", d, err)
	}
	people, err := s.People(context.Background(), KindShow, "1")
	if err != nil || len(people) != 1 || people[0].Person.ID != "character-3" || people[0].With == nil || people[0].With.ID != "person-2" {
		t.Fatalf("%+v %v", people, err)
	}
	airings, err := s.Schedule(context.Background(), KindShow, "1")
	if err != nil || len(airings) != 2 || airings[1].Season != 2 || airings[1].Number != 3 {
		t.Fatalf("%+v %v", airings, err)
	}
}

func TestTVmazeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s := &tvmazeSource{c: newTVmazeClient(srv.URL, srv.Client())}
	if _, err := s.Details(context.Background(), KindShow, "9"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
