package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

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
	if len(res.Entries) != 2 || res.Ignored != 1 {
		t.Fatalf("entries = %d, ignored = %d: %+v", len(res.Entries), res.Ignored, res.Entries)
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
