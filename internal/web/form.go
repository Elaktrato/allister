package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Shared form handling. The one entry form parser is parseEntryForm (add.go,
// used by add, edit and manual create); the schema-generated item forms are
// in details.go.

// maxFormBytes caps an ordinary form body. maxTextFormBytes caps the forms
// that carry long texts (the entry's notes and the run's review, 10,000
// runes each, or a unit note): 10,000 four-byte runes are about 120 KB
// url-encoded, so both texts and the other fields fit with room to spare.
const (
	maxFormBytes     = 64 << 10
	maxTextFormBytes = 512 << 10
)

// parseForm limits the body to maxFormBytes and parses it. On failure it has
// written the response (413 for an oversized body, else 400) and returns false.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	return parseFormLimit(w, r, maxFormBytes)
}

// parseTextForm is parseForm for the forms with long texts (maxTextFormBytes).
func parseTextForm(w http.ResponseWriter, r *http.Request) bool {
	return parseFormLimit(w, r, maxTextFormBytes)
}

func parseFormLimit(w http.ResponseWriter, r *http.Request, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return false
	}
	return true
}

// returnTarget is the return URL of an entry action: the literal "/" (the
// home page's cards), a custom list page (listReturn) or else safeReturn's
// plugin-scoped path.
func returnTarget(raw, pluginID string) string {
	if raw == "/" || listReturn(raw) {
		return raw
	}
	return safeReturn(raw, pluginID)
}

// safeReturn returns raw when it is a same-site path below the plugin's
// prefix /{pluginID} (exactly "/{pluginID}", or followed by "?" or "/": the
// plugin's list with its filters and its item pages), else "/{pluginID}".
// Every other first segment is rejected, including other plugins, /list
// (the entry routes are never a return target), /people and /settings. It
// also rejects schemes, "//", backslashes, fragments, dot-dot path
// segments, and control characters or spaces. Percent escapes are refused
// in the path; in the query (a filtered list URL) only well-formed escapes
// of printable bytes pass.
func safeReturn(raw, pluginID string) string {
	def := "/" + pluginID
	rest, ok := strings.CutPrefix(raw, def)
	if pluginID == "" || !ok || (rest != "" && rest[0] != '?' && rest[0] != '/') {
		return def
	}
	if strings.Contains(raw, "//") || strings.ContainsAny(raw, "\\#") {
		return def
	}
	path, query, _ := strings.Cut(raw, "?")
	if strings.Contains(path, "%") || !printableEscapes(query) {
		return def
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return def
		}
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return def
		}
	}
	return raw
}

// printableEscapes reports whether every "%" in s starts a two-digit hex
// escape of a byte that is not a control character.
func printableEscapes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) {
			return false
		}
		b, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil || b < 0x20 || b == 0x7f {
			return false
		}
		i += 2
	}
	return true
}

type statusOpt struct {
	Value, Label string
	Selected     bool
}

func invalidMessage(err error) string {
	m := strings.TrimPrefix(err.Error(), "invalid: ")
	if m == "" {
		return "Invalid input."
	}
	return strings.ToUpper(m[:1]) + m[1:] + "."
}
