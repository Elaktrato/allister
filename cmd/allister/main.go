package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/jobs"
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

	providers := map[provider.Source]provider.Provider{
		provider.AniList: anilist.New(anilist.DefaultURL, nil),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// One worker, at least 2s between jobs to respect AniList's rate limit.
	queue := jobs.NewQueue(database, jobs.WithMinInterval(2*time.Second))
	queue.Register(jobs.KindSyncAnime, jobs.SyncAnime(jobs.SyncDeps{
		Store:     store.New(database),
		Providers: providers,
		CoversDir: coversDir,
	}))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		queue.Run(ctx)
	}()

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: web.NewServer(web.Deps{
			Settings:  settings.New(database),
			Providers: providers,
			Jobs:      queue,
			CoversDir: coversDir,
		}),
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	log.Printf("listening on %s (data dir %s)", srv.Addr, cfg.DataDir)
	err = srv.ListenAndServe()
	stop()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
