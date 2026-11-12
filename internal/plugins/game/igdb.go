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
	"sync"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// Default endpoints of the IGDB API and its Twitch OAuth token endpoint.
const (
	DefaultIGDBBase     = "https://api.igdb.com/v4"
	DefaultIGDBTokenURL = "https://id.twitch.tv/oauth2/token"
)

const (
	// maxIGDBBody bounds an IGDB response so a misbehaving server cannot
	// exhaust memory.
	maxIGDBBody = 8 << 20
	// tokenEarlyRefresh refreshes a cached token 60 seconds before it expires.
	tokenEarlyRefresh = 60 * time.Second
	igdbUserAgent     = "allister (+https://github.com/allister/allister)"
	// igdbSteamSource is the external_game_source value of Steam.
	igdbSteamSource = 1
	// igdbGameFields is the details query without its where clause.
	igdbGameFields = "fields name,slug,summary,first_release_date,platforms.name,genres.name,themes.name,keywords.name,involved_companies.company.name,involved_companies.developer,involved_companies.publisher,cover.url,similar_games.id,similar_games.name,similar_games.first_release_date,similar_games.cover.url,external_games.uid,external_games.external_game_source;"
	// defaultIGDBTokenTTL is used when Twitch omits expires_in.
	defaultIGDBTokenTTL = time.Hour
)

// igdbKeyFunc resolves one Twitch app credential for a request. It is called
// on every request, so a credential saved in the settings takes effect without
// a restart.
type igdbKeyFunc func(context.Context) (string, error)

// igdbClient is a small IGDB JSON client. It caches the OAuth token with the
// client ID it was issued for and its expiry. Its methods are safe for
// concurrent use.
type igdbClient struct {
	base, tokenURL string
	clientID       igdbKeyFunc
	clientSecret   igdbKeyFunc
	hc             *http.Client

	mu            sync.Mutex
	token         string
	tokenClientID string
	expires       time.Time
}

// newIGDBClient returns a client for base and tokenURL (the production
// endpoints when empty) that resolves its credentials through clientID and
// clientSecret and sends requests through hc (http.DefaultClient when nil).
func newIGDBClient(base, tokenURL string, clientID, clientSecret igdbKeyFunc, hc *http.Client) *igdbClient {
	if base == "" {
		base = DefaultIGDBBase
	}
	if tokenURL == "" {
		tokenURL = DefaultIGDBTokenURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &igdbClient{
		base:         strings.TrimRight(base, "/"),
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		hc:           hc,
	}
}

// Post sends an Apicalypse query to endpoint and decodes the JSON response
// into out. A 401 refreshes the token once and repeats the request; a second
// 401 means the credentials are refused.
//
// Errors wrap plugin.ErrNotConfigured (missing or refused credentials),
// plugin.ErrNotFound (404), plugin.ErrRateLimited (429), plugin.ErrUnavailable
// (5xx, timeout, network) or plugin.ErrBadResponse (any other status, an
// oversized or malformed body). Neither the secret nor the token is ever part
// of an error, including the token request URL.
func (c *igdbClient) Post(ctx context.Context, endpoint, query string, out any) error {
	id, secret, err := c.credentials(ctx)
	if err != nil {
		return err
	}
	token, err := c.tokenFor(ctx, id, secret)
	if err != nil {
		return err
	}
	status, body, err := c.send(ctx, id, token, endpoint, query)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		c.dropToken(token)
		if token, err = c.tokenFor(ctx, id, secret); err != nil {
			return err
		}
		if status, body, err = c.send(ctx, id, token, endpoint, query); err != nil {
			return err
		}
		if status == http.StatusUnauthorized {
			return fmt.Errorf("igdb: %w", plugin.ErrNotConfigured)
		}
	}
	if err := igdbStatusError(status); err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: igdb: %v", plugin.ErrBadResponse, err)
	}
	return nil
}

// credentials resolves and validates the Twitch app credentials for one
// request. A missing or empty credential is not configured; a resolver
// failure is transient.
func (c *igdbClient) credentials(ctx context.Context) (string, string, error) {
	if c.clientID == nil || c.clientSecret == nil {
		return "", "", fmt.Errorf("igdb: %w", plugin.ErrNotConfigured)
	}
	id, err := c.clientID(ctx)
	if err != nil {
		return "", "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	secret, err := c.clientSecret(ctx)
	if err != nil {
		return "", "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
	if id == "" || secret == "" {
		return "", "", fmt.Errorf("igdb: %w", plugin.ErrNotConfigured)
	}
	return id, secret, nil
}

// send performs one data request. A non-200 response is returned with its
// status and no body; the caller maps it.
func (c *igdbClient) send(ctx context.Context, clientID, token, endpoint, query string) (int, []byte, error) {
	// The token travels in a header, never in the URL, but request and
	// transport errors are still deliberately not wrapped: an error must not
	// carry the query or credential of a request back to the caller.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+strings.TrimLeft(endpoint, "/"), strings.NewReader(query))
	if err != nil {
		return 0, nil, fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	req.Header.Set("Client-ID", clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", igdbUserAgent)

	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		return 0, nil, fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIGDBBody+1))
	if err != nil {
		return 0, nil, fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	if len(body) > maxIGDBBody {
		return 0, nil, fmt.Errorf("%w: igdb response too large", plugin.ErrBadResponse)
	}
	return resp.StatusCode, body, nil
}

func igdbStatusError(status int) error {
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusBadRequest, status == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: igdb status %d", plugin.ErrBadResponse, status)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("igdb: %w", plugin.ErrNotConfigured)
	case status == http.StatusNotFound:
		return fmt.Errorf("igdb: %w", plugin.ErrNotFound)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("igdb: %w", plugin.ErrRateLimited)
	case status >= 500, status == http.StatusRequestTimeout:
		return fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	default:
		return fmt.Errorf("%w: igdb status %d", plugin.ErrBadResponse, status)
	}
}

// tokenFor returns a cached token for clientID or fetches a new one. The
// mutex is held across the token HTTP call, so concurrent callers share one
// fetch; the injected http.Client (with its timeout, set by the plugin) bounds
// how long that wait can be.
func (c *igdbClient) tokenFor(ctx context.Context, clientID, secret string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.tokenClientID == clientID && time.Now().Before(c.expires.Add(-tokenEarlyRefresh)) {
		return c.token, nil
	}
	return c.fetchToken(ctx, clientID, secret)
}

func (c *igdbClient) fetchToken(ctx context.Context, clientID, secret string) (string, error) {
	u, err := url.Parse(c.tokenURL)
	if err != nil {
		return "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	query := u.Query()
	query.Set("client_id", clientID)
	query.Set("client_secret", secret)
	query.Set("grant_type", "client_credentials")
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", igdbUserAgent)

	resp, err := c.hc.Do(req)
	if err != nil {
		// The URL carries the secret; the error is deliberately not wrapped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("igdb: token request: %w", plugin.ErrNotConfigured)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("igdb: token request: %w", plugin.ErrRateLimited)
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return "", fmt.Errorf("igdb: token request: %w", plugin.ErrUnavailable)
	default:
		return "", fmt.Errorf("%w: igdb token status %d", plugin.ErrBadResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIGDBBody+1))
	if err != nil {
		return "", fmt.Errorf("igdb: %w", plugin.ErrUnavailable)
	}
	if len(body) > maxIGDBBody {
		return "", fmt.Errorf("%w: igdb token response too large", plugin.ErrBadResponse)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: igdb token: %v", plugin.ErrBadResponse, err)
	}
	out.AccessToken = strings.TrimSpace(out.AccessToken)
	if out.AccessToken == "" {
		return "", fmt.Errorf("%w: igdb token response has no access token", plugin.ErrBadResponse)
	}

	c.token = out.AccessToken
	c.tokenClientID = clientID
	ttl := defaultIGDBTokenTTL
	if out.ExpiresIn > 0 {
		ttl = time.Duration(out.ExpiresIn) * time.Second
	}
	c.expires = time.Now().Add(ttl)
	return c.token, nil
}

// dropToken drops token from the cache unless another caller already replaced
// it, in which case that newer token is reused.
func (c *igdbClient) dropToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == token {
		c.token = ""
		c.tokenClientID = ""
		c.expires = time.Time{}
	}
}

// igdbSource is the game plugin's IGDB source. The HTTP and OAuth plumbing
// lives in igdbClient; the queries and mapping stay here.
type igdbSource struct{ c *igdbClient }

var _ plugin.Source = (*igdbSource)(nil)

func (s *igdbSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	q = plugin.SingleLine(q, maxTitleRunes)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	body := fmt.Sprintf(`search "%s"; fields name,slug,first_release_date,cover.url; where version_parent = null; limit 20;`, igdbQuote(q))
	var games []igdbGame
	if err := s.c.Post(ctx, "games", body, &games); err != nil {
		return nil, err
	}
	out := make([]plugin.ItemSummary, 0, len(games))
	for _, g := range games {
		if g.ID <= 0 {
			continue
		}
		sum := igdbSummary(g.ID, g.Name, g.Cover.URL, g.FirstReleaseDate)
		if len(sum.Titles) == 0 {
			continue
		}
		out = append(out, sum)
	}
	return out, nil
}

// Details resolves a positive numeric IGDB id or a non-empty slug (a pasted
// IGDB URL carries the slug) and returns the game with its numeric id, so a
// matched slug and a search-add converge on one item. An empty or
// non-positive numeric id is not found.
func (s *igdbSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindGame {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.byIDOrSlug(ctx, id)
}

// byIDOrSlug runs the details query for a numeric id (where id = <n>) or, for
// anything else, a slug (where slug = "<slug>"). Lookup and Details share it,
// so the two cannot drift.
func (s *igdbSource) byIDOrSlug(ctx context.Context, id string) (plugin.ItemData, error) {
	id = strings.TrimSpace(id)
	if n, err := strconv.Atoi(id); err == nil {
		if n <= 0 {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		return s.details(ctx, "where id = "+strconv.Itoa(n)+"; limit 1;")
	}
	slug := plugin.SingleLine(id, maxIDRunes)
	if slug == "" {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.details(ctx, `where slug = "`+igdbQuote(slug)+`"; limit 1;`)
}

func (s *igdbSource) People(_ context.Context, kind, _ string) ([]plugin.Credit, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	return nil, nil
}

func (s *igdbSource) Recommendations(ctx context.Context, kind, id string) ([]plugin.ItemSummary, error) {
	if kind != KindGame {
		return nil, plugin.ErrNotFound
	}
	n, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil || n <= 0 {
		return nil, plugin.ErrNotFound
	}
	g, err := s.game(ctx, "where id = "+strconv.Itoa(n)+"; limit 1;")
	if err != nil {
		return nil, err
	}
	recs := make([]plugin.ItemSummary, 0, len(g.SimilarGames))
	for _, sim := range g.SimilarGames {
		if sim.ID <= 0 {
			continue
		}
		recs = append(recs, igdbSummary(sim.ID, sim.Name, sim.Cover.URL, sim.FirstReleaseDate))
	}
	return gameRecommendations(recs), nil
}

func (s *igdbSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindGame {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	value = strings.TrimSpace(value)
	switch idType {
	case "igdb":
		return s.byIDOrSlug(ctx, value)
	case "steam":
		appID := plugin.SingleLine(value, maxIDRunes)
		if appID == "" {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		var rows []igdbExternalGameRef
		body := fmt.Sprintf(`fields game; where uid = "%s" & external_game_source = %d; limit 1;`, igdbQuote(appID), igdbSteamSource)
		if err := s.c.Post(ctx, "external_games", body, &rows); err != nil {
			return plugin.ItemData{}, err
		}
		if len(rows) == 0 || rows[0].Game <= 0 {
			return plugin.ItemData{}, plugin.ErrNotFound
		}
		d, err := s.details(ctx, "where id = "+strconv.Itoa(rows[0].Game)+"; limit 1;")
		if err != nil {
			return plugin.ItemData{}, err
		}
		// The lookup found the game by its Steam app id even when the details
		// response did not carry the external game.
		if d.ExternalIDs == nil {
			d.ExternalIDs = map[string]string{}
		}
		d.ExternalIDs["steam"] = appID
		return d, nil
	}
	return plugin.ItemData{}, plugin.ErrNotFound
}

func (s *igdbSource) details(ctx context.Context, where string) (plugin.ItemData, error) {
	g, err := s.game(ctx, where)
	if err != nil {
		return plugin.ItemData{}, err
	}
	tb := s.timeToBeat(ctx, g.ID)
	// A cancelled request is not a source failure; time-to-beat only ignores
	// real source errors.
	if err := ctx.Err(); err != nil {
		return plugin.ItemData{}, err
	}
	return gameData(igdbGameRecord(g, tb)), nil
}

func (s *igdbSource) game(ctx context.Context, where string) (igdbGame, error) {
	var games []igdbGame
	if err := s.c.Post(ctx, "games", igdbGameFields+" "+where, &games); err != nil {
		return igdbGame{}, err
	}
	if len(games) == 0 || games[0].ID <= 0 {
		return igdbGame{}, plugin.ErrNotFound
	}
	return games[0], nil
}

// timeToBeat is best-effort: any failure is ignored, so a flaky endpoint
// never fails the overview, the sync or an import row.
func (s *igdbSource) timeToBeat(ctx context.Context, gameID int) igdbTimeToBeat {
	body := fmt.Sprintf("fields hastily,normally,completely,count; where game_id = %d; limit 1;", gameID)
	var rows []igdbTimeToBeat
	if err := s.c.Post(ctx, "game_time_to_beats", body, &rows); err != nil || len(rows) == 0 {
		return igdbTimeToBeat{}
	}
	return rows[0]
}

type igdbGame struct {
	ID                int                   `json:"id"`
	Name              string                `json:"name"`
	Slug              string                `json:"slug"`
	Summary           string                `json:"summary"`
	FirstReleaseDate  int64                 `json:"first_release_date"`
	Platforms         []igdbNamed           `json:"platforms"`
	Genres            []igdbNamed           `json:"genres"`
	Themes            []igdbNamed           `json:"themes"`
	Keywords          []igdbNamed           `json:"keywords"`
	InvolvedCompanies []igdbInvolvedCompany `json:"involved_companies"`
	Cover             igdbCoverImage        `json:"cover"`
	SimilarGames      []igdbSimilarGame     `json:"similar_games"`
	ExternalGames     []igdbExternalGame    `json:"external_games"`
}

type igdbNamed struct {
	Name string `json:"name"`
}

type igdbCoverImage struct {
	URL string `json:"url"`
}

type igdbInvolvedCompany struct {
	Developer bool      `json:"developer"`
	Publisher bool      `json:"publisher"`
	Company   igdbNamed `json:"company"`
}

type igdbSimilarGame struct {
	ID               int            `json:"id"`
	Name             string         `json:"name"`
	FirstReleaseDate int64          `json:"first_release_date"`
	Cover            igdbCoverImage `json:"cover"`
}

type igdbExternalGame struct {
	UID    igdbUID `json:"uid"`
	Source int     `json:"external_game_source"`
}

type igdbExternalGameRef struct {
	Game int `json:"game"`
}

type igdbTimeToBeat struct {
	Hastily    int64 `json:"hastily"`
	Normally   int64 `json:"normally"`
	Completely int64 `json:"completely"`
	Count      int   `json:"count"`
}

// igdbUID decodes an external game uid, which IGDB returns as a string for
// some sources and as a number for others.
type igdbUID string

func (u *igdbUID) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*u = ""
		return nil
	}
	if s[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*u = igdbUID(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("igdb uid: %w", err)
	}
	*u = igdbUID(n.String())
	return nil
}

func igdbGameRecord(g igdbGame, tb igdbTimeToBeat) gameRecord {
	rec := gameRecord{
		Source:      SourceIGDB,
		ID:          strconv.Itoa(g.ID),
		CoverURL:    igdbCover(g.Cover.URL),
		ExternalURL: igdbGameURL(g.Slug, g.ID),
		ExternalIDs: map[string]string{"igdb": strconv.Itoa(g.ID)},
	}
	if name := plugin.SingleLine(g.Name, maxTitleRunes); name != "" {
		rec.Titles = []plugin.Title{{Lang: "en", Text: name}}
	}

	fields := map[string]plugin.FieldValue{}
	if g.FirstReleaseDate > 0 {
		released := time.Unix(g.FirstReleaseDate, 0).UTC()
		day := plugin.DateOnly(released)
		fields["released_on"] = plugin.FieldValue{Date: &day}
		year := float64(released.Year())
		fields["year"] = plugin.FieldValue{Num: &year}
	}
	platforms := make([]string, 0, len(g.Platforms))
	for _, p := range g.Platforms {
		platforms = append(platforms, p.Name)
	}
	if joined := igdbJoinNames(platforms, 0); joined != "" {
		fields["platforms"] = plugin.FieldValue{Text: joined}
	}
	var developers, publishers []string
	for _, c := range g.InvolvedCompanies {
		name := plugin.SingleLine(c.Company.Name, maxTermRunes)
		if name == "" {
			continue
		}
		if c.Developer && len(developers) < 3 {
			developers = append(developers, name)
		}
		if c.Publisher && len(publishers) < 3 {
			publishers = append(publishers, name)
		}
	}
	if joined := igdbJoinNames(developers, 3); joined != "" {
		fields["developer"] = plugin.FieldValue{Text: joined}
	}
	if joined := igdbJoinNames(publishers, 3); joined != "" {
		fields["publisher"] = plugin.FieldValue{Text: joined}
	}
	if g.Summary != "" {
		fields["summary"] = plugin.FieldValue{Text: g.Summary}
	}
	igdbSetMinutes(fields, "time_main", tb.Hastily)
	igdbSetMinutes(fields, "time_extra", tb.Normally)
	igdbSetMinutes(fields, "time_complete", tb.Completely)
	rec.Fields = fields

	for _, genre := range g.Genres {
		rec.Genres = append(rec.Genres, genre.Name)
	}
	for _, theme := range g.Themes {
		rec.Tags = append(rec.Tags, plugin.Tag{Name: theme.Name})
	}
	for _, keyword := range g.Keywords {
		rec.Tags = append(rec.Tags, plugin.Tag{Name: keyword.Name})
	}
	for _, external := range g.ExternalGames {
		if external.Source != igdbSteamSource || external.UID == "" {
			continue
		}
		rec.ExternalIDs["steam"] = string(external.UID)
		break
	}
	return rec
}

func igdbSummary(id int, name, cover string, released int64) plugin.ItemSummary {
	sum := plugin.ItemSummary{
		Source:   SourceIGDB,
		ID:       strconv.Itoa(id),
		Kind:     KindGame,
		CoverURL: igdbCover(cover),
	}
	if title := plugin.SingleLine(name, maxTitleRunes); title != "" {
		sum.Titles = []plugin.Title{{Lang: "en", Text: title}}
	}
	if year := igdbYear(released); year > 0 {
		n := float64(year)
		sum.Fields = map[string]plugin.FieldValue{"year": {Num: &n}}
	}
	return sum
}

func igdbYear(released int64) int {
	if released <= 0 {
		return 0
	}
	return time.Unix(released, 0).UTC().Year()
}

// igdbJoinNames sanitises names and joins them with ", ". limit <= 0 means no
// limit.
func igdbJoinNames(names []string, limit int) string {
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

// igdbSetMinutes maps IGDB's seconds to whole minutes, omitting zero values.
func igdbSetMinutes(fields map[string]plugin.FieldValue, key string, seconds int64) {
	minutes := seconds / 60
	if minutes <= 0 {
		return
	}
	n := float64(minutes)
	fields[key] = plugin.FieldValue{Num: &n}
}

func igdbCover(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	raw = strings.ReplaceAll(raw, "t_thumb", "t_cover_big")
	return plugin.HTTPSURL(raw)
}

func igdbGameURL(slug string, id int) string {
	slug = plugin.SingleLine(slug, maxIDRunes)
	if slug == "" {
		if id <= 0 {
			return ""
		}
		return "https://www.igdb.com/games/" + strconv.Itoa(id)
	}
	return "https://www.igdb.com/games/" + url.PathEscape(slug)
}

// igdbQuote escapes a value for use inside a double-quoted Apicalypse string.
func igdbQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
