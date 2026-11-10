package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
	"github.com/allister/allister/internal/plugins/anime"
)

func TestServeDrainsInFlightRequestsOnShutdown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte("done"))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, srv, ln, 5*time.Second) }()

	body := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	<-started
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was in flight", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the request finished")
	}
	if got := <-body; got != "done" {
		t.Fatalf("response %q", got)
	}
}

func TestServeReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "256.0.0.1:bad"}
	if err := serve(context.Background(), srv, time.Second); err == nil {
		t.Fatal("expected listen error")
	}
}

// TestSyncJobsWiresAllInstalledPlugins: every installed plugin (enabled or
// not, with or without sources: a manual item may still hold a remote cover)
// gets the generic sync job carrying its own plugin id.
func TestSyncJobsWiresAllInstalledPlugins(t *testing.T) {
	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	noSources := plugintest.BookPlugin(plugintest.WithID("nosrc"), plugintest.WithDefaultEnabled(false))
	noSources.M.Sources, noSources.Sources = nil, nil
	noSources.M.Importers, noSources.Importers = nil, nil
	for _, p := range []plugin.Plugin{anime.New(anime.Options{}), plugintest.BookPlugin(), plugintest.TVPlugin(), noSources} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	got := syncJobs(reg)
	if len(got) != 4 {
		t.Fatalf("sync jobs for %d plugins: %v", len(got), got)
	}
	for i, id := range []string{"anime", "book", "tv", "nosrc"} {
		job := got[id]
		if job == nil {
			t.Fatalf("no sync job for %s", id)
		}
		if kind, payload := job(int64(i + 7)); kind != jobs.KindSyncItem || payload != (jobs.SyncPayload{ItemID: int64(i + 7), PluginID: id}) {
			t.Fatalf("%s: got %q %+v", id, kind, payload)
		}
	}
}

func TestRemoveRestoreTempsDeletesOnlyRestoreSpools(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"restore-123.zip", "restore-x.zip", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removeRestoreTemps(dir)
	left, _ := os.ReadDir(tmp)
	if len(left) != 1 || left[0].Name() != "keep.txt" {
		t.Fatalf("left = %v", left)
	}
	removeRestoreTemps(filepath.Join(dir, "missing")) // no tmp dir: no-op
}
