package web

import (
	"net/url"
	"sync"
	"time"

	"github.com/allister/allister/internal/plugin"
)

const (
	cacheTTL        = 10 * time.Minute
	cacheMaxEntries = 100
)

// cachedDetail is what the overview of an unsaved item fetched from its
// plugin's source: the details and the credits (CreditsFailed when People
// failed). Recommendations are loaded late and cached apart (recCache).
// DetailsOnly marks an entry the add dialog stored after a lone Details
// call: the add flow uses it, the overview treats it as a miss.
type cachedDetail struct {
	Data          plugin.ItemData
	DetailsOnly   bool
	Credits       []plugin.Credit
	CreditsFailed bool
}

type cacheItem struct {
	d  cachedDetail
	at time.Time
}

// animeCache is a small TTL cache of live item data keyed by liveKey,
// bounded in size, so that the add handler can save what the overview
// already fetched without another source call.
type animeCache struct {
	mu  sync.Mutex
	now func() time.Time
	max int
	ttl time.Duration
	m   map[string]cacheItem
}

// liveKey is the cache key of an unsaved item's live data. Every part is
// path escaped, so the "/" separators cannot collide.
func liveKey(pluginID, source, kind, id string) string {
	return url.PathEscape(pluginID) + "/" + url.PathEscape(source) + "/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}

func newAnimeCache(now func() time.Time, max int, ttl time.Duration) *animeCache {
	return &animeCache{now: now, max: max, ttl: ttl, m: map[string]cacheItem{}}
}

func (c *animeCache) get(key string) (cachedDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.m[key]
	if !ok {
		return cachedDetail{}, false
	}
	if c.now().Sub(it.at) >= c.ttl {
		delete(c.m, key)
		return cachedDetail{}, false
	}
	return it.d, true
}

func (c *animeCache) put(key string, d cachedDetail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, it := range c.m {
		if now.Sub(it.at) >= c.ttl {
			delete(c.m, k)
		}
	}
	if _, exists := c.m[key]; !exists {
		for len(c.m) >= c.max {
			oldest, first := "", true
			var at time.Time
			for k, it := range c.m {
				if first || it.at.Before(at) {
					oldest, at, first = k, it.at, false
				}
			}
			delete(c.m, oldest)
		}
	}
	c.m[key] = cacheItem{d: d, at: now}
}
