package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/settings"
)

type fakeImports struct {
	calls []struct{ plugin, importer, account, mode string }
	err   error
}

func (f *fakeImports) SubmitAccount(ctx context.Context, pluginID, importerID, account, mode string) (int64, error) {
	f.calls = append(f.calls, struct{ plugin, importer, account, mode string }{pluginID, importerID, account, mode})
	return 1, f.err
}

type fakeScheduleQueue struct {
	next []struct {
		kind string
		at   time.Time
	}
}

func (f *fakeScheduleQueue) EnqueueAt(ctx context.Context, kind string, payload any, at time.Time) error {
	f.next = append(f.next, struct {
		kind string
		at   time.Time
	}{kind, at})
	return nil
}

// TestScheduledImportRunsAndReschedules: the job imports the stored account
// with the stored mode and schedules the next round; off or empty accounts
// end the chain.
func TestScheduledImportRunsAndReschedules(t *testing.T) {
	d := openDB(t)
	st := settings.New(d)
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	imports, q := &fakeImports{}, &fakeScheduleQueue{}
	h := ScheduledImport(ScheduledImportDeps{Imports: imports, Settings: st, Queue: q, Now: func() time.Time { return now },
		Logf: func(string, ...any) {}})
	payload, _ := json.Marshal(ScheduledImportPayload{PluginID: "anime", ImporterID: "anilist-user"})

	// nothing stored yet: no run, no next round
	if err := h(ctx, payload); err != nil || len(imports.calls) != 0 || len(q.next) != 0 {
		t.Fatalf("%v %d %d", err, len(imports.calls), len(q.next))
	}
	if err := st.SetImportAccount(ctx, "anime", "anilist-user", "someone"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetImportMode(ctx, "anime", "anilist-user", "newer"); err != nil {
		t.Fatal(err)
	}
	// an account without a schedule does not run either
	if err := h(ctx, payload); err != nil || len(imports.calls) != 0 {
		t.Fatalf("%v %d", err, len(imports.calls))
	}
	if err := st.SetImportSchedule(ctx, "anime", "anilist-user", "daily"); err != nil {
		t.Fatal(err)
	}
	if err := h(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if len(imports.calls) != 1 || imports.calls[0].account != "someone" || imports.calls[0].mode != "newer" ||
		imports.calls[0].plugin != "anime" || imports.calls[0].importer != "anilist-user" {
		t.Fatalf("%+v", imports.calls)
	}
	if len(q.next) != 1 || q.next[0].kind != KindScheduledImport || !q.next[0].at.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("%+v", q.next)
	}
	// weekly
	if err := st.SetImportSchedule(ctx, "anime", "anilist-user", "weekly"); err != nil {
		t.Fatal(err)
	}
	_ = h(ctx, payload)
	if len(q.next) != 2 || !q.next[1].at.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("%+v", q.next)
	}
	// a failing round still schedules the next one
	imports.err = errors.New("another import is still active")
	if err := h(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if len(q.next) != 3 {
		t.Fatalf("no next round after a failed one: %+v", q.next)
	}
	// off ends the chain
	if err := st.SetImportSchedule(ctx, "anime", "anilist-user", "off"); err != nil {
		t.Fatal(err)
	}
	before := len(imports.calls)
	if err := h(ctx, payload); err != nil || len(imports.calls) != before || len(q.next) != 3 {
		t.Fatalf("%v %d %d", err, len(imports.calls), len(q.next))
	}
}
