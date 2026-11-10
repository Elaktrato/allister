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
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// KindSyncItem is the job kind handled by SyncItem.
const KindSyncItem = "sync_item"

// manualSource is the source of items typed in by the user: there is
// nothing to fetch for them.
const manualSource = "manual"

// SyncPayload is the payload of a sync_item job.
type SyncPayload struct {
	ItemID int64 `json:"item_id"`
	// PluginID names the item's plugin. It must match the stored item, and
	// the queue pauses the job while the plugin is disabled (WithPluginGate).
	PluginID string `json:"plugin_id"`
}

// SyncJobFor returns the web.Deps.SyncJobs entry of a plugin: the kind and
// payload of the sync job of its newly saved item itemID.
func SyncJobFor(pluginID string) func(itemID int64) (kind string, payload any) {
	return func(itemID int64) (string, any) {
		return KindSyncItem, SyncPayload{ItemID: itemID, PluginID: pluginID}
	}
}

// SyncDeps are the dependencies of the SyncItem handler.
type SyncDeps struct {
	Library  *library.Library
	Registry *plugin.Registry
	// HTTP downloads the images; nil gets covers.Download's default client.
	HTTP *http.Client
	// CoversDir is DATA_DIR/covers.
	CoversDir string
}

func isRemote(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

// SyncItem returns the handler of sync_item jobs. It downloads a remote
// cover, fetches the credits from the item's plugin source (Source.People),
// stores them and downloads the people's images. It ends with sync_state
// done, or partial plus a wrapped ErrRetryLater when only images failed, or
// partial plus the source's error when the credits could not be fetched
// (wrapped with ErrRetryLater for rate limits and unavailability,
// ErrPermanent for a bad answer, a plain error otherwise). A deleted item is
// a success; a payload whose plugin id does not match the item fails at once
// (ErrPermanent). A manual item, or an item whose plugin does not provide
// its source, has nothing to fetch: only its remote cover is downloaded.
func SyncItem(deps SyncDeps) Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p SyncPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("sync item: bad payload: %w", err)
		}
		lib := deps.Library
		it, err := lib.Item(ctx, p.ItemID)
		if err != nil {
			return fmt.Errorf("sync item %d: %w", p.ItemID, err)
		}
		if it == nil {
			return nil // deleted meanwhile
		}
		if it.PluginID != p.PluginID {
			return fmt.Errorf("sync item %d: job names plugin %q but the item belongs to plugin %q: %w",
				it.ID, p.PluginID, it.PluginID, ErrPermanent)
		}
		pl, ok := deps.Registry.Get(it.PluginID)
		if !ok {
			return fmt.Errorf("sync item %d: plugin %q is not installed", it.ID, it.PluginID)
		}
		var src plugin.Source
		if it.Source != manualSource {
			src = pl.Source(it.Source)
		}
		if src == nil {
			return syncCoverOnly(ctx, deps, it)
		}

		var failures []string
		if isRemote(it.CoverPath) {
			if name, err := covers.Download(ctx, deps.HTTP, deps.CoversDir, it.CoverPath); err != nil {
				failures = append(failures, fmt.Sprintf("cover: %v", err))
			} else if err := setCover(ctx, deps, it.ID, it.CoverPath, name); err != nil {
				return fmt.Errorf("sync item %d: %w", it.ID, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		credits, err := src.People(ctx, it.Kind, it.SourceID)
		switch {
		case errors.Is(err, plugin.ErrNotFound):
			credits = nil
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if serr := setState(ctx, lib, it.ID, "partial"); serr != nil {
				log.Printf("sync item %d: %v", it.ID, serr)
			}
			if errors.Is(err, plugin.ErrRateLimited) || errors.Is(err, plugin.ErrUnavailable) {
				// Short source trouble: retry on the bounded partial path
				// instead of burning attempts (the cause stays in the chain).
				return fmt.Errorf("sync item %d: people: %w: %w", it.ID, err, ErrRetryLater)
			}
			if errors.Is(err, plugin.ErrBadResponse) {
				// The source answered with unusable data: it will answer the
				// same way again, so fail at once instead of retrying.
				return fmt.Errorf("sync item %d: people: %w: %w", it.ID, err, ErrPermanent)
			}
			return fmt.Errorf("sync item %d: people: %w", it.ID, err)
		}
		if err := lib.SaveCredits(ctx, it.ID, credits); err != nil {
			return fmt.Errorf("sync item %d: %w", it.ID, err)
		}
		people, err := lib.ItemPeople(ctx, it.ID)
		if err != nil {
			return fmt.Errorf("sync item %d: %w", it.ID, err)
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
			ok, err := lib.SetPersonImagePathIf(ctx, person.ID, person.ImagePath, name)
			if err != nil {
				return fmt.Errorf("sync item %d: %w", it.ID, err)
			}
			if !ok {
				discardDownload(ctx, deps, name)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// the calendar feed's schedule: best effort, the feed reports stale
		// ones and the sync_schedule job retries them
		if err := refreshSchedule(ctx, deps.Library, deps.Registry, it); err != nil {
			log.Printf("sync item %d: schedule: %v", it.ID, err)
		}

		if len(failures) == 0 {
			return setState(ctx, lib, it.ID, "done")
		}
		if err := setState(ctx, lib, it.ID, "partial"); err != nil {
			return err
		}
		return fmt.Errorf("sync item %d: %d image(s) failed (first: %s): %w", it.ID, len(failures), failures[0], ErrRetryLater)
	}
}

// syncCoverOnly finishes an item without a source to ask (a manual item or a
// plugin without the item's source): there are no credits to fetch, but a
// remote cover URL (an imported entry) is downloaded. A failed download
// leaves the URL in place, marks the item partial and asks for a later retry.
func syncCoverOnly(ctx context.Context, deps SyncDeps, it *library.Item) error {
	if isRemote(it.CoverPath) {
		name, err := covers.Download(ctx, deps.HTTP, deps.CoversDir, it.CoverPath)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if serr := setState(ctx, deps.Library, it.ID, "partial"); serr != nil {
				return serr
			}
			return fmt.Errorf("sync item %d: cover: %v: %w", it.ID, err, ErrRetryLater)
		}
		if err := setCover(ctx, deps, it.ID, it.CoverPath, name); err != nil {
			return fmt.Errorf("sync item %d: %w", it.ID, err)
		}
	}
	return setState(ctx, deps.Library, it.ID, "done")
}

func setState(ctx context.Context, lib *library.Library, id int64, state string) error {
	if err := lib.SetSyncState(ctx, id, state); err != nil {
		return fmt.Errorf("sync item %d: set state %s: %w", id, state, err)
	}
	return nil
}

// setCover stores a downloaded cover only while the item still holds the
// remote URL the job started from (compare-and-set): the user may have
// replaced or removed the cover during the download. Then the downloaded
// file is discarded (when nothing else references it) and the job carries
// on without error.
func setCover(ctx context.Context, deps SyncDeps, id int64, expected, name string) error {
	ok, err := deps.Library.SetCoverPathIf(ctx, id, expected, name)
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
	used, err := deps.Library.ImageReferenced(ctx, name)
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
