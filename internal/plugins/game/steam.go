package game

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

// Default endpoints of the official Steam Web API and the public store API.
const (
	DefaultSteamAPIBase   = "https://api.steampowered.com"
	DefaultSteamStoreBase = "https://store.steampowered.com"
)

const (
	// maxSteamBody bounds a Steam response so a misbehaving server cannot
	// exhaust memory.
	maxSteamBody   = 8 << 20
	steamUserAgent = "allister (+https://github.com/allister/allister)"
	// steamStoreAppPrefix is where a store app page lives.
	steamStoreAppPrefix = "https://store.steampowered.com/app/"
	// steamHeaderPrefix serves the store header image of an app.
	steamHeaderPrefix = "https://cdn.cloudflare.steamstatic.com/steam/apps/"
)

// steamReleaseDateLayouts are the date layouts the store uses, most specific
// first. Values such as "Coming soon" or "Q4 2024" match none and yield no
// date.
var steamReleaseDateLayouts = []string{"2 Jan, 2006", "Jan 2, 2006", "2006"}

// steamKeyFunc resolves the Steam Web API key for one request. It is called on
// every keyed request, so a key saved in the settings takes effect without a
// restart.
type steamKeyFunc func(context.Context) (string, error)

// steamClient talks to two Steam APIs: the official Web API (the account
// library) and the public store API (search and metadata). The store calls are
// keyless; the source checks for a key before calling them, and the key is
// never sent to the store. Its methods are safe for concurrent use.
type steamClient struct {
	apiBase, storeBase string
	key                steamKeyFunc
	hc                 *http.Client
}

// newSteamClient returns a client for the production endpoints when a base is
// empty, resolving its key through key and sending requests through hc
// (http.DefaultClient when nil).
func newSteamClient(apiBase, storeBase string, key func(context.Context) (string, error), hc *http.Client) *steamClient {
	if apiBase == "" {
		apiBase = DefaultSteamAPIBase
	}
	if storeBase == "" {
		storeBase = DefaultSteamStoreBase
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &steamClient{
		apiBase:   strings.TrimRight(apiBase, "/"),
		storeBase: strings.TrimRight(storeBase, "/"),
		key:       key,
		hc:        hc,
	}
}

// requireKey resolves the key for one call. A missing resolver or an empty key
// means the source is not configured; a resolver failure is transient. The
// returned key is trimmed.
func (c *steamClient) requireKey(ctx context.Context) (string, error) {
	if c.key == nil {
		return "", fmt.Errorf("steam: %w", plugin.ErrNotConfigured)
	}
	key, err := c.key(ctx)
	if err != nil {
		return "", fmt.Errorf("steam: %w", plugin.ErrUnavailable)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("steam: %w", plugin.ErrNotConfigured)
	}
	return key, nil
}

// steamGame is one owned game of an account. PlaytimeForever and
// Playtime2Weeks are minutes, as the Web API reports them.
type steamGame struct {
	AppID           int    `json:"appid"`
	Name            string `json:"name"`
	PlaytimeForever int    `json:"playtime_forever"`
	Playtime2Weeks  int    `json:"playtime_2weeks"`
	IconURL         string `json:"img_icon_url"`
}

// ownedGames fetches the account's library through the official Web API. The
// key travels in the query and no transport error is wrapped, so neither a
// key nor a URL can end up in an error.
//
// An empty or private library is actionable bad input: the user has to make
// the profile and its game details public. Errors otherwise wrap
// plugin.ErrNotConfigured (no or refused key), plugin.ErrNotFound,
// plugin.ErrRateLimited, plugin.ErrUnavailable or plugin.ErrBadResponse.
func (c *steamClient) ownedGames(ctx context.Context, steamID string) ([]steamGame, error) {
	key, err := c.requireKey(ctx)
	if err != nil {
		return nil, err
	}
	req, err := c.request(ctx, c.apiBase, "/IPlayerService/GetOwnedGames/v1/", url.Values{
		"key":                       {key},
		"steamid":                   {strings.TrimSpace(steamID)},
		"include_appinfo":           {"1"},
		"include_played_free_games": {"1"},
		"format":                    {"json"},
	})
	if err != nil {
		return nil, err
	}
	body, err := c.getBody(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Response struct {
			Games []steamGame `json:"games"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: steam owned games: %v", plugin.ErrBadResponse, err)
	}
	if len(out.Response.Games) == 0 {
		return nil, fmt.Errorf("%w: Steam returned no games; make your profile and game details public", plugin.ErrBadInput)
	}
	return out.Response.Games, nil
}

// steamStoreSearchItem is one item of a store search response.
type steamStoreSearchItem struct {
	Type      string `json:"type"`
	ID        int    `json:"id"`
	Name      string `json:"name"`
	TinyImage string `json:"tiny_image"`
}

// search runs a keyless store search and maps the app results to summaries.
// Non-app results (DLC, bundles, ...) are skipped.
func (c *steamClient) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	req, err := c.request(ctx, c.storeBase, "/api/storesearch/", url.Values{
		"term": {q},
		"l":    {"english"},
		"cc":   {"us"},
	})
	if err != nil {
		return nil, err
	}
	body, err := c.getBody(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []steamStoreSearchItem `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: steam store search: %v", plugin.ErrBadResponse, err)
	}
	summaries := make([]plugin.ItemSummary, 0, len(out.Items))
	for _, item := range out.Items {
		if item.Type != "app" || item.ID <= 0 {
			continue
		}
		title := plugin.SingleLine(item.Name, maxTitleRunes)
		if title == "" {
			continue
		}
		summaries = append(summaries, plugin.ItemSummary{
			Source:   SourceSteam,
			ID:       strconv.Itoa(item.ID),
			Kind:     KindGame,
			Titles:   []plugin.Title{{Lang: "en", Text: title}},
			CoverURL: plugin.HTTPSURL(item.TinyImage),
		})
	}
	return summaries, nil
}

// steamPlatforms is the platform block of a store response.
type steamPlatforms struct {
	Windows bool `json:"windows"`
	Mac     bool `json:"mac"`
	Linux   bool `json:"linux"`
}

// steamAppData is the "data" member of a store appdetails response.
type steamAppData struct {
	Type             string       `json:"type"`
	Name             string       `json:"name"`
	SteamAppID       int          `json:"steam_appid"`
	ShortDescription string       `json:"short_description"`
	AboutTheGame     string       `json:"about_the_game"`
	HeaderImage      string       `json:"header_image"`
	Developers       []string     `json:"developers"`
	Publishers       []string     `json:"publishers"`
	Genres           []steamNamed `json:"genres"`
	Categories       []steamNamed `json:"categories"`
	ReleaseDate      struct {
		ComingSoon bool   `json:"coming_soon"`
		Date       string `json:"date"`
	} `json:"release_date"`
	Platforms steamPlatforms `json:"platforms"`
	// Website is parsed but not mapped: the game kind has no website field and
	// the store page is the external URL.
	Website string `json:"website"`
}

type steamNamed struct {
	Description string `json:"description"`
}

type steamAppDetails struct {
	Success bool         `json:"success"`
	Data    steamAppData `json:"data"`
}

// details fetches the keyless store metadata of one app and maps it to the
// neutral game record. A missing or unsuccessful entry, and every type other
// than "game" (DLC, demo, software, ...), is plugin.ErrNotFound.
func (c *steamClient) details(ctx context.Context, appid string) (gameRecord, error) {
	id, err := steamAppID(appid)
	if err != nil {
		return gameRecord{}, err
	}
	req, err := c.request(ctx, c.storeBase, "/api/appdetails", url.Values{
		"appids": {id},
		"l":      {"english"},
	})
	if err != nil {
		return gameRecord{}, err
	}
	body, err := c.getBody(req)
	if err != nil {
		return gameRecord{}, err
	}
	var out map[string]steamAppDetails
	if err := json.Unmarshal(body, &out); err != nil {
		return gameRecord{}, fmt.Errorf("%w: steam app details: %v", plugin.ErrBadResponse, err)
	}
	app, ok := out[id]
	if !ok || !app.Success || app.Data.Type != "game" {
		return gameRecord{}, plugin.ErrNotFound
	}
	return steamGameRecord(id, app.Data), nil
}

// request builds a GET request with the shared headers. The URL is built
// before the query (and so before a key) is attached, so an invalid base can
// never put a key into an error text.
func (c *steamClient) request(ctx context.Context, base, path string, query url.Values) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("steam: %w", plugin.ErrUnavailable)
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", steamUserAgent)
	return req, nil
}

// getBody performs one request and returns its body. Transport errors are
// deliberately not wrapped (a key can be part of the request URL); the caller
// maps the status through steamStatusError.
func (c *steamClient) getBody(req *http.Request) ([]byte, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("steam: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()

	if err := steamStatusError(resp.StatusCode); err != nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSteamBody+1))
	if err != nil {
		return nil, fmt.Errorf("steam: %w", plugin.ErrUnavailable)
	}
	if len(body) > maxSteamBody {
		return nil, fmt.Errorf("%w: steam response too large", plugin.ErrBadResponse)
	}
	return body, nil
}

// steamStatusError maps a Steam status code. 401/403 mean the credential was
// refused; 404 means the item is unknown; 429 and 5xx/timeout are retryable.
func steamStatusError(status int) error {
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("steam: %w", plugin.ErrNotConfigured)
	case status == http.StatusNotFound:
		return fmt.Errorf("steam: %w", plugin.ErrNotFound)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("steam: %w", plugin.ErrRateLimited)
	case status >= 500, status == http.StatusRequestTimeout:
		return fmt.Errorf("steam: %w", plugin.ErrUnavailable)
	default:
		return fmt.Errorf("%w: steam status %d", plugin.ErrBadResponse, status)
	}
}

// steamAppID validates and normalises a store app id.
func steamAppID(raw string) (string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return "", plugin.ErrNotFound
	}
	return strconv.Itoa(n), nil
}

// steamGameRecord maps one appdetails response to the neutral game record.
func steamGameRecord(appID string, d steamAppData) gameRecord {
	rec := gameRecord{
		Source:      SourceSteam,
		ID:          appID,
		CoverURL:    d.HeaderImage,
		ExternalURL: steamStoreAppPrefix + appID,
		ExternalIDs: map[string]string{"steam": appID},
	}
	if d.SteamAppID > 0 {
		rec.ExternalIDs["steam"] = strconv.Itoa(d.SteamAppID)
	}
	if name := plugin.SingleLine(d.Name, maxTitleRunes); name != "" {
		rec.Titles = []plugin.Title{{Lang: "en", Text: name}}
	}

	fields := map[string]plugin.FieldValue{}
	if !d.ReleaseDate.ComingSoon {
		if day, ok := steamReleaseDate(d.ReleaseDate.Date); ok {
			date := plugin.DateOnly(day)
			fields["released_on"] = plugin.FieldValue{Date: &date}
			year := float64(date.Year())
			fields["year"] = plugin.FieldValue{Num: &year}
		}
	}
	if platforms := steamPlatformNames(d.Platforms); platforms != "" {
		fields["platforms"] = plugin.FieldValue{Text: platforms}
	}
	if developer := steamJoinNames(d.Developers, 3); developer != "" {
		fields["developer"] = plugin.FieldValue{Text: developer}
	}
	if publisher := steamJoinNames(d.Publishers, 3); publisher != "" {
		fields["publisher"] = plugin.FieldValue{Text: publisher}
	}
	if summary := steamSummary(d); summary != "" {
		fields["summary"] = plugin.FieldValue{Text: summary}
	}
	rec.Fields = fields

	for _, genre := range d.Genres {
		rec.Genres = append(rec.Genres, genre.Description)
	}
	for _, category := range d.Categories {
		rec.Tags = append(rec.Tags, plugin.Tag{Name: category.Description})
	}
	return rec
}

// steamSummary prefers the short description and falls back to the longer
// about text, both converted from store HTML to plain text.
func steamSummary(d steamAppData) string {
	text := d.ShortDescription
	if strings.TrimSpace(text) == "" {
		text = d.AboutTheGame
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return anilist.PlainText(text)
}

// steamReleaseDate parses the store's release date. Unknown formats, including
// "Coming soon" and quarters, yield no date.
func steamReleaseDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range steamReleaseDateLayouts {
		if day, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return day, true
		}
	}
	return time.Time{}, false
}

func steamPlatformNames(p steamPlatforms) string {
	var names []string
	if p.Windows {
		names = append(names, "Windows")
	}
	if p.Mac {
		names = append(names, "macOS")
	}
	if p.Linux {
		names = append(names, "Linux")
	}
	return strings.Join(names, ", ")
}

// steamJoinNames sanitises names and joins them with ", ". limit <= 0 means no
// limit.
func steamJoinNames(names []string, limit int) string {
	clean := make([]string, 0, len(names))
	for _, raw := range names {
		if limit > 0 && len(clean) == limit {
			break
		}
		if name := plugin.SingleLine(raw, maxTermRunes); name != "" {
			clean = append(clean, name)
		}
	}
	return strings.Join(clean, ", ")
}

// steamHeaderURL is the store header image of an app, which the account import
// uses as an entry cover.
func steamHeaderURL(appID int) string {
	if appID <= 0 {
		return ""
	}
	return steamHeaderPrefix + strconv.Itoa(appID) + "/header.jpg"
}

// steamSource is the game plugin's Steam source: store search and details plus
// the Steam external-id lookup. The account library is fetched by the account
// importer through the same client. The key gates every call that reaches a
// server; People and Recommendations need no key because they make no call.
type steamSource struct{ c *steamClient }

var _ plugin.Source = (*steamSource)(nil)

func (s *steamSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	if _, err := s.c.requireKey(ctx); err != nil {
		return nil, err
	}
	q = plugin.SingleLine(q, maxTermRunes)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	return s.c.search(ctx, q)
}

func (s *steamSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindGame {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	if _, err := s.c.requireKey(ctx); err != nil {
		return plugin.ItemData{}, err
	}
	rec, err := s.c.details(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return gameData(rec), nil
}

func (s *steamSource) People(_ context.Context, kind, _ string) ([]plugin.Credit, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	return nil, nil
}

func (s *steamSource) Recommendations(_ context.Context, kind, _ string) ([]plugin.ItemSummary, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	return nil, nil
}

func (s *steamSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindGame || idType != SourceSteam {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.Details(ctx, kind, value)
}
