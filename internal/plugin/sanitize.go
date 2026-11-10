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

// SingleLine returns s as one clean line. Every run of whitespace and C0/C1
// control characters, NUL included, becomes ONE space; the result is trimmed
// and cut to at most maxRunes runes.
//
// Whitespace is unicode.IsSpace, so NBSP (U+00A0), U+2028 and U+2029 count as
// whitespace and turn into a space. U+FEFF (byte order mark / zero width
// no-break space) and U+200B (zero width space) are NOT whitespace to Go and
// are kept as they are: they are format characters, not controls, the library
// accepts them, and U+200D (zero width joiner) must survive for emoji
// sequences. Invalid UTF-8 bytes (including encoded lone surrogates) are
// dropped, and so is a literal U+FFFD in otherwise valid text (it is only
// ever the mark of a decoding failure).
func SingleLine(s string, maxRunes int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			continue
		case r == 0 || isControl(r) || unicode.IsSpace(r):
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
// control characters are removed except newline and tab (NUL and the other
// controls vanish WITHOUT leaving a space, unlike SingleLine), CRLF and CR
// become a newline, the result is trimmed and cut to at most maxRunes runes.
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

// maxURLRunes is the longest URL HTTPSURL accepts.
const maxURLRunes = 2048

// HTTPSURL returns s as a clean absolute http(s) URL, or "" when it is not
// one. Control characters and invalid UTF-8 are stripped first, then the
// value is trimmed. A protocol-relative "//host/x" becomes "https://host/x";
// "http://" is kept as given. Userinfo ("user:pw@") is removed so a
// credential never ends up stored or rendered, the scheme and host are
// lower-cased, and path, query and fragment are kept as given. A URL with no
// host, a host containing whitespace, a scheme other than http/https or more
// than 2048 runes is "".
func HTTPSURL(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	if utf8.RuneCountInString(s) > maxURLRunes {
		return ""
	}
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || strings.IndexFunc(u.Host, unicode.IsSpace) >= 0 {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	// Rebuild by hand: url.URL.String would percent-escape an IDN host. The
	// part after the authority (path, query, fragment) is kept as given.
	rest := s[strings.Index(s, "//")+2:]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[i:]
	} else {
		rest = ""
	}
	out := scheme + "://" + strings.ToLower(u.Host) + rest
	if utf8.RuneCountInString(out) > maxURLRunes {
		return ""
	}
	return out
}
