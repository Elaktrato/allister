// Package covers downloads cover and person images into a local directory and
// maps stored image values to URLs.
//
// Convention: items.cover_path and people.image_path hold the remote URL until
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

// MaxImageBytes is the per-file limit of Download (2 MiB): background
// downloads fetch the source's covers (about 40 KB at the larger size) and a
// photo of every credited person (about 21 KB each), so anything near the
// limit is not a normal image and must not fill the disk unasked.
const MaxImageBytes = 2 << 20

// MaxUploadBytes is the per-file limit of Save (10 MiB): a cover the user
// uploads on purpose (a photo or scan) may be much larger than a source's
// image, and it is one file per item, not one per credited person.
const MaxUploadBytes = 10 << 20

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
// a client with a 20s timeout. Content over MaxImageBytes is ErrTooLarge. No
// partial file is left on error.
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
	name, err := writeHashed(dir, resp.Body, MaxImageBytes, func([]byte) (string, error) { return ext, nil })
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

// ErrTooLarge is returned for content over the limit (MaxImageBytes for
// Download, MaxUploadBytes for Save).
var ErrTooLarge = errors.New("image too large")

// Save stores an uploaded image into dir and returns its file name
// (<sha256 hex><ext>). The type is decided by sniffing the first 512 bytes,
// never by client-supplied metadata. At most MaxUploadBytes are accepted. No
// partial file is left on error.
func Save(dir string, r io.Reader) (string, error) {
	name, err := writeHashed(dir, r, MaxUploadBytes, func(head []byte) (string, error) {
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

// SaveRestored stores a cover file of a backup archive into dir like Save,
// accepting every type Download stores: the sniffed types of Save plus AVIF,
// which http.DetectContentType cannot recognise and which is checked by its
// ISO-BMFF file type box (major brand avif or avis). A downloaded cover's
// remote URL is not retained, so refusing its file would lose the cover.
func SaveRestored(dir string, r io.Reader) (string, error) {
	name, err := writeHashed(dir, r, MaxUploadBytes, func(head []byte) (string, error) {
		if ext, ok := uploadTypes[http.DetectContentType(head)]; ok {
			return ext, nil
		}
		if isAVIF(head) {
			return ".avif", nil
		}
		return "", ErrNotImage
	})
	if err != nil {
		return "", fmt.Errorf("save restored cover: %w", err)
	}
	return name, nil
}

// isAVIF reports whether head starts with an ISO-BMFF ftyp box whose major
// brand is avif (still image) or avis (image sequence).
func isAVIF(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	brand := string(head[8:12])
	return brand == "avif" || brand == "avis"
}

// writeHashed reads r (at most limit bytes), asks pick for the extension given
// the first 512 bytes, and stores the content as <sha256><ext> in dir through
// a temp file and rename. Nothing is left behind on error.
func writeHashed(dir string, r io.Reader, limit int64, pick func(head []byte) (string, error)) (string, error) {
	r = io.LimitReader(r, limit+1)
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
	if total > limit {
		return "", ErrTooLarge
	}
	name := hex.EncodeToString(h.Sum(nil)) + ext
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return name, nil
}
