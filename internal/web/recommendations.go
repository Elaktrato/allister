package web

// Late-loading recommendations: the item overview never fetches them
// inline. It renders a placeholder that loads
// GET /{plugin}/{source}/{id}/recommendations (hx-trigger="load", plus a
// plain link without JavaScript). The endpoint asks the plugin's source with
// a deadline, caches results for a while and always answers 200 with a
// fragment (cards, nothing, or an "unavailable" notice with a retry), never
// a 500, so a slow or failing source cannot break or delay the overview.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

const (
	// recTimeout bounds one Source.Recommendations call.
	recTimeout = 4 * time.Second
	// recCacheMaxEntries bounds the recommendations cache (entries live
	// cacheTTL, 10 minutes).
	recCacheMaxEntries = 200
)

// recCache is a small TTL cache of recommendation results keyed by liveKey
// (plugin, source, kind, id), bounded in size. Only successful results are
// stored, and values are copied on the way in and out, so neither the
// source nor a caller can change a cached entry.
type recCache struct {
	mu  sync.Mutex
	now func() time.Time
	max int
	ttl time.Duration
	m   map[string]recEntry
}

type recEntry struct {
	recs []plugin.ItemSummary
	at   time.Time
}

func newRecCache(now func() time.Time, max int, ttl time.Duration) *recCache {
	return &recCache{now: now, max: max, ttl: ttl, m: map[string]recEntry{}}
}

func (c *recCache) get(key string) ([]plugin.ItemSummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(e.at) >= c.ttl {
		delete(c.m, key)
		return nil, false
	}
	return copySummaries(e.recs), true
}

func (c *recCache) put(key string, recs []plugin.ItemSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, e := range c.m {
		if now.Sub(e.at) >= c.ttl {
			delete(c.m, k)
		}
	}
	if _, exists := c.m[key]; !exists {
		for len(c.m) >= c.max {
			oldest, first := "", true
			var at time.Time
			for k, e := range c.m {
				if first || e.at.Before(at) {
					oldest, at, first = k, e.at, false
				}
			}
			delete(c.m, oldest)
		}
	}
	c.m[key] = recEntry{recs: copySummaries(recs), at: now}
}

// copySummaries deep-copies the slices and maps of summaries (FieldValue's
// pointers and lists included).
func copySummaries(in []plugin.ItemSummary) []plugin.ItemSummary {
	if in == nil {
		return nil
	}
	out := make([]plugin.ItemSummary, len(in))
	for i, s := range in {
		s.Titles = append([]plugin.Title(nil), s.Titles...)
		if s.Fields != nil {
			f := make(map[string]plugin.FieldValue, len(s.Fields))
			for k, v := range s.Fields {
				f[k] = copyFieldValue(v)
			}
			s.Fields = f
		}
		out[i] = s
	}
	return out
}

func copyFieldValue(v plugin.FieldValue) plugin.FieldValue {
	if v.Num != nil {
		n := *v.Num
		v.Num = &n
	}
	if v.Date != nil {
		d := *v.Date
		v.Date = &d
	}
	if v.List != nil {
		v.List = append([]string(nil), v.List...)
	}
	return v
}

// recommendationsURL is the endpoint of an item's recommendations (with
// ?kind= when the plugin has several kinds).
func recommendationsURL(m plugin.Manifest, source, id, kind string) string {
	return itemAction(m, source, id, kind, "recommendations", nil)
}

// recsView feeds recommendations.html: the cards, or Failed with a retry
// of URL. Nothing to show (no cards, not failed) renders an empty fragment.
type recsView struct {
	Heading string
	Cards   []linkCard
	Failed  bool
	// URL is the endpoint itself (retry); ItemURL the item's overview (the
	// full page links back to it).
	URL, ItemURL string
}

// getRecommendations answers GET /{plugin}/{source}/{id}/recommendations.
// Unknown or undeclared sources, a manual item, an unknown kind, a kind
// without the recommendations section and an id that needs ?kind= are 404
// (a disabled plugin never gets here: the plugin guard answers 404). The
// item need not be saved. Every other outcome is 200: the cards, an empty
// fragment, or the "unavailable" notice when the source failed or timed out.
func (s *server) getRecommendations(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	ref := itemRef{Plugin: pluginID, Source: r.PathValue("source"), ID: r.PathValue("id"), Kind: r.URL.Query().Get("kind")}
	if !ok || !validPathID(ref.Source) || !validPathID(ref.ID) || (ref.Kind != "" && !hasKind(m, ref.Kind)) ||
		ref.Source == manualSource || !hasSource(m, ref.Source) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return
	}
	k, ok := s.recommendationsKind(r.Context(), m, ref)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return
	}
	v := recsView{Heading: "Recommendations", URL: recommendationsURL(m, ref.Source, ref.ID, k.Key),
		ItemURL: itemURL(m, ref.Source, ref.ID, k.Key)}
	for _, sec := range k.Sections {
		if sec.Type == plugin.SectionRecommendations && sec.Label != "" {
			v.Heading = sec.Label
			break
		}
	}
	key := liveKey(m.ID, ref.Source, k.Key, ref.ID)
	recs, hit := s.recs.get(key)
	if !hit {
		var err error
		recs, err = s.fetchRecommendations(r.Context(), m.ID, ref.Source, k.Key, ref.ID)
		switch {
		case err == nil:
			s.recs.put(key, recs)
		case errors.Is(err, plugin.ErrNotFound):
			recs = nil
			s.recs.put(key, nil)
		default:
			log.Printf("recommendations %s: %v", key, err)
			v.Failed = true
		}
	}
	if !v.Failed {
		v.Cards = recommendationCards(m, k, recs, s.titleLang(r.Context(), m))
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "recommendations", "recommendations_block", v)
		return
	}
	s.render(w, r, "recommendations", v)
}

// recommendationsKind resolves the kind of ref like the overview does: the
// named kind, the plugin's only kind, or the kind of the one saved item
// holding the id. ok is false when none applies or the kind declares no
// recommendations section.
func (s *server) recommendationsKind(ctx context.Context, m plugin.Manifest, ref itemRef) (plugin.KindDef, bool) {
	var k plugin.KindDef
	switch {
	case ref.Kind != "":
		k, _ = kindDef(m, ref.Kind)
	case len(m.Kinds) == 1:
		k = m.Kinds[0]
	default:
		it, sk, err := s.findSaved(ctx, m, ref)
		if err != nil {
			if !errors.Is(err, errAmbiguousKind) {
				log.Printf("recommendations %s/%s/%s: %v", m.ID, ref.Source, ref.ID, err)
			}
			return plugin.KindDef{}, false
		}
		if it == nil {
			return plugin.KindDef{}, false
		}
		k = sk
	}
	return k, hasSection(k, plugin.SectionRecommendations)
}

// fetchRecommendations asks the plugin's source with the s.recTimeout
// deadline. The call runs in its own goroutine, so even a source that
// ignores its context cannot hold the response past the deadline (its late
// result is dropped); a panicking source is an error.
func (s *server) fetchRecommendations(ctx context.Context, pluginID, sourceID, kind, id string) ([]plugin.ItemSummary, error) {
	src := s.source(pluginID, sourceID)
	if src == nil {
		return nil, errNoSource
	}
	ctx, cancel := context.WithTimeout(ctx, s.recTimeout)
	defer cancel()
	type result struct {
		recs []plugin.ItemSummary
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- result{err: fmt.Errorf("recommendations: source panic: %v", p)}
			}
		}()
		recs, err := src.Recommendations(ctx, kind, id)
		ch <- result{recs: recs, err: err}
	}()
	select {
	case res := <-ch:
		return res.recs, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// recommendationCards turns summaries into cards. A card is shown only when
// it can link into the app: it has an id and the plugin owns its kind
// (default: the item's kind) and source.
func recommendationCards(m plugin.Manifest, k plugin.KindDef, recs []plugin.ItemSummary, lang string) []linkCard {
	var out []linkCard
	for _, rc := range recs {
		kind := rc.Kind
		if kind == "" {
			kind = k.Key
		}
		if rc.ID == "" || !hasSource(m, rc.Source) || !hasKind(m, kind) {
			continue
		}
		out = append(out, linkCard{Title: orID(library.ResolveTitle(rc.Titles, lang, titleFallback(m, k, kind)), rc.ID),
			Href: itemURL(m, rc.Source, rc.ID, kind), Cover: httpURL(rc.CoverURL)})
	}
	return out
}
