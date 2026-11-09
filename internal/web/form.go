package web

import (
	"errors"
	"net/http"
	"strings"
)

// Shared form handling. The one entry form parser is parseEntryForm (add.go,
// used by add, edit and manual create); the schema-generated item forms are
// in details.go.

const maxFormBytes = 64 << 10

// parseForm limits the body to maxFormBytes and parses it. On failure it has
// written the response (413 for an oversized body, else 400) and returns false.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
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

// safeReturn returns raw when it is a same-site path below the plugin's
// prefix /{pluginID} (exactly "/{pluginID}", or followed by "?" or "/": the
// plugin's list with its filters and its item pages), else "/{pluginID}".
// Every other first segment is rejected, including other plugins, /list
// (the entry routes are never a return target), /people and /settings. It
// also rejects schemes, "//", backslashes, fragments, percent escapes and
// control characters or spaces.
func safeReturn(raw, pluginID string) string {
	def := "/" + pluginID
	rest, ok := strings.CutPrefix(raw, def)
	if pluginID == "" || !ok || (rest != "" && rest[0] != '?' && rest[0] != '/') {
		return def
	}
	if strings.Contains(raw, "//") || strings.ContainsAny(raw, "\\#%") {
		return def
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return def
		}
	}
	return raw
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
