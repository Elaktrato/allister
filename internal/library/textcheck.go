package library

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type textMode int

const (
	// singleLine rejects every control character, tab and newline included.
	singleLine textMode = iota
	// multiLine (long text) additionally allows tab, newline and carriage return.
	multiLine
)

// cleanText rejects text that must never be stored: invalid UTF-8, NUL and
// C0/C1/DEL control characters (tab, newline and CR only in multiLine mode).
// path names the value in the ErrInvalid message, for example "title".
func cleanText(path, s string, mode textMode) error {
	if !utf8.ValidString(s) {
		return invalid("%s is not valid UTF-8", path)
	}
	for _, r := range s {
		if mode == multiLine && (r == '\t' || r == '\n' || r == '\r') {
			continue
		}
		if r == 0 {
			return invalid("%s contains a NUL character", path)
		}
		if unicode.IsControl(r) {
			return invalid("%s contains a control character (U+%04X)", path, r)
		}
	}
	return nil
}

// checkHTTPURL accepts "" or an http(s) URL with a host.
func checkHTTPURL(path, s string) error {
	if s == "" {
		return nil
	}
	if err := cleanText(path, s, singleLine); err != nil {
		return err
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return invalid("%s must be empty or an http(s) URL", path)
	}
	return nil
}

// checkImageRef accepts "", an http(s) URL, or a plain local file base name
// (no path separators, no "..", no ":" so "javascript:" and "data:" fail).
func checkImageRef(path, s string) error {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(strings.ToLower(s), "http") && strings.Contains(s, "://") {
		return checkHTTPURL(path, s)
	}
	if err := cleanText(path, s, singleLine); err != nil {
		return err
	}
	if strings.ContainsAny(s, `/\:`) || strings.Contains(s, "..") || strings.TrimSpace(s) != s {
		return invalid("%s must be empty, an http(s) URL or a plain file name", path)
	}
	return nil
}

func checkSyncState(s string) error {
	switch s {
	case "pending", "partial", "done":
		return nil
	}
	return invalid("sync state %q must be pending, partial or done", s)
}

// AcceptsURL reports whether the library stores s as a URL: empty, or an
// absolute http(s) URL with a host and no control characters. Adapters over
// sources that are not sanitised use it to drop a value instead of failing the
// whole save.
func AcceptsURL(s string) bool { return checkHTTPURL("url", s) == nil }

// AcceptsImageRef reports whether the library stores s as an image reference:
// empty, an http(s) URL or a plain local file name.
func AcceptsImageRef(s string) bool { return checkImageRef("image", s) == nil }
