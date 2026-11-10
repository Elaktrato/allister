package jobs

// The scheduled_import job runs a stored account import (for example a
// profile name on the source's site) with the stored import mode and re-enqueues itself for
// the next round while the schedule is enabled. It is a normal job row, so a
// pending next round survives a restart; saving the settings enqueues the
// first one.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/allister/allister/internal/settings"
)

// KindScheduledImport is the job kind of a recurring account import.
const KindScheduledImport = "scheduled_import"

// ScheduledImportPayload names the importer whose stored account is imported.
type ScheduledImportPayload struct {
	PluginID   string `json:"plugin_id"`
	ImporterID string `json:"importer_id"`
}

// ImportScheduler is the part of the import runner the job calls.
type ImportScheduler interface {
	SubmitAccount(ctx context.Context, pluginID, importerID, account, mode string) (int64, error)
}

// ScheduledQueue schedules the job's next round.
type ScheduledQueue interface {
	EnqueueAt(ctx context.Context, kind string, payload any, at time.Time) error
}

// ScheduledImportDeps are the dependencies of the ScheduledImport handler.
type ScheduledImportDeps struct {
	Imports  ImportScheduler
	Settings *settings.Store
	Queue    ScheduledQueue
	// Now replaces time.Now (tests); nil gets the real clock.
	Now  func() time.Time
	Logf func(format string, args ...any)
}

// scheduleInterval is the time between two rounds; 0 means off.
func scheduleInterval(schedule string) time.Duration {
	switch schedule {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	return 0
}

// ScheduledImport returns the handler of scheduled_import jobs. A failed
// round (another import is active, the account is unknown, the source is
// down) is logged and the next round is still scheduled; the chain only ends
// when the schedule is turned off or no account is stored.
func ScheduledImport(deps ScheduledImportDeps) Handler {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	logf := deps.Logf
	if logf == nil {
		logf = log.Printf
	}
	return func(ctx context.Context, raw json.RawMessage) error {
		var p ScheduledImportPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("scheduled import: bad payload: %w", err)
		}
		every := scheduleInterval(deps.Settings.ImportSchedule(ctx, p.PluginID, p.ImporterID))
		if every <= 0 {
			return nil // off: the chain ends here
		}
		account := deps.Settings.ImportAccount(ctx, p.PluginID, p.ImporterID)
		if account == "" {
			return nil // nothing stored: saving the settings starts the chain again
		}
		mode := deps.Settings.ImportMode(ctx, p.PluginID, p.ImporterID)
		if _, err := deps.Imports.SubmitAccount(ctx, p.PluginID, p.ImporterID, account, mode); err != nil {
			logf("scheduled import %s/%s: %v", p.PluginID, p.ImporterID, err)
		}
		return deps.Queue.EnqueueAt(ctx, KindScheduledImport, p, now().Add(every))
	}
}
