package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

func TestSyncCastTroubleTakesPartialPath(t *testing.T) {
	for _, perr := range []error{provider.ErrRateLimited, provider.ErrUnavailable} {
		e := newSyncEnv(t)
		clk := newClock()
		q := testQueue(t, e.d, clk)
		q.Register(KindSyncAnime, SyncAnime(e.deps))
		id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
		e.prov.err = perr
		jid := enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: id})
		clk.Advance(time.Hour)
		step(t, q)
		r := row(t, e.d, jid)
		if r.State != "pending" || r.Attempts != 0 || r.Partials != 1 {
			t.Fatalf("%v: %+v", perr, r)
		}
		if s := e.saved(t, id); s.SyncState != "partial" {
			t.Fatalf("state %q", s.SyncState)
		}
		// the cause stays in the chain and the log column
		err := e.run(id)
		if !errors.Is(err, perr) || !errors.Is(err, ErrRetryLater) {
			t.Fatalf("err = %v", err)
		}
		if r.LastError == "" {
			t.Fatal("cause not recorded")
		}
	}
}

func TestSyncOtherCastErrorStillConsumesAttempts(t *testing.T) {
	e := newSyncEnv(t)
	clk := newClock()
	q := testQueue(t, e.d, clk)
	q.Register(KindSyncAnime, SyncAnime(e.deps))
	id := e.save(t, provider.AniList, e.srv.URL+"/ok/cover")
	e.prov.err = errors.New("boom")
	jid := enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: id})
	clk.Advance(time.Hour)
	step(t, q)
	if r := row(t, e.d, jid); r.State != "pending" || r.Attempts != 1 || r.Partials != 0 {
		t.Fatalf("%+v", r)
	}
}

func liveSyncJobs(t *testing.T, e *syncEnv, animeID int64) int {
	t.Helper()
	var n int
	err := e.d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind=? AND json_extract(payload_json,'$.anime_id')=?`,
		KindSyncAnime, animeID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSweepSyncEnqueuesStuckAnimeOnly(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	q := testQueue(t, e.d, newClock())
	mk := func(extID, state string) int64 {
		a := provider.Anime{SearchResult: provider.SearchResult{Source: provider.AniList, ID: extID,
			Titles: []provider.Title{{Lang: "en", Text: "T" + extID}}}}
		id, err := e.st.SaveAnime(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.SetSyncState(ctx, id, state); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pendingNoJob := mk("1", "pending")
	partialNoJob := mk("2", "partial")
	done := mk("3", "done")
	pendingLive := mk("4", "pending")
	partialFailed := mk("5", "partial")
	partialRunning := mk("6", "partial")
	enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: pendingLive})
	fj := enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: partialFailed})
	e.d.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, fj)
	rj := enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: partialRunning})
	e.d.Exec(`UPDATE jobs SET state='running' WHERE id=?`, rj)
	// a live job of another kind does not count
	enqueue(t, q, "other", SyncPayload{AnimeID: pendingNoJob})

	n, err := q.SweepSync(ctx, e.st)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for id, want := range map[int64]int{pendingNoJob: 1, partialNoJob: 1, done: 0, pendingLive: 1, partialFailed: 2, partialRunning: 1} {
		if got := liveSyncJobs(t, e, id); got != want {
			t.Errorf("anime %d: %d jobs, want %d", id, got, want)
		}
	}
	// idempotent
	if n, err := q.SweepSync(ctx, e.st); err != nil || n != 0 {
		t.Fatalf("second sweep n=%d err=%v", n, err)
	}
}

func TestRecoverResetsRunningBeforeSweep(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	q := testQueue(t, e.d, newClock())
	id := e.save(t, provider.AniList, "")
	jid := enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: id})
	e.d.Exec(`UPDATE jobs SET state='running' WHERE id=?`, jid)
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if r := row(t, e.d, jid); r.State != "pending" {
		t.Fatalf("%+v", r)
	}
	if n, err := q.SweepSync(ctx, e.st); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if liveSyncJobs(t, e, id) != 1 {
		t.Fatal("duplicate job")
	}
}

func TestSweepCarriesPluginIDAndSkipsGatedLiveJobs(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	g := &gate{}
	g.set("anime", true) // disabled: its pending jobs stay live, so the sweep must not duplicate them
	q := testQueue(t, e.d, newClock(), WithPluginGate(g.allow))
	mk := func(extID string) int64 {
		id, err := e.st.SaveAnime(ctx, provider.Anime{SearchResult: provider.SearchResult{Source: provider.AniList, ID: extID,
			Titles: []provider.Title{{Lang: "en", Text: "T" + extID}}}}, store.WithSyncState("pending"))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := mk("1"), mk("2")
	enqueue(t, q, KindSyncAnime, SyncPayload{AnimeID: a, PluginID: "anime"})
	n, err := q.SweepSync(ctx, e.st)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if liveSyncJobs(t, e, a) != 1 || liveSyncJobs(t, e, b) != 1 {
		t.Fatal("sweep duplicated or missed jobs")
	}
	var pid string
	if err := e.d.QueryRow(`SELECT json_extract(payload_json,'$.plugin_id') FROM jobs
		WHERE json_extract(payload_json,'$.anime_id') = ?`, b).Scan(&pid); err != nil || pid != "anime" {
		t.Fatalf("plugin_id %q err=%v", pid, err)
	}
}
