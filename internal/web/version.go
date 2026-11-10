package web

import (
	"encoding/json"
	"net/http"

	"github.com/allister/allister/internal/version"
)

// getVersion answers GET /version with the build's version information as
// JSON ({"version", "commit", "date", "go"}). /healthz stays a plain "ok".
func getVersion(w http.ResponseWriter, r *http.Request) {
	b, err := json.Marshal(version.Get())
	if err != nil {
		http.Error(w, "version unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
