package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gate struct {
	mu       sync.Mutex
	disabled map[string]bool
}

func (g *gate) set(id string, off bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disabled == nil {
		g.disabled = map[string]bool{}
	}
	g.disabled[id] = off
}

func (g *gate) allow(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.disabled[id]
}

type pl struct {
	PluginID string `json:"plugin_id,omitempty"`
	N        int    `json:"n"`
}

func TestDisabledPluginJobsPauseAndResume(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	g := &gate{}
	q := testQueue(t, d, clk, WithPluginGate(g.allow))
	var ran []string
	q.Register("k", func(ctx context.Context, p json.RawMessage) error {
		var v pl
		json.Unmarshal(p, &v)
		ran = append(ran, fmt.Sprintf("%s/%d", v.PluginID, v.N))
		return nil
	})
	a := enqueue(t, q, "k", pl{PluginID: "anime", N: 1})
	free := enqueue(t, q, "k", pl{N: 2}) // no plugin_id
	b := enqueue(t, q, "k", pl{PluginID: "book", N: 3})
	before := row(t, d, a)

	g.set("anime", true)
	for step(t, q) {
	}
	if fmt.Sprint(ran) != "[/2 book/3]" {
		t.Fatalf("ran %v", ran)
	}
	got := row(t, d, a)
	if got != before || got.State != "pending" || got.Attempts != 0 {
		t.Fatalf("gated job changed: %+v vs %+v", got, before)
	}
	if row(t, d, free).State != "done" || row(t, d, b).State != "done" {
		t.Fatal("other jobs must run")
	}
	// still blocked after more polls and time
	clk.Advance(time.Hour)
	if step(t, q) {
		t.Fatal("gated job claimed")
	}
	g.set("anime", false)
	if !step(t, q) || row(t, d, a).State != "done" {
		t.Fatalf("job did not resume: %+v", row(t, d, a))
	}
	if fmt.Sprint(ran) != "[/2 book/3 anime/1]" {
		t.Fatalf("ran %v", ran)
	}
}

func TestGateWithoutDisabledPluginsOrOptionClaimsEverything(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	q := testQueue(t, d, clk) // no gate
	q.Register("k", func(context.Context, json.RawMessage) error { return nil })
	enqueue(t, q, "k", pl{PluginID: "anime"})
	if !step(t, q) {
		t.Fatal("ungated queue must claim")
	}
}

func TestGateManyDisabledPluginsAndJobs(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	g := &gate{}
	var ids []string
	for i := 0; i < 600; i++ { // beyond SQLite's old 999-variable limit once doubled
		id := fmt.Sprintf("p%d", i)
		ids = append(ids, id)
		g.set(id, true)
	}
	g.set("p7", false)
	q := testQueue(t, d, clk, WithPluginGate(g.allow))
	var n atomic.Int64
	q.Register("k", func(context.Context, json.RawMessage) error { n.Add(1); return nil })
	for i := 0; i < 600; i++ {
		enqueue(t, q, "k", pl{PluginID: ids[i%len(ids)], N: i})
	}
	enqueue(t, q, "k", pl{PluginID: "p7"})
	for step(t, q) {
	}
	if n.Load() != 2 { // i%600 == 7 once, plus the extra
		t.Fatalf("ran %d, want 2", n.Load())
	}
	var pending int
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state='pending'`).Scan(&pending)
	if pending != 599 {
		t.Fatalf("pending %d", pending)
	}
}

func TestGatedClaimIsAtomicAcrossWorkers(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	g := &gate{}
	g.set("off", true)
	var count atomic.Int64
	h := func(ctx context.Context, p json.RawMessage) error {
		count.Add(1)
		time.Sleep(time.Millisecond)
		return nil
	}
	q1 := testQueue(t, d, clk, WithPluginGate(g.allow))
	q2 := testQueue(t, d, clk, WithPluginGate(g.allow))
	q1.Register("k", h)
	q2.Register("k", h)
	const total = 40
	for i := 0; i < total; i++ {
		enqueue(t, q1, "k", pl{PluginID: "on"})
		enqueue(t, q1, "k", pl{PluginID: "off"})
	}
	var wg sync.WaitGroup
	for _, q := range []*Queue{q1, q2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ran, err := q.runOnce(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !ran {
					return
				}
			}
		}()
	}
	wg.Wait()
	var done, pending int
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state='done'`).Scan(&done)
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state='pending'`).Scan(&pending)
	if count.Load() != total || done != total || pending != total {
		t.Fatalf("ran %d done %d pending %d, want %d each", count.Load(), done, pending, total)
	}
}

// TestGateToleratesMalformedPayload: one pending row whose payload is not
// JSON (written by hand or by an old bug) must not stop the queue: with and
// without a disabled plugin the other jobs still run, a gated job still
// waits, and the malformed row fails on its own (here as an unknown kind).
func TestGateToleratesMalformedPayload(t *testing.T) {
	d := openDB(t)
	clk := newClock()
	g := &gate{}
	q := testQueue(t, d, clk, WithPluginGate(g.allow))
	var ran []string
	q.Register("k", func(ctx context.Context, p json.RawMessage) error {
		var v pl
		json.Unmarshal(p, &v)
		ran = append(ran, fmt.Sprintf("%s/%d", v.PluginID, v.N))
		return nil
	})
	res, err := d.Exec(`INSERT INTO jobs(kind, payload_json, run_after) VALUES ('legacy', 'not json {', ?)`, q.stamp(clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := res.LastInsertId()
	gated := enqueue(t, q, "k", pl{PluginID: "anime", N: 1})
	free := enqueue(t, q, "k", pl{PluginID: "book", N: 2})
	g.set("anime", true)
	for step(t, q) {
	}
	if fmt.Sprint(ran) != "[book/2]" {
		t.Fatalf("ran %v", ran)
	}
	if row(t, d, free).State != "done" || row(t, d, gated).State != "pending" || row(t, d, bad).State != "failed" {
		t.Fatalf("states: free %+v gated %+v bad %+v", row(t, d, free), row(t, d, gated), row(t, d, bad))
	}
	g.set("anime", false)
	if !step(t, q) || row(t, d, gated).State != "done" {
		t.Fatalf("gated job did not resume: %+v", row(t, d, gated))
	}
}
