package movie

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/tmdb"
)

const movieDetailsBody = `{
  "id": 1,
  "title": "The Matrix",
  "original_title": "Matrix Original",
  "overview": "A hacker learns the truth.",
  "status": "Released",
  "release_date": "1999-03-31",
  "runtime": 136,
  "original_language": "en",
  "poster_path": "/m.jpg",
  "genres": [{"name": "Action"}, {"name": "Science Fiction"}],
  "production_companies": [{"name": "Warner Bros."}, {"name": "Village Roadshow"}],
  "production_countries": [{"iso_3166_1": "US", "name": "United States of America"}],
  "spoken_languages": [
    {"english_name": "German", "iso_639_1": "de", "name": "Deutsch"},
    {"english_name": "English", "iso_639_1": "en", "name": "English"}
  ],
  "belongs_to_collection": {"id": 10, "name": "The Matrix Collection"},
  "credits": {
    "cast": [
      {"id": 2, "name": "Keanu Reeves", "character": "Neo", "credit_id": "c1", "profile_path": "/a.jpg", "order": 0},
      {"id": 3, "name": "Hugo Weaving", "character": "Agent Smith", "credit_id": "c2", "order": 12},
      {"id": 6, "name": "Actor Six", "character": "Six", "order": 20}
    ],
    "crew": [
      {"id": 4, "name": "Lana Wachowski", "job": "Director", "credit_id": "c3", "profile_path": "/d.jpg"},
      {"id": 4, "name": "Lana Wachowski", "job": "Director", "credit_id": "c4"},
      {"id": 4, "name": "Lana Wachowski", "job": "Writer", "credit_id": "c5"},
      {"id": 5, "name": "Editor Person", "job": "Editor", "credit_id": "c6"}
    ]
  },
  "keywords": {"keywords": [{"name": "Cyberpunk"}]},
  "recommendations": {"results": [{"id": 2, "title": "The Matrix Reloaded", "original_title": "Reloaded Original", "release_date": "2003-05-15", "poster_path": "/r.jpg"}]},
  "external_ids": {"imdb_id": "tt0133093"}
}`

func movieKey(key string) tmdb.KeyFunc {
	return func(context.Context) (string, error) { return key, nil }
}

func TestTMDBMovieSearchDetailsCreditsAndRecommendations(t *testing.T) {
	var searchQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("api_key") != "key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/search/movie":
			searchQuery = r.URL.Query().Get("query")
			if r.URL.Query().Get("include_adult") != "false" {
				http.Error(w, "adult filter missing", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"The Matrix","original_title":"Matrix Original","release_date":"1999-03-31","poster_path":"/m.jpg"}]}`))
		case "/movie/1":
			if got := r.URL.Query().Get("append_to_response"); got != "credits,recommendations,external_ids,keywords" {
				http.Error(w, "append_to_response = "+got, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(movieDetailsBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := &tmdbSource{c: tmdb.New(srv.URL, movieKey("key"), srv.Client())}
	ctx := context.Background()

	search, err := s.Search(ctx, KindMovie, "  matrix  ")
	if err != nil || len(search) != 1 {
		t.Fatalf("search = %+v, %v", search, err)
	}
	if searchQuery != "matrix" {
		t.Fatalf("query = %q, want trimmed", searchQuery)
	}
	got := search[0]
	if got.Source != SourceTMDB || got.ID != "1" || got.Kind != KindMovie || len(got.Titles) != 2 ||
		got.Titles[0] != (plugin.Title{Lang: "en", Text: "The Matrix"}) ||
		got.Titles[1] != (plugin.Title{Lang: "original", Text: "Matrix Original"}) ||
		got.CoverURL != "https://image.tmdb.org/t/p/w500/m.jpg" ||
		got.URL != "https://www.themoviedb.org/movie/1" {
		t.Fatalf("search summary = %+v", got)
	}
	if y := got.Fields["year"].Num; y == nil || *y != 1999 {
		t.Fatalf("search year = %+v", got.Fields["year"])
	}

	d, err := s.Details(ctx, KindMovie, "1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != SourceTMDB || d.ID != "1" || d.Kind != KindMovie ||
		len(d.Titles) != 2 || d.Titles[0].Text != "The Matrix" || d.Titles[1] != (plugin.Title{Lang: "original", Text: "Matrix Original"}) {
		t.Fatalf("details summary = %+v", d.ItemSummary)
	}
	if y := d.Fields["year"].Num; y == nil || *y != 1999 {
		t.Fatalf("year = %+v", d.Fields["year"])
	}
	if rt := d.Fields["runtime"].Num; rt == nil || *rt != 136 {
		t.Fatalf("runtime = %+v", d.Fields["runtime"])
	}
	wantDate := time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC)
	if day := d.Fields["released_on"].Date; day == nil || !day.Equal(wantDate) {
		t.Fatalf("released_on = %+v", d.Fields["released_on"])
	}
	for key, want := range map[string]string{
		"director": "Lana Wachowski", "studio": "Warner Bros.", "country": "United States of America",
		"language": "English", "collection": "The Matrix Collection", "release_status": "released",
		"synopsis": "A hacker learns the truth.",
	} {
		if got := d.Fields[key].Text; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Action" || d.Genres[1] != "Science Fiction" {
		t.Fatalf("genres = %+v", d.Genres)
	}
	if len(d.Tags) != 1 || d.Tags[0].Name != "Cyberpunk" {
		t.Fatalf("tags = %+v", d.Tags)
	}
	if len(d.ExternalIDs) != 2 || d.ExternalIDs["tmdb"] != "1" || d.ExternalIDs["imdb"] != "tt0133093" {
		t.Fatalf("external ids = %+v", d.ExternalIDs)
	}
	if d.CoverURL != "https://image.tmdb.org/t/p/w500/m.jpg" || d.ExternalURL != "https://www.themoviedb.org/movie/1" {
		t.Fatalf("urls = %q %q", d.CoverURL, d.ExternalURL)
	}

	people, err := s.People(ctx, KindMovie, "1")
	if err != nil || len(people) != 5 {
		t.Fatalf("people = %+v, %v", people, err)
	}
	cast := people[0]
	if cast.Role != "actor" || cast.Detail != "MAIN" || cast.Person.ID != "character-c1" ||
		cast.Person.Name != "Neo" || cast.With == nil || cast.With.ID != "person-2" ||
		cast.With.Name != "Keanu Reeves" || cast.With.ImageURL != "https://image.tmdb.org/t/p/w185/a.jpg" ||
		cast.WithRole != "actor" {
		t.Fatalf("lead cast = %+v", cast)
	}
	if people[1].Detail != "SUPPORTING" || people[1].Person.ID != "character-c2" {
		t.Fatalf("supporting cast = %+v", people[1])
	}
	if people[2].Person.ID != "character-6-Six" {
		t.Fatalf("fallback character id = %+v", people[2])
	}
	director := people[3]
	if director.Role != "director" || director.Person.ID != "person-4" || director.Person.Name != "Lana Wachowski" ||
		director.Detail != "Director" || director.With != nil || director.Person.ImageURL != "https://image.tmdb.org/t/p/w185/d.jpg" {
		t.Fatalf("director = %+v", director)
	}
	if writer := people[4]; writer.Role != "writer" || writer.Person.ID != "person-4" || writer.Detail != "Writer" {
		t.Fatalf("writer = %+v", people[4])
	}

	recs, err := s.Recommendations(ctx, KindMovie, "1")
	if err != nil || len(recs) != 1 {
		t.Fatalf("recommendations = %+v, %v", recs, err)
	}
	rec := recs[0]
	if rec.Source != SourceTMDB || rec.ID != "2" || rec.Kind != KindMovie ||
		len(rec.Titles) != 2 || rec.Titles[0].Text != "The Matrix Reloaded" ||
		rec.CoverURL != "https://image.tmdb.org/t/p/w500/r.jpg" {
		t.Fatalf("recommendation = %+v", rec)
	}
	if y := rec.Fields["year"].Num; y == nil || *y != 2003 {
		t.Fatalf("recommendation year = %+v", rec.Fields["year"])
	}
}

func TestTMDBMovieLookupTMDBAndIMDB(t *testing.T) {
	var findSource string
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("api_key") != "key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/find/tt1":
			findSource = r.URL.Query().Get("external_source")
			_, _ = w.Write([]byte(`{"movie_results":[{"id":1},{"id":2}]}`))
		case "/movie/1":
			_, _ = w.Write([]byte(`{"id":1,"title":"The Matrix","external_ids":{"imdb_id":"tt1"}}`))
		case "/movie/9":
			_, _ = w.Write([]byte(`{"id":9,"title":"Direct Lookup"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := &tmdbSource{c: tmdb.New(srv.URL, movieKey("key"), srv.Client())}
	ctx := context.Background()

	direct, err := s.Lookup(ctx, KindMovie, "tmdb", "9")
	if err != nil || direct.ID != "9" || len(direct.Titles) == 0 || direct.Titles[0].Text != "Direct Lookup" {
		t.Fatalf("tmdb lookup = %+v, %v", direct, err)
	}

	found, err := s.Lookup(ctx, KindMovie, "imdb", "tt1")
	if err != nil || found.ID != "1" || findSource != "imdb_id" ||
		found.ExternalIDs["tmdb"] != "1" || found.ExternalIDs["imdb"] != "tt1" {
		t.Fatalf("imdb lookup = %+v, %v (external_source = %q)", found, err, findSource)
	}

	before := requests.Load()
	if _, err := s.Lookup(ctx, KindMovie, "tvdb", "123"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("unsupported id type err = %v, want ErrNotFound", err)
	}
	if requests.Load() != before {
		t.Fatalf("unsupported id type made %d requests", requests.Load()-before)
	}
}

func TestTMDBMovieNeedsKey(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	s := &tmdbSource{c: tmdb.New(srv.URL, movieKey(""), srv.Client())}

	if _, err := s.Details(context.Background(), KindMovie, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func TestTMDBMovieKeyRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	s := &tmdbSource{c: tmdb.New(srv.URL, movieKey("s3cr3t-key"), srv.Client())}

	_, err := s.Details(context.Background(), KindMovie, "1")
	if !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-key") {
		t.Fatalf("error leaks the key: %v", err)
	}
}

func TestTMDBMovieKeywordsBothShapes(t *testing.T) {
	cases := []struct{ name, keywords string }{
		{"keywords", `{"keywords":[{"name":"Cyberpunk"}]}`},
		{"results", `{"results":[{"name":"Cyberpunk"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"id":1,"title":"The Matrix","keywords":` + c.keywords + `}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			s := &tmdbSource{c: tmdb.New(srv.URL, movieKey("key"), srv.Client())}
			d, err := s.Details(context.Background(), KindMovie, "1")
			if err != nil || len(d.Tags) != 1 || d.Tags[0].Name != "Cyberpunk" {
				t.Fatalf("tags = %+v, %v", d.Tags, err)
			}
		})
	}
}

func TestTMDBMovieReleaseStatusAndLanguage(t *testing.T) {
	for _, c := range []struct{ status, want string }{
		{"Released", "released"},
		{"Planned", "upcoming"},
		{"In Production", "upcoming"},
		{"Post Production", "upcoming"},
		{"Rumored", "upcoming"},
		{"Canceled", "other"},
		{"Something Else", "other"},
	} {
		if got := tmdbReleaseStatus(c.status); got != c.want {
			t.Fatalf("tmdbReleaseStatus(%q) = %q, want %q", c.status, got, c.want)
		}
	}
	if got := tmdbLanguage(tmdbMovie{OriginalLanguage: "fr", SpokenLanguages: []tmdbSpokenLanguage{{EnglishName: "French", Code: "fr"}}}); got != "French" {
		t.Fatalf("matching language = %q", got)
	}
	if got := tmdbLanguage(tmdbMovie{OriginalLanguage: "fr", SpokenLanguages: []tmdbSpokenLanguage{{EnglishName: "German", Code: "de"}}}); got != "German" {
		t.Fatalf("first English name = %q", got)
	}
	if got := tmdbLanguage(tmdbMovie{OriginalLanguage: "fr"}); got != "fr" {
		t.Fatalf("language code fallback = %q", got)
	}
}
