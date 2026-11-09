package web

import (
	"sync"
	"time"

	"github.com/allister/allister/internal/provider"
)

const (
	cacheTTL        = 10 * time.Minute
	cacheMaxEntries = 100
)

// cachedDetail is what an unsaved overview fetched from the provider.
type cachedDetail struct {
	Anime      provider.Anime
	Cast       []provider.CastMember
	CastFailed bool
}

type cacheItem struct {
	d  cachedDetail
	at time.Time
}

// animeCache is a small TTL cache, bounded in size, so that the add handler
// can save what the overview already fetched without another provider call.
type animeCache struct {
	mu  sync.Mutex
	now func() time.Time
	max int
	ttl time.Duration
	m   map[string]cacheItem
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
