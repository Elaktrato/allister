package jobs

// The sync_schedule job refreshes one item's stored airing schedule for the
// calendar feed. The schedule is fetched only for entries that are still
// active and whose source declares the optional schedule capability; the
// queue's plugin gate holds the job while the plugin is disabled.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// KindSchedule is the job kind that refreshes an item's airing schedule.
const KindSchedule = "sync_schedule"

// SchedulePayload is the payload of a sync_schedule job.
type SchedulePayload struct {
	ItemID   int64  `json:"item_id"`
	PluginID string `json:"plugin_id"`
}

// ScheduleJobFor returns the schedule job of a plugin, like SyncJobFor.
func ScheduleJobFor(pluginID string) func(itemID int64) (kind string, payload any) {
	return func(itemID int64) (string, any) {
		return KindSchedule, SchedulePayload{ItemID: itemID, PluginID: pluginID}
	}
}

// ScheduleDeps are the dependencies of the Schedule handler.
type ScheduleDeps struct {
	Library  *library.Library
	Registry *plugin.Registry
}

// Schedule returns the handler of sync_schedule jobs. It stores the
// source's air dates for active entries; a source that answers "not found"
// stores an empty schedule (the marker keeps it from being asked again), rate
// limits and outages ask for a later retry, an unusable answer fails the job
// permanently; a source without a usable credential keeps the stored schedule
// and finishes done.
func Schedule(deps ScheduleDeps) Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p SchedulePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("schedule: bad payload: %w", err)
		}
		it, err := deps.Library.Item(ctx, p.ItemID)
		if err != nil {
			return fmt.Errorf("schedule item %d: %w", p.ItemID, err)
		}
		if it == nil {
			return nil // deleted meanwhile
		}
		if it.PluginID != p.PluginID {
			return fmt.Errorf("schedule item %d: job names plugin %q but the item belongs to plugin %q: %w",
				it.ID, p.PluginID, it.PluginID, ErrPermanent)
		}
		if err := refreshSchedule(ctx, deps.Library, deps.Registry, it); err != nil {
			return fmt.Errorf("schedule item %d: %w", it.ID, err)
		}
		return nil
	}
}

// refreshSchedule stores the item's airing schedule; it is the shared path of
// the sync_schedule job and the item sync. Inactive entries keep whatever is
// stored (the feed ignores it), sources without the capability are skipped. A
// source that still needs its credential keeps the stored schedule and counts
// as done (asked again only after a key is saved).
func refreshSchedule(ctx context.Context, lib *library.Library, reg *plugin.Registry, it *library.Item) error {
	en, err := lib.Entry(ctx, it.ID)
	if err != nil {
		return err
	}
	if en == nil || en.Status != library.Active {
		return nil
	}
	src, supported := scheduleSource(reg, it)
	if !supported {
		return nil
	}
	if src == nil {
		return nil // the plugin is gone; nothing to ask
	}
	airings, err := src.Schedule(ctx, it.Kind, it.SourceID)
	switch {
	case errors.Is(err, plugin.ErrNotFound):
		airings = nil // the source does not know the item: record "asked"
	case errors.Is(err, plugin.ErrNotConfigured):
		// The source has no usable credential: it cannot answer until the
		// user configures it, so retrying is pointless. Keep the stored
		// schedule and finish; the key itself is never logged.
		return nil
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
		return fmt.Errorf("fetch schedule: %w: %w", err, ErrRetryLater)
	case errors.Is(err, plugin.ErrBadResponse):
		return fmt.Errorf("fetch schedule: %w: %w", err, ErrPermanent)
	case err != nil:
		return fmt.Errorf("fetch schedule: %w", err)
	}
	converted := make([]library.Airing, 0, len(airings))
	for _, a := range airings {
		converted = append(converted, library.Airing{Season: a.Season, Number: a.Number, AirsAt: a.AirsAt})
	}
	return lib.SaveSchedule(ctx, it.ID, converted, time.Now())
}

// scheduleSource returns the item's source when it declares the schedule
// capability (and is implemented); the second result reports whether the
// manifest allows asking, so a source that changed its mind is skipped rather
// than type-asserted blindly.
func scheduleSource(reg *plugin.Registry, it *library.Item) (plugin.ScheduleSource, bool) {
	m, ok := reg.Manifest(it.PluginID)
	if !ok {
		return nil, false
	}
	for _, s := range m.Sources {
		if s.ID != it.Source || !s.SupportsSchedule {
			continue
		}
		pl, ok := reg.Get(it.PluginID)
		if !ok {
			return nil, true
		}
		src, ok := pl.Source(it.Source).(plugin.ScheduleSource)
		if !ok {
			return nil, true
		}
		return src, true
	}
	return nil, false
}
