package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

// steamTestRecorder records the requests the fake API and store servers see,
// so a test can assert both what was sent (including the key) and that a call
// made no request at all.
type steamTestRecorder struct {
	mu       sync.Mutex
	requests []steamTestRequest
}

type steamTestRequest struct {
	scope string // "api" or "store"
	path  string
	query url.Values
}

func (r *steamTestRecorder) record(scope string, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, steamTestRequest{scope: scope, path: req.URL.Path, query: req.URL.Query()})
}

func (r *steamTestRecorder) all() []steamTestRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]steamTestRequest(nil), r.requests...)
}

func (r *steamTestRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// only returns the single recorded request and fails when there was not
// exactly one or it went to the wrong server.
func (r *steamTestRecorder) only(t *testing.T, scope string) steamTestRequest {
	t.Helper()
	reqs := r.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want exactly 1", len(reqs))
	}
	if reqs[0].scope != scope {
		t.Fatalf("request scope = %q, want %q", reqs[0].scope, scope)
	}
	return reqs[0]
}

func newSteamTestServers(t *testing.T, rec *steamTestRecorder, api, store http.HandlerFunc) (apiSrv, storeSrv *httptest.Server) {
	t.Helper()
	if api == nil {
		api = http.NotFound
	}
	if store == nil {
		store = http.NotFound
	}
	apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record("api", r)
		api(w, r)
	}))
	t.Cleanup(apiSrv.Close)
	storeSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record("store", r)
		store(w, r)
	}))
	t.Cleanup(storeSrv.Close)
	return apiSrv, storeSrv
}

func steamTestKey(key string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return key, nil }
}

func newSteamTestClient(t *testing.T, rec *steamTestRecorder, api, store http.HandlerFunc, key string) (*steamClient, *steamSource) {
	t.Helper()
	apiSrv, storeSrv := newSteamTestServers(t, rec, api, store)
	c := newSteamClient(apiSrv.URL, storeSrv.URL, steamTestKey(key), apiSrv.Client())
	return c, &steamSource{c: c}
}

// steamRoundTripFunc lets a test fail a request at the transport layer.
type steamRoundTripFunc func(*http.Request) (*http.Response, error)

func (f steamRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSteamOwnedGames(t *testing.T) {
	rec := &steamTestRecorder{}
	api := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/IPlayerService/GetOwnedGames/v1/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"response":{"game_count":2,"games":[
			{"appid":440,"name":"Team Fortress 2","playtime_forever":1234,"playtime_2weeks":12,"img_icon_url":"abc"},
			{"appid":730,"name":"Counter-Strike 2","playtime_forever":0}
		]}}`)
	}
	c, _ := newSteamTestClient(t, rec, api, nil, "secret-key")

	games, err := c.ownedGames(context.Background(), "76561198000000000")
	if err != nil {
		t.Fatalf("ownedGames: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want 2", len(games))
	}
	first := games[0]
	if first.AppID != 440 || first.Name != "Team Fortress 2" || first.PlaytimeForever != 1234 ||
		first.Playtime2Weeks != 12 || first.IconURL != "abc" {
		t.Errorf("first game = %+v", first)
	}
	if second := games[1]; second.AppID != 730 || second.PlaytimeForever != 0 {
		t.Errorf("second game = %+v", second)
	}

	req := rec.only(t, "api")
	want := map[string]string{
		"key":                       "secret-key",
		"steamid":                   "76561198000000000",
		"include_appinfo":           "1",
		"include_played_free_games": "1",
		"format":                    "json",
	}
	for name, value := range want {
		if got := req.query.Get(name); got != value {
			t.Errorf("query %s = %q, want %q", name, got, value)
		}
	}
}

func TestSteamOwnedGamesEmptyIsActionable(t *testing.T) {
	rec := &steamTestRecorder{}
	for _, body := range []string{
		`{"response":{"game_count":0,"games":[]}}`,
		`{"response":{}}`,
		`{"response":{"games":null}}`,
	} {
		c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}, nil, "secret-key")
		_, err := c.ownedGames(context.Background(), "76561198000000000")
		if !errors.Is(err, plugin.ErrBadInput) {
			t.Fatalf("body %s: err = %v, want ErrBadInput", body, err)
		}
		if !strings.Contains(err.Error(), "no games") || !strings.Contains(err.Error(), "make your profile and game details public") {
			t.Errorf("body %s: error is not actionable: %v", body, err)
		}
	}
}

func TestSteamOwnedGamesStatusErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, plugin.ErrNotConfigured},
		{http.StatusForbidden, plugin.ErrNotConfigured},
		{http.StatusNotFound, plugin.ErrNotFound},
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusBadRequest, plugin.ErrBadResponse},
		{http.StatusRequestTimeout, plugin.ErrUnavailable},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
		{http.StatusServiceUnavailable, plugin.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			rec := &steamTestRecorder{}
			c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream says no", tc.status)
			}, nil, "secret-key")
			_, err := c.ownedGames(context.Background(), "76561198000000000")
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Errorf("status %d: error leaks the key: %v", tc.status, err)
			}
		})
	}
}

func TestSteamOwnedGamesTransportErrorHasNoKey(t *testing.T) {
	const key = "top-secret-key"
	c := newSteamClient("https://api.example.test", "https://store.example.test", steamTestKey(key), &http.Client{
		Transport: steamRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			// The real error text contains the request URL, and with it the key.
			return nil, fmt.Errorf("dial failed for %s", r.URL)
		}),
	})
	_, err := c.ownedGames(context.Background(), "76561198000000000")
	if !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("transport error leaks the key: %v", err)
	}
}

func TestSteamOwnedGamesMalformedBody(t *testing.T) {
	rec := &steamTestRecorder{}
	c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html>not json`)
	}, nil, "secret-key")
	if _, err := c.ownedGames(context.Background(), "76561198000000000"); !errors.Is(err, plugin.ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse", err)
	}
}

func TestSteamSourceRequiresKeyBeforeRequest(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, nil, "")

	ctx := context.Background()
	calls := map[string]func() error{
		"search": func() error {
			_, err := src.Search(ctx, KindGame, "portal")
			return err
		},
		"details": func() error {
			_, err := src.Details(ctx, KindGame, "400")
			return err
		},
		"lookup": func() error {
			_, err := src.Lookup(ctx, KindGame, "steam", "400")
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, plugin.ErrNotConfigured) {
			t.Errorf("%s: err = %v, want ErrNotConfigured", name, err)
		}
	}
	if got := rec.count(); got != 0 {
		t.Errorf("requests = %d, want 0 (an empty key must not reach either server)", got)
	}
}

func TestSteamKeyResolverErrorIsUnavailable(t *testing.T) {
	rec := &steamTestRecorder{}
	apiSrv, storeSrv := newSteamTestServers(t, rec, nil, nil)
	c := newSteamClient(apiSrv.URL, storeSrv.URL, func(context.Context) (string, error) {
		return "", errors.New("settings unavailable")
	}, apiSrv.Client())
	src := &steamSource{c: c}

	if _, err := src.Search(context.Background(), KindGame, "portal"); !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestSteamSourceSearch(t *testing.T) {
	rec := &steamTestRecorder{}
	store := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/storesearch/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"total":4,"items":[
			{"type":"app","id":400,"name":"Portal","tiny_image":"//cdn.example.test/portal.jpg","platforms":{"windows":true}},
			{"type":"dlc","id":401,"name":"Portal DLC","tiny_image":"https://cdn.example.test/dlc.jpg"},
			{"type":"app","id":0,"name":"Broken"},
			{"type":"app","id":402,"name":"   "}
		]}`)
	}
	_, src := newSteamTestClient(t, rec, nil, store, "secret-key")

	got, err := src.Search(context.Background(), KindGame, "portal")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1: %+v", len(got), got)
	}
	sum := got[0]
	if sum.Source != SourceSteam || sum.ID != "400" || sum.Kind != KindGame {
		t.Errorf("summary = %+v", sum)
	}
	if len(sum.Titles) != 1 || sum.Titles[0] != (plugin.Title{Lang: "en", Text: "Portal"}) {
		t.Errorf("titles = %+v", sum.Titles)
	}
	if sum.CoverURL != "https://cdn.example.test/portal.jpg" {
		t.Errorf("cover = %q", sum.CoverURL)
	}

	req := rec.only(t, "store")
	if req.query.Get("term") != "portal" || req.query.Get("l") != "english" || req.query.Get("cc") != "us" {
		t.Errorf("query = %v", req.query)
	}
	if _, ok := req.query["key"]; ok {
		t.Errorf("store search sent the API key: %v", req.query)
	}
}

func TestSteamSourceSearchKindAndEmptyTerm(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, nil, "secret-key")

	if _, err := src.Search(context.Background(), "movie", "portal"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("wrong kind: err = %v, want ErrNotFound", err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("wrong kind made %d requests", got)
	}

	got, err := src.Search(context.Background(), KindGame, "   ")
	if err != nil || len(got) != 0 {
		t.Errorf("empty term = %v, %v; want empty result", got, err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("empty term made %d requests", got)
	}
}

func TestSteamSourceSearchStatusErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, plugin.ErrNotConfigured},
		{http.StatusForbidden, plugin.ErrNotConfigured},
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			rec := &steamTestRecorder{}
			_, src := newSteamTestClient(t, rec, nil, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", tc.status)
			}, "secret-key")
			_, err := src.Search(context.Background(), KindGame, "portal")
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
}

func TestSteamSourceDetails(t *testing.T) {
	rec := &steamTestRecorder{}
	store := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/appdetails" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"730":{"success":true,"data":{
			"type":"game",
			"name":"Counter-Strike 2",
			"steam_appid":730,
			"short_description":"For over two decades, <strong>Counter-Strike</strong> &amp; friends<br>have defined the genre.",
			"about_the_game":"<p>This must not be used while a short description exists.</p>",
			"header_image":"https://cdn.example.test/steam/apps/730/header.jpg",
			"developers":["Valve","Hidden Path Entertainment","Third Studio","Fourth Studio"],
			"publishers":["Valve"],
			"genres":[{"id":"1","description":"Action"},{"id":"37","description":"Free to Play"}],
			"categories":[{"id":1,"description":"Multi-player"},{"id":2,"description":"Steam Achievements"}],
			"release_date":{"coming_soon":false,"date":"22 Aug, 2012"},
			"platforms":{"windows":true,"mac":true,"linux":false},
			"website":"https://www.counter-strike.net/"
		}}}`)
	}
	_, src := newSteamTestClient(t, rec, nil, store, "secret-key")

	it, err := src.Details(context.Background(), KindGame, "730")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if it.Source != SourceSteam || it.ID != "730" || it.Kind != KindGame {
		t.Errorf("identity = %s/%s/%s", it.Source, it.ID, it.Kind)
	}
	if len(it.Titles) != 1 || it.Titles[0] != (plugin.Title{Lang: "en", Text: "Counter-Strike 2"}) {
		t.Errorf("titles = %+v", it.Titles)
	}
	wantSummary := "For over two decades, Counter-Strike & friends\nhave defined the genre."
	if got := it.Fields["summary"].Text; got != wantSummary {
		t.Errorf("summary = %q, want %q", got, wantSummary)
	}
	if got := it.Fields["developer"].Text; got != "Valve, Hidden Path Entertainment, Third Studio" {
		t.Errorf("developer = %q", got)
	}
	if got := it.Fields["publisher"].Text; got != "Valve" {
		t.Errorf("publisher = %q", got)
	}
	if got := it.Fields["platforms"].Text; got != "Windows, macOS" {
		t.Errorf("platforms = %q", got)
	}
	if got := steamFieldDate(it, "released_on"); got != "2012-08-22" {
		t.Errorf("released_on = %q", got)
	}
	if year, ok := steamFieldNum(it, "year"); !ok || year != 2012 {
		t.Errorf("year = %v, %v", year, ok)
	}
	if len(it.Genres) != 2 || it.Genres[0] != "Action" || it.Genres[1] != "Free to Play" {
		t.Errorf("genres = %v", it.Genres)
	}
	if len(it.Tags) != 2 || it.Tags[0].Name != "Multi-player" || it.Tags[1].Name != "Steam Achievements" {
		t.Errorf("tags = %v", it.Tags)
	}
	if it.CoverURL != "https://cdn.example.test/steam/apps/730/header.jpg" {
		t.Errorf("cover = %q", it.CoverURL)
	}
	if it.ExternalURL != "https://store.steampowered.com/app/730" {
		t.Errorf("external url = %q", it.ExternalURL)
	}
	if it.ExternalIDs["steam"] != "730" {
		t.Errorf("external ids = %v", it.ExternalIDs)
	}

	req := rec.only(t, "store")
	if req.query.Get("appids") != "730" || req.query.Get("l") != "english" {
		t.Errorf("query = %v", req.query)
	}
	if _, ok := req.query["key"]; ok {
		t.Errorf("store details sent the API key: %v", req.query)
	}
}

func TestSteamSourceDetailsFallsBackToAboutTheGame(t *testing.T) {
	rec := &steamTestRecorder{}
	store := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"1":{"success":true,"data":{
			"type":"game","name":"X","short_description":"",
			"about_the_game":"<p>Deep <b>lore</b></p>"
		}}}`)
	}
	_, src := newSteamTestClient(t, rec, nil, store, "secret-key")

	it, err := src.Details(context.Background(), KindGame, "1")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if got := it.Fields["summary"].Text; got != "Deep lore" {
		t.Errorf("summary = %q, want %q", got, "Deep lore")
	}
}

func TestSteamSourceDetailsReleaseDates(t *testing.T) {
	cases := []struct {
		name       string
		date       string
		comingSoon bool
		wantDate   string
		wantYear   float64
	}{
		{"day first", "2 Jan, 2006", false, "2006-01-02", 2006},
		{"month first", "Jan 2, 2006", false, "2006-01-02", 2006},
		{"year only", "2006", false, "2006-01-01", 2006},
		{"single digit day", "5 Mar, 2021", false, "2021-03-05", 2021},
		{"coming soon flag", "2 Jan, 2006", true, "", 0},
		{"quarter", "Q4 2024", false, "", 0},
		{"coming soon text", "Coming soon", false, "", 0},
		{"empty", "", false, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &steamTestRecorder{}
			_, src := newSteamTestClient(t, rec, nil, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"1":{"success":true,"data":{"type":"game","name":"X","release_date":{"coming_soon":%t,"date":%q}}}}`, tc.comingSoon, tc.date)
			}, "secret-key")
			it, err := src.Details(context.Background(), KindGame, "1")
			if err != nil {
				t.Fatalf("Details: %v", err)
			}
			if got := steamFieldDate(it, "released_on"); got != tc.wantDate {
				t.Errorf("released_on = %q, want %q", got, tc.wantDate)
			}
			year, ok := steamFieldNum(it, "year")
			if tc.wantYear == 0 {
				if ok {
					t.Errorf("year = %v, want omitted", year)
				}
				return
			}
			if !ok || year != tc.wantYear {
				t.Errorf("year = %v, %v; want %v", year, ok, tc.wantYear)
			}
		})
	}
}

func TestSteamSourceDetailsNotFound(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"success false", `{"1":{"success":false}}`},
		{"empty object", `{}`},
		{"other app key", `{"999":{"success":true,"data":{"type":"game","name":"X"}}}`},
		{"dlc", `{"1":{"success":true,"data":{"type":"dlc","name":"X"}}}`},
		{"demo", `{"1":{"success":true,"data":{"type":"demo","name":"X"}}}`},
		{"software", `{"1":{"success":true,"data":{"type":"software","name":"X"}}}`},
		{"video", `{"1":{"success":true,"data":{"type":"video","name":"X"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &steamTestRecorder{}
			_, src := newSteamTestClient(t, rec, nil, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}, "secret-key")
			if _, err := src.Details(context.Background(), KindGame, "1"); !errors.Is(err, plugin.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestSteamSourceDetailsRejectsInvalidIDs(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, nil, "secret-key")

	for _, id := range []string{"", "  ", "abc", "0", "-4", "1.5"} {
		if _, err := src.Details(context.Background(), KindGame, id); !errors.Is(err, plugin.ErrNotFound) {
			t.Errorf("id %q: err = %v, want ErrNotFound", id, err)
		}
	}
	if got := rec.count(); got != 0 {
		t.Errorf("invalid ids made %d requests", got)
	}
	if _, err := src.Details(context.Background(), "movie", "730"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("wrong kind: err = %v, want ErrNotFound", err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("wrong kind made %d requests", got)
	}
}

func TestSteamSourceDetailsStatusErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, plugin.ErrNotConfigured},
		{http.StatusForbidden, plugin.ErrNotConfigured},
		{http.StatusNotFound, plugin.ErrNotFound},
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			rec := &steamTestRecorder{}
			_, src := newSteamTestClient(t, rec, nil, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", tc.status)
			}, "secret-key")
			if _, err := src.Details(context.Background(), KindGame, "730"); !errors.Is(err, tc.want) {
				t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
			}
		})
	}
}

func TestSteamSourceDetailsMalformedBody(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}, "secret-key")
	if _, err := src.Details(context.Background(), KindGame, "730"); !errors.Is(err, plugin.ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse", err)
	}
}

func TestSteamSourcePeopleAndRecommendationsAreNil(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, nil, "")

	people, err := src.People(context.Background(), KindGame, "730")
	if err != nil || people != nil {
		t.Errorf("People = %v, %v; want nil, nil", people, err)
	}
	recs, err := src.Recommendations(context.Background(), KindGame, "730")
	if err != nil || recs != nil {
		t.Errorf("Recommendations = %v, %v; want nil, nil", recs, err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("people/recommendations made %d requests", got)
	}

	if _, err := src.People(context.Background(), "movie", "730"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("People wrong kind: err = %v", err)
	}
	if _, err := src.Recommendations(context.Background(), "movie", "730"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Recommendations wrong kind: err = %v", err)
	}
}

func TestSteamSourceLookup(t *testing.T) {
	rec := &steamTestRecorder{}
	store := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/appdetails" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"440":{"success":true,"data":{"type":"game","name":"Team Fortress 2","steam_appid":440}}}`)
	}
	_, src := newSteamTestClient(t, rec, nil, store, "secret-key")

	it, err := src.Lookup(context.Background(), KindGame, "steam", "440")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if it.Source != SourceSteam || it.ID != "440" || it.ExternalIDs["steam"] != "440" {
		t.Errorf("lookup = %+v", it)
	}
	if len(it.Titles) != 1 || it.Titles[0].Text != "Team Fortress 2" {
		t.Errorf("titles = %+v", it.Titles)
	}
	req := rec.only(t, "store")
	if req.query.Get("appids") != "440" {
		t.Errorf("query = %v", req.query)
	}
}

func TestSteamSourceLookupRejectsOtherTypes(t *testing.T) {
	rec := &steamTestRecorder{}
	_, src := newSteamTestClient(t, rec, nil, nil, "secret-key")

	if _, err := src.Lookup(context.Background(), KindGame, "igdb", "440"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("igdb lookup: err = %v, want ErrNotFound", err)
	}
	if _, err := src.Lookup(context.Background(), KindGame, "steam", "not-a-number"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("bad value: err = %v, want ErrNotFound", err)
	}
	if _, err := src.Lookup(context.Background(), "movie", "steam", "440"); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("wrong kind: err = %v, want ErrNotFound", err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("rejected lookups made %d requests", got)
	}
}

func TestSteamDefaults(t *testing.T) {
	if DefaultSteamAPIBase != "https://api.steampowered.com" {
		t.Errorf("DefaultSteamAPIBase = %q", DefaultSteamAPIBase)
	}
	if DefaultSteamStoreBase != "https://store.steampowered.com" {
		t.Errorf("DefaultSteamStoreBase = %q", DefaultSteamStoreBase)
	}
	c := newSteamClient("", "", nil, nil)
	if c.apiBase != DefaultSteamAPIBase || c.storeBase != DefaultSteamStoreBase {
		t.Errorf("defaults = %q %q", c.apiBase, c.storeBase)
	}
	if c.hc != http.DefaultClient {
		t.Error("nil http.Client was not replaced by http.DefaultClient")
	}
	if _, err := c.requireKey(context.Background()); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Errorf("nil key resolver: err = %v, want ErrNotConfigured", err)
	}
	c = newSteamClient("https://api.example.test/", "https://store.example.test/", steamTestKey("k"), nil)
	if c.apiBase != "https://api.example.test" || c.storeBase != "https://store.example.test" {
		t.Errorf("trimmed = %q %q", c.apiBase, c.storeBase)
	}
}

func TestSteamHeaderURL(t *testing.T) {
	if got := steamHeaderURL(730); got != "https://cdn.cloudflare.steamstatic.com/steam/apps/730/header.jpg" {
		t.Errorf("steamHeaderURL(730) = %q", got)
	}
	if got := steamHeaderURL(0); got != "" {
		t.Errorf("steamHeaderURL(0) = %q, want empty", got)
	}
}

func steamFieldDate(d plugin.ItemData, key string) string {
	v, ok := d.Fields[key]
	if !ok || v.Date == nil {
		return ""
	}
	return v.Date.Format("2006-01-02")
}

func steamFieldNum(d plugin.ItemData, key string) (float64, bool) {
	v, ok := d.Fields[key]
	if !ok || v.Num == nil {
		return 0, false
	}
	return *v.Num, true
}
