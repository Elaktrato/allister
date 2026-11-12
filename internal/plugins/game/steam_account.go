package game

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// The account import's actionable reasons. They end up verbatim in the
// import's bad-input message, so they name the fix instead of the cause.
const (
	steamAccountIDHint  = "Enter the 17-digit SteamID64"
	steamAccountKeyHint = "The Steam Web API key is missing or refused; add it in Settings > Plugins."
)

// steamAccountImporter imports a Steam account's owned games. It shares the
// Steam client with the store source: the library is the only keyed call.
// The generic import runner resolves every entry through IGDB first and the
// Steam store fallback (the manifest's ResolveVia); this importer only
// describes the library.
type steamAccountImporter struct{ c *steamClient }

var _ plugin.AccountImporter = steamAccountImporter{}

// Parse refuses a file: this importer fetches the library of a SteamID64.
func (steamAccountImporter) Parse(context.Context, io.Reader) (plugin.ParseResult, error) {
	return plugin.ParseResult{}, fmt.Errorf("%w: this importer takes a SteamID64, not a file", plugin.ErrBadInput)
}

// ImportAccount validates the SteamID64, fetches the owned games and returns
// one entry per game: the appid as the Steam external id, the playtime in
// seconds, Active when the game has been played and Planned when it has not,
// and the store header image as cover. A missing or refused key is bad input
// with a pointer to the settings (not a retryable configuration error); an
// empty library keeps the client's "make your profile public" reason.
func (si steamAccountImporter) ImportAccount(ctx context.Context, steamID string) (plugin.ParseResult, error) {
	steamID = strings.TrimSpace(steamID)
	if !isSteamID64(steamID) {
		return plugin.ParseResult{}, fmt.Errorf("%w: %s", plugin.ErrBadInput, steamAccountIDHint)
	}

	games, err := si.c.ownedGames(ctx, steamID)
	if err != nil {
		if errors.Is(err, plugin.ErrNotConfigured) {
			return plugin.ParseResult{}, fmt.Errorf("%w: %s", plugin.ErrBadInput, steamAccountKeyHint)
		}
		return plugin.ParseResult{}, err
	}

	res := plugin.ParseResult{Entries: make([]plugin.ParsedEntry, 0, len(games))}
	for _, g := range games {
		// A game without a positive appid cannot be identified or resolved;
		// the Web API never sends one, so this only guards broken data.
		if g.AppID <= 0 {
			res.Ignored++
			continue
		}
		minutes := max(g.PlaytimeForever, 0)
		status := plugin.Active
		if minutes == 0 {
			status = plugin.Planned
		}
		res.Entries = append(res.Entries, plugin.ParsedEntry{
			Kind:        KindGame,
			Title:       plugin.SingleLine(g.Name, maxTitleRunes),
			ExternalIDs: map[string]string{SourceSteam: strconv.Itoa(g.AppID)},
			Status:      status,
			Progress:    map[string]float64{"playtime": float64(minutes) * 60},
			CoverURL:    plugin.HTTPSURL(steamHeaderURL(g.AppID)),
		})
	}
	return res, nil
}

// isSteamID64 reports whether s is exactly 17 ASCII digits. Full-width and
// other Unicode digits are rejected: the Web API only accepts ASCII.
func isSteamID64(s string) bool {
	if len(s) != 17 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
