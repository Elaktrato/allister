package web

import (
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/allister/allister/internal/importer/yamtrack"
	"github.com/allister/allister/internal/provider"
)

const (
	maxImportBody   = 20 << 20
	maxImportMemory = 1 << 20
)

var importStateLabels = map[string]string{
	"pending": "Pending", "running": "Running", "done": "Done", "failed": "Failed", "cancelled": "Cancelled",
}

// importLine is one listed row with its link (unmatched rows only).
type importLine struct {
	Title, Message, URL string
}

// importPage is the data of the import page and of its progress fragment.
type importPage struct {
	Error string
	// Conflict shows a link to the progress of the running import.
	Conflict bool
	Report   *yamtrack.Report
	State    string
	ETA      string
	// Lists holds the non-empty row lists (unmatched, failed, skipped).
	Lists []importList
}

// importList is one capped list of rows; More counts the rows not shown.
type importList struct {
	Heading, Note string
	Count, More   int
	Rows          []importLine
}

func importLines(items []yamtrack.Item, link bool) []importLine {
	out := make([]importLine, 0, len(items))
	for _, it := range items {
		l := importLine{Title: it.Title, Message: it.Message}
		if link && it.SourceID != "" {
			l.URL = animeURL(string(provider.Manual), it.SourceID)
		}
		out = append(out, l)
	}
	return out
}

// formatETA is a rough, human estimate of the remaining time.
func formatETA(d time.Duration) string {
	mins := int(d.Round(time.Minute) / time.Minute)
	switch {
	case mins < 1:
		return "less than a minute"
	case mins < 90:
		return fmt.Sprintf("about %d min", mins)
	}
	return fmt.Sprintf("about %.1f h", d.Hours())
}

func (s *server) loadImport(r *http.Request, p importPage) (importPage, error) {
	rep, err := s.deps.Imports.Latest(r.Context())
	if err != nil {
		return p, err
	}
	p.Report = rep
	if rep != nil {
		p.State = importStateLabels[rep.State]
		add := func(l importList) {
			if len(l.Rows) > 0 {
				p.Lists = append(p.Lists, l)
			}
		}
		add(importList{Heading: "Unmatched", Count: rep.Unmatched, More: rep.MoreUnmatched, Rows: importLines(rep.UnmatchedRows, true),
			Note: "Not found on AniList: added as manual entries with the data from Yamtrack."})
		add(importList{Heading: "Failed", Count: rep.Failed, More: rep.MoreFailed, Rows: importLines(rep.FailedRows, false)})
		add(importList{Heading: "Skipped", Count: rep.Skipped, More: rep.MoreSkipped, Rows: importLines(rep.SkippedRows, false)})
		if rep.Active {
			p.ETA = formatETA(rep.ETA)
		}
	}
	return p, nil
}

func (s *server) getImport(w http.ResponseWriter, r *http.Request) {
	if s.deps.Imports == nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.loadImport(r, importPage{})
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	if isPartialRequest(r) {
		s.renderPartial(w, "import", "import_progress", p)
		return
	}
	s.render(w, r, "import", p)
}

func (s *server) postImport(w http.ResponseWriter, r *http.Request) {
	if s.deps.Imports == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	err := r.ParseMultipartForm(maxImportMemory)
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "The file is larger than 20 MiB.", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	reject := func(status int, p importPage) {
		p, lerr := s.loadImport(r, p)
		if lerr != nil {
			s.fail(w, r, lerr, "")
			return
		}
		s.renderStatus(w, r, status, "import", p)
	}
	fh := firstFile(r, "file")
	if fh == nil {
		reject(http.StatusUnprocessableEntity, importPage{Error: "Please choose the Yamtrack CSV file to upload."})
		return
	}
	f, err := fh.Open()
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	defer f.Close()
	_, err = s.deps.Imports.Submit(r.Context(), fh.Filename, f)
	var active *yamtrack.ActiveError
	switch {
	case err == nil:
		http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
	case errors.As(err, &active):
		reject(http.StatusConflict, importPage{Conflict: true, Error: "An import is already running. Wait for it to finish or cancel it first."})
	case errors.Is(err, yamtrack.ErrBadFormat), errors.Is(err, yamtrack.ErrNoAnimeRows):
		reject(http.StatusUnprocessableEntity, importPage{Error: uploadMessage(err)})
	default:
		log.Printf("import upload: %v", err)
		s.fail(w, r, err, "")
	}
}

func firstFile(r *http.Request, field string) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	if fhs := r.MultipartForm.File[field]; len(fhs) > 0 {
		return fhs[0]
	}
	return nil
}

func uploadMessage(err error) string {
	return "This file cannot be imported: " + err.Error() + "."
}

func (s *server) postImportCancel(w http.ResponseWriter, r *http.Request) {
	if s.deps.Imports == nil {
		http.NotFound(w, r)
		return
	}
	if err := s.deps.Imports.Cancel(r.Context()); err != nil {
		s.fail(w, r, err, "")
		return
	}
	http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
}

func (s *server) postImportResume(w http.ResponseWriter, r *http.Request) {
	if s.deps.Imports == nil {
		http.NotFound(w, r)
		return
	}
	_, err := s.deps.Imports.Resume(r.Context())
	var active *yamtrack.ActiveError
	reject := func(status int, p importPage) {
		p, lerr := s.loadImport(r, p)
		if lerr != nil {
			s.fail(w, r, lerr, "")
			return
		}
		s.renderStatus(w, r, status, "import", p)
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
	case errors.As(err, &active):
		reject(http.StatusConflict, importPage{Conflict: true, Error: "An import is already running. Wait for it to finish or cancel it first."})
	case errors.Is(err, yamtrack.ErrNothingToResume):
		reject(http.StatusUnprocessableEntity, importPage{Error: "There is nothing to resume."})
	default:
		s.fail(w, r, err, "")
	}
}
