// Package importer runs imports generically for every plugin: Submit parses
// an uploaded file ONCE through a plugin importer and stores its rows, and a
// background job (KindRun) resolves each row through the importer's
// ResolveVia source (Lookup by external id, paced by the source's
// RequestsPerMinute) and saves it to the list through the library.
//
// Row outcomes: pending (not processed yet), imported (also for rows that
// were adjusted to pass validation; the adjustment is in the message),
// unmatched (not known to the source, saved as a manual item that keeps its
// external id), skipped (already in the list, or not importable) and failed.
// Every outcome is recorded at once, so an interrupted job continues with the
// unfinished rows. One import is active (pending or running) at a time.
package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/allister/allister/internal/ids"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// KindRun is the job kind handled by Runner.Handler.
const KindRun = "import_run"

const (
	// maxRetries is how often one row is retried in-process after the source
	// answered rate limited or unavailable, before the job gives up for now.
	maxRetries = 5
	// rateLimitWait is the pause before such a retry (rate limit windows are
	// usually a minute).
	rateLimitWait = time.Minute
	// listCap bounds each list of rows in a Report.
	listCap = 200

	maxFilenameRunes = 200
	maxTitleRunes    = 300
	maxNoteRunes     = 500
	manualSource     = "manual"
	nowSQL           = `strftime('%Y-%m-%dT%H:%M:%SZ','now')`

	// legacyNote is the note of an import made by the single-plugin importer
	// of an older version (importer_id ''): its rows cannot be read any more.
	legacyNote = "created by an older version; upload the file again"
)

// Import states.
const (
	statePending   = "pending"
	stateRunning   = "running"
	stateDone      = "done"
	stateFailed    = "failed"
	stateCancelled = "cancelled"
)

// Row outcomes (import_rows.outcome).
const (
	outcomePending   = "pending"
	outcomeImported  = "imported"
	outcomeUnmatched = "unmatched"
	outcomeSkipped   = "skipped"
	outcomeFailed    = "failed"
	outcomeUpdated   = "updated"
)

var (
	// ErrBadFile is matched by *BadFileError: the importer said the file is
	// not in its format (plugin.ErrBadInput).
	ErrBadFile = errors.New("the file is not in the importer's format")
	// ErrNoRows is matched by *NoRowsError: the file holds nothing importable.
	ErrNoRows = errors.New("the file contains no importable rows")
	// ErrUnknownImporter: the plugin is not installed or not enabled, or it
	// does not declare the importer.
	ErrUnknownImporter = errors.New("unknown or disabled importer")
	// ErrSourceMissing matches ErrUnknownImporter too: the importer names a
	// ResolveVia source that its plugin does not provide.
	ErrSourceMissing = fmt.Errorf("%w: its source is not provided by the plugin", ErrUnknownImporter)
	// ErrNoLookup matches ErrUnknownImporter too: the importer's ResolveVia
	// source does not support lookup (plugin.Validate refuses such a
	// manifest; the runner checks again on its own).
	ErrNoLookup = fmt.Errorf("%w: its source cannot look up external ids", ErrUnknownImporter)
	// ErrActive is wrapped by *ActiveError: another import is pending or running.
	ErrActive = errors.New("another import is still active")
	// ErrNothingToResume: the latest import is not failed or cancelled, has no
	// pending rows, or was made by an older version.
	ErrNothingToResume = errors.New("there is nothing to resume")

	// errStopped: the import was cancelled while a row was being retried.
	errStopped = errors.New("import stopped")
)

// BadFileError is the importer's reason why a file is not in its format. It
// matches ErrBadFile and unwraps to the importer's error.
type BadFileError struct {
	// Reason is a single line of at most 300 characters taken from the
	// importer's error (without the "plugin: bad input" prefix); it is plugin
	// text and must be escaped when shown.
	Reason string
	Err    error
}

func (e *BadFileError) Error() string        { return e.Reason }
func (e *BadFileError) Unwrap() error        { return e.Err }
func (e *BadFileError) Is(target error) bool { return target == ErrBadFile }

// NoRowsError names what the file lacks ("books": the kind's plural
// label, lower-cased, or the plugin's name for a plugin with several kinds).
type NoRowsError struct{ What string }

func (e *NoRowsError) Error() string {
	return "the file contains no importable " + e.What + " rows"
}
func (e *NoRowsError) Is(target error) bool { return target == ErrNoRows }

// ActiveError carries the id of the import that blocks a new upload.
type ActiveError struct{ ID int64 }

func (e *ActiveError) Error() string { return fmt.Sprintf("import %d is still active", e.ID) }
func (e *ActiveError) Unwrap() error { return ErrActive }

// Payload is the payload of an import_run job. PluginID lets the queue's
// plugin gate pause the job while the plugin is disabled (jobs.WithPluginGate).
type Payload struct {
	ImportID int64  `json:"import_id"`
	PluginID string `json:"plugin_id"`
}

// Queue is the part of jobs.Queue the runner needs.
type Queue interface {
	Enqueue(ctx context.Context, kind string, payload any) error
}

// Deps are the runner's dependencies.
type Deps struct {
	DB       *sql.DB
	Library  *library.Library
	Registry *plugin.Registry
	Queue    Queue
	// SyncJobs maps a plugin id to the sync job of a newly saved item, like
	// web.Deps.SyncJobs (cmd/allister builds one map for both). A plugin
	// without an entry gets no sync job.
	SyncJobs map[string]func(itemID int64) (kind string, payload any)
	// Now and Sleep replace time.Now and a context-aware sleep (tests); nil
	// gets the real ones.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// Logf logs problems that cannot be returned; nil gets log.Printf.
	Logf func(format string, args ...any)
}

// Runner stores uploaded files as imports and runs the import job.
type Runner struct {
	d     Deps
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	logf  func(format string, args ...any)

	mu   sync.Mutex
	last map[string]time.Time // per plugin/source: time of the last request, for pacing

	// opMu serialises Submit, Resume and reconcile: between committing an
	// import and enqueuing its job the import has no job, and reconcile must
	// not mistake that for a stalled import.
	opMu sync.Mutex
}

// New returns a Runner.
func New(d Deps) *Runner {
	r := &Runner{d: d, now: d.Now, sleep: d.Sleep, logf: d.Logf, last: map[string]time.Time{}}
	if r.now == nil {
		r.now = time.Now
	}
	if r.sleep == nil {
		r.sleep = sleepCtx
	}
	if r.logf == nil {
		r.logf = log.Printf
	}
	return r
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// sourceRef is one lookup source of an importer.
type sourceRef struct {
	source plugin.Source
	info   plugin.SourceInfo
}

// importerRef is a resolved importer of an installed plugin.
type importerRef struct {
	plugin   plugin.Plugin
	manifest plugin.Manifest
	info     plugin.ImporterInfo
	// source and srcInfo are the primary ResolveVia source; source is nil when
	// the importer resolves through none (or the plugin does not provide it).
	source   plugin.Source
	srcInfo  plugin.SourceInfo
	fallback []sourceRef
}

// resolve finds the importer of an installed plugin (enabled or not).
func (r *Runner) resolve(pluginID, importerID string) (*importerRef, bool) {
	m, ok := r.d.Registry.Manifest(pluginID)
	if !ok {
		return nil, false
	}
	p, ok := r.d.Registry.Get(pluginID)
	if !ok {
		return nil, false
	}
	ref := &importerRef{plugin: p, manifest: m}
	found := false
	for _, im := range m.Importers {
		if im.ID == importerID {
			ref.info, found = im, true
			break
		}
	}
	if !found {
		return nil, false
	}
	if ref.info.ResolveVia != "" {
		for _, s := range m.Sources {
			if s.ID == ref.info.ResolveVia {
				ref.srcInfo = s
				ref.source = p.Source(s.ID)
			}
		}
	}
	for _, id := range ref.info.ResolveViaFallback {
		for _, s := range m.Sources {
			if s.ID == id {
				ref.fallback = append(ref.fallback, sourceRef{source: p.Source(s.ID), info: s})
				break
			}
		}
	}
	return ref, true
}

// sourceRefs is the primary source followed by its fallbacks.
func (ref *importerRef) sourceRefs() []sourceRef {
	out := []sourceRef{{source: ref.source, info: ref.srcInfo}}
	return append(out, ref.fallback...)
}

// sourceNames names the resolution sources for an unmatched-row reason.
func (ref *importerRef) sourceNames() string {
	var names []string
	for _, sr := range ref.sourceRefs() {
		if sr.info.Name != "" {
			names = append(names, sr.info.Name)
		}
	}
	return strings.Join(names, " or ")
}

func (ref *importerRef) kind(key string) (plugin.KindDef, bool) {
	for _, k := range ref.manifest.Kinds {
		if k.Key == key {
			return k, true
		}
	}
	return plugin.KindDef{}, false
}

// usable reports why an importer that resolves through a source cannot run:
// ErrSourceMissing (the plugin does not provide the source) or ErrNoLookup
// (the source cannot look up). nil for a usable or manual-only importer.
func (ref *importerRef) usable() error {
	if ref.info.ResolveVia == "" {
		return nil
	}
	for _, sr := range ref.sourceRefs() {
		if sr.source == nil {
			return ErrSourceMissing
		}
		if !sr.info.SupportsLookup {
			return ErrNoLookup
		}
	}
	return nil
}

// idLabel names the importer's external id type (its label, else the type).
func (ref *importerRef) idLabel() string {
	if ref.info.ExternalIDLabel != "" {
		return ref.info.ExternalIDLabel
	}
	return ref.info.ExternalIDType
}

// noRowsWhat is the NoRowsError subject of the plugin.
func (ref *importerRef) noRowsWhat() string {
	if len(ref.manifest.Kinds) == 1 && ref.manifest.Kinds[0].LabelPlural != "" {
		return strings.ToLower(ref.manifest.Kinds[0].LabelPlural)
	}
	return strings.ToLower(ref.manifest.Name)
}

type queryer interface {
	execer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// reconcile marks pending or running imports that have no live import_run
// job (pending or running, matched by payload import_id) as failed: the
// queue gave up on the job (or it is gone) and nothing else will ever finish
// the import. The job's last_error becomes the import's note. Callers hold
// opMu.
func reconcile(ctx context.Context, x execer) error {
	_, err := x.ExecContext(ctx, `UPDATE imports SET state=?, finished_at=`+nowSQL+`,
		note = 'Stopped: ' || COALESCE((SELECT NULLIF(j.last_error, '') FROM jobs j
			WHERE j.kind = ? AND CASE WHEN json_valid(j.payload_json) THEN json_extract(j.payload_json, '$.import_id') END = imports.id
			ORDER BY j.id DESC LIMIT 1), 'the import job is no longer queued')
		WHERE state IN ('pending','running') AND NOT EXISTS (SELECT 1 FROM jobs j
			WHERE j.kind = ? AND j.state IN ('pending','running')
			AND CASE WHEN json_valid(j.payload_json) THEN json_extract(j.payload_json, '$.import_id') END = imports.id)`, stateFailed, KindRun, KindRun)
	if err != nil {
		return fmt.Errorf("reconcile imports: %w", err)
	}
	return nil
}

// Reconcile fails stalled imports (see reconcile); Latest does this itself.
func (r *Runner) Reconcile(ctx context.Context) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	return reconcile(ctx, r.d.DB)
}

// activeCheck reconciles, then returns *ActiveError when an import is still
// pending or running. Callers hold opMu and pass their transaction.
func (r *Runner) activeCheck(ctx context.Context, tx queryer) error {
	if err := reconcile(ctx, tx); err != nil {
		return err
	}
	var activeID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM imports WHERE state IN ('pending','running') ORDER BY id DESC LIMIT 1`).Scan(&activeID)
	if err == nil {
		return &ActiveError{ID: activeID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("import: %w", err)
	}
	return nil
}

// Resume puts the latest import back to pending when it is failed or
// cancelled and still has pending rows, and enqueues a new job for it. It
// returns *ActiveError while another import is active and ErrNothingToResume
// when there is nothing to continue (also for an import of an older version).
func (r *Runner) Resume(ctx context.Context) (int64, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	tx, err := r.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("resume import: %w", err)
	}
	defer tx.Rollback()
	if err := r.activeCheck(ctx, tx); err != nil {
		return 0, err
	}
	var id int64
	var state, pluginID, importerID string
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.state, i.plugin_id, i.importer_id,
		(SELECT COUNT(*) FROM import_rows r WHERE r.import_id = i.id AND r.outcome = ?)
		FROM imports i ORDER BY i.id DESC LIMIT 1`, outcomePending).Scan(&id, &state, &pluginID, &importerID, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNothingToResume
	}
	if err != nil {
		return 0, fmt.Errorf("resume import: %w", err)
	}
	if (state != stateFailed && state != stateCancelled) || pending == 0 || importerID == "" {
		return 0, ErrNothingToResume
	}
	if _, err := tx.ExecContext(ctx, `UPDATE imports SET state=?, finished_at=NULL, note='' WHERE id=?`, statePending, id); err != nil {
		return 0, fmt.Errorf("resume import: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("resume import: %w", err)
	}
	if err := r.d.Queue.Enqueue(ctx, KindRun, Payload{ImportID: id, PluginID: pluginID}); err != nil {
		if _, uerr := r.d.DB.ExecContext(context.WithoutCancel(ctx),
			`UPDATE imports SET state=?, finished_at=`+nowSQL+`, note=? WHERE id=?`, stateFailed, "Stopped: could not queue the job: "+err.Error(), id); uerr != nil {
			r.logf("import %d: mark failed: %v", id, uerr)
		}
		return 0, fmt.Errorf("resume import: enqueue: %w", err)
	}
	return id, nil
}

// setNote sets (or with "" clears) the status line of a running import.
func (r *Runner) setNote(ctx context.Context, id int64, note string) {
	if _, err := r.d.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE imports SET note=? WHERE id=? AND state=?`, note, id, stateRunning); err != nil {
		r.logf("import %d: set note: %v", id, err)
	}
}

// Cancel marks the active import cancelled; the job stops at the next row.
// It does nothing when no import is active.
func (r *Runner) Cancel(ctx context.Context) error {
	_, err := r.d.DB.ExecContext(ctx, `UPDATE imports SET state=?, finished_at=`+nowSQL+`, note='' WHERE state IN ('pending','running')`, stateCancelled)
	if err != nil {
		return fmt.Errorf("cancel import: %w", err)
	}
	return nil
}

// fail ends an import that cannot run with a note.
func (r *Runner) fail(ctx context.Context, id int64, note string) error {
	_, err := r.d.DB.ExecContext(context.WithoutCancel(ctx),
		`UPDATE imports SET state=?, finished_at=`+nowSQL+`, note=? WHERE id=? AND state IN ('pending','running')`, stateFailed, note, id)
	if err != nil {
		return fmt.Errorf("import %d: %w", id, err)
	}
	return nil
}

// Handler is the import_run job handler. It processes the pending rows of
// the import in order, recording each outcome at once, so a re-run (after a
// crash, shutdown, ErrRetryLater or a pause) continues with the unfinished
// rows. On context cancellation it returns the context's error; when the
// source stays unavailable it returns an error wrapping jobs.ErrRetryLater;
// when its plugin is disabled while it runs it pauses with jobs.ErrPaused
// (before the next row; an import whose last row is done finishes instead).
// An import of an older version, or of an importer that is no longer
// installed, is failed with a note.
func (r *Runner) Handler() jobs.Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("import: bad payload: %w", err)
		}
		var state, pluginID, importerID, mode string
		err := r.d.DB.QueryRowContext(ctx, `SELECT state, plugin_id, importer_id, mode FROM imports WHERE id=?`, p.ImportID).
			Scan(&state, &pluginID, &importerID, &mode)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // deleted
		}
		if err != nil {
			return fmt.Errorf("import %d: %w", p.ImportID, err)
		}
		if state == stateDone || state == stateFailed || state == stateCancelled {
			return nil // already finished
		}
		if importerID == "" {
			return r.fail(ctx, p.ImportID, legacyNote)
		}
		ref, ok := r.resolve(pluginID, importerID)
		if !ok {
			return r.fail(ctx, p.ImportID, "Stopped: the importer is no longer installed")
		}
		switch err := ref.usable(); {
		case errors.Is(err, ErrSourceMissing):
			return r.fail(ctx, p.ImportID, "Stopped: the importer's source is no longer provided by its plugin")
		case err != nil:
			return r.fail(ctx, p.ImportID, "Stopped: the importer's source cannot look up external ids")
		}
		if _, err := r.d.DB.ExecContext(ctx, `UPDATE imports SET state=?, note='' WHERE id=? AND state=?`, stateRunning, p.ImportID, statePending); err != nil {
			return fmt.Errorf("import %d: %w", p.ImportID, err)
		}
		for {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("import %d: %w", p.ImportID, err)
			}
			if state, err := r.state(ctx, p.ImportID); err != nil {
				return err
			} else if state != stateRunning {
				return nil // cancelled
			}
			row, ok, err := r.nextPending(ctx, p.ImportID)
			if err != nil {
				return err
			}
			if !ok {
				break // no rows left: finish even when the plugin is off
			}
			// Switched off mid-run with rows left: stop before the next row.
			// The queue keeps the job pending (its plugin gate holds it while
			// the plugin is off) and the import stays running, so enabling
			// the plugin resumes it with the unfinished rows.
			if !r.d.Registry.IsEnabled(pluginID) {
				return fmt.Errorf("import %d: plugin %q is disabled, paused: %w", p.ImportID, pluginID, jobs.ErrPaused)
			}
			if err := r.process(ctx, ref, p.ImportID, mode, row); errors.Is(err, errStopped) {
				return nil // cancelled while retrying
			} else if err != nil {
				return err
			}
		}
		_, err = r.d.DB.ExecContext(context.WithoutCancel(ctx),
			`UPDATE imports SET state=?, finished_at=`+nowSQL+`, note='' WHERE id=? AND state IN ('pending','running')`, stateDone, p.ImportID)
		if err != nil {
			return fmt.Errorf("import %d: %w", p.ImportID, err)
		}
		return nil
	}
}

func (r *Runner) state(ctx context.Context, id int64) (string, error) {
	var s string
	err := r.d.DB.QueryRowContext(ctx, `SELECT state FROM imports WHERE id=?`, id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("import %d: %w", id, err)
	}
	return s, nil
}

type pendingRow struct {
	seq  int
	data string
}

func (r *Runner) nextPending(ctx context.Context, id int64) (pendingRow, bool, error) {
	var p pendingRow
	err := r.d.DB.QueryRowContext(ctx, `SELECT seq, data FROM import_rows WHERE import_id=? AND outcome=? ORDER BY seq LIMIT 1`,
		id, outcomePending).Scan(&p.seq, &p.data)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("import %d: %w", id, err)
	}
	return p, true, nil
}

func (r *Runner) record(ctx context.Context, importID int64, seq int, outcome string, itemID int64, msg string) error {
	var iid any
	if itemID != 0 {
		iid = itemID
	}
	_, err := r.d.DB.ExecContext(context.WithoutCancel(ctx),
		`UPDATE import_rows SET outcome=?, item_id=?, message=? WHERE import_id=? AND seq=?`, outcome, iid, msg, importID, seq)
	if err != nil {
		return fmt.Errorf("import %d: record row %d: %w", importID, seq, err)
	}
	return nil
}

// pace blocks until the next request to the source may be sent. The first
// request to a source does not wait.
func (r *Runner) pace(ctx context.Context, ref *importerRef, si plugin.SourceInfo) error {
	key := ref.manifest.ID + "/" + si.ID
	interval := time.Minute / time.Duration(max(si.RequestsPerMinute, 1))
	r.mu.Lock()
	last := r.last[key]
	r.mu.Unlock()
	if !last.IsZero() {
		if wait := interval - r.now().Sub(last); wait > 0 {
			if err := r.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	r.mu.Lock()
	r.last[key] = r.now()
	r.mu.Unlock()
	return nil
}

// rowCtx is one row being processed.
type rowCtx struct {
	ref      *importerRef
	importID int64
	seq      int
	mode     string // how an existing entry is treated (Mode*)
	entry    plugin.ParsedEntry
	kind     plugin.KindDef
	idType   string
	value    string
}

func (rc *rowCtx) pluginID() string { return rc.ref.manifest.ID }

// process handles one row. It returns an error only when the job must stop:
// context cancellation, the source staying unavailable, or a database failure.
func (r *Runner) process(ctx context.Context, ref *importerRef, importID int64, mode string, pr pendingRow) error {
	rc := &rowCtx{ref: ref, importID: importID, seq: pr.seq, idType: ref.info.ExternalIDType, mode: normalizeMode(mode)}
	if err := json.Unmarshal([]byte(pr.data), &rc.entry); err != nil {
		return r.record(ctx, importID, pr.seq, outcomeFailed, 0, "stored row is unreadable: "+err.Error())
	}
	kind, ok := ref.kind(rc.entry.Kind)
	if !ok {
		return r.record(ctx, importID, pr.seq, outcomeSkipped, 0, fmt.Sprintf("unknown kind %q", plugin.SingleLine(rc.entry.Kind, 40)))
	}
	rc.kind = kind
	if rc.idType != "" {
		rc.value = plugin.SingleLine(rc.entry.ExternalIDs[rc.idType], maxTitleRunes)
	}
	if rc.value == "" { // no declared id: a manual-only row, or an older stored row
		if ref.info.ManualRows && rc.entry.Title != "" {
			return r.saveManualRow(context.WithoutCancel(ctx), rc, "saved as a manual entry (no "+ref.idLabel()+")")
		}
		return r.record(ctx, importID, pr.seq, outcomeSkipped, 0, noIDMessage(ref))
	}
	if saved, err := r.d.Library.ItemByExternalID(ctx, rc.pluginID(), kind.Key, rc.idType, rc.value); err != nil {
		return fmt.Errorf("import %d: %w", importID, err)
	} else if saved != nil {
		return r.handleSaved(ctx, rc, saved)
	}
	if ref.source == nil {
		return r.saveUnmatched(context.WithoutCancel(ctx), rc, "saved as a manual entry")
	}

	var data plugin.ItemData
	found := false
	for _, sr := range ref.sourceRefs() {
		name := sr.info.Name
		for attempt := 0; ; attempt++ {
			if attempt > 0 { // a Cancel during the wait must stop the retries
				if state, err := r.state(ctx, importID); err != nil {
					return err
				} else if state != stateRunning {
					return errStopped
				}
			}
			if err := r.pace(ctx, ref, sr.info); err != nil {
				return fmt.Errorf("import %d: %w", importID, err)
			}
			d, err := sr.source.Lookup(ctx, kind.Key, rc.idType, rc.value)
			if err == nil {
				data, found = d, true
				break
			}
			if ctx.Err() != nil {
				return fmt.Errorf("import %d: %w", importID, ctx.Err())
			}
			if errors.Is(err, plugin.ErrBadResponse) { // a retry would get the same answer
				return r.record(ctx, importID, pr.seq, outcomeFailed, 0, err.Error())
			}
			if errors.Is(err, plugin.ErrNotFound) {
				break // try the next source
			}
			if errors.Is(err, plugin.ErrRateLimited) || errors.Is(err, plugin.ErrUnavailable) {
				if attempt >= maxRetries {
					r.setNote(ctx, importID, name+" is not answering, the import will be retried later")
					return fmt.Errorf("import %d: %s %s: %s is not answering: %w: %w", importID, rc.idType, rc.value, name, jobs.ErrRetryLater, err)
				}
				r.setNote(ctx, importID, fmt.Sprintf("Waiting for %s (retry %d/%d)", name, attempt+1, maxRetries))
				if err := r.sleep(ctx, rateLimitWait); err != nil {
					return fmt.Errorf("import %d: %w", importID, err)
				}
				continue
			}
			return r.record(ctx, importID, pr.seq, outcomeFailed, 0, err.Error())
		}
		if found {
			break
		}
	}

	// The lookup is paid for: save and record even when shutdown has begun.
	dctx := context.WithoutCancel(ctx)
	r.setNote(dctx, importID, "")
	if !found {
		return r.saveUnmatched(dctx, rc, "not found on "+ref.sourceNames())
	}
	if data.Kind == "" {
		data.Kind = kind.Key
	}
	if data.ExternalIDs[rc.idType] == "" {
		ext := make(map[string]string, len(data.ExternalIDs)+1)
		for k, v := range data.ExternalIDs {
			ext[k] = v
		}
		ext[rc.idType] = rc.value
		data.ExternalIDs = ext
	}
	entry, notes := buildEntry(kind, &library.Item{ProgressKeys: kind.Progress.Defaults, Fields: data.Fields}, rc.entry)
	id, err := r.d.Library.AddToList(dctx, rc.pluginID(), kind.Key, data, entry)
	var exists *library.AlreadySavedError
	switch {
	case errors.As(err, &exists):
		saved, ferr := r.d.Library.Item(dctx, exists.ExistingID)
		if ferr != nil {
			return fmt.Errorf("import %d: %w", importID, ferr)
		}
		if saved == nil {
			return r.record(dctx, importID, pr.seq, outcomeSkipped, 0, "already in your list")
		}
		return r.handleSaved(dctx, rc, saved)
	case errors.Is(err, library.ErrAlreadySaved):
		return r.record(dctx, importID, pr.seq, outcomeSkipped, 0, "already in your list")
	case err != nil:
		return r.record(dctx, importID, pr.seq, outcomeFailed, 0, err.Error())
	}
	return r.finishSaved(dctx, rc, id, outcomeImported, joinNotes(notes...))
}

// noIDMessage is the reason of a row without the importer's external id.
func noIDMessage(ref *importerRef) string {
	if label := ref.idLabel(); label != "" {
		return "no " + label + " id in the row, it cannot be matched"
	}
	return "no external id in the row, it cannot be matched"
}

// syncJob enqueues the sync job of a saved item; it returns a note when that
// failed.
func (r *Runner) syncJob(ctx context.Context, pluginID string, itemID int64) string {
	mk := r.d.SyncJobs[pluginID]
	if mk == nil {
		return ""
	}
	kind, payload := mk(itemID)
	if err := r.d.Queue.Enqueue(ctx, kind, payload); err != nil {
		return "sync not queued: " + err.Error()
	}
	return ""
}

// handleSaved deals with a row whose item is already stored. With an import
// mode other than skip, an existing entry is updated from the row (only when
// the row is newer for ModeNewer); otherwise a stored entry is skipped. When
// the item has no entry (removed, or never created) the entry is created from
// the row. Either way an item whose sync never started (state pending, e.g. a
// crash before its job was enqueued) gets a sync job.
func (r *Runner) handleSaved(ctx context.Context, rc *rowCtx, saved *library.Item) error {
	dctx := context.WithoutCancel(ctx)
	le, err := r.d.Library.Entry(dctx, saved.ID)
	if err != nil {
		return fmt.Errorf("import %d: %w", rc.importID, err)
	}
	syncNote := ""
	if saved.SyncState == "pending" {
		syncNote = r.syncJob(dctx, rc.pluginID(), saved.ID)
	}
	if le != nil {
		// a "newer" row without a source timestamp cannot prove it is newer
		if rc.mode == ModeAlways || (rc.mode == ModeNewer && rc.entry.UpdatedAt != nil) {
			newerThan := rc.entry.UpdatedAt
			if rc.mode == ModeAlways {
				newerThan = nil
			}
			upd := library.Entry{Status: library.Status(rc.entry.Status), Rating: rc.entry.Rating, Progress: rc.entry.Progress}
			updated, uerr := r.d.Library.ApplyImport(dctx, saved.ID, upd, newerThan)
			if uerr != nil {
				return r.record(dctx, rc.importID, rc.seq, outcomeFailed, saved.ID, uerr.Error())
			}
			if updated {
				return r.record(dctx, rc.importID, rc.seq, outcomeUpdated, saved.ID, joinNotes("updated from the source", syncNote))
			}
		}
		return r.record(dctx, rc.importID, rc.seq, outcomeSkipped, saved.ID, joinNotes("already in your list", syncNote))
	}
	entry, notes := buildEntry(rc.kind, saved, rc.entry)
	entry.ItemID = saved.ID
	err = r.d.Library.CreateEntry(dctx, entry)
	switch {
	case errors.Is(err, library.ErrEntryExists):
		return r.record(dctx, rc.importID, rc.seq, outcomeSkipped, saved.ID, joinNotes("already in your list", syncNote))
	case err != nil:
		return r.record(dctx, rc.importID, rc.seq, outcomeFailed, saved.ID, err.Error())
	}
	label := strings.ToLower(rc.kind.Label)
	return r.record(dctx, rc.importID, rc.seq, outcomeImported, saved.ID,
		joinNotes(append([]string{"added to your list (" + label + " was already saved)"}, append(notes, syncNote)...)...))
}

func joinNotes(notes ...string) string {
	var out []string
	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, "; ")
}

// saveUnmatched keeps a row the source does not know as a manual item.
func (r *Runner) saveUnmatched(ctx context.Context, rc *rowCtx, reason string) error {
	return r.saveManual(ctx, rc, reason, outcomeUnmatched)
}

// saveManualRow keeps a row that has no source id as a normal imported
// manual item.
func (r *Runner) saveManualRow(ctx context.Context, rc *rowCtx, reason string) error {
	return r.saveManual(ctx, rc, reason, outcomeImported)
}

// saveManual stores a manual item with the row's title, all external ids and
// the row fields (which also supply progress totals), then records the row.
func (r *Runner) saveManual(ctx context.Context, rc *rowCtx, reason, outcome string) error {
	title := rc.entry.Title
	if title == "" && rc.value != "" {
		title = rc.ref.idLabel() + " #" + rc.value
	}
	if title == "" {
		return r.record(ctx, rc.importID, rc.seq, outcomeSkipped, 0, "row has no title")
	}
	manualID, err := ids.NewManualID()
	if err != nil {
		return r.record(ctx, rc.importID, rc.seq, outcomeFailed, 0, err.Error())
	}
	lang := rc.ref.info.FallbackTitleLang
	if lang == "" && len(rc.kind.TitleLanguages) > 0 {
		lang = rc.kind.TitleLanguages[0]
	}
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: manualSource, ID: manualID, Kind: rc.kind.Key,
			Titles: []plugin.Title{{Lang: lang, Text: title}}},
	}
	if len(rc.entry.ExternalIDs) > 0 {
		d.ExternalIDs = make(map[string]string, len(rc.entry.ExternalIDs))
		for k, v := range rc.entry.ExternalIDs {
			d.ExternalIDs[k] = v
		}
	}
	if len(rc.entry.Fields) > 0 {
		d.Fields = make(map[string]plugin.FieldValue, len(rc.entry.Fields))
		for k, v := range rc.entry.Fields {
			d.Fields[k] = v
		}
	}
	if u := plugin.HTTPSURL(rc.entry.CoverURL); u != "" {
		d.CoverURL = u
	}
	entry, notes := buildEntry(rc.kind, &library.Item{ProgressKeys: rc.kind.Progress.Defaults, Fields: d.Fields}, rc.entry)
	id, err := r.d.Library.AddToList(ctx, rc.pluginID(), rc.kind.Key, d, entry, library.WithSyncState("pending"))
	switch {
	case errors.Is(err, library.ErrAlreadySaved):
		return r.record(ctx, rc.importID, rc.seq, outcomeSkipped, 0, "already in your list")
	case err != nil:
		return r.record(ctx, rc.importID, rc.seq, outcomeFailed, 0, err.Error())
	}
	return r.finishSaved(ctx, rc, id, outcome, joinNotes(append([]string{reason}, notes...)...))
}

// finishSaved enqueues the sync job (cover, credits) for a saved item and
// records the row.
func (r *Runner) finishSaved(ctx context.Context, rc *rowCtx, itemID int64, outcome, msg string) error {
	return r.record(ctx, rc.importID, rc.seq, outcome, itemID, joinNotes(msg, r.syncJob(ctx, rc.pluginID(), itemID)))
}
