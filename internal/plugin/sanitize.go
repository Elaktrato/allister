package plugin

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitising helpers for plugin output.
//
// The library rejects control characters, NUL, invalid UTF-8 and non-http(s)
// URLs. Plugins should therefore run every string they take from an external
// source (titles, names, synopses, genres, tags, URLs ...) through these
// helpers before returning it, so a quirk of the source can never make an
// item unsaveable. User input is deliberately NOT sanitised this way: what a
// user types is rejected by the library, never silently rewritten.

func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }

// truncateRunes returns at most max runes of s (never splitting a rune).
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

// SingleLine returns s as one clean line: invalid UTF-8 and NUL are removed,
// every run of whitespace and C0/C1 control characters (tab, newline and CR
// included) becomes ONE space, the result is trimmed and cut to at most
// maxRunes runes. Zero-width joiners and emoji are kept.
func SingleLine(s string, maxRunes int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			continue // invalid byte (or a literal U+FFFD, which is just as useless)
		case r == 0:
			continue
		case isControl(r) || unicode.IsSpace(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(truncateRunes(b.String(), maxRunes))
}

// MultiLine returns s as clean multi-line text: invalid UTF-8, NUL and C0/C1
// control characters are removed except newline and tab, CRLF and CR become
// a newline, the result is trimmed and cut to at most maxRunes runes.
func MultiLine(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			continue
		case r == '\n' || r == '\t':
		case isControl(r):
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(truncateRunes(b.String(), maxRunes))
}

// HTTPSURL returns s as an absolute http(s) URL, or "" when it is not one.
// Control characters are stripped first, the value is trimmed, a
// protocol-relative "//host/x" becomes "https://host/x" and "http://" is kept
// as given. A URL without a host is "".
func HTTPSURL(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return s
}
