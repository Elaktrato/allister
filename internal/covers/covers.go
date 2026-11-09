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
	name, err := writeHashed(dir, resp.Body, func([]byte) (string, error) { return ext, nil })
	if err != nil {
		return "", fmt.Errorf("download cover: %w", err)
	}
	return name, nil
}

// uploadTypes are the sniffed content types Save accepts. avif is absent
// because http.DetectContentType cannot recognise it; svg is deliberately
// excluded (scriptable).
var uploadTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// ErrNotImage is returned by Save when the content is not an accepted image.
var ErrNotImage = errors.New("not a supported image (jpeg, png, webp or gif)")

// ErrTooLarge is returned by Save for content over 10 MiB.
var ErrTooLarge = errors.New("image larger than 10 MiB")

// Save stores an uploaded image into dir and returns its file name
// (<sha256 hex><ext>). The type is decided by sniffing the first 512 bytes,
// never by client-supplied metadata. At most 10 MiB are accepted. No partial
// file is left on error.
func Save(dir string, r io.Reader) (string, error) {
	name, err := writeHashed(dir, r, func(head []byte) (string, error) {
		ext, ok := uploadTypes[http.DetectContentType(head)]
		if !ok {
			return "", ErrNotImage
		}
		return ext, nil
	})
	if err != nil {
		return "", fmt.Errorf("save cover: %w", err)
	}
	return name, nil
}

// writeHashed reads r (at most maxBytes), asks pick for the extension given
// the first 512 bytes, and stores the content as <sha256><ext> in dir through
// a temp file and rename. Nothing is left behind on error.
func writeHashed(dir string, r io.Reader, pick func(head []byte) (string, error)) (string, error) {
	r = io.LimitReader(r, maxBytes+1)
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	head = head[:n]
	ext, err := pick(head)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	h := sha256.New()
	total := int64(len(head))
	_, err = io.MultiWriter(tmp, h).Write(head)
	if err == nil {
		var m int64
		m, err = io.Copy(io.MultiWriter(tmp, h), r)
		total += m
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if total > maxBytes {
		return "", ErrTooLarge
	}
	name := hex.EncodeToString(h.Sum(nil)) + ext
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return name, nil
}
