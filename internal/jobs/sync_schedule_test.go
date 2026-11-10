package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

func schedulePayload(id int64) json.RawMessage {
	b, _ := json.Marshal(SchedulePayload{ItemID: id, PluginID: "tv"})
	return b
}

func activeEntry(t *testing.T, lib *library.Library, id int64, st library.Status) {
	t.Helper()
	if err := lib.CreateEntry(context.Background(), library.Entry{ItemID: id, Status: st}); err != nil {
		t.Fatal(err)
	}
}

// TestScheduleStoresAiringsForActiveEntries: the job stores the source's air
// dates for an active entry, and only for one.
func TestScheduleStoresAiringsForActiveEntries(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", "")
	activeEntry(t, e.lib, id, library.Active)
	at := time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)
	e.src.Airings = []plugin.Airing{{Number: 3, AirsAt: at}}
	h := Schedule(ScheduleDeps{Library: e.lib, Registry: e.reg})
	if err := h(context.Background(), schedulePayload(id)); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.ScheduleEntries(context.Background())
	if err != nil || len(got) != 1 || got[0].ItemID != id || got[0].Number != 3 || !got[0].AirsAt.Equal(at) {
		t.Fatalf("%+v %v", got, err)
	}
	// a paused entry is never asked
	paused := e.saveID(t, "tvdata", "43", "")
	activeEntry(t, e.lib, paused, library.Paused)
	e.src.Airings = nil
	e.src.Errs = map[string]error{plugintest.OpSchedule: plugin.ErrNotFound}
	before := e.src.Calls(plugintest.OpSchedule)
	if err := h(context.Background(), schedulePayload(paused)); err != nil {
		t.Fatal(err)
	}
	if n := e.src.Calls(plugintest.OpSchedule); n != before {
		t.Fatalf("schedule calls for a paused entry = %d (before %d)", n, before)
	}
}

// TestScheduleErrors: short trouble asks for a retry, an unusable answer
// fails permanently, and a not-found answer records an empty schedule.
func TestScheduleErrors(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", "")
	activeEntry(t, e.lib, id, library.Active)
	h := Schedule(ScheduleDeps{Library: e.lib, Registry: e.reg})
	e.src.Errs = map[string]error{plugintest.OpSchedule: plugin.ErrRateLimited}
	if err := h(context.Background(), schedulePayload(id)); !errors.Is(err, ErrRetryLater) {
		t.Fatalf("rate limited: %v", err)
	}
	e.src.Errs = map[string]error{plugintest.OpSchedule: plugin.ErrBadResponse}
	if err := h(context.Background(), schedulePayload(id)); !errors.Is(err, ErrPermanent) {
		t.Fatalf("bad response: %v", err)
	}
	e.src.Errs = map[string]error{plugintest.OpSchedule: plugin.ErrNotFound}
	if err := h(context.Background(), schedulePayload(id)); err != nil {
		t.Fatalf("not found: %v", err)
	}
	if st, err := e.lib.StaleSchedules(context.Background(), time.Now().Add(-24*time.Hour)); err != nil || len(st) != 0 {
		t.Fatalf("not-found marker missing: %+v %v", st, err)
	}
}

// TestSyncItemRefreshesTheSchedule: the item sync stores the schedule of an
// active entry (so adding to the watching list fills the calendar at once)
// and skips other statuses.
func TestSyncItemRefreshesTheSchedule(t *testing.T) {
	e := newSyncEnv(t)
	id := e.save(t, "tvdata", e.srv.URL+"/ok/cover")
	activeEntry(t, e.lib, id, library.Active)
	e.src.Airings = []plugin.Airing{{Number: 1, AirsAt: time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)}}
	if err := e.run(id); err != nil {
		t.Fatal(err)
	}
	got, err := e.lib.ScheduleEntries(context.Background())
	if err != nil || len(got) != 1 || got[0].ItemID != id {
		t.Fatalf("%+v %v", got, err)
	}
	planned := e.saveID(t, "tvdata", "44", e.srv.URL+"/ok/cover")
	activeEntry(t, e.lib, planned, library.Planned)
	e.src.Airings = nil
	if err := e.run(planned); err != nil {
		t.Fatal(err)
	}
	if n := e.src.Calls(plugintest.OpSchedule); n != 1 {
		t.Fatalf("schedule calls = %d, want only the active entry's", n)
	}
}
