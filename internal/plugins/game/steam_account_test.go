package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// TestSteamAccountRejectsInvalidIDs: an account import takes the 17-digit
// SteamID64; anything else is actionable bad input and reaches no server.
func TestSteamAccountRejectsInvalidIDs(t *testing.T) {
	rec := &steamTestRecorder{}
	c, _ := newSteamTestClient(t, rec, nil, nil, "secret-key")
	imp := steamAccountImporter{c: c}

	for _, id := range []string{
		"",
		"   ",
		"7656119800000000",   // 16 digits
		"765611980000000000", // 18 digits
		"7656119800000000a",
		"7656 1198000000000",
		"7656119 800000000", // 17 bytes with an interior space: the character check
		"-7656119800000000",
		"７６５６１１９８０００００００００", // full-width digits are not ASCII
	} {
		_, err := imp.ImportAccount(context.Background(), id)
		if !errors.Is(err, plugin.ErrBadInput) {
			t.Errorf("id %q: err = %v, want ErrBadInput", id, err)
			continue
		}
		if !strings.Contains(err.Error(), "17-digit SteamID64") {
			t.Errorf("id %q: error is not actionable: %v", id, err)
		}
	}
	if got := rec.count(); got != 0 {
		t.Errorf("invalid ids made %d requests", got)
	}
}

// TestSteamAccountKeyProblems: a missing or refused key is bad input with a
// pointer to the settings, not a transient configuration error.
func TestSteamAccountKeyProblems(t *testing.T) {
	t.Run("keyless", func(t *testing.T) {
		rec := &steamTestRecorder{}
		c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"response":{"games":[{"appid":440,"name":"Team Fortress 2"}]}}`)
		}, nil, "")
		_, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
		assertSteamKeyError(t, err)
		if got := rec.count(); got != 0 {
			t.Errorf("a keyless import made %d requests", got)
		}
	})

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			rec := &steamTestRecorder{}
			c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", status)
			}, nil, "secret-key")
			_, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
			assertSteamKeyError(t, err)
			// the refusal came from the server, not from a local key check
			if got := rec.count(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
}

func assertSteamKeyError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v, want ErrBadInput", err)
	}
	if errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("err = %v, must not stay ErrNotConfigured", err)
	}
	for _, phrase := range []string{"Steam Web API key", "Settings > Plugins"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("error %q does not contain %q", err, phrase)
		}
	}
}

// TestSteamAccountLibrary: every owned game becomes one entry keyed by its
// Steam appid, with playtime in seconds, a status derived from the playtime
// and the store header as cover.
func TestSteamAccountLibrary(t *testing.T) {
	rec := &steamTestRecorder{}
	api := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/IPlayerService/GetOwnedGames/v1/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"response":{"game_count":3,"games":[
			{"appid":440,"name":"  Team\nFortress 2  ","playtime_forever":1234},
			{"appid":730,"name":"Counter-Strike 2","playtime_forever":0},
			{"appid":0,"name":"Broken","playtime_forever":5}
		]}}`)
	}
	c, _ := newSteamTestClient(t, rec, api, nil, "secret-key")

	res, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), " 76561198000000000 ")
	if err != nil {
		t.Fatalf("ImportAccount: %v", err)
	}
	// the appid-0 game is a skipped row with a reason, not silently ignored
	if len(res.Entries) != 2 || res.Ignored != 0 || len(res.Skipped) != 1 || res.Skipped[0].Title != "Broken" {
		t.Fatalf("entries = %d, ignored = %d, skipped = %+v: %+v", len(res.Entries), res.Ignored, res.Skipped, res.Entries)
	}

	played := res.Entries[0]
	if played.Kind != KindGame || played.Title != "Team Fortress 2" {
		t.Errorf("played identity = %+v", played)
	}
	if played.ExternalIDs["steam"] != "440" {
		t.Errorf("played external ids = %v", played.ExternalIDs)
	}
	if played.Progress["playtime"] != 1234*60 {
		t.Errorf("played playtime = %v, want %d", played.Progress["playtime"], 1234*60)
	}
	if played.Status != plugin.Active {
		t.Errorf("played status = %q, want Active", played.Status)
	}
	if played.CoverURL != "https://cdn.cloudflare.steamstatic.com/steam/apps/440/header.jpg" {
		t.Errorf("played cover = %q", played.CoverURL)
	}
	if played.UpdatedAt != nil {
		t.Errorf("played updatedAt = %v, want nil", played.UpdatedAt)
	}

	unplayed := res.Entries[1]
	if unplayed.ExternalIDs["steam"] != "730" || unplayed.Title != "Counter-Strike 2" {
		t.Errorf("unplayed identity = %+v", unplayed)
	}
	if unplayed.Progress["playtime"] != 0 {
		t.Errorf("unplayed playtime = %v, want 0", unplayed.Progress["playtime"])
	}
	if unplayed.Status != plugin.Planned {
		t.Errorf("unplayed status = %q, want Planned", unplayed.Status)
	}
	if unplayed.CoverURL != "https://cdn.cloudflare.steamstatic.com/steam/apps/730/header.jpg" {
		t.Errorf("unplayed cover = %q", unplayed.CoverURL)
	}

	req := rec.only(t, "api")
	if got := req.query.Get("steamid"); got != "76561198000000000" {
		t.Errorf("steamid = %q, want the trimmed id", got)
	}
}

// TestSteamAccountLastPlayed: rtime_last_played (Unix seconds) becomes the
// row's LastActive and UpdatedAt (UTC instants), so "update when newer"
// sees a play after the entry's last change; 0 (never played, or Steam does
// not know) leaves both nil.
func TestSteamAccountLastPlayed(t *testing.T) {
	rec := &steamTestRecorder{}
	api := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"response":{"game_count":3,"games":[
			{"appid":440,"name":"Played","playtime_forever":90,"rtime_last_played":1700000000},
			{"appid":730,"name":"Unknown","playtime_forever":30,"rtime_last_played":0},
			{"appid":570,"name":"Never","playtime_forever":0}
		]}}`)
	}
	c, _ := newSteamTestClient(t, rec, api, nil, "secret-key")
	res, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
	if err != nil {
		t.Fatalf("ImportAccount: %v", err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	want := time.Unix(1700000000, 0).UTC()
	played := res.Entries[0]
	if played.LastActive == nil || !played.LastActive.Equal(want) || played.LastActive.Location() != time.UTC {
		t.Errorf("last active = %v, want %v UTC", played.LastActive, want)
	}
	if played.UpdatedAt == nil || !played.UpdatedAt.Equal(want) {
		t.Errorf("updated at = %v, want %v", played.UpdatedAt, want)
	}
	if played.Status != plugin.Active {
		t.Errorf("played status = %q, want Active (the core applies the idle rule)", played.Status)
	}
	for _, e := range res.Entries[1:] {
		if e.LastActive != nil || e.UpdatedAt != nil {
			t.Errorf("%s: last active %v, updated at %v, want nil", e.Title, e.LastActive, e.UpdatedAt)
		}
	}
	if res.Entries[1].Status != plugin.Active || res.Entries[2].Status != plugin.Planned {
		t.Errorf("statuses = %q, %q", res.Entries[1].Status, res.Entries[2].Status)
	}
}

// TestSteamAccountPrivateLibrary: Steam answers an empty list for a private
// profile; the importer keeps the client's actionable reason.
func TestSteamAccountPrivateLibrary(t *testing.T) {
	rec := &steamTestRecorder{}
	c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"response":{"game_count":0,"games":[]}}`)
	}, nil, "secret-key")

	_, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v, want ErrBadInput", err)
	}
	if !strings.Contains(err.Error(), "make your profile and game details public") {
		t.Errorf("error is not actionable: %v", err)
	}
}

// TestSteamAccountTransientErrorsStay: only the key is mapped to bad input; an
// unavailable or rate-limited API stays retryable.
func TestSteamAccountTransientErrorsStay(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusTooManyRequests, plugin.ErrRateLimited},
		{http.StatusInternalServerError, plugin.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			rec := &steamTestRecorder{}
			c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", tc.status)
			}, nil, "secret-key")
			_, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if errors.Is(err, plugin.ErrBadInput) {
				t.Fatalf("err = %v, want no ErrBadInput", err)
			}
		})
	}
}

// TestSteamAccountParseRefusesFile: an account importer takes the SteamID64,
// not an uploaded file.
func TestSteamAccountParseRefusesFile(t *testing.T) {
	_, err := (steamAccountImporter{}).Parse(context.Background(), strings.NewReader("appid,title\n"))
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v, want ErrBadInput", err)
	}
	if !strings.Contains(err.Error(), "takes a SteamID64, not a file") {
		t.Errorf("error is not actionable: %v", err)
	}
}

// TestSteamAccountBrokenGames: an owned game without a positive appid cannot
// be identified, so it is reported as a skipped row with a reason; a negative
// playtime (the Web API never sends one) is clamped to 0, so the game counts
// as unplayed.
func TestSteamAccountBrokenGames(t *testing.T) {
	rec := &steamTestRecorder{}
	c, _ := newSteamTestClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"response":{"games":[{"appid":0,"name":"No Id"},{"appid":-3,"name":"Negative Id"},{"appid":440,"name":"Team Fortress 2","playtime_forever":-5}]}}`)
	}, nil, "secret-key")
	res, err := steamAccountImporter{c: c}.ImportAccount(context.Background(), "76561198000000000")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ignored != 0 || len(res.Skipped) != 2 || len(res.Entries) != 1 {
		t.Fatalf("res = %+v", res)
	}
	for i, s := range res.Skipped {
		if s.Reason == "" || s.Title == "" {
			t.Errorf("skipped %d = %+v, want a title and a reason", i, s)
		}
	}
	if e := res.Entries[0]; e.Progress["playtime"] != 0 || e.Status != plugin.Planned {
		t.Fatalf("negative playtime entry = %+v", e)
	}
}
