package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// KindRefreshItem is the job kind handled by RefreshItem: one item of a
// bulk "Update from source" run (library.StartRefreshRun).
const KindRefreshItem = library.RefreshJobKind

// RefreshPayload is the payload of a refresh_item job. PluginID names the
// item's plugin: the queue pauses the job while the plugin is disabled
// (WithPluginGate).
type RefreshPayload struct {
	RunID    int64  `json:"run_id"`
	ItemID   int64  `json:"item_id"`
	PluginID string `json:"plugin_id"`
}

// RefreshJobFor returns the kind and payload of the refresh_item job of
// the plugin's item itemID in run runID.
func RefreshJobFor(pluginID string, runID int64) func(itemID int64) (kind string, payload any) {
	return func(itemID int64) (string, any) {
		return KindRefreshItem, RefreshPayload{RunID: runID, ItemID: itemID, PluginID: pluginID}
	}
}

// RefreshItem returns the handler of refresh_item jobs. It asks the item's
// source (Details, and People when the item's kind has a people section),
// fills the item without changing a stored value (library.FillFromSource)
// and records the item in its run: "updated" when something was written,
// "unchanged" when nothing was new (or the item is gone), "failed" with its
// title when the source does not know it (ErrNotFound), answered with
// unusable data (ErrBadResponse, or values the library refuses) or the item
// has no source to ask. Rate limits and unavailability retry later
// (ErrRetryLater). ErrNotConfigured records the item failed and stops the
// run (reason library.RefreshStoppedCredential), which finishes it at once
// with the items not asked counted as failed; every later job of a
// finished or stopped run returns without asking the source or counting.
// A People answer
// other than a rate limit, unavailability or a missing credential fills
// without credits. When the fill wrote a cover, banner or trailer whose
// stored image is a remote URL, enqueueSync is asked to download it (the
// plugin's cover-only sync); its failure is logged.
func RefreshItem(deps SyncDeps, enqueueSync func(ctx context.Context, pluginID string, itemID int64) error) Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p RefreshPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("refresh item: bad payload: %w: %w", err, ErrPermanent)
		}
		lib := deps.Library
		run, err := lib.RefreshRunByID(ctx, p.RunID)
		if err != nil {
			return fmt.Errorf("refresh item %d: %w", p.ItemID, err)
		}
		if run == nil || run.FinishedAt != nil || run.Stopped != "" {
			// gone, or finished (stopped) before this job: the run counted
			// its item already, so neither ask the source nor count
			return nil
		}
		record := func(outcome, title string) error {
			if err := lib.RecordRefresh(ctx, run.ID, outcome, title); err != nil {
				return fmt.Errorf("refresh item %d: %w", p.ItemID, err)
			}
			return nil
		}
		it, err := lib.Item(ctx, p.ItemID)
		if err != nil {
			return fmt.Errorf("refresh item %d: %w", p.ItemID, err)
		}
		if it == nil {
			return record(library.RefreshUnchanged, "") // deleted meanwhile
		}
		var kind plugin.KindDef
		m, _ := deps.Registry.Manifest(it.PluginID)
		if i := slices.IndexFunc(m.Kinds, func(k plugin.KindDef) bool { return k.Key == it.Kind }); i >= 0 {
			kind = m.Kinds[i]
		}
		title := library.ResolveTitle(it.Titles, "", kind.TitleFallback)
		if title == "" && len(it.Titles) > 0 {
			title = it.Titles[0].Text
		}
		if it.PluginID != p.PluginID {
			return record(library.RefreshFailed, title)
		}
		var src plugin.Source
		if pl, ok := deps.Registry.Get(it.PluginID); ok && it.Source != manualSource {
			src = pl.Source(it.Source)
		}
		if src == nil {
			return record(library.RefreshFailed, title)
		}

		// sourceTrouble handles an error of the source: retry later, stop the
		// run, or record the item failed (handled true, with the job's error).
		sourceTrouble := func(op string, err error) (bool, error) {
			switch {
			case ctx.Err() != nil:
				return true, ctx.Err()
			case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
				return true, fmt.Errorf("refresh item %d: %s: %w: %w", it.ID, op, err, ErrRetryLater)
			case errors.Is(err, plugin.ErrNotConfigured):
				// counted first, so the stop keeps its title
				if rerr := record(library.RefreshFailed, title); rerr != nil {
					return true, rerr
				}
				if serr := lib.StopRefreshRun(ctx, run.ID, library.RefreshStoppedCredential); serr != nil {
					return true, fmt.Errorf("refresh item %d: %w", it.ID, serr)
				}
				return true, nil
			}
			return false, nil
		}
		d, err := src.Details(ctx, it.Kind, it.SourceID)
		if err != nil {
			if handled, herr := sourceTrouble("details", err); handled {
				return herr
			}
			if errors.Is(err, plugin.ErrNotFound) || errors.Is(err, plugin.ErrBadResponse) {
				return record(library.RefreshFailed, title)
			}
			return fmt.Errorf("refresh item %d: details: %w", it.ID, err)
		}
		var credits []plugin.Credit
		withCredits := false
		if slices.ContainsFunc(kind.Sections, func(s plugin.SectionDef) bool { return s.Type == plugin.SectionPeople }) {
			credits, err = src.People(ctx, it.Kind, it.SourceID)
			if err != nil {
				if handled, herr := sourceTrouble("people", err); handled {
					return herr
				}
				// unknown or unusable credits: fill the rest without them
				log.Printf("refresh item %d: people: %v", it.ID, err)
				credits = nil
			} else {
				withCredits = true
			}
		}
		applied, err := lib.FillFromSource(ctx, it.ID, d, credits, withCredits)
		switch {
		case errors.Is(err, library.ErrNoEntry):
			return record(library.RefreshUnchanged, "")
		case errors.Is(err, library.ErrInvalid):
			log.Printf("refresh item %d: %v", it.ID, err)
			return record(library.RefreshFailed, title)
		case err != nil:
			return fmt.Errorf("refresh item %d: %w", it.ID, err)
		}
		if len(applied) == 0 {
			return record(library.RefreshUnchanged, "")
		}
		if err := record(library.RefreshUpdated, ""); err != nil {
			return err
		}
		if enqueueSync == nil || !tookRemoteImage(ctx, lib, it.ID, applied) {
			return nil
		}
		// counted already: a failed enqueue must not count the item again
		if err := enqueueSync(ctx, it.PluginID, it.ID); err != nil {
			log.Printf("refresh item %d: image sync: %v", it.ID, err)
		}
		return nil
	}
}

// tookRemoteImage reports whether applied wrote the cover, the banner or
// the trailer and the item now stores that image as a remote URL.
func tookRemoteImage(ctx context.Context, lib *library.Library, itemID int64, applied []string) bool {
	it, err := lib.Item(ctx, itemID)
	if err != nil || it == nil {
		return false
	}
	for _, k := range applied {
		switch {
		case k == "cover" && isRemote(it.CoverPath),
			k == "banner" && isRemote(it.BannerPath),
			k == "trailer" && it.Trailer != nil && isRemote(it.Trailer.ThumbnailURL):
			return true
		}
	}
	return false
}
