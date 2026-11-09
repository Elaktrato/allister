package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/store"
)

// Shared list-entry form handling for the add dialog (anime.go) and the edit
// dialog (list.go).

const (
	maxCommentRunes = 2000
	maxFormBytes    = 64 << 10
)

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

// safeReturn returns raw when it is a same-site path below /anime (exactly
// "/anime", or "/anime" followed by "?" or "/"), else "/anime". It rejects
// schemes, "//", backslashes, fragments and control characters or spaces.
func safeReturn(raw string) string {
	const def = "/anime"
	rest, ok := strings.CutPrefix(raw, "/anime")
	if !ok || (rest != "" && rest[0] != '?' && rest[0] != '/') {
		return def
	}
	if strings.Contains(raw, "//") || strings.ContainsAny(raw, "\\#") {
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

// addForm is the add dialog's data; the string fields echo the user's input.
type addForm struct {
	Action, CancelURL              string
	Status, Rating, Progress       string
	StartedOn, FinishedOn, Comment string
	Episodes                       *int
	Statuses                       []statusOpt
	Error, ExistingURL             string
	// Heading and Submit label the dialog; Return is the hidden return path
	// (edit dialog only, already validated by safeReturn).
	Heading, Submit, Return string
}

func newAddForm(source, id string) addForm {
	return addForm{
		Action:    addURL(source, id),
		CancelURL: animeURL(source, id),
		Status:    string(store.PlanToWatch),
		Heading:   "Add to list",
		Submit:    "Add",
	}
}

func (f addForm) withStatuses() addForm {
	f.Statuses = nil
	for _, st := range statusOrder {
		f.Statuses = append(f.Statuses, statusOpt{string(st), statusLabels[st], string(st) == f.Status})
	}
	return f
}

// parseEntryForm reads and validates the form fields; msg is a user-facing
// message and non-empty on error.
func parseEntryForm(r *http.Request, f addForm) (addForm, store.ListEntry, string) {
	f.Status = strings.TrimSpace(r.PostFormValue("status"))
	f.Rating = strings.TrimSpace(r.PostFormValue("rating"))
	f.Progress = strings.TrimSpace(r.PostFormValue("progress"))
	f.StartedOn = strings.TrimSpace(r.PostFormValue("started_on"))
	f.FinishedOn = strings.TrimSpace(r.PostFormValue("finished_on"))
	f.Comment = strings.TrimSpace(r.PostFormValue("comment"))
	if rs := []rune(f.Comment); len(rs) > maxCommentRunes {
		f.Comment = string(rs[:maxCommentRunes])
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

func parseDay(v, label string) (*time.Time, string) {
	if v == "" {
		return nil, ""
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, label + " must be a date like 2024-01-31."
	}
	return &t, ""
}

func invalidMessage(err error) string {
	m := strings.TrimPrefix(err.Error(), "invalid: ")
	if m == "" {
		return "Invalid input."
	}
	return strings.ToUpper(m[:1]) + m[1:] + "."
}
