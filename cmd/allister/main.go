package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/db"
	"github.com/allister/allister/internal/settings"
	"github.com/allister/allister/internal/web"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "allister.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	addr := ":" + cfg.Port
	log.Printf("listening on %s (data dir %s)", addr, cfg.DataDir)
	log.Fatal(http.ListenAndServe(addr, web.NewServer(web.Deps{
		Settings: settings.New(database),
	})))
}
