package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/store"
)

// Shared form handling. The generic entry form parser is parseEntryForm
// (add.go, used by add and edit); the legacy anime-shaped one below is kept
// only for the manual form (manual.go) until it becomes generic.

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

// legacyEntryForm is the list entry part of the legacy manual form; the
// string fields echo the user's input.
//
// TODO(Task 10): remove when the manual form uses parseEntryForm.
type legacyEntryForm struct {
	Status, Rating, Progress       string
	StartedOn, FinishedOn, Comment string
	Statuses                       []statusOpt
	Error                          string
}

func (f legacyEntryForm) withStatuses() legacyEntryForm {
	f.Statuses = nil
	for _, st := range statusOrder {
		f.Statuses = append(f.Statuses, statusOpt{string(st), statusLabels[st], string(st) == f.Status})
	}
	return f
}

// parseLegacyEntryForm reads and validates the legacy (anime-shaped) form
// fields; msg is a user-facing message and non-empty on error.
//
// TODO(Task 10): remove when the manual form uses parseEntryForm.
func parseLegacyEntryForm(r *http.Request, f legacyEntryForm) (legacyEntryForm, store.ListEntry, string) {
	f.Status = strings.TrimSpace(r.PostFormValue("status"))
	f.Rating = strings.TrimSpace(r.PostFormValue("rating"))
	f.Progress = strings.TrimSpace(r.PostFormValue("progress"))
	f.StartedOn = strings.TrimSpace(r.PostFormValue("started_on"))
	f.FinishedOn = strings.TrimSpace(r.PostFormValue("finished_on"))
	f.Comment = strings.TrimSpace(r.PostFormValue("comment"))
	if rs := []rune(f.Comment); len(rs) > library.MaxCommentRunes {
		f.Comment = string(rs[:library.MaxCommentRunes])
	}
	e := store.ListEntry{Status: store.Status(f.Status), Comment: f.Comment}
	if _, ok := statusLabels[e.Status]; !ok {
		return f, e, "Please choose a status."
	}
	if f.Rating != "" {
		n, err := strconv.Atoi(f.Rating)
		if err != nil || n < 0 || n > 10 {
			return f, e, "Rating must be a whole number from 0 to 10, or blank for unrated."
		}
		e.Rating = &n
	}
	if f.Progress != "" {
		n, err := strconv.Atoi(f.Progress)
		if err != nil || n < 0 {
			return f, e, "Progress must be a whole number, 0 or more."
		}
		e.Progress = n
	}
	var msg string
	if e.StartedOn, msg = parseDay(f.StartedOn, "Start date"); msg != "" {
		return f, e, msg
	}
	if e.FinishedOn, msg = parseDay(f.FinishedOn, "Finish date"); msg != "" {
		return f, e, msg
	}
	return f, e, ""
}

func invalidMessage(err error) string {
	m := strings.TrimPrefix(err.Error(), "invalid: ")
	if m == "" {
		return "Invalid input."
	}
	return strings.ToUpper(m[:1]) + m[1:] + "."
}
