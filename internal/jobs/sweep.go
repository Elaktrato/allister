package jobs

import (
	"context"
	"fmt"

	"github.com/allister/allister/internal/library"
)

// SweepSync enqueues one sync_item job for every item (of any plugin) that
// still needs a sync and has no pending or running one (a failed job does not
// count, which is how items whose job gave up get another chance). Each job
// carries the item's plugin id. Call it at startup after Recover. It reports
// how many jobs it enqueued and is idempotent.
func (q *Queue) SweepSync(ctx context.Context, lib *library.Library) (int, error) {
	items, err := lib.NeedsSync(ctx, KindSyncItem)
	if err != nil {
		return 0, fmt.Errorf("sweep sync: %w", err)
	}
	for i, it := range items {
		if err := q.Enqueue(ctx, KindSyncItem, SyncPayload{ItemID: it.ItemID, PluginID: it.PluginID}); err != nil {
			return i, fmt.Errorf("sweep sync: %w", err)
		}
	}
	return len(items), nil
}
