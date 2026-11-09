package main

import (
	"log"
	"net/http"

	"github.com/allister/allister/internal/config"
	"github.com/allister/allister/internal/web"
)

func main() {
	cfg := config.Load()
	addr := ":" + cfg.Port
	log.Printf("listening on %s (data dir %s)", addr, cfg.DataDir)
	log.Fatal(http.ListenAndServe(addr, web.NewServer(web.Deps{})))
}
