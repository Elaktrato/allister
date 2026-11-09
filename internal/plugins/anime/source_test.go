package anime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "provider", "anilist", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// route picks a fixture by the GraphQL query the client sent.
func route(query string) string {
	switch {
	case strings.Contains(query, "characters("):
		return "cast.json"
	case strings.Contains(query, "Page("):
		return "search.json"
	case strings.Contains(query, "idMal: $idMal"):
		return "anime_mal.json"
	}
	return "anime.json"
}

// newSource returns the anilist source against a fake AniList that answers
// with the fixture for each query (or with body/status when body is set).
func newSource(t *testing.T, body []byte, status int, queries *[]string) plugin.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(b, &req)
		if queries != nil {
			*queries = append(*queries, req.Query)
		}
		out := body
		if out == nil {
			out = fixture(t, route(req.Query))
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	s := New(Options{AniListURL: srv.URL, HTTP: srv.Client()}).Source("anilist")
	if s == nil {
		t.Fatal("no anilist source")
	}
	return s
}

func titles(ts []plugin.Title) map[string]string {
	m := map[string]string{}
	for _, t := range ts {
		m[t.Lang] = t.Text
	}
	return m
}

func ctx() context.Context { return context.Background() }

func TestSourceSearchMapsToSummaries(t *testing.T) {
	s := newSource(t, nil, 0, nil)
	res, err := s.Search(ctx(), "anime", "frieren")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len = %d", len(res))
	}
	r := res[0]
	if r.Source != "anilist" || r.Kind != "anime" || r.ID != "154587" {
		t.Errorf("identity = %+v", r)
	}
	if tm := titles(r.Titles); tm["ja"] != "葬送のフリーレン" || tm["ja-latn"] != "Sousou no Frieren" || tm["en"] != "Frieren: Beyond Journey’s End" {
		t.Errorf("titles = %v", r.Titles)
	}
	if r.Fields["type"].Text != "TV" || r.Fields["studio"].Text != "MADHOUSE" {
		t.Errorf("fields = %+v", r.Fields)
	}
	if n := r.Fields["episodes"].Num; n == nil || *n != 28 {
		t.Errorf("episodes = %v", n)
	}
	if n := r.Fields["year"].Num; n == nil || *n != 2023 {
		t.Errorf("year = %v", n)
	}
	if r.Fields["synopsis"].Text == "" || strings.Contains(r.Fields["synopsis"].Text, "<") {
		t.Errorf("synopsis = %q", r.Fields["synopsis"].Text)
	}
	if !strings.HasPrefix(r.CoverURL, "https://") {
		t.Errorf("cover = %q", r.CoverURL)
	}
	if _, ok := res[1].Fields["episodes"]; ok {
		t.Errorf("unknown episodes must be absent: %+v", res[1].Fields)
	}
}

func TestSourceDetailsMapsFieldsTitlesRelations(t *testing.T) {
	s := newSource(t, nil, 0, nil)
	d, err := s.Details(ctx(), "anime", "154587")
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != "anilist" || d.Kind != "anime" || d.ID != "154587" {
		t.Errorf("identity = %+v", d.ItemSummary)
	}
	if d.ExternalURL != "https://anilist.co/anime/154587" {
		t.Errorf("external url = %q", d.ExternalURL)
	}
	if len(d.ExternalIDs) != 1 || d.ExternalIDs["mal"] != "52991" {
		t.Errorf("external ids = %v", d.ExternalIDs)
	}
	if d.Fields["source_material"].Text != "MANGA" {
		t.Errorf("source_material = %+v", d.Fields["source_material"])
	}
	for key, want := range map[string]string{"started_on": "2023-09-29", "ended_on": "2024-03-22"} {
		v := d.Fields[key].Date
		if v == nil || v.Format("2006-01-02") != want || !v.Equal(plugin.DateOnly(*v)) || v.Location() != time.UTC {
			t.Errorf("%s = %v, want %s at UTC midnight", key, v, want)
		}
	}
	rel := map[string]plugin.Relation{}
	for _, r := range d.Relations {
		if r.Item.Source != "anilist" {
			t.Errorf("relation source = %q", r.Item.Source)
		}
		rel[r.Item.ID] = r
	}
	if r := rel["118586"]; r.Kind != "source" || r.Item.Kind != "manga" || r.Item.Fields["type"].Text != "MANGA" {
		t.Errorf("source relation = %+v", r)
	}
	if r := rel["182255"]; r.Kind != "sequel" || r.Item.Kind != "anime" || r.Item.Fields["type"].Text != "TV" || titles(r.Item.Titles)["ja-latn"] == "" {
		t.Errorf("sequel relation = %+v", r)
	}
	if r := rel["170068"]; r.Kind != "other" || r.Item.Kind != "anime" {
		t.Errorf("other relation = %+v", r)
	}
}

func TestSourceDetailsMapsGenresAndTagsWithRankAndSpoiler(t *testing.T) {
	var queries []string
	s := newSource(t, nil, 0, &queries)
	d, err := s.Details(ctx(), "anime", "154587")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Genres, ",") != "Adventure,Drama,Fantasy" {
		t.Errorf("genres = %v", d.Genres)
	}
	if len(d.Tags) != 28 {
		t.Fatalf("tags = %d", len(d.Tags))
	}
	if tg := d.Tags[0]; tg.Name != "Travel" || tg.Rank == nil || *tg.Rank != 96 || tg.Spoiler {
		t.Errorf("tag 0 = %+v", tg)
	}
	var spoiler bool
	for _, tg := range d.Tags {
		if tg.Name == "Time Skip" {
			spoiler = tg.Spoiler
		}
	}
	if !spoiler {
		t.Error("Time Skip must be flagged as spoiler")
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "genres") || !strings.Contains(queries[0], "tags { name rank isMediaSpoiler }") {
		t.Errorf("query = %v", queries)
	}
}

func TestSourceRecommendations(t *testing.T) {
	var queries []string
	s := newSource(t, nil, 0, &queries)
	recs, err := s.Recommendations(ctx(), "anime", "154587")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("len = %d", len(recs))
	}
	r := recs[0]
	if r.Source != "anilist" || r.Kind != "anime" || r.ID != "21827" || titles(r.Titles)["en"] != "Violet Evergarden" || r.CoverURL == "" {
		t.Errorf("rec = %+v", r)
	}
	if len(queries) != 1 {
		t.Errorf("requests = %d, want 1 (same single request as Details until it is split)", len(queries))
	}
}

func TestSourcePeopleCarriesVoiceActorAsCounterpart(t *testing.T) {
	s := newSource(t, nil, 0, nil)
	cr, err := s.People(ctx(), "anime", "154587")
	if err != nil {
		t.Fatal(err)
	}
	if len(cr) != 4 {
		t.Fatalf("len = %d", len(cr))
	}
	c := cr[0]
	if c.Role != "character" || c.Person.ID != "176754" || c.Person.Name != "Frieren" ||
		c.Person.URL != "https://anilist.co/character/176754" || c.Person.ImageURL == "" {
		t.Errorf("character = %+v", c)
	}
	if c.With == nil || c.With.Name != "Atsumi Tanezaki" || c.With.ID != "112215" || c.With.URL != "https://anilist.co/staff/112215" || c.WithRole != "Voice actor" {
		t.Errorf("with = %+v role %q", c.With, c.WithRole)
	}
	if cr[2].With == nil || cr[2].With.Name != "Chiaki Kobayashi" {
		t.Errorf("must take the FIRST voice actor: %+v", cr[2].With)
	}
}

func TestSourcePersonWithoutActor(t *testing.T) {
	body := `{"data":{"Media":{"characters":{"edges":[{"role":"SUPPORTING","node":{"id":1,"name":{"full":"X"}},"voiceActors":[]}]}}}}`
	cr, err := newSource(t, []byte(body), 0, nil).People(ctx(), "anime", "1")
	if err != nil || len(cr) != 1 || cr[0].With != nil || cr[0].WithRole != "" {
		t.Fatalf("cr=%+v err=%v", cr, err)
	}
}

func TestSourcePersonImageComesFromClient(t *testing.T) {
	body := `{"data":{"Media":{"characters":{"edges":[{"role":"MAIN","node":{"id":1,"name":{"full":"X"},"image":{"large":"https://img.example/c.png"}},
	  "voiceActors":[{"id":2,"name":{"full":"Y"},"image":{"large":"https://img.example/a.png"}}]}]}}}}`
	cr, err := newSource(t, []byte(body), 0, nil).People(ctx(), "anime", "1")
	if err != nil || len(cr) != 1 {
		t.Fatalf("cr=%+v err=%v", cr, err)
	}
	if cr[0].Person.ImageURL != "https://img.example/c.png" || cr[0].With.ImageURL != "https://img.example/a.png" {
		t.Errorf("images = %q / %q", cr[0].Person.ImageURL, cr[0].With.ImageURL)
	}
}

func TestSourceLookupByMALID(t *testing.T) {
	var queries []string
	s := newSource(t, nil, 0, &queries)
	d, err := s.Lookup(ctx(), "anime", "mal", "52991")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "154587" || d.ExternalIDs["mal"] != "52991" || len(d.Genres) == 0 {
		t.Errorf("lookup = %+v", d)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "idMal: $idMal") {
		t.Errorf("queries = %v", queries)
	}
}

func TestSourceLookupNotFound(t *testing.T) {
	s := newSource(t, fixture(t, "notfound_mal.json"), 404, nil)
	_, err := s.Lookup(ctx(), "anime", "mal", "999999")
	if !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	for _, c := range []struct{ kind, idType, value string }{
		{"anime", "tmdb", "1"}, {"anime", "", "1"}, {"anime", "anilist", "154587"}, {"manga", "mal", "1"}, {"anime", "mal", "abc"},
	} {
		if _, err := s.Lookup(ctx(), c.kind, c.idType, c.value); !errors.Is(err, plugin.ErrNotFound) {
			t.Errorf("Lookup(%+v) err = %v, want ErrNotFound", c, err)
		}
	}
}

func TestSourceUnknownKind(t *testing.T) {
	var queries []string
	s := newSource(t, nil, 0, &queries)
	if _, err := s.Search(ctx(), "manga", "x"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Search err = %v", err)
	}
	if _, err := s.Details(ctx(), "manga", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Details err = %v", err)
	}
	if _, err := s.People(ctx(), "manga", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("People err = %v", err)
	}
	if _, err := s.Recommendations(ctx(), "manga", "1"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Recommendations err = %v", err)
	}
	if len(queries) != 0 {
		t.Errorf("made %d requests for an unknown kind", len(queries))
	}
}

func TestSourceErrorMapping(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
		legacy string
	}{
		{"not found", 404, string(fixtureOrEmpty("notfound.json")), plugin.ErrNotFound, "provider: not found"},
		{"rate limited", 429, `{}`, plugin.ErrRateLimited, "provider: rate limited"},
		{"server error", 500, `{}`, plugin.ErrUnavailable, "provider: unavailable"},
		{"malformed", 200, `not json`, plugin.ErrUnavailable, "provider: unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSource(t, []byte(c.body), c.status, nil)
			calls := map[string]func() error{
				"Search":          func() error { _, err := s.Search(ctx(), "anime", "x"); return err },
				"Details":         func() error { _, err := s.Details(ctx(), "anime", "1"); return err },
				"People":          func() error { _, err := s.People(ctx(), "anime", "1"); return err },
				"Recommendations": func() error { _, err := s.Recommendations(ctx(), "anime", "1"); return err },
				"Lookup":          func() error { _, err := s.Lookup(ctx(), "anime", "mal", "1"); return err },
			}
			for name, call := range calls {
				err := call()
				if !errors.Is(err, c.want) {
					t.Errorf("%s: err = %v, want %v", name, err, c.want)
				}
				// the cause is preserved: the message still carries the legacy sentinel
				if err != nil && !strings.Contains(err.Error(), c.legacy) {
					t.Errorf("%s: cause lost in %q", name, err)
				}
			}
		})
	}
}

func fixtureOrEmpty(name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "provider", "anilist", "testdata", name))
	if err != nil {
		return nil
	}
	return b
}

func TestSourceErrorMappingKeepsLegacyCause(t *testing.T) {
	s := newSource(t, []byte(`{}`), 429, nil)
	_, err := s.Details(ctx(), "anime", "1")
	if !errors.Is(err, plugin.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	var legacy interface{ Unwrap() []error }
	if !errors.As(err, &legacy) {
		t.Fatalf("error must wrap both the plugin sentinel and the cause: %T", err)
	}
	if len(legacy.Unwrap()) != 2 {
		t.Errorf("unwrap = %v", legacy.Unwrap())
	}
}

func TestSourceContextCancelIsNotMapped(t *testing.T) {
	s := newSource(t, nil, 0, nil)
	c, cancel := context.WithCancel(ctx())
	cancel()
	_, err := s.Details(c, "anime", "1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSourceNetworkErrorIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	s := New(Options{AniListURL: url}).Source("anilist")
	if _, err := s.Search(ctx(), "anime", "x"); !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// hostile is an AniList answer full of quirks that the library would refuse.
const hostile = `{"data":{"Media":{"id":7,"idMal":8,
 "title":{"romaji":"Line\nBreak\u0000Title","english":"  ","native":"\u0085日本\t語"},
 "format":"WEIRD","episodes":0,"seasonYear":99999,
 "startDate":{"year":2020,"month":1,"day":2},"endDate":{"year":null,"month":null,"day":null},
 "source":"MAN\u0000GA","description":"<p>First\r\nline\u0000 \u0001second\u009f</p>",
 "coverImage":{"large":"//cdn.example/x.jpg"},
 "studios":{"nodes":[{"name":"Studio\tName\n"}]},"siteUrl":"javascript:alert(1)",
 "genres":["Ac\ttion","\u0000"," Drama "],
 "tags":[{"name":"Ma\ngic","rank":5,"isMediaSpoiler":false},{"name":"` + "very long tag name that goes on and on and on and on and on and on and on forever" + `","rank":500}],
 "relations":{"edges":[{"relationType":"SEQUEL","node":{"id":9,"type":"ANIME","format":"TV\n","title":{"romaji":"Next\nOne"}}}]},
 "recommendations":{"nodes":[{"mediaRecommendation":{"id":10,"title":{"romaji":"Rec\tTitle"},"coverImage":{"large":"//cdn.example/r.jpg"}}}]}}}}`

const hostileCast = `{"data":{"Media":{"characters":{"edges":[
 {"role":"MAIN","node":{"id":1,"name":{"full":"Na\nme"},"image":{"large":"//cdn.example/c.png"},"siteUrl":"ftp://x/y"},
  "voiceActors":[{"id":2,"name":{"full":"Ac\u0000tor"},"image":{"large":"data:x"},"siteUrl":"//anilist.co/staff/2"}]},
 {"role":"MAIN","node":{"id":3,"name":{"full":" \n "}},"voiceActors":[]}]}}}}`

func TestSourceSanitisesHostileData(t *testing.T) {
	s := newSource(t, []byte(hostile), 0, nil)
	d, err := s.Details(ctx(), "anime", "7")
	if err != nil {
		t.Fatal(err)
	}
	tm := titles(d.Titles)
	if tm["ja-latn"] != "Line BreakTitle" {
		t.Errorf("romaji = %q, want newline as space and NUL removed", tm["ja-latn"])
	}
	if tm["ja"] != "日本 語" {
		t.Errorf("native = %q", tm["ja"])
	}
	if _, ok := tm["en"]; ok {
		t.Errorf("blank english title must be dropped: %v", d.Titles)
	}
	if got := d.Fields["synopsis"].Text; got != "First\nline second" {
		t.Errorf("synopsis = %q", got)
	}
	if d.CoverURL != "https://cdn.example/x.jpg" {
		t.Errorf("cover = %q", d.CoverURL)
	}
	if d.ExternalURL != "" {
		t.Errorf("javascript: url must be dropped, got %q", d.ExternalURL)
	}
	if d.Fields["studio"].Text != "Studio Name" || d.Fields["source_material"].Text != "MANGA" {
		t.Errorf("fields = %+v", d.Fields)
	}
	if _, ok := d.Fields["type"]; ok {
		t.Errorf("unknown format must not become a type value: %+v", d.Fields["type"])
	}
	if _, ok := d.Fields["episodes"]; ok {
		t.Errorf("0 episodes must be absent")
	}
	if _, ok := d.Fields["year"]; ok {
		t.Errorf("out of range year must be absent")
	}
	if strings.Join(d.Genres, "|") != "Ac tion|Drama" {
		t.Errorf("genres = %q", d.Genres)
	}
	for _, tg := range d.Tags {
		if tg.Rank != nil && *tg.Rank > 100 {
			t.Errorf("rank = %d", *tg.Rank)
		}
		if len([]rune(tg.Name)) > 60 {
			t.Errorf("tag not truncated: %q", tg.Name)
		}
	}
	if d.Relations[0].Item.Titles[0].Text != "Next One" || d.Relations[0].Item.Fields["type"].Text != "TV" {
		t.Errorf("relation = %+v", d.Relations[0])
	}

	recs, err := s.Recommendations(ctx(), "anime", "7")
	if err != nil || recs[0].Titles[0].Text != "Rec Title" || recs[0].CoverURL != "https://cdn.example/r.jpg" {
		t.Errorf("recs = %+v err=%v", recs, err)
	}

	cs := newSource(t, []byte(hostileCast), 0, nil)
	cr, err := cs.People(ctx(), "anime", "7")
	if err != nil {
		t.Fatal(err)
	}
	if len(cr) != 1 {
		t.Fatalf("a credit without a character name must be dropped: %+v", cr)
	}
	c := cr[0]
	if c.Person.Name != "Na me" || c.Person.ImageURL != "https://cdn.example/c.png" || c.Person.URL != "" {
		t.Errorf("person = %+v", c.Person)
	}
	if c.With == nil || c.With.Name != "Actor" || c.With.ImageURL != "" || c.With.URL != "https://anilist.co/staff/2" {
		t.Errorf("with = %+v", c.With)
	}
}

func testRegistry(t *testing.T, p plugin.Plugin) *plugin.Registry {
	t.Helper()
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	return reg
}

func newLibrary(t *testing.T, p plugin.Plugin) *library.Library {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return library.New(d, testRegistry(t, p))
}

func TestSourceOutputIsAcceptedByLibrary(t *testing.T) {
	// realistic fixture data and hostile data must both be saveable
	for name, body := range map[string][]byte{"fixture": nil, "hostile": []byte(hostile)} {
		t.Run(name, func(t *testing.T) {
			p := New(Options{})
			srcBody, castBody := body, body
			if name == "hostile" {
				castBody = []byte(hostileCast)
			}
			s := newSource(t, srcBody, 0, nil)
			cs := newSource(t, castBody, 0, nil)
			lib := newLibrary(t, p)
			id := "154587"
			if name == "hostile" {
				id = "7"
			}
			d, err := s.Details(ctx(), "anime", id)
			if err != nil {
				t.Fatal(err)
			}
			itemID, err := lib.SaveItem(ctx(), "anime", "anime", d)
			if err != nil {
				t.Fatalf("SaveItem: %v", err)
			}
			cr, err := cs.People(ctx(), "anime", id)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.SaveCredits(ctx(), itemID, cr); err != nil {
				t.Fatalf("SaveCredits: %v", err)
			}
			if err := lib.ValidateItem("anime", "anime", d); err != nil {
				t.Fatalf("ValidateItem: %v", err)
			}
		})
	}
}

func TestSourceSearchSummariesSurviveAsRelationItems(t *testing.T) {
	// search results are plain summaries; make sure hostile search data is clean too
	body := `{"data":{"Page":{"media":[{"id":1,"title":{"romaji":"A\nB"},"format":"OVA","episodes":2,"seasonYear":2001,
	 "description":"x\u0000y","coverImage":{"large":"//c/x.jpg"},"studios":{"nodes":[]}}]}}}`
	res, err := newSource(t, []byte(body), 0, nil).Search(ctx(), "anime", "a")
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res[0].Titles[0].Text != "A B" || res[0].Fields["synopsis"].Text != "xy" || res[0].CoverURL != "https://c/x.jpg" || res[0].Fields["type"].Text != "OVA" {
		t.Errorf("res = %+v", res[0])
	}
}
