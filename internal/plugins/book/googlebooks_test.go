package book

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func gbServer(t *testing.T, routes map[string]string, key *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key != nil {
			*key = r.URL.Query().Get("key")
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gbSource(t *testing.T, routes map[string]string, key string) plugin.Source {
	t.Helper()
	srv := gbServer(t, routes, nil)
	return &googleBooksSource{c: newGoogleBooksClient(srv.URL, key, srv.Client())}
}

func TestGoogleBooksSearchMapsSummaries(t *testing.T) {
	s := gbSource(t, map[string]string{
		"/volumes": `{"items":[{"id":"gb1","volumeInfo":{"title":"Dune","authors":["Frank Herbert"],"publishedDate":"1965-08-02","pageCount":544,"language":"en","imageLinks":{"thumbnail":"http://books.google.com/t.jpg"},"industryIdentifiers":[{"type":"ISBN_13","identifier":"9780441013593"}]}}]}`,
	}, "")
	res, err := s.Search(context.Background(), KindBook, "dune")
	if err != nil || len(res) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	it := res[0]
	if it.Source != SourceGoogleBooks || it.ID != "gb1" || it.Kind != KindBook ||
		it.Titles[0] != (plugin.Title{Lang: "en", Text: "Dune"}) ||
		it.Fields["author"].Text != "Frank Herbert" || it.Fields["year"].Num == nil || *it.Fields["year"].Num != 1965 ||
		it.Fields["pages"].Num == nil || *it.Fields["pages"].Num != 544 ||
		it.CoverURL != "https://books.google.com/t.jpg" {
		t.Fatalf("summary = %+v", it)
	}
}

func TestGoogleBooksDetailsMapsVolume(t *testing.T) {
	s := gbSource(t, map[string]string{
		"/volumes/gb1": `{"id":"gb1","volumeInfo":{"title":"Dune","subtitle":"A Novel","authors":["Frank Herbert","Brian Herbert"],"publisher":"Ace","publishedDate":"2005-08-02","description":"<p>Set on Arrakis.</p>","pageCount":544,"categories":["Fiction / Science Fiction / General","Fiction / Fantasy"],"language":"en","industryIdentifiers":[{"type":"ISBN_10","identifier":"0441013597"}],"imageLinks":{"thumbnail":"http://books.google.com/t.jpg"},"infoLink":"http://books.google.com/books?id=gb1"}}`,
	}, "")
	d, err := s.Details(context.Background(), KindBook, "gb1")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "gb1" || d.Kind != KindBook || d.Titles[0] != (plugin.Title{Lang: "en", Text: "Dune: A Novel"}) ||
		d.Fields["pages"].Num == nil || *d.Fields["pages"].Num != 544 || d.Fields["publisher"].Text != "Ace" ||
		d.Fields["year"].Num == nil || *d.Fields["year"].Num != 2005 || d.Fields["author"].Text != "Frank Herbert" ||
		d.Fields["published_on"].Date == nil || d.Fields["published_on"].Date.Format("2006-01-02") != "2005-08-02" ||
		d.ExternalIDs["isbn"] != "9780441013593" || !strings.Contains(d.Fields["synopsis"].Text, "Set on Arrakis.") ||
		d.CoverURL != "https://books.google.com/t.jpg" || d.ExternalURL != "https://books.google.com/books?id=gb1" {
		t.Fatalf("data = %+v", d)
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Science Fiction" || d.Genres[1] != "Fantasy" {
		t.Fatalf("genres = %v", d.Genres)
	}
	people, err := s.People(context.Background(), KindBook, "gb1")
	if err != nil || len(people) != 2 || people[0].Person.ID == "" || people[0].Person.ID != googleBooksPersonID("Frank Herbert") ||
		people[1].Person.Name != "Brian Herbert" {
		t.Fatalf("people = %+v %v", people, err)
	}
}

func TestGoogleBooksLookupByISBN(t *testing.T) {
	s := gbSource(t, map[string]string{
		"/volumes": `{"items":[{"id":"other","volumeInfo":{"title":"Other","industryIdentifiers":[{"type":"ISBN_13","identifier":"9780000000000"}]}},{"id":"gb1","volumeInfo":{"title":"Dune","industryIdentifiers":[{"type":"ISBN_13","identifier":"9780441013593"}]}}]}`,
	}, "")
	d, err := s.Lookup(context.Background(), KindBook, "isbn", "978-0-441-01359-3")
	if err != nil || d.ID != "gb1" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := s.Lookup(context.Background(), KindBook, "isbn", "9789999999999"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("no match err = %v", err)
	}
}

func TestGoogleBooksAuthorIDsAreStable(t *testing.T) {
	a := googleBooksPersonID("Frank Herbert")
	if a != googleBooksPersonID(" frank   herbert ") || a == googleBooksPersonID("Brian Herbert") || !strings.HasPrefix(a, "gb-") {
		t.Fatalf("ids: %q %q %q", a, googleBooksPersonID(" frank  herbert "), googleBooksPersonID("Brian Herbert"))
	}
}

func TestGoogleBooksKeyIsSentAndRedacted(t *testing.T) {
	var gotKey string
	srv := gbServer(t, map[string]string{"/volumes": `{}`}, &gotKey)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		http.Error(w, "server says secret", http.StatusInternalServerError)
	})
	s := &googleBooksSource{c: newGoogleBooksClient(srv.URL, "sekret", srv.Client())}
	_, err := s.Search(context.Background(), KindBook, "dune")
	if err == nil || !errors.Is(err, plugin.ErrUnavailable) || strings.Contains(err.Error(), "sekret") {
		t.Fatalf("err = %v", err)
	}
	if gotKey != "sekret" {
		t.Fatalf("key = %q", gotKey)
	}
}

func TestGoogleBooksErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not found", http.StatusNotFound, `{}`, plugin.ErrNotFound},
		{"rate limited", http.StatusTooManyRequests, `{}`, plugin.ErrRateLimited},
		{"quota", http.StatusForbidden, `{}`, plugin.ErrRateLimited},
		{"unavailable", http.StatusInternalServerError, `{}`, plugin.ErrUnavailable},
		{"bad json", http.StatusOK, `{`, plugin.ErrBadResponse},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			s := &googleBooksSource{c: newGoogleBooksClient(srv.URL, "", srv.Client())}
			_, err := s.Search(context.Background(), KindBook, "dune")
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestGoogleBooksLookupByISBN10(t *testing.T) {
	s := gbSource(t, map[string]string{
		"/volumes": `{"items":[{"id":"gb1","volumeInfo":{"title":"Dune","industryIdentifiers":[{"type":"ISBN_10","identifier":"0441013597"}]}}]}`,
	}, "")
	d, err := s.Lookup(context.Background(), KindBook, "isbn", "9780441013593")
	if err != nil || d.ID != "gb1" || d.ExternalIDs["isbn"] != "9780441013593" {
		t.Fatalf("%+v %v", d, err)
	}
}
