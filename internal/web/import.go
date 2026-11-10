package web

import (
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/allister/allister/internal/importer"
	"github.com/allister/allister/internal/plugin"
)

const (
	// maxImportBody caps the upload: plugin importers read the whole stream,
	// so this is the only size limit.
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

// importerOption is one importer of an enabled plugin on the upload form.
// Value is "<plugin id>/<importer id>"; every text comes from the manifest.
type importerOption struct {
	Value, Name, PluginName, FileHint, Accept, SourceName string
	Selected                                              bool
}

// importPage is the data of the import page and of its progress fragment.
type importPage struct {
	Error string
	// Conflict shows a link to the progress of the running import.
	Conflict bool
	// Importers lists the importers of the enabled plugins; Single is the
	// only one when there is exactly one (the form then needs no choice).
	Importers []importerOption
	Single    *importerOption
	Report    *importer.Report
	State     string
	ETA       string
	// Paused explains an active import whose plugin is disabled (the job
	// queue holds its job until the plugin is enabled again).
	Paused string
	// Lists holds the non-empty row lists (unmatched, failed, skipped).
	Lists []importList
}

// importList is one capped list of rows; More counts the rows not shown.
type importList struct {
	Heading, Note string
	Count, More   int
	Rows          []importLine
}

// importerOptions lists the importers of the enabled plugins in registry
// order; selected ("<plugin>/<importer>") is marked.
func (s *server) importerOptions(selected string) []importerOption {
	var out []importerOption
	for _, m := range s.deps.Registry.Manifests() {
		if !s.deps.Registry.IsEnabled(m.ID) {
			continue
		}
		for _, im := range m.Importers {
			if im.AccountLabel != "" {
				continue // account importers live on the plugin's settings page
			}
			o := importerOption{Value: m.ID + "/" + im.ID, Name: im.Name, PluginName: m.Name, FileHint: im.FileHint,
				Accept: strings.Join(im.Accept, ",")}
			if si, ok := sourceInfo(m, im.ResolveVia); ok {
				o.SourceName = si.Name
			}
			o.Selected = o.Value == selected
			out = append(out, o)
		}
	}
	return out
}

func importLines(m plugin.Manifest, items []importer.Item, link bool) []importLine {
	out := make([]importLine, 0, len(items))
	for _, it := range items {
		l := importLine{Title: it.Title, Message: it.Message}
		if link && it.SourceID != "" && m.ID != "" {
			l.URL = itemURL(m, manualSource, it.SourceID, it.Kind)
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

// unmatchedNote explains the unmatched list with the names of the import's
// source and importer (from its plugin's manifest).
func unmatchedNote(m plugin.Manifest, importerID string) string {
	src, from := "the source", "the file"
	for _, im := range m.Importers {
		if im.ID != importerID {
			continue
		}
		from = im.Name
		if si, ok := sourceInfo(m, im.ResolveVia); ok {
			src = si.Name
		}
	}
	return "Not found on " + src + ": added as manual entries with the data from " + from + "."
}

func (s *server) loadImport(r *http.Request, p importPage) (importPage, error) {
	if p.Importers == nil {
		p.Importers = s.importerOptions("")
	}
	if len(p.Importers) == 1 {
		p.Single = &p.Importers[0]
	}
	rep, err := s.deps.Imports.Latest(r.Context())
	if err != nil {
		return p, err
	}
	p.Report = rep
	if rep != nil {
		m, _ := s.deps.Registry.Manifest(rep.PluginID)
		p.State = importStateLabels[rep.State]
		add := func(l importList) {
			if len(l.Rows) > 0 {
				p.Lists = append(p.Lists, l)
			}
		}
		add(importList{Heading: "Unmatched", Count: rep.Unmatched, More: rep.MoreUnmatched, Rows: importLines(m, rep.UnmatchedRows, true),
			Note: unmatchedNote(m, rep.ImporterID)})
		add(importList{Heading: "Failed", Count: rep.Failed, More: rep.MoreFailed, Rows: importLines(m, rep.FailedRows, false)})
		add(importList{Heading: "Skipped", Count: rep.Skipped, More: rep.MoreSkipped, Rows: importLines(m, rep.SkippedRows, false)})
		if rep.Active {
			p.ETA = formatETA(rep.ETA)
			if !s.deps.Registry.IsEnabled(rep.PluginID) {
				name := rep.PluginID
				if m.Name != "" {
					name = m.Name
				}
				p.Paused = "This import is paused because the " + name + " plugin is disabled. Enable it on the Plugins page of Settings to continue, or cancel the import."
			}
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

// chosenImporter resolves the form's importer field ("<plugin>/<importer>")
// against the enabled importers. An empty field means the only enabled
// importer when there is exactly one (a client that posts only the file).
func chosenImporter(opts []importerOption, value string) (pluginID, importerID string, ok bool) {
	if value == "" {
		if len(opts) != 1 {
			return "", "", false
		}
		value = opts[0].Value
	}
	for _, o := range opts {
		if o.Value == value {
			pluginID, importerID, _ = strings.Cut(o.Value, "/")
			return pluginID, importerID, true
		}
	}
	return "", "", false
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
	value := ""
	if r.MultipartForm != nil && len(r.MultipartForm.Value["importer"]) > 0 {
		value = r.MultipartForm.Value["importer"][0]
	}
	opts := s.importerOptions(value)
	reject := func(status int, p importPage) {
		p.Importers = opts
		p, lerr := s.loadImport(r, p)
		if lerr != nil {
			s.fail(w, r, lerr, "")
			return
		}
		s.renderStatus(w, r, status, "import", p)
	}
	pluginID, importerID, ok := chosenImporter(opts, value)
	if !ok {
		msg := "This importer is not available."
		if value == "" {
			msg = "Please choose what to import from."
		}
		reject(http.StatusUnprocessableEntity, importPage{Error: msg})
		return
	}
	fh := firstFile(r, "file")
	if fh == nil {
		msg := "Please choose the file to upload."
		if len(opts) == 1 {
			msg = "Please choose the " + opts[0].Name + " file to upload."
		}
		reject(http.StatusUnprocessableEntity, importPage{Error: msg})
		return
	}
	f, err := fh.Open()
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	defer f.Close()
	_, err = s.deps.Imports.Submit(r.Context(), pluginID, importerID, fh.Filename, f)
	var active *importer.ActiveError
	var bad *importer.BadFileError
	switch {
	case err == nil:
		http.Redirect(w, r, "/settings/import", http.StatusSeeOther)
	case errors.As(err, &active):
		reject(http.StatusConflict, importPage{Conflict: true, Error: "An import is already running. Wait for it to finish or cancel it first."})
	case errors.As(err, &bad):
		reject(http.StatusUnprocessableEntity, importPage{Error: uploadMessage(bad.Reason)})
	case errors.Is(err, importer.ErrNoRows):
		reject(http.StatusUnprocessableEntity, importPage{Error: uploadMessage(err.Error())})
	case errors.Is(err, importer.ErrUnknownImporter):
		reject(http.StatusUnprocessableEntity, importPage{Error: "This importer is not available."})
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

func uploadMessage(reason string) string {
	return "This file cannot be imported: " + reason + "."
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
	var active *importer.ActiveError
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
	case errors.Is(err, importer.ErrNothingToResume):
		reject(http.StatusUnprocessableEntity, importPage{Error: "There is nothing to resume."})
	default:
		s.fail(w, r, err, "")
	}
}
