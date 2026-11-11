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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/allister/allister/internal/appearance"
	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime"
	"github.com/allister/allister/internal/plugins/book"
	"github.com/allister/allister/internal/plugins/manga"
	"github.com/allister/allister/internal/plugins/movie"
	"github.com/allister/allister/internal/plugins/tv"
	"github.com/allister/allister/internal/settings"
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
	removeRestoreTemps(cfg.DataDir)
	database, err := db.Open(filepath.Join(cfg.DataDir, "allister.db"))
	if err != nil {
		return err
	}
	defer database.Close()

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// The worker outlives the HTTP drain: an in-flight request may still
	// enqueue a job.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()

	settingsStore := settings.New(database)
	// The stored key wins; when none is saved the environment still supplies
	// one. Resolved per request, so a key saved in the settings takes effect
	// without a restart.
	fallback := map[string]string{"tmdb": cfg.TMDBKey}
	sourceKey := func(ctx context.Context, sourceID string) (string, error) {
		return resolveSourceKey(ctx, settingsStore.SourceKey, fallback, sourceID)
	}
	tmdbKey := func(ctx context.Context) (string, error) { return sourceKey(ctx, "tmdb") }
	registry := plugin.NewRegistry(settingsStore)
	if err := registry.Register(manga.New(manga.Options{})); err != nil {
		log.Printf("startup: %v", err)
	}
	if err := registry.Register(book.New(book.Options{GoogleBooksKey: cfg.GoogleBooksKey})); err != nil {
		log.Printf("startup: %v", err)
	}
	if err := registry.Register(tv.New(tv.Options{TMDBKey: tmdbKey})); err != nil {
		log.Printf("startup: %v", err)
	}
	if err := registry.Register(movie.New(movie.Options{TMDBKey: tmdbKey})); err != nil {
		log.Printf("startup: %v", err)
	}
	if err := registry.Register(anime.New(anime.Options{})); err != nil {
		log.Printf("startup: %v", err)
	}

	// One worker, at least 2s between jobs to respect the sources' rate limits.
	// Jobs of a disabled plugin stay pending until it is enabled again.
	queue := jobs.NewQueue(database, jobs.WithMinInterval(2*time.Second),
		jobs.WithPluginGate(registry.IsEnabled))
	lib := library.New(database, registry)
	queue.Register(jobs.KindSyncItem, jobs.SyncItem(jobs.SyncDeps{
		Library:   lib,
		Registry:  registry,
		CoversDir: coversDir,
	}))
	queue.Register(jobs.KindSchedule, jobs.Schedule(jobs.ScheduleDeps{Library: lib, Registry: registry}))
	syncJobsByPlugin := syncJobs(registry)
	imports := importer.New(importer.Deps{
		DB: database, Library: lib, Registry: registry, Queue: queue, SyncJobs: syncJobsByPlugin,
	})
	queue.Register(importer.KindRun, imports.Handler())
	queue.Register(jobs.KindScheduledImport, jobs.ScheduledImport(jobs.ScheduledImportDeps{
		Imports: imports, Settings: settingsStore, Queue: queue,
	}))
	// Reset crashed jobs first, then re-queue items whose sync never finished
	// (jobs that gave up, or were lost) so the "partially synced" state heals.
	if err := queue.Recover(workerCtx); err != nil {
		log.Printf("startup: %v", err)
	} else if n, err := queue.SweepSync(workerCtx, lib); err != nil {
		log.Printf("startup: %v", err)
	} else if n > 0 {
		log.Printf("startup: queued %d unfinished item syncs", n)
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
			Settings: settingsStore,
			Registry: registry,
			Jobs:     queue,
			SyncJobs: syncJobsByPlugin,
			// the calendar feed enqueues schedule refreshes through these
			ScheduleJobs: scheduleJobs(registry),
			// saving an import schedule starts its repeat chain
			ScheduleImport: func(pluginID, importerID string) (string, any) {
				return jobs.KindScheduledImport, jobs.ScheduledImportPayload{PluginID: pluginID, ImporterID: importerID}
			},
			CoversDir: coversDir,
			DataDir:   cfg.DataDir,
			Imports:   imports,
			Library:   lib,
			// the same resolver the plugins use: stored key, else the environment
			SourceKey: sourceKey,
			// uploaded fonts live in DATA_DIR/fonts next to the covers
			Appearance: appearance.NewService(settingsStore, appearance.NewFontStore(database, filepath.Join(cfg.DataDir, "fonts"))),
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

// resolveSourceKey returns the stored key of sourceID, or fallback[sourceID]
// when none is saved. A lookup failure is returned (never replaced by the
// fallback), so a broken settings store stays visible.
func resolveSourceKey(ctx context.Context, stored func(context.Context, string) (string, error), fallback map[string]string, sourceID string) (string, error) {
	key, err := stored(ctx, sourceID)
	if err != nil || key != "" {
		return key, err
	}
	return fallback[sourceID], nil
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

// syncJobs maps every installed plugin to the generic sync job of a newly
// saved item (web.Deps.SyncJobs). All plugins get one, with or without a
// source: the job downloads a remote cover of any item and fetches credits
// only when the item's source exists.
func syncJobs(reg *plugin.Registry) map[string]func(itemID int64) (kind string, payload any) {
	out := map[string]func(int64) (string, any){}
	for _, m := range reg.Manifests() {
		out[m.ID] = jobs.SyncJobFor(m.ID)
	}
	return out
}

// scheduleJobs wires the schedule refresh job of every installed plugin for
// the calendar feed.
func scheduleJobs(reg *plugin.Registry) map[string]func(itemID int64) (kind string, payload any) {
	out := map[string]func(int64) (string, any){}
	for _, m := range reg.Manifests() {
		out[m.ID] = jobs.ScheduleJobFor(m.ID)
	}
	return out
}

// removeRestoreTemps deletes upload spools a backup restore left in
// DATA_DIR/tmp when the process died mid-restore (a normal restore removes
// its own file on every path).
func removeRestoreTemps(dataDir string) {
	// ReadDir, not Glob: DATA_DIR may contain pattern characters
	dir := filepath.Join(dataDir, "tmp")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return // no tmp dir yet
	}
	for _, e := range ents {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, "restore-") || !strings.HasSuffix(n, ".zip") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			log.Printf("startup: %v", err)
		}
	}
}
