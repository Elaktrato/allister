package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/provider/anilist"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/store"
	"github.com/allister/allister/internal/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := config.Load()
	coversDir := filepath.Join(cfg.DataDir, "covers")
	if err := os.MkdirAll(coversDir, 0o755); err != nil {
		return err
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "allister.db"))
	if err != nil {
		return err
	}
	defer database.Close()

	st := store.New(database)
	aniClient := anilist.New(anilist.DefaultURL, nil)
	providers := map[provider.Source]provider.Provider{
		provider.AniList: aniClient,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// The worker outlives the HTTP drain: an in-flight request may still
	// enqueue a job.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()

	settingsStore := settings.New(database)
	registry := plugin.NewRegistry(settingsStore)
	if err := registry.Register(anime.New(anime.Options{Provider: aniClient})); err != nil {
		log.Printf("startup: %v", err)
	}

	// One worker, at least 2s between jobs to respect AniList's rate limit.
	// Jobs of a disabled plugin stay pending until it is enabled again.
	queue := jobs.NewQueue(database, jobs.WithMinInterval(2*time.Second),
		jobs.WithPluginGate(registry.IsEnabled))
	queue.Register(jobs.KindSyncAnime, jobs.SyncAnime(jobs.SyncDeps{
		Store:     st,
		Providers: providers,
		CoversDir: coversDir,
	}))
	importer := yamtrack.New(database, st, aniClient, queue)
	queue.Register(yamtrack.KindImport, importer.Handler())
	// Reset crashed jobs first, then re-queue anime whose sync never finished
	// (jobs that gave up, or were lost) so the "partially synced" state heals.
	if err := queue.Recover(workerCtx); err != nil {
		log.Printf("startup: %v", err)
	} else if n, err := queue.SweepSync(workerCtx, st); err != nil {
		log.Printf("startup: %v", err)
	} else if n > 0 {
		log.Printf("startup: queued %d unfinished anime syncs", n)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		queue.Run(workerCtx)
	}()

	// No Read/WriteTimeout: uploads and slow responses must not be cut off.
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: web.NewServer(web.Deps{
			Settings:      settingsStore,
			Registry:      registry,
			Store:         st,
			Providers:     providers,
			Jobs:          queue,
			CoversDir:     coversDir,
			Imports:       importer,
			ParseAnimeURL: anilist.ParseAnimeURL,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("listening on %s (data dir %s)", srv.Addr, cfg.DataDir)
	err = serve(sigCtx, srv, 10*time.Second)
	// HTTP is drained (or failed to start): now stop the worker, then the DB closes.
	stopWorker()
	wg.Wait()
	return err
}

// serve listens on srv.Addr and runs srv until ctx is cancelled, then shuts it
// down gracefully and returns only after Shutdown has finished. A normal
// shutdown returns nil; a listen/serve failure is returned.
func serve(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	return serveListener(ctx, srv, ln, shutdownTimeout)
}

func serveListener(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
