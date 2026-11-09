package yamtrack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/allister/allister/internal/ids"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

// KindImport is the job kind handled by Importer.Handler.
const KindImport = "import_yamtrack"

const (
	// anilistRequestsPerMinute is AniList's currently degraded rate limit.
	anilistRequestsPerMinute = 30
	// maxRetries is how often one row is retried in-process after AniList
	// answered rate limited or unavailable, before the job gives up for now.
	maxRetries = 5
	// rateLimitWait is the pause before such a retry (AniList's window is a minute).
	rateLimitWait = time.Minute
	// listCap bounds each list of rows in a Report.
	listCap = 200

	maxFilenameRunes = 200
	nowSQL           = `strftime('%Y-%m-%dT%H:%M:%SZ','now')`
)

// Import states.
const (
	statePending   = "pending"
	stateRunning   = "running"
	stateDone      = "done"
	stateFailed    = "failed"
	stateCancelled = "cancelled"
)

// Row outcomes (import_rows.outcome). Rows adjusted to pass validation are
// "imported" with the adjustment in the message.
const (
	outcomePending   = "pending"
	outcomeImported  = "imported"
	outcomeUnmatched = "unmatched"
	outcomeSkipped   = "skipped"
	outcomeFailed    = "failed"
)

var (
	// ErrNoAnimeRows: the file is a Yamtrack export but holds nothing importable.
	ErrNoAnimeRows = errors.New("the file contains no importable anime rows")
	// ErrActive is wrapped by *ActiveError: another import is pending or running.
	ErrActive = errors.New("another import is still active")
)

// ActiveError carries the id of the import that blocks a new upload.
type ActiveError struct{ ID int64 }

func (e *ActiveError) Error() string { return fmt.Sprintf("import %d is still active", e.ID) }
func (e *ActiveError) Unwrap() error { return ErrActive }

// Payload is the payload of an import_yamtrack job.
type Payload struct {
	ImportID int64 `json:"import_id"`
}

// Resolver finds an anime by its MyAnimeList id (anilist.Client).
type Resolver interface {
	AnimeByMALID(ctx context.Context, malID string) (provider.Anime, error)
}

// Queue is the part of jobs.Queue the importer needs.
type Queue interface {
	Enqueue(ctx context.Context, kind string, payload any) error
}

// Option configures an Importer.
type Option func(*Importer)

// WithClock replaces time.Now and the context-aware sleep (tests).
func WithClock(now func() time.Time, sleep func(ctx context.Context, d time.Duration) error) Option {
	return func(im *Importer) { im.now, im.sleep = now, sleep }
}

// Importer stores uploaded exports and runs the import job.
type Importer struct {
	db    *sql.DB
	store *store.Store
	res   Resolver
	queue Queue
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last time.Time // time of the last AniList request, for pacing
}

// New returns an Importer. res may be nil when only uploads and reports are used.
func New(d *sql.DB, st *store.Store, res Resolver, q Queue, opts ...Option) *Importer {
	im := &Importer{db: d, store: st, res: res, queue: q, now: time.Now, sleep: sleepCtx}
	for _, o := range opts {
		o(im)
	}
	return im
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

// Submit parses an export, stores its rows as a new import and enqueues the
// job. Nothing is stored when the file is bad (ErrBadFormat, ErrNoAnimeRows)
// or another import is active (*ActiveError).
func (im *Importer) Submit(ctx context.Context, filename string, r io.Reader) (int64, error) {
	rows, skipped, err := Parse(r)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, ErrNoAnimeRows
	}
	filename = truncate(filepath.Base(strings.ReplaceAll(filename, `\`, "/")), maxFilenameRunes)

	tx, err := im.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	defer tx.Rollback()
	var activeID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM imports WHERE state IN ('pending','running') ORDER BY id DESC LIMIT 1`).Scan(&activeID)
	if err == nil {
		return 0, &ActiveError{ID: activeID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("import: %w", err)
	}
	ignored, stored := 0, 0
	for _, s := range skipped {
		if s.Ignored {
			ignored++
		} else {
			stored++
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO imports(filename, state, total, ignored) VALUES (?,?,?,?)`,
		filename, statePending, len(rows)+stored, ignored)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO import_rows(import_id, seq, mal_id, title, outcome, message, data) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	defer stmt.Close()
	seq := 0
	for _, row := range rows {
		seq++
		data, err := json.Marshal(row)
		if err != nil {
			return 0, fmt.Errorf("import: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, id, seq, row.MALID, row.Title, outcomePending, "", string(data)); err != nil {
			return 0, fmt.Errorf("import: %w", err)
		}
	}
	for _, s := range skipped {
		if s.Ignored {
			continue
		}
		seq++
		if _, err := stmt.ExecContext(ctx, id, seq, "", s.Title, outcomeSkipped, s.Reason, ""); err != nil {
			return 0, fmt.Errorf("import: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	if err := im.queue.Enqueue(ctx, KindImport, Payload{ImportID: id}); err != nil {
		if _, uerr := im.db.ExecContext(context.WithoutCancel(ctx),
			`UPDATE imports SET state=?, finished_at=`+nowSQL+` WHERE id=?`, stateFailed, id); uerr != nil {
			log.Printf("import %d: mark failed: %v", id, uerr)
		}
		return 0, fmt.Errorf("import: enqueue: %w", err)
	}
	return id, nil
}

// Cancel marks the active import cancelled; the job stops at the next row.
// It does nothing when no import is active.
func (im *Importer) Cancel(ctx context.Context) error {
	_, err := im.db.ExecContext(ctx, `UPDATE imports SET state=?, finished_at=`+nowSQL+` WHERE state IN ('pending','running')`, stateCancelled)
	if err != nil {
		return fmt.Errorf("cancel import: %w", err)
	}
	return nil
}

// Handler is the import_yamtrack job handler. It processes the pending rows
// of the import in order, recording each outcome at once, so a re-run (after a
// crash, shutdown or ErrRetryLater) continues with the unfinished rows. On
// context cancellation it returns the context's error; when AniList stays
// unavailable it returns an error wrapping jobs.ErrRetryLater.
func (im *Importer) Handler() jobs.Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("import: bad payload: %w", err)
		}
		state, err := im.state(ctx, p.ImportID)
		if err != nil {
			return err
		}
		if state == "" || state == stateDone || state == stateFailed || state == stateCancelled {
			return nil // unknown (deleted) or already finished
		}
		if _, err := im.db.ExecContext(ctx, `UPDATE imports SET state=? WHERE id=? AND state=?`, stateRunning, p.ImportID, statePending); err != nil {
			return fmt.Errorf("import %d: %w", p.ImportID, err)
		}
		for {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("import %d: %w", p.ImportID, err)
			}
			if state, err := im.state(ctx, p.ImportID); err != nil {
				return err
			} else if state != stateRunning {
				return nil // cancelled
			}
			row, ok, err := im.nextPending(ctx, p.ImportID)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			if err := im.process(ctx, p.ImportID, row); err != nil {
				return err
			}
		}
		_, err = im.db.ExecContext(context.WithoutCancel(ctx),
			`UPDATE imports SET state=?, finished_at=`+nowSQL+` WHERE id=? AND state IN ('pending','running')`, stateDone, p.ImportID)
		if err != nil {
			return fmt.Errorf("import %d: %w", p.ImportID, err)
		}
		return nil
	}
}

func (im *Importer) state(ctx context.Context, id int64) (string, error) {
	var s string
	err := im.db.QueryRowContext(ctx, `SELECT state FROM imports WHERE id=?`, id).Scan(&s)
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

func (im *Importer) nextPending(ctx context.Context, id int64) (pendingRow, bool, error) {
	var r pendingRow
	err := im.db.QueryRowContext(ctx, `SELECT seq, data FROM import_rows WHERE import_id=? AND outcome=? ORDER BY seq LIMIT 1`,
		id, outcomePending).Scan(&r.seq, &r.data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, fmt.Errorf("import %d: %w", id, err)
	}
	return r, true, nil
}

func (im *Importer) record(ctx context.Context, importID int64, seq int, outcome string, animeID int64, msg string) error {
	var aid any
	if animeID != 0 {
		aid = animeID
	}
	_, err := im.db.ExecContext(context.WithoutCancel(ctx),
		`UPDATE import_rows SET outcome=?, anime_id=?, message=? WHERE import_id=? AND seq=?`, outcome, aid, msg, importID, seq)
	if err != nil {
		return fmt.Errorf("import %d: record row %d: %w", importID, seq, err)
	}
	return nil
}

// pace blocks until the next AniList request may be sent.
func (im *Importer) pace(ctx context.Context) error {
	const interval = time.Minute / anilistRequestsPerMinute
	im.mu.Lock()
	last := im.last
	im.mu.Unlock()
	if !last.IsZero() {
		if wait := interval - im.now().Sub(last); wait > 0 {
			if err := im.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	im.mu.Lock()
	im.last = im.now()
	im.mu.Unlock()
	return nil
}

// process handles one row. It returns an error only when the job must stop:
// context cancellation, AniList staying unavailable, or a database failure.
func (im *Importer) process(ctx context.Context, importID int64, pr pendingRow) error {
	var row Row
	if err := json.Unmarshal([]byte(pr.data), &row); err != nil {
		return im.record(ctx, importID, pr.seq, outcomeFailed, 0, "stored row is unreadable: "+err.Error())
	}
	rec := func(outcome string, animeID int64, msg string) error {
		return im.record(ctx, importID, pr.seq, outcome, animeID, msg)
	}
	if saved, err := im.store.FindByMALID(ctx, row.MALID); err != nil {
		return fmt.Errorf("import %d: %w", importID, err)
	} else if saved != nil {
		return rec(outcomeSkipped, saved.ID, "already in your list")
	}

	var anime provider.Anime
	notFound := false
	for attempt := 0; ; attempt++ {
		if err := im.pace(ctx); err != nil {
			return fmt.Errorf("import %d: %w", importID, err)
		}
		a, err := im.res.AnimeByMALID(ctx, row.MALID)
		if err == nil {
			anime = a
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("import %d: %w", importID, ctx.Err())
		}
		if errors.Is(err, provider.ErrNotFound) {
			notFound = true
			break
		}
		if errors.Is(err, provider.ErrRateLimited) || errors.Is(err, provider.ErrUnavailable) {
			if attempt >= maxRetries {
				return fmt.Errorf("import %d: MAL id %s: AniList is not answering: %w: %w", importID, row.MALID, jobs.ErrRetryLater, err)
			}
			if err := im.sleep(ctx, rateLimitWait); err != nil {
				return fmt.Errorf("import %d: %w", importID, err)
			}
			continue
		}
		return rec(outcomeFailed, 0, err.Error())
	}

	// The lookup is paid for: save and record even when shutdown has begun.
	dctx := context.WithoutCancel(ctx)
	if notFound {
		return im.saveUnmatched(dctx, importID, pr.seq, row)
	}
	if anime.MALID == "" {
		anime.MALID = row.MALID
	}
	entry, notes := buildEntry(row, anime.Episodes)
	id, err := im.store.AddToList(dctx, anime, entry)
	switch {
	case errors.Is(err, store.ErrAlreadySaved):
		if saved, ferr := im.store.FindBySource(dctx, anime.Source, anime.ID); ferr == nil && saved != nil {
			id = saved.ID
		}
		return rec(outcomeSkipped, id, "already in your list")
	case err != nil:
		return rec(outcomeFailed, 0, err.Error())
	}
	return im.finishSaved(dctx, importID, pr.seq, id, outcomeImported, strings.Join(notes, "; "))
}

func (im *Importer) saveUnmatched(ctx context.Context, importID int64, seq int, row Row) error {
	manualID, err := ids.NewManualID()
	if err != nil {
		return im.record(ctx, importID, seq, outcomeFailed, 0, err.Error())
	}
	a := provider.Anime{
		SearchResult: provider.SearchResult{
			Source: provider.Manual, ID: manualID,
			Titles: []provider.Title{{Lang: "ja-latn", Text: row.Title}},
		},
		MALID: row.MALID,
	}
	if strings.HasPrefix(row.ImageURL, "http://") || strings.HasPrefix(row.ImageURL, "https://") {
		a.CoverURL = row.ImageURL
	}
	entry, notes := buildEntry(row, nil)
	id, err := im.store.AddToList(ctx, a, entry, store.WithSyncState("pending"))
	switch {
	case errors.Is(err, store.ErrAlreadySaved):
		return im.record(ctx, importID, seq, outcomeSkipped, 0, "already in your list")
	case err != nil:
		return im.record(ctx, importID, seq, outcomeFailed, 0, err.Error())
	}
	msg := strings.Join(append([]string{"not found on AniList"}, notes...), "; ")
	return im.finishSaved(ctx, importID, seq, id, outcomeUnmatched, msg)
}

// finishSaved enqueues the sync job (cover, cast) for a saved anime and
// records the row.
func (im *Importer) finishSaved(ctx context.Context, importID int64, seq int, animeID int64, outcome, msg string) error {
	if err := im.queue.Enqueue(ctx, jobs.KindSyncAnime, jobs.SyncPayload{AnimeID: animeID}); err != nil {
		note := "sync not queued: " + err.Error()
		if msg != "" {
			note = msg + "; " + note
		}
		msg = note
	}
	return im.record(ctx, importID, seq, outcome, animeID, msg)
}

// buildEntry maps a row to a list entry that passes store validation:
// progress above a known episode total is clamped, and a finish date before
// the start date drops both dates. notes describes the adjustments.
func buildEntry(row Row, episodes *int) (store.ListEntry, []string) {
	e := store.ListEntry{
		Status: row.Status, Rating: row.Rating, Progress: row.Progress,
		StartedOn: row.StartedOn, FinishedOn: row.FinishedOn, Comment: row.Comment,
	}
	var notes []string
	if episodes != nil && e.Progress > *episodes {
		e.Progress = *episodes
		notes = append(notes, fmt.Sprintf("progress clamped to %d", *episodes))
	}
	if e.StartedOn != nil && e.FinishedOn != nil && e.FinishedOn.Before(*e.StartedOn) {
		e.StartedOn, e.FinishedOn = nil, nil
		notes = append(notes, "dates dropped (finish date was before start date)")
	}
	return e, notes
}
