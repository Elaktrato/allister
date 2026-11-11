// Package tmdb is the shared The Movie Database client of the TV and movies
// plugins. It resolves the API key per request, so a key saved in the settings
// takes effect without a restart.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// DefaultBase is the production TMDB API root.
const DefaultBase = "https://api.themoviedb.org/3"

// MaxBody bounds how much of a response is read, so a misbehaving server or a
// wrong base URL cannot exhaust memory.
const MaxBody = 8 << 20

const (
	imageBase        = "https://image.tmdb.org/t/p"
	defaultImageSize = "w500"
	userAgent        = "allister (+https://github.com/allister/allister)"
)

// KeyFunc resolves the API key for one request. It is called on every Get, so
// a key changed in the settings is used without a restart.
type KeyFunc func(context.Context) (string, error)

// Client is a small TMDB JSON client. Its methods are safe for concurrent use.
type Client struct {
	base string
	key  KeyFunc
	hc   *http.Client
}

// New returns a client for base (DefaultBase when empty) that resolves its key
// through key and sends requests through hc (http.DefaultClient when nil).
func New(base string, key KeyFunc, hc *http.Client) *Client {
	if base == "" {
		base = DefaultBase
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: hc}
}

// Get fetches path and decodes the JSON response into out. A v3 key is sent as
// the api_key query parameter, a v4 token (eyJ...) as a bearer token. values is
// cloned and never modified.
//
// Errors wrap plugin.ErrNotConfigured (no key, or 401/403), plugin.ErrNotFound
// (404), plugin.ErrRateLimited (429), plugin.ErrUnavailable (5xx, timeout,
// network) or plugin.ErrBadResponse (any other status, oversized or malformed
// body). The key is never part of an error, including the request URL.
func (c *Client) Get(ctx context.Context, path string, values url.Values, out any) error {
	key, err := c.resolveKey(ctx)
	if err != nil {
		return err
	}
	query := cloneValues(values)
	// Build the request before adding the key, so an invalid base cannot put
	// the key into the error text.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("tmdb: %w", plugin.ErrUnavailable)
	}
	if strings.HasPrefix(key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		query.Set("api_key", key)
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.hc.Do(req)
	if err != nil {
		// The error can contain the request URL (and with it a v3 key), so it
		// is deliberately not wrapped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("tmdb: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("tmdb: %w", plugin.ErrNotConfigured)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("tmdb: %w", plugin.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("tmdb: %w", plugin.ErrRateLimited)
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return fmt.Errorf("tmdb: %w", plugin.ErrUnavailable)
	default:
		return fmt.Errorf("%w: tmdb status %d", plugin.ErrBadResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return fmt.Errorf("tmdb: %w", plugin.ErrUnavailable)
	}
	if len(body) > MaxBody {
		return fmt.Errorf("%w: tmdb response too large", plugin.ErrBadResponse)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: tmdb: %v", plugin.ErrBadResponse, err)
	}
	return nil
}

// resolveKey reads the key for this request. A missing resolver or an empty
// key means the source is not configured; a resolver failure is transient.
func (c *Client) resolveKey(ctx context.Context) (string, error) {
	if c.key == nil {
		return "", fmt.Errorf("tmdb: %w", plugin.ErrNotConfigured)
	}
	key, err := c.key(ctx)
	if err != nil {
		return "", fmt.Errorf("tmdb: %w", plugin.ErrUnavailable)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("tmdb: %w", plugin.ErrNotConfigured)
	}
	return key, nil
}

func cloneValues(values url.Values) url.Values {
	out := make(url.Values, len(values)+1)
	for k, vs := range values {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// FindItem is one id in a Find result.
type FindItem struct {
	ID int `json:"id"`
}

// FindResult holds the ids TMDB knows for an external id.
type FindResult struct {
	MovieResults []FindItem `json:"movie_results"`
	TVResults    []FindItem `json:"tv_results"`
}

// Find resolves externalID (for example an IMDb id) to the movie and TV ids
// TMDB knows. source is TMDB's external_source value, for example "imdb_id".
func (c *Client) Find(ctx context.Context, externalID, source string) (FindResult, error) {
	var res FindResult
	values := url.Values{"external_source": {source}}
	if err := c.Get(ctx, "/find/"+url.PathEscape(externalID), values, &res); err != nil {
		return FindResult{}, err
	}
	return res, nil
}

// Image returns the TMDB image URL for path at size (default w500). An empty
// path has no image and yields "".
func Image(path, size string) string {
	if path == "" {
		return ""
	}
	if size == "" {
		size = defaultImageSize
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return imageBase + "/" + size + path
}
