package jobs

import (
	"context"
	"fmt"

	"github.com/allister/allister/internal/store"
)

// SweepSync enqueues one sync_anime job for every anime that still needs a
// sync and has no pending or running one (a failed job does not count, which
// is how anime whose job gave up get another chance). Call it at startup after
// Recover. It reports how many jobs it enqueued and is idempotent.
func (q *Queue) SweepSync(ctx context.Context, st *store.Store) (int, error) {
	ids, err := st.NeedsSync(ctx, KindSyncAnime)
	if err != nil {
		return 0, fmt.Errorf("sweep sync: %w", err)
	}
	for i, id := range ids {
		if err := q.Enqueue(ctx, KindSyncAnime, SyncPayload{AnimeID: id}); err != nil {
			return i, fmt.Errorf("sweep sync: %w", err)
		}
	}
	return len(ids), nil
}
