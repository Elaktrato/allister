// Package version is the build's version information. The variables are set
// at link time; a plain `go build` keeps the defaults:
//
//	go build -ldflags "-X github.com/allister/allister/internal/version.Version=v0.1.0 \
//	  -X github.com/allister/allister/internal/version.Commit=$(git rev-parse HEAD) \
//	  -X github.com/allister/allister/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// The Dockerfile passes them from its VERSION, COMMIT and DATE build args.
package version

import (
	"runtime"
	"strings"
)

// Link-time values (go build -ldflags -X).
var (
	// Version is the release tag (v1.2.3, v1.2.3-rc.1) or "dev".
	Version = "dev"
	// Commit is the full git commit hash or "unknown".
	Commit = "unknown"
	// Date is the build time (RFC 3339, UTC) or "unknown".
	Date = "unknown"
)

// Info is the version information of the running binary (GET /version).
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
}

// Get returns the version information; an empty link-time value reads as
// its default.
func Get() Info {
	return Info{
		Version: or(Version, "dev"),
		Commit:  or(Commit, "unknown"),
		Date:    or(Date, "unknown"),
		Go:      runtime.Version(),
	}
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// String is the short human form: "v0.1.0 (0123456, 2026-11-10)", the
// version alone for a development build.
func (i Info) String() string {
	var extra []string
	if i.Commit != "unknown" {
		extra = append(extra, i.Commit[:min(7, len(i.Commit))])
	}
	if i.Date != "unknown" {
		day, _, _ := strings.Cut(i.Date, "T")
		extra = append(extra, day)
	}
	if len(extra) == 0 {
		return i.Version
	}
	return i.Version + " (" + strings.Join(extra, ", ") + ")"
}
