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

func olServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func olSource(t *testing.T, routes map[string]string) plugin.Source {
	t.Helper()
	srv := olServer(t, routes)
	return &openLibrarySource{c: newOpenLibraryClient(srv.URL, srv.Client())}
}

func TestOpenLibrarySearchMapsSummaries(t *testing.T) {
	s := olSource(t, map[string]string{
		"/search.json": `{"docs":[{"key":"/works/OL1W","title":"Dune","author_name":["Frank Herbert"],"first_publish_year":1965,"cover_i":123,"isbn":["9780441013593"]}]}`,
	})
	res, err := s.Search(context.Background(), KindBook, "dune")
	if err != nil || len(res) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	it := res[0]
	if it.Source != SourceOpenLibrary || it.ID != "OL1W" || it.Kind != KindBook ||
		it.Titles[0] != (plugin.Title{Lang: "en", Text: "Dune"}) ||
		it.Fields["author"].Text != "Frank Herbert" || it.Fields["year"].Num == nil || *it.Fields["year"].Num != 1965 ||
		it.CoverURL != "https://covers.openlibrary.org/b/id/123-L.jpg" ||
		it.URL != "https://openlibrary.org/works/OL1W" {
		t.Fatalf("summary = %+v", it)
	}
}

func TestOpenLibraryDetailsChoosesEditionAndMaps(t *testing.T) {
	s := olSource(t, map[string]string{
		"/works/OL1W.json": `{"title":"Dune","description":"<p>Set on Arrakis.</p>","subjects":["Science fiction","Fantasy","Dune (Imaginary place)","nyt:best-seller"],"covers":[99],"first_publish_date":"1965","authors":[{"author":{"key":"/authors/OL2A"}}]}`,
		"/works/OL1W/editions.json": `{"entries":[
			{"title":"Dune","number_of_pages":544,"isbn_13":["9780441013593"],"isbn_10":["0441013597"],"publishers":["Ace"],"publish_date":"2005-08-02","languages":[{"key":"/languages/eng"}],"covers":[123],"series":["Dune"],"works":[{"key":"/works/OL1W"}]},
			{"title":"Der Wüstenplanet","number_of_pages":600,"isbn_13":["9783453317178"],"languages":[{"key":"/languages/ger"}],"covers":[124],"key":"/books/OL2M"}]}`,
		"/authors/OL2A.json": `{"name":"Frank Herbert","photos":[7],"links":[{"url":"https://authors.example/fh"}]}`,
	})
	d, err := s.Details(context.Background(), KindBook, "OL1W")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "OL1W" || d.Kind != KindBook || d.Fields["pages"].Num == nil || *d.Fields["pages"].Num != 544 ||
		d.Fields["publisher"].Text != "Ace" || d.Fields["series"].Text != "Dune" ||
		d.Fields["year"].Num == nil || *d.Fields["year"].Num != 1965 ||
		d.Fields["author"].Text != "Frank Herbert" || d.ExternalIDs["isbn"] != "9780441013593" {
		t.Fatalf("data = %+v", d)
	}
	if !strings.Contains(d.Fields["synopsis"].Text, "Set on Arrakis.") {
		t.Fatalf("synopsis = %q", d.Fields["synopsis"].Text)
	}
	if len(d.Titles) != 1 || d.Titles[0] != (plugin.Title{Lang: "en", Text: "Dune"}) {
		t.Fatalf("titles = %+v", d.Titles)
	}
	if len(d.Genres) != 2 || d.Genres[0] != "Science Fiction" || d.Genres[1] != "Fantasy" {
		t.Fatalf("genres = %v", d.Genres)
	}
	if len(d.Tags) != 1 || d.Tags[0].Name != "Dune (Imaginary place)" {
		t.Fatalf("tags = %+v", d.Tags)
	}
	if len(d.Relations) != 0 || d.ExternalURL != "https://openlibrary.org/works/OL1W" {
		t.Fatalf("links = %+v", d)
	}
	people, err := s.People(context.Background(), KindBook, "OL1W")
	if err != nil || len(people) != 1 || people[0].Role != "author" || people[0].Person.ID != "OL2A" ||
		people[0].Person.Name != "Frank Herbert" || people[0].Person.ImageURL != "https://covers.openlibrary.org/a/olid/OL2A-M.jpg" ||
		people[0].Person.URL != "https://openlibrary.org/authors/OL2A" {
		t.Fatalf("people = %+v %v", people, err)
	}
}

func TestOpenLibraryGermanEditionAddsTranslatedTitle(t *testing.T) {
	s := olSource(t, map[string]string{
		"/works/OL1W.json":          `{"title":"Dune","authors":[],"covers":[]}`,
		"/works/OL1W/editions.json": `{"entries":[{"title":"Der Wüstenplanet","number_of_pages":600,"isbn_13":["9783453317178"],"languages":[{"key":"/languages/ger"}],"covers":[124],"key":"/books/OL2M"}]}`,
	})
	d, err := s.Details(context.Background(), KindBook, "OL1W")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Titles) != 2 || d.Titles[1] != (plugin.Title{Lang: "de", Text: "Der Wüstenplanet"}) {
		t.Fatalf("titles = %+v", d.Titles)
	}
}

func TestOpenLibraryMissingAuthorIsSkipped(t *testing.T) {
	s := olSource(t, map[string]string{
		"/works/OL1W.json":          `{"title":"Dune","authors":[{"author":{"key":"/authors/OL404A"}}]}`,
		"/works/OL1W/editions.json": `{"entries":[{"title":"Dune","number_of_pages":544,"languages":[{"key":"/languages/eng"}],"covers":[123]}]}`,
	})
	d, err := s.Details(context.Background(), KindBook, "OL1W")
	if err != nil {
		t.Fatal(err)
	}
	if d.Fields["author"].Text != "" || len(d.Relations) != 0 {
		t.Fatalf("data = %+v", d)
	}
}

func TestOpenLibraryLookupByISBN(t *testing.T) {
	s := olSource(t, map[string]string{
		"/isbn/9780441013593.json": `{"title":"Dune","number_of_pages":544,"isbn_13":["9780441013593"],"publishers":["Ace"],"publish_date":"2005-08-02","languages":[{"key":"/languages/eng"}],"covers":[123],"works":[{"key":"/works/OL1W"}]}`,
		"/works/OL1W.json":         `{"title":"Dune","authors":[{"author":{"key":"/authors/OL2A"}}]}`,
		"/authors/OL2A.json":       `{"name":"Frank Herbert"}`,
	})
	d, err := s.Lookup(context.Background(), KindBook, "isbn", "978-0-441-01359-3")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "OL1W" || d.Fields["pages"].Num == nil || *d.Fields["pages"].Num != 544 ||
		d.Fields["publisher"].Text != "Ace" || d.ExternalIDs["isbn"] != "9780441013593" {
		t.Fatalf("data = %+v", d)
	}
}

func TestOpenLibraryErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not found", http.StatusNotFound, `{}`, plugin.ErrNotFound},
		{"rate limited", http.StatusTooManyRequests, `{}`, plugin.ErrRateLimited},
		{"unavailable", http.StatusInternalServerError, `{}`, plugin.ErrUnavailable},
		{"bad json", http.StatusOK, `{`, plugin.ErrBadResponse},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			s := &openLibrarySource{c: newOpenLibraryClient(srv.URL, srv.Client())}
			_, err := s.Search(context.Background(), KindBook, "dune")
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}
