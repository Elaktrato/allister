package web

import (
	"context"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
	"github.com/allister/allister/internal/plugins/anime"
)

// Seeding helpers: the web tests save anime items and list entries through
// the library, mapping the fake AniList data with the anime plugin's own
// mapping (what its source returns is what the sync saves).

// The anime plugin's id and its one kind.
const (
	animePluginID = "anime"
	animeKind     = "anime"
)

// animeRegistry is the production wiring of cmd/allister for the tests: the
// anime plugin on top of es, with p as its AniList client (nil: a client that
// is always unavailable).
func animeRegistry(t testing.TB, es plugin.EnabledStore, p anilist.Provider) *plugin.Registry {
	t.Helper()
	if p == nil {
		p = unavailableProvider{}
	}
	reg := plugin.NewRegistry(es)
	if err := reg.Register(anime.New(anime.Options{Provider: p})); err != nil {
		t.Fatal(err)
	}
	return reg
}

// unavailableProvider answers every call with anilist.ErrUnavailable.
type unavailableProvider struct{}

func (unavailableProvider) Name() anilist.Source { return anilist.AniList }
func (unavailableProvider) Search(context.Context, string, string) ([]anilist.SearchResult, error) {
	return nil, anilist.ErrUnavailable
}
func (unavailableProvider) Media(context.Context, string, string) (anilist.Media, error) {
	return anilist.Media{}, anilist.ErrUnavailable
}
func (unavailableProvider) Cast(context.Context, string) ([]anilist.CastMember, error) {
	return nil, anilist.ErrUnavailable
}

// listEntry is the list entry of an anime item as the tests read and write
// it: Progress is the "episode" progress.
type listEntry struct {
	ItemID                int64
	Status                library.Status
	Rating                *int
	Progress              int
	StartedOn, FinishedOn *time.Time
	Comment               string
}

func (e listEntry) entry() library.Entry {
	return library.Entry{ItemID: e.ItemID, Status: e.Status, Rating: e.Rating, Comment: e.Comment,
		StartedOn: e.StartedOn, FinishedOn: e.FinishedOn, Progress: map[string]float64{"episode": float64(e.Progress)}}
}

// saveAnime saves a as an item of the anime plugin (sync state pending
// unless opts say otherwise).
func saveAnime(ctx context.Context, lib *library.Library, a anilist.Media, opts ...library.SaveOption) (int64, error) {
	return lib.SaveItem(ctx, animePluginID, animeKind, anime.ItemData(a), opts...)
}

// addToList saves a with the list entry e (e.ItemID is ignored).
func addToList(ctx context.Context, lib *library.Library, a anilist.Media, e listEntry, opts ...library.SaveOption) (int64, error) {
	return lib.AddToList(ctx, animePluginID, animeKind, anime.ItemData(a), e.entry(), opts...)
}

// upsertEntry writes the list entry e.
func upsertEntry(ctx context.Context, lib *library.Library, e listEntry) error {
	return lib.UpsertEntry(ctx, e.entry())
}

// getEntry reads the list entry of itemID (nil when there is none).
func getEntry(ctx context.Context, lib *library.Library, itemID int64) (*listEntry, error) {
	en, err := lib.Entry(ctx, itemID)
	if err != nil || en == nil {
		return nil, err
	}
	return &listEntry{ItemID: en.ItemID, Status: en.Status, Rating: en.Rating, Progress: int(en.Progress["episode"]),
		StartedOn: en.StartedOn, FinishedOn: en.FinishedOn, Comment: en.Comment}, nil
}

// findBySource returns the anime item saved from (source, id), or nil.
func findBySource(ctx context.Context, lib *library.Library, source anilist.Source, id string) (*library.Item, error) {
	return lib.ItemBySource(ctx, animePluginID, animeKind, string(source), id)
}

// findByMAL returns the anime item with the MyAnimeList id mal, or nil.
func findByMAL(ctx context.Context, lib *library.Library, mal string) (*library.Item, error) {
	return lib.ItemByExternalID(ctx, animePluginID, animeKind, "mal", mal)
}

// saveCast saves the cast of itemID as its credits.
func saveCast(ctx context.Context, lib *library.Library, itemID int64, cast []anilist.CastMember) error {
	return lib.SaveCredits(ctx, itemID, anime.Credits(cast))
}
