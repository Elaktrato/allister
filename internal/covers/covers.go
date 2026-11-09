// Package covers downloads cover and person images into a local directory and
// maps stored image values to URLs.
//
// Convention: anime.cover_path and people.image_path hold the remote URL until
// a download succeeded; afterwards they hold the file's base name relative to
// DATA_DIR/covers.
package covers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxBytes = 10 << 20 // 10 MiB

var extensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"image/avif": ".avif",
}

// Src returns the URL to use in templates for a stored image value: remote
// URLs unchanged, "" for empty, otherwise /covers/<escaped name>.
func Src(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return "/covers/" + url.PathEscape(path)
}

// Download fetches an image over http(s) into dir and returns its file name
// (sha256 of the content plus an extension from Content-Type). A nil hc means
// a client with a 20s timeout. No partial file is left on error.
func Download(ctx context.Context, hc *http.Client, dir, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("download cover: unsupported url %q", rawURL)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download cover: %s returned status %d", rawURL, resp.StatusCode)
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return "", fmt.Errorf("download cover: bad content type %q: %w", resp.Header.Get("Content-Type"), err)
	}
	ext, ok := extensions[strings.ToLower(mt)]
	if !ok {
		return "", fmt.Errorf("download cover: unsupported content type %q", mt)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	if n > maxBytes {
		return "", errors.New("download cover: image larger than 10 MiB")
	}
	name := hex.EncodeToString(h.Sum(nil)) + ext
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	return name, nil
}
