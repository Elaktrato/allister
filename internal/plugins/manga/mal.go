package manga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/allister/allister/internal/plugin"
)

const (
	malDefaultURL      = "https://api.myanimelist.net/v2"
	malDefaultInterval = time.Second
	malMaxBody         = 1 << 20
	malTimeout         = 10 * time.Second
)

// malClient is a small client for the official MyAnimeList API v2. The
// Client ID travels only in the X-MAL-CLIENT-ID header and is never part of
// a URL, an error or a log line. Requests are paced to one per interval.
type malClient struct {
	base     string
	http     *http.Client
	clientID func(context.Context) (string, error)
	interval time.Duration

	mu   sync.Mutex
	last time.Time
}

func newMALClient(base string, hc *http.Client, id func(context.Context) (string, error), interval time.Duration) *malClient {
	if base == "" {
		base = malDefaultURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: malTimeout}
	}
	if interval <= 0 {
		interval = malDefaultInterval
	}
	return &malClient{base: strings.TrimRight(base, "/"), http: hc, clientID: id, interval: interval}
}

// wait blocks until one interval has passed since the previous request.
func (c *malClient) wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := time.Until(c.last.Add(c.interval)); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.last = time.Now()
	return nil
}

// Serialization returns the magazine names of a manga by its MAL id. An
// unset Client ID is plugin.ErrNotConfigured.
func (c *malClient) Serialization(ctx context.Context, malID string) ([]string, error) {
	if err := checkID(malID); err != nil {
		return nil, err
	}
	if c.clientID == nil {
		return nil, fmt.Errorf("mal: no Client ID: %w", plugin.ErrNotConfigured)
	}
	id, err := c.clientID(ctx)
	if err != nil {
		return nil, fmt.Errorf("mal: Client ID lookup failed: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("mal: no Client ID: %w", plugin.ErrNotConfigured)
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	u := c.base + "/manga/" + malID + "?fields=serialization"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mal: %w", plugin.ErrUnavailable)
	}
	req.Header.Set("X-MAL-CLIENT-ID", id)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// the transport error is dropped: it could echo request details
		var ue *url.Error
		if errors.As(err, &ue) && ue.Timeout() {
			return nil, fmt.Errorf("mal: request timed out: %w", plugin.ErrUnavailable)
		}
		return nil, fmt.Errorf("mal: request failed: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("mal: manga %s: %w", malID, plugin.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("mal: %w", plugin.ErrRateLimited)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("mal: Client ID refused (HTTP %d): %w", resp.StatusCode, plugin.ErrNotConfigured)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("mal: HTTP %d: %w", resp.StatusCode, plugin.ErrUnavailable)
	}
	var doc struct {
		Serialization []struct {
			Node struct {
				Name string `json:"name"`
			} `json:"node"`
		} `json:"serialization"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, malMaxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("mal: unreadable answer: %w", plugin.ErrBadResponse)
	}
	var out []string
	for _, s := range doc.Serialization {
		if n := plugin.SingleLine(s.Node.Name, maxNameRunes); n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}
