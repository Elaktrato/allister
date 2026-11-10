package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/allister/allister/internal/backup"
)

// maxBackupBody caps a backup restore upload (512 MiB).
const maxBackupBody = 512 << 20

// backupPage is the data of the backup page. Report is set only in the
// response to a restore.
type backupPage struct {
	Error      string
	CanRestore bool
	Report     *backup.Report
	Lists      []backupList
}

// backupList is one capped row list of a restore report.
type backupList struct {
	Heading, Note string
	Count, More   int
	Rows          []backupLine
}

// backupLine is one listed backup item; every text comes from the uploaded
// file (cleaned by package backup) and is escaped by the template.
type backupLine struct {
	Title, Identity, Message string
}

func backupLines(rows []backup.Row) []backupLine {
	out := make([]backupLine, 0, len(rows))
	for _, r := range rows {
		l := backupLine{Title: r.Title, Message: r.Reason}
		if l.Title == "" {
			l.Title = fmt.Sprintf("Item %d", r.Index)
		}
		if r.PluginID != "" || r.SourceID != "" {
			l.Identity = fmt.Sprintf("%s / %s / %s %s", r.PluginID, r.Kind, r.Source, r.SourceID)
		}
		out = append(out, l)
	}
	return out
}

func (s *server) backupPage(p backupPage) backupPage {
	p.CanRestore = s.deps.DataDir != "" && s.deps.Library != nil
	if rep := p.Report; rep != nil {
		add := func(l backupList) {
			if len(l.Rows) > 0 {
				p.Lists = append(p.Lists, l)
			}
		}
		add(backupList{Heading: "Failed", Count: rep.Failed, More: rep.MoreFailed, Rows: backupLines(rep.FailedRows),
			Note: "These items were not restored because their data is not valid."})
		add(backupList{Heading: "Unknown plugin", Count: rep.UnknownPlugin, More: rep.MoreUnknown, Rows: backupLines(rep.UnknownRows),
			Note: "These items belong to plugins that are not installed and were not restored."})
		add(backupList{Heading: "Restored with notes", Count: len(rep.Notes) + rep.MoreNotes, More: rep.MoreNotes, Rows: backupLines(rep.Notes)})
		add(backupList{Heading: "Skipped", Count: rep.Skipped, More: rep.MoreSkipped, Rows: backupLines(rep.SkippedRows),
			Note: "These items already exist and were left unchanged."})
	}
	return p
}

func (s *server) getBackup(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "backup", s.backupPage(backupPage{}))
}

// getBackupDownload streams the backup zip. It is read-only.
//
// Failure handling: headers and the first bytes go out while the archive is
// still being produced. When producing it fails before anything was written
// the answer is a plain 500. After bytes were sent a status can no longer be
// changed, and returning normally would let net/http end the response
// cleanly, so the browser would save the truncated zip as a finished
// download. Instead the handler panics with http.ErrAbortHandler, the
// standard way to make net/http abort the response (the connection is reset
// and the download is marked failed) without logging a stack trace. A client
// that went away needs neither.
func (s *server) getBackupDownload(w http.ResponseWriter, r *http.Request) {
	if s.deps.Library == nil {
		http.NotFound(w, r)
		return
	}
	var plugins []backup.PluginVersion
	for _, m := range s.deps.Registry.Manifests() {
		plugins = append(plugins, backup.PluginVersion{ID: m.ID, Version: m.Version})
	}
	now := s.now()
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="allister-backup-`+now.UTC().Format("2006-01-02")+`.zip"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	cw := &countingWriter{w: w}
	err := s.backupWrite(r.Context(), cw, s.deps.Library, plugins, s.deps.CoversDir, s.appearance, now)
	if err == nil {
		return
	}
	log.Printf("backup download: %v", err)
	switch {
	case r.Context().Err() != nil:
		return // the client is gone
	case cw.n == 0:
		h.Del("Content-Disposition")
		http.Error(w, "The backup could not be created (see the log).", http.StatusInternalServerError)
	default:
		panic(http.ErrAbortHandler)
	}
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// postBackupRestore restores an uploaded backup (multipart field "file") by
// merging it into the library. One restore runs at a time (409 otherwise);
// the upload is capped (413) and spooled to a temp file in DATA_DIR/tmp that
// is removed on every path.
func (s *server) postBackupRestore(w http.ResponseWriter, r *http.Request) {
	if s.deps.DataDir == "" || s.deps.Library == nil {
		http.NotFound(w, r)
		return
	}
	refuse := func(status int, msg string) {
		s.renderStatus(w, r, status, "backup", s.backupPage(backupPage{Error: msg}))
	}
	if !s.restoreMu.TryLock() {
		refuse(http.StatusConflict, "A restore is already running. Wait for it to finish, then try again.")
		return
	}
	defer s.restoreMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBackupBody)
	path, err := s.spoolBackup(r)
	if path != "" {
		defer os.Remove(path)
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		refuse(http.StatusRequestEntityTooLarge, fmt.Sprintf("The file is larger than %d MiB.", s.maxBackupBody>>20))
		return
	case errors.Is(err, errNoBackupFile):
		refuse(http.StatusUnprocessableEntity, "Please choose the backup file (.zip) to restore.")
		return
	case err != nil:
		if r.Context().Err() != nil {
			log.Printf("backup restore: upload aborted: %v", err)
			return
		}
		s.internalError(w, r, err)
		return
	}
	if s.spoolHook != nil {
		s.spoolHook(path)
	}
	f, err := os.Open(path)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rep, err := backup.Restore(r.Context(), f, fi.Size(), backup.Options{
		Library: s.deps.Library, CoversDir: s.deps.CoversDir, Appearance: s.appearance,
		// disabled plugins' jobs wait at the queue's gate
		Enqueue: func(ctx context.Context, itemID int64, pluginID string) { s.enqueueSync(ctx, pluginID, itemID) },
	})
	var rej *backup.RejectError
	switch {
	case errors.As(err, &rej):
		log.Printf("backup restore: refused: %s", rej.Msg)
		refuse(http.StatusUnprocessableEntity, rej.Msg)
		return
	case err != nil:
		logRestore(rep, err)
		if r.Context().Err() != nil {
			return // the client is gone
		}
		s.renderStatus(w, r, http.StatusInternalServerError, "backup", s.backupPage(backupPage{Report: rep,
			Error: "The restore stopped because of an internal error (see the log). The items counted as created were restored."}))
		return
	}
	logRestore(rep, nil)
	status := http.StatusOK
	if rep.Aborted != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderStatus(w, r, status, "backup", s.backupPage(backupPage{Report: rep, Error: rep.Aborted}))
}

var errNoBackupFile = errors.New("no backup file in the upload")

// spoolBackup copies the multipart field "file" to a new temp file (0600) in
// DATA_DIR/tmp and returns its path. On error it returns "" (and removes the
// partial file) unless the path is set, in which case the caller removes it.
func (s *server) spoolBackup(r *http.Request) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", errNoBackupFile
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", errNoBackupFile
		}
		if err != nil {
			return "", err
		}
		if part.FormName() != "file" || part.FileName() == "" {
			if _, err := io.Copy(io.Discard, part); err != nil {
				return "", err
			}
			continue
		}
		dir := filepath.Join(s.deps.DataDir, "tmp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		f, err := os.CreateTemp(dir, "restore-*.zip")
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, part)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(f.Name())
			return "", err
		}
		return f.Name(), nil
	}
}

// logRestore writes the restore result to the log (texts from the file are
// already cleaned of control characters by package backup).
func logRestore(rep *backup.Report, err error) {
	if rep == nil {
		rep = &backup.Report{}
	}
	log.Printf("backup restore: created %d, skipped %d, failed %d, unknown plugin %d, ignored archive entries %d",
		rep.Created, rep.Skipped, rep.Failed, rep.UnknownPlugin, rep.Ignored)
	for _, row := range rep.FailedRows {
		log.Printf("backup restore: item %d failed (%q %q %q %q): %s", row.Index, row.PluginID, row.Kind, row.Source, row.SourceID, row.Reason)
	}
	for _, n := range rep.Appearance {
		log.Printf("backup restore: appearance: %s", n)
	}
	if rep.Aborted != "" {
		log.Printf("backup restore: %s", rep.Aborted)
	}
	if err != nil {
		log.Printf("backup restore: %v", err)
	}
}
