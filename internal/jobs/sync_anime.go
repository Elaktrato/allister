package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

// KindSyncAnime is the job kind handled by SyncAnime.
const KindSyncAnime = "sync_anime"

// SyncPayload is the payload of a sync_anime job.
type SyncPayload struct {
	AnimeID int64 `json:"anime_id"`
	// PluginID names the owning plugin so the queue can pause the job while
	// the plugin is disabled (see WithPluginGate). Empty means never gated.
	PluginID string `json:"plugin_id,omitempty"`
}

// AnimeSyncJob returns the kind and payload of the sync job of the saved
// anime item itemID. It is the anime plugin's entry of web.Deps.SyncJobs, so
// the web layer enqueues the job without knowing its kind (Task 11 renames
// the kind).
func AnimeSyncJob(itemID int64) (kind string, payload any) {
	return KindSyncAnime, SyncPayload{AnimeID: itemID, PluginID: "anime"}
}

// SyncDeps are the dependencies of the SyncAnime handler.
type SyncDeps struct {
	Store     *store.Store
	Providers map[provider.Source]provider.Provider
	HTTP      *http.Client
	CoversDir string
}

func isRemote(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

// SyncAnime downloads the cover, fetches and stores the cast and downloads
// person images. It ends with sync_state done, or partial plus a wrapped
// ErrRetryLater when only images failed, or partial plus the provider's error
// when the cast could not be fetched (wrapped with ErrRetryLater for rate
// limits and unavailability, a plain error otherwise).
func SyncAnime(deps SyncDeps) Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p SyncPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("sync anime: bad payload: %w", err)
		}
		st := deps.Store
		saved, err := st.GetByID(ctx, p.AnimeID)
		if err != nil {
			return fmt.Errorf("sync anime %d: %w", p.AnimeID, err)
		}
		if saved == nil {
			return nil // deleted meanwhile
		}
		if saved.Source == provider.Manual {
			return syncManual(ctx, deps, saved)
		}
		prov, ok := deps.Providers[saved.Source]
		if !ok {
			return fmt.Errorf("sync anime %d: no provider for source %q", saved.ID, saved.Source)
		}

		var failures []string
		if isRemote(saved.CoverPath) {
			if name, err := covers.Download(ctx, deps.HTTP, deps.CoversDir, saved.CoverPath); err != nil {
				failures = append(failures, fmt.Sprintf("cover: %v", err))
			} else if err := setCover(ctx, deps, saved.ID, saved.CoverPath, name); err != nil {
				return fmt.Errorf("sync anime %d: %w", saved.ID, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		cast, err := prov.Cast(ctx, saved.Anime.ID)
		switch {
		case errors.Is(err, provider.ErrNotFound):
			cast = nil
		case err != nil:
			if serr := setState(ctx, st, saved.ID, "partial"); serr != nil {
				log.Printf("sync anime %d: %v", saved.ID, serr)
			}
			if errors.Is(err, provider.ErrRateLimited) || errors.Is(err, provider.ErrUnavailable) {
				// Short provider trouble: retry on the bounded partial path
				// instead of burning attempts (the cause stays in the chain).
				return fmt.Errorf("sync anime %d: cast: %w: %w", saved.ID, err, ErrRetryLater)
			}
			return fmt.Errorf("sync anime %d: cast: %w", saved.ID, err)
		}
		if err := st.SaveCast(ctx, saved.ID, cast); err != nil {
			return fmt.Errorf("sync anime %d: %w", saved.ID, err)
		}
		people, err := st.AnimePeople(ctx, saved.ID)
		if err != nil {
			return fmt.Errorf("sync anime %d: %w", saved.ID, err)
		}
		for _, person := range people {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !isRemote(person.ImagePath) {
				continue
			}
			name, err := covers.Download(ctx, deps.HTTP, deps.CoversDir, person.ImagePath)
			if err != nil {
				failures = append(failures, fmt.Sprintf("person %d: %v", person.ID, err))
				continue
			}
			ok, err := st.SetPersonImagePathIf(ctx, person.ID, person.ImagePath, name)
			if err != nil {
				return fmt.Errorf("sync anime %d: %w", saved.ID, err)
			}
			if !ok {
				discardDownload(ctx, deps, name)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if len(failures) == 0 {
			return setState(ctx, st, saved.ID, "done")
		}
		if err := setState(ctx, st, saved.ID, "partial"); err != nil {
			return err
		}
		first := failures[0]
		return fmt.Errorf("sync anime %d: %d image(s) failed (first: %s): %w", saved.ID, len(failures), first, ErrRetryLater)
	}
}

// syncManual finishes a manual entry: there is no cast to fetch, but a remote
// cover URL (an imported entry) is downloaded. A failed download leaves the
// URL in place, marks the entry partial and asks for a later retry.
func syncManual(ctx context.Context, deps SyncDeps, saved *store.Saved) error {
	if isRemote(saved.CoverPath) {
		name, err := covers.Download(ctx, deps.HTTP, deps.CoversDir, saved.CoverPath)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if serr := setState(ctx, deps.Store, saved.ID, "partial"); serr != nil {
				return serr
			}
			return fmt.Errorf("sync anime %d: cover: %v: %w", saved.ID, err, ErrRetryLater)
		}
		if err := setCover(ctx, deps, saved.ID, saved.CoverPath, name); err != nil {
			return fmt.Errorf("sync anime %d: %w", saved.ID, err)
		}
	}
	return setState(ctx, deps.Store, saved.ID, "done")
}

func setState(ctx context.Context, st *store.Store, id int64, state string) error {
	if err := st.SetSyncState(ctx, id, state); err != nil {
		return fmt.Errorf("sync anime %d: set state %s: %w", id, state, err)
	}
	return nil
}

// setCover stores a downloaded cover only while the item still holds the
// remote URL the job started from (compare-and-set): the user may have
// replaced or removed the cover during the download. Then the downloaded
// file is discarded (when nothing else references it) and the job carries
// on without error.
func setCover(ctx context.Context, deps SyncDeps, id int64, expected, name string) error {
	ok, err := deps.Store.SetAnimeCoverPathIf(ctx, id, expected, name)
	if err != nil {
		return err
	}
	if !ok {
		discardDownload(ctx, deps, name)
	}
	return nil
}

// discardDownload removes a downloaded file that lost its compare-and-set,
// unless another row references the same content-hash name. Failures only
// leave a harmless orphan and are logged.
func discardDownload(ctx context.Context, deps SyncDeps, name string) {
	used, err := deps.Store.CoverReferenced(ctx, name)
	if err != nil {
		log.Printf("sync: discard %s: %v", name, err)
		return
	}
	if used {
		return
	}
	if err := os.Remove(filepath.Join(deps.CoversDir, name)); err != nil && !os.IsNotExist(err) {
		log.Printf("sync: discard %s: %v", name, err)
	}
}
