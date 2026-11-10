package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serve returns a client against a test server that always answers with body.
// The last request body is stored in *got when got is non-nil.
func serve(t *testing.T, status int, body []byte, got *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client())
}

func titleMap(ts []Title) map[string]string {
	m := map[string]string{}
	for _, t := range ts {
		m[t.Lang] = t.Text
	}
	return m
}

func TestName(t *testing.T) {
	if New("x", nil).Name() != AniList {
		t.Fatal("Name() must be anilist")
	}
}

func TestSearchMapsResults(t *testing.T) {
	var body string
	c := serve(t, 200, fixture(t, "search.json"), &body)
	res, err := c.Search(context.Background(), TypeAnime, "  Sousou no Frieren ")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len = %d, want 3", len(res))
	}
	r := res[0]
	if r.Source != AniList || r.ID != "154587" || r.Type != "TV" {
		t.Errorf("bad identity: %+v", r)
	}
	tm := titleMap(r.Titles)
	if tm["ja"] != "葬送のフリーレン" || tm["ja-latn"] != "Sousou no Frieren" || tm["en"] != "Frieren: Beyond Journey’s End" {
		t.Errorf("titles = %v", r.Titles)
	}
	if r.Episodes == nil || *r.Episodes != 28 || r.Year == nil || *r.Year != 2023 {
		t.Errorf("episodes/year = %v/%v", r.Episodes, r.Year)
	}
	if r.Studio != "MADHOUSE" || !strings.HasPrefix(r.CoverURL, "https://") {
		t.Errorf("studio/cover = %q/%q", r.Studio, r.CoverURL)
	}
	if strings.Contains(r.Synopsis, "<") || strings.Contains(r.Synopsis, "&") || !strings.Contains(r.Synopsis, "\n") {
		t.Errorf("synopsis not plain text: %q", r.Synopsis)
	}
	if res[1].Episodes != nil {
		t.Errorf("unknown episodes must be nil, got %d", *res[1].Episodes)
	}
	var req struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Variables["q"] != "Sousou no Frieren" {
		t.Errorf("q variable = %v, want trimmed", req.Variables["q"])
	}
}

func TestSearchEmptyQueryMakesNoRequest(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&n, 1) }))
	defer srv.Close()
	res, err := New(srv.URL, nil).Search(context.Background(), TypeAnime, "  \t")
	if err != nil || res == nil || len(res) != 0 {
		t.Fatalf("res=%v err=%v, want empty non-nil slice", res, err)
	}
	if n != 0 {
		t.Fatalf("made %d requests", n)
	}
}

func TestSearchCapsAtTwenty(t *testing.T) {
	var body string
	c := serve(t, 200, []byte(`{"data":{"Page":{"media":[]}}}`), &body)
	if _, err := c.Search(context.Background(), TypeAnime, "x"); err != nil {
		t.Fatal(err)
	}
	var req struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Variables["perPage"] != float64(20) {
		t.Errorf("perPage = %v, want 20", req.Variables["perPage"])
	}
	// also cap client-side if the server over-delivers
	var items []string
	for i := 1; i <= 25; i++ {
		items = append(items, `{"id":1,"title":{"romaji":"t"}}`)
	}
	c = serve(t, 200, []byte(`{"data":{"Page":{"media":[`+strings.Join(items, ",")+`]}}}`), nil)
	res, err := c.Search(context.Background(), TypeAnime, "x")
	if err != nil || len(res) != 20 {
		t.Fatalf("len=%d err=%v, want 20", len(res), err)
	}
}

func TestOversizedResponseMapsUnavailable(t *testing.T) {
	c := serve(t, 200, []byte(`{"data":{"Page":{"media":[]}}}`+strings.Repeat(" ", 100)), nil)
	c.maxBody = 64
	if _, err := c.Search(context.Background(), TypeAnime, "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// a body of exactly the limit is still accepted
	body := `{"data":{"Page":{"media":[]}}}`
	c = serve(t, 200, []byte(body), nil)
	c.maxBody = int64(len(body))
	if _, err := c.Search(context.Background(), TypeAnime, "x"); err != nil {
		t.Fatalf("body at limit: %v", err)
	}
}

func TestAnimeMapsTitlesRelationsRecsMALID(t *testing.T) {
	c := serve(t, 200, fixture(t, "anime.json"), nil)
	a, err := c.Media(context.Background(), TypeAnime, "154587")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "154587" || a.Source != AniList || a.MALID != "52991" {
		t.Errorf("identity: %+v", a.SearchResult)
	}
	if a.ExternalURL != "https://anilist.co/anime/154587" || a.SourceMaterial != "MANGA" {
		t.Errorf("url/source = %q/%q", a.ExternalURL, a.SourceMaterial)
	}
	if tm := titleMap(a.Titles); len(tm) != 3 || tm["ja-latn"] != "Sousou no Frieren" {
		t.Errorf("titles = %v", a.Titles)
	}
	if a.StartDate == nil || a.StartDate.Format("2006-01-02") != "2023-09-29" {
		t.Errorf("start = %v", a.StartDate)
	}
	if a.EndDate == nil || a.EndDate.Format("2006-01-02") != "2024-03-22" {
		t.Errorf("end = %v", a.EndDate)
	}
	if strings.Contains(a.Synopsis, "<") {
		t.Errorf("synopsis has tags: %q", a.Synopsis)
	}

	kinds := map[string]Relation{}
	for _, r := range a.Relations {
		if r.Source != AniList {
			t.Errorf("relation source = %s", r.Source)
		}
		kinds[r.ID] = r
	}
	if r, ok := kinds["118586"]; !ok || r.Kind != "adaptation" || r.Type != "MANGA" || r.MediaType != "MANGA" {
		t.Errorf("manga source relation = %+v ok=%v", r, ok)
	}
	if r, ok := kinds["182255"]; !ok || r.Kind != "sequel" || r.Type != "TV" || titleMap(r.Titles)["ja-latn"] == "" {
		t.Errorf("sequel relation = %+v", r)
	}
	if r, ok := kinds["170068"]; !ok || r.Kind != "other" {
		t.Errorf("side story relation = %+v", r)
	}

	if len(a.Recommendations) == 0 {
		t.Fatal("no recommendations")
	}
	rec := a.Recommendations[0]
	if rec.Source != AniList || rec.ID != "21827" || rec.CoverURL == "" || titleMap(rec.Titles)["en"] != "Violet Evergarden" {
		t.Errorf("rec = %+v", rec)
	}
}

func TestAnimeRelationsFilterAndRecsSkipNull(t *testing.T) {
	body := `{"data":{"Media":{"id":1,"title":{"romaji":"A"},
	 "relations":{"edges":[
	  {"relationType":"SOURCE","node":{"id":10,"type":"MANGA","format":"NOVEL","title":{"romaji":"N"}}},
	  {"relationType":"SIDE_STORY","node":{"id":11,"type":"MANGA","format":"MANGA","title":{"romaji":"M"}}},
	  {"relationType":"PREQUEL","node":{"id":12,"type":"ANIME","format":"TV","title":{"romaji":"P"}}}]},
	 "recommendations":{"nodes":[{"mediaRecommendation":null},{"mediaRecommendation":{"id":5,"title":{"romaji":"R"},"coverImage":{"large":"u"}}}]}}}}`
	a, err := serve(t, 200, []byte(body), nil).Media(context.Background(), TypeAnime, "1")
	if err != nil {
		t.Fatal(err)
	}
	if a.MALID != "" || a.StartDate != nil || a.EndDate != nil {
		t.Errorf("absent fields: %q %v %v", a.MALID, a.StartDate, a.EndDate)
	}
	// every related media type is kept; the kind is the normalised relation
	if len(a.Relations) != 3 || a.Relations[0].Kind != "source" || a.Relations[0].MediaType != "MANGA" ||
		a.Relations[1].Kind != "other" || a.Relations[1].MediaType != "MANGA" || a.Relations[2].Kind != "prequel" {
		t.Errorf("relations = %+v", a.Relations)
	}
	if len(a.Recommendations) != 1 || a.Recommendations[0].ID != "5" {
		t.Errorf("recs = %+v", a.Recommendations)
	}
}

func TestCastMapsVoiceActors(t *testing.T) {
	var body string
	c := serve(t, 200, fixture(t, "cast.json"), &body)
	cast, err := c.Cast(context.Background(), "154587")
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 4 {
		t.Fatalf("len = %d", len(cast))
	}
	m := cast[0]
	if m.Role != "MAIN" || m.Character.Name != "Frieren" || m.Character.ID != "176754" || m.Character.Source != AniList ||
		m.Character.URL != "https://anilist.co/character/176754" || m.Character.ImageURL == "" {
		t.Errorf("character = %+v", m)
	}
	if m.Actor == nil || m.Actor.Name != "Atsumi Tanezaki" || m.Actor.URL != "https://anilist.co/staff/112215" {
		t.Errorf("actor = %+v", m.Actor)
	}
	if !strings.Contains(body, "voiceActors(language: JAPANESE)") {
		t.Errorf("query must request Japanese voice actors: %s", body)
	}
	if !strings.Contains(body, "ROLE") {
		t.Errorf("query must sort by ROLE")
	}
}

func TestCastActorNilWhenNone(t *testing.T) {
	body := `{"data":{"Media":{"characters":{"edges":[{"role":"SUPPORTING","node":{"id":1,"name":{"full":"X"}},"voiceActors":[]}]}}}}`
	cast, err := serve(t, 200, []byte(body), nil).Cast(context.Background(), "1")
	if err != nil || len(cast) != 1 || cast[0].Actor != nil {
		t.Fatalf("cast=%+v err=%v", cast, err)
	}
}

func TestRateLimitedMapsError(t *testing.T) {
	c := serve(t, 429, []byte(`{"errors":[{"message":"Too Many Requests.","status":429}]}`), nil)
	_, err := c.Search(context.Background(), TypeAnime, "x")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerErrorMapsUnavailable(t *testing.T) {
	c := serve(t, 500, []byte(`oops`), nil)
	_, err := c.Media(context.Background(), TypeAnime, "1")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestNotFoundMapsErrNotFound(t *testing.T) {
	// GraphQL-level "Not Found." (AniList answers this with HTTP 404)
	c := serve(t, 404, fixture(t, "notfound.json"), nil)
	if _, err := c.Media(context.Background(), TypeAnime, "99999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: err = %v", err)
	}
	c = serve(t, 200, fixture(t, "notfound.json"), nil)
	if _, err := c.Cast(context.Background(), "99999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("200+errors: err = %v", err)
	}
}

// TestErrorClassification: an answer that will be the same on every retry
// (malformed data on a 200, a GraphQL error that is not "not found", an
// unexpected 4xx) is ErrBadResponse, never ErrUnavailable, so an import
// records the row as failed instead of retrying it for minutes; transport
// trouble, 5xx and 429 stay retryable.
func TestErrorClassification(t *testing.T) {
	sentinels := []error{ErrNotFound, ErrRateLimited, ErrUnavailable, ErrBadResponse}
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"malformed json on 200", 200, `{"data":`, ErrBadResponse},
		{"malformed data on 200", 200, `{"data":{"Page":"not an object"}}`, ErrBadResponse},
		{"graphql error on 200", 200, `{"errors":[{"message":"Invalid query","status":400}]}`, ErrBadResponse},
		{"graphql error without status", 200, `{"errors":[{"message":"boom"}]}`, ErrBadResponse},
		{"graphql rate limit on 200", 200, `{"errors":[{"message":"Too Many Requests.","status":429}]}`, ErrRateLimited},
		{"graphql server error on 200", 200, `{"errors":[{"message":"Internal","status":500}]}`, ErrUnavailable},
		{"teapot", 418, `{}`, ErrBadResponse},
		{"bad request", 400, `{"errors":[{"message":"Validation","status":400}]}`, ErrBadResponse},
		{"forbidden", 403, `oops`, ErrBadResponse},
		{"server error", 500, `oops`, ErrUnavailable},
		{"bad gateway", 502, `<html>`, ErrUnavailable},
		{"rate limited", 429, `{}`, ErrRateLimited},
		{"not found", 404, `{}`, ErrNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := serve(t, c.status, []byte(c.body), nil).Search(context.Background(), TypeAnime, "x")
			for _, s := range sentinels {
				if got := errors.Is(err, s); got != (s == c.want) {
					t.Errorf("err = %v: errors.Is(%v) = %v", err, s, got)
				}
			}
		})
	}
}

func TestNetworkErrorMapsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := New(url, nil).Search(context.Background(), TypeAnime, "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestContextCancelled(t *testing.T) {
	c := serve(t, 200, fixture(t, "search.json"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Search(ctx, TypeAnime, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAnimeByMALIDMapsAnime(t *testing.T) {
	var body string
	c := serve(t, 200, fixture(t, "anime_mal.json"), &body)
	a, err := c.MediaByMALID(context.Background(), TypeAnime, " 52991 ")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "154587" || a.Source != AniList || a.MALID != "52991" {
		t.Errorf("identity: %+v", a.SearchResult)
	}
	if tm := titleMap(a.Titles); len(tm) != 3 || tm["ja-latn"] != "Sousou no Frieren" {
		t.Errorf("titles = %v", a.Titles)
	}
	if a.Episodes == nil || *a.Episodes != 28 || a.StartDate == nil || a.ExternalURL == "" {
		t.Errorf("detail not mapped: %+v", a)
	}
	if len(a.Relations) == 0 && len(a.Recommendations) == 0 {
		t.Error("relations and recommendations not mapped")
	}
	if !strings.Contains(body, "idMal: $idMal") || !strings.Contains(body, `"idMal":52991`) {
		t.Errorf("request does not query by idMal: %s", body)
	}
}

func TestAnimeByMALIDNotFound(t *testing.T) {
	c := serve(t, 404, fixture(t, "notfound_mal.json"), nil)
	if _, err := c.MediaByMALID(context.Background(), TypeAnime, "99999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// A null Media without errors is not found either.
	c = serve(t, 200, []byte(`{"data":{"Media":null}}`), nil)
	if _, err := c.MediaByMALID(context.Background(), TypeAnime, "5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("null media: err = %v", err)
	}
	// A Media without an id is not found.
	c = serve(t, 200, []byte(`{"data":{"Media":{"id":0}}}`), nil)
	if _, err := c.MediaByMALID(context.Background(), TypeAnime, "5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: err = %v", err)
	}
}

func TestAnimeByMALIDRejectsNonDigitsWithoutRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	for _, id := range []string{"", "abc", "12a", "-5", "1.5", "0", "99999999999999999999", "１２"} {
		if _, err := c.MediaByMALID(context.Background(), TypeAnime, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: err = %v", id, err)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests made", hits)
	}
}

func TestAnimeMapsGenresAndTags(t *testing.T) {
	var body string
	c := serve(t, 200, fixture(t, "anime.json"), &body)
	a, err := c.Media(context.Background(), TypeAnime, "154587")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Query, "genres") || !strings.Contains(req.Query, "tags { name rank isMediaSpoiler }") {
		t.Errorf("query does not request genres and tags: %s", req.Query)
	}
	if len(a.Genres) != 3 || a.Genres[0] != "Adventure" || a.Genres[2] != "Fantasy" {
		t.Errorf("genres = %v", a.Genres)
	}
	if len(a.Tags) != 28 {
		t.Fatalf("tags = %d, want 28", len(a.Tags))
	}
	if a.Tags[0].Name != "Travel" || a.Tags[0].Rank == nil || *a.Tags[0].Rank != 96 || a.Tags[0].Spoiler {
		t.Errorf("tag 0 = %+v", a.Tags[0])
	}
	var found bool
	for _, tg := range a.Tags {
		if tg.Name == "Time Skip" {
			found = true
			if !tg.Spoiler {
				t.Errorf("Time Skip must be a spoiler: %+v", tg)
			}
		}
	}
	if !found {
		t.Error("Time Skip tag missing")
	}
}

func TestAnimeGenresAndTagsAreCleaned(t *testing.T) {
	body := `{"data":{"Media":{"id":1,"title":{"romaji":"A"},
	 "genres":[" Action ","","  ","Drama"],
	 "tags":[{"name":" Magic ","rank":50,"isMediaSpoiler":false},{"name":"","rank":10},{"name":"  "},
	  {"name":"Zero","rank":0},{"name":"Full","rank":100},{"name":"Over","rank":101},{"name":"Neg","rank":-1},{"name":"NoRank","rank":null,"isMediaSpoiler":true}]}}}`
	a, err := serve(t, 200, []byte(body), nil).Media(context.Background(), TypeAnime, "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Genres) != 2 || a.Genres[0] != "Action" || a.Genres[1] != "Drama" {
		t.Errorf("genres = %v", a.Genres)
	}
	rank := func(tg Tag) int {
		if tg.Rank == nil {
			return -99
		}
		return *tg.Rank
	}
	want := []struct {
		name string
		rank int
		spo  bool
	}{{"Magic", 50, false}, {"Zero", 0, false}, {"Full", 100, false}, {"Over", -99, false}, {"Neg", -99, false}, {"NoRank", -99, true}}
	if len(a.Tags) != len(want) {
		t.Fatalf("tags = %+v", a.Tags)
	}
	for i, w := range want {
		if a.Tags[i].Name != w.name || rank(a.Tags[i]) != w.rank || a.Tags[i].Spoiler != w.spo {
			t.Errorf("tag %d = %+v, want %+v", i, a.Tags[i], w)
		}
	}
}

// TestAniListRequestsMediumForPeopleAndLargeForCovers: person photos (the
// characters and voice actors of the cast) are requested at the smaller
// "medium" size, covers at "large".
func TestAniListRequestsMediumForPeopleAndLargeForCovers(t *testing.T) {
	var castBody string
	cast, err := serve(t, 200, fixture(t, "cast.json"), &castBody).Cast(context.Background(), "154587")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(castBody, "image { medium }") != 2 || strings.Contains(castBody, "large") {
		t.Errorf("cast query must request image { medium } for characters and voice actors only: %s", castBody)
	}
	if len(cast) == 0 || !strings.Contains(cast[0].Character.ImageURL, "/medium/") ||
		cast[0].Actor == nil || !strings.Contains(cast[0].Actor.ImageURL, "/medium/") {
		t.Errorf("cast images not mapped from medium: %+v", cast)
	}
	for name, call := range map[string]func(c *Client) error{
		"anime": func(c *Client) error { _, err := c.Media(context.Background(), TypeAnime, "154587"); return err },
		"mal":   func(c *Client) error { _, err := c.MediaByMALID(context.Background(), TypeAnime, "52991"); return err },
		"search": func(c *Client) error {
			_, err := c.Search(context.Background(), TypeAnime, "frieren")
			return err
		},
	} {
		var body string
		fx := map[string]string{"anime": "anime.json", "mal": "anime_mal.json", "search": "search.json"}[name]
		if err := call(serve(t, 200, fixture(t, fx), &body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(body, "coverImage { large }") || strings.Contains(body, "coverImage { medium }") {
			t.Errorf("%s query must request coverImage { large }: %s", name, body)
		}
	}
}

func TestSentinelsNameAniList(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrRateLimited, ErrUnavailable, ErrBadResponse} {
		if !strings.HasPrefix(err.Error(), "anilist: ") {
			t.Errorf("%q must start with \"anilist: \"", err)
		}
	}
}
