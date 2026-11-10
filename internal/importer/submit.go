package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// Import modes: how an import treats entries that already exist.
const (
	// ModeSkip leaves existing entries untouched (the default).
	ModeSkip = "skip"
	// ModeNewer updates an existing entry only when the source entry was
	// modified after the local one.
	ModeNewer = "newer"
	// ModeAlways updates an existing entry unconditionally.
	ModeAlways = "always"
)

// normalizeMode maps anything unknown onto the safe default.
func normalizeMode(mode string) string {
	switch mode {
	case ModeNewer, ModeAlways:
		return mode
	}
	return ModeSkip
}

// storedRow is one row as written by Submit.
type storedRow struct {
	idType, id, title, outcome, message, data string
}

// Submit parses the file through the plugin's importer, stores its rows (the
// parsed entries as pending; the importer's skipped rows, entries of an
// unknown kind and entries without the importer's external id as skipped
// with their reasons) as a new import with the skip mode and enqueues the
// job. Nothing is stored when the plugin or importer is unknown or disabled
// (ErrUnknownImporter), the importer's ResolveVia source is not provided by
// the plugin (ErrSourceMissing) or cannot look up (ErrNoLookup; only an empty
// ResolveVia means manual-only), the file is not in the format
// (*BadFileError), holds nothing importable (*NoRowsError) or another import
// is active (*ActiveError).
func (r *Runner) Submit(ctx context.Context, pluginID, importerID, filename string, in io.Reader) (int64, error) {
	return r.submitMode(ctx, pluginID, importerID, filename, ModeSkip, in)
}

// SubmitMode is Submit with an import mode for existing entries
// (ModeSkip, ModeNewer, ModeAlways).
func (r *Runner) SubmitMode(ctx context.Context, pluginID, importerID, filename, mode string, in io.Reader) (int64, error) {
	return r.submitMode(ctx, pluginID, importerID, filename, mode, in)
}

func (r *Runner) submitMode(ctx context.Context, pluginID, importerID, filename, mode string, in io.Reader) (int64, error) {
	ref, ok := r.resolve(pluginID, importerID)
	if !ok || !r.d.Registry.IsEnabled(pluginID) {
		return 0, ErrUnknownImporter
	}
	imp := ref.plugin.Importer(importerID)
	if imp == nil {
		return 0, ErrUnknownImporter
	}
	if err := ref.usable(); err != nil {
		return 0, err
	}
	res, err := imp.Parse(ctx, in)
	if err != nil {
		if errors.Is(err, plugin.ErrBadInput) {
			reason := strings.TrimPrefix(err.Error(), plugin.ErrBadInput.Error()+": ")
			return 0, &BadFileError{Reason: plugin.SingleLine(reason, maxTitleRunes), Err: err}
		}
		return 0, fmt.Errorf("import: %w", err)
	}
	filename = truncate(filepath.Base(strings.ReplaceAll(filename, `\`, "/")), maxFilenameRunes)
	return r.finishSubmit(ctx, ref, plugin.SingleLine(filename, maxFilenameRunes), res, mode)
}

// SubmitAccount fetches an account's list through an AccountImporter and
// stores it like Submit, with the given import mode.
func (r *Runner) SubmitAccount(ctx context.Context, pluginID, importerID, account, mode string) (int64, error) {
	ref, ok := r.resolve(pluginID, importerID)
	if !ok || !r.d.Registry.IsEnabled(pluginID) {
		return 0, ErrUnknownImporter
	}
	imp, ok := ref.plugin.Importer(importerID).(plugin.AccountImporter)
	if !ok {
		return 0, ErrUnknownImporter
	}
	if err := ref.usable(); err != nil {
		return 0, err
	}
	res, err := imp.ImportAccount(ctx, account)
	if err != nil {
		if errors.Is(err, plugin.ErrBadInput) {
			reason := strings.TrimPrefix(err.Error(), plugin.ErrBadInput.Error()+": ")
			return 0, &BadFileError{Reason: plugin.SingleLine(reason, maxTitleRunes), Err: err}
		}
		return 0, fmt.Errorf("import: %w", err)
	}
	return r.finishSubmit(ctx, ref, plugin.SingleLine(account, maxFilenameRunes), res, mode)
}

// finishSubmit stores the parsed rows as a new import and enqueues its job.
func (r *Runner) finishSubmit(ctx context.Context, ref *importerRef, filename string, res plugin.ParseResult, mode string) (int64, error) {
	rows, entries, err := r.storedRows(ref, res)
	if err != nil {
		return 0, err
	}
	if entries == 0 {
		return 0, &NoRowsError{What: ref.noRowsWhat()}
	}
	mode = normalizeMode(mode)
	r.opMu.Lock()
	defer r.opMu.Unlock()
	tx, err := r.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	defer tx.Rollback()
	if err := r.activeCheck(ctx, tx); err != nil {
		return 0, err
	}
	ins, err := tx.ExecContext(ctx, `INSERT INTO imports(filename, state, total, ignored, plugin_id, importer_id, mode) VALUES (?,?,?,?,?,?,?)`,
		filename, statePending, len(rows), max(res.Ignored, 0), ref.manifest.ID, ref.info.ID, mode)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	id, err := ins.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO import_rows(import_id, seq, external_id_type, external_id, title, outcome, message, data) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	defer stmt.Close()
	for i, row := range rows {
		if _, err := stmt.ExecContext(ctx, id, i+1, row.idType, row.id, row.title, row.outcome, row.message, row.data); err != nil {
			return 0, fmt.Errorf("import: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("import: %w", err)
	}
	if err := r.d.Queue.Enqueue(ctx, KindRun, Payload{ImportID: id, PluginID: ref.manifest.ID}); err != nil {
		if _, uerr := r.d.DB.ExecContext(context.WithoutCancel(ctx),
			`UPDATE imports SET state=?, finished_at=`+nowSQL+` WHERE id=?`, stateFailed, id); uerr != nil {
			r.logf("import %d: mark failed: %v", id, uerr)
		}
		return 0, fmt.Errorf("import: enqueue: %w", err)
	}
	return id, nil
}

// storedRows turns a parse result into the rows to store, entries first (in
// file order), then the importer's skipped rows. An entry of an unknown kind
// or without the importer's external id is stored as skipped with the
// reason. entries counts the rows that will be processed.
func (r *Runner) storedRows(ref *importerRef, res plugin.ParseResult) (rows []storedRow, entries int, err error) {
	idType := ref.info.ExternalIDType
	for _, e := range res.Entries {
		value := ""
		if idType != "" {
			value = plugin.SingleLine(e.ExternalIDs[idType], maxTitleRunes)
		}
		e.Title = plugin.SingleLine(e.Title, maxTitleRunes)
		if e.Title == "" && value != "" {
			e.Title = ref.idLabel() + " #" + value
			e.Notes = append(e.Notes, "no title in the file, using "+e.Title)
		}
		row := storedRow{idType: idType, id: value, title: e.Title}
		if _, ok := ref.kind(e.Kind); !ok {
			row.outcome, row.message = outcomeSkipped, fmt.Sprintf("unknown kind %q", plugin.SingleLine(e.Kind, 40))
			rows = append(rows, row)
			continue
		}
		if value == "" {
			row.outcome, row.message = outcomeSkipped, noIDMessage(ref)
			rows = append(rows, row)
			continue
		}
		b, err := json.Marshal(e)
		if err != nil {
			return nil, 0, fmt.Errorf("import: %w", err)
		}
		row.outcome, row.data = outcomePending, string(b)
		rows = append(rows, row)
		entries++
	}
	for _, s := range res.Skipped {
		rows = append(rows, storedRow{title: plugin.SingleLine(s.Title, maxTitleRunes), outcome: outcomeSkipped,
			message: plugin.SingleLine(s.Reason, maxNoteRunes)})
	}
	return rows, entries, nil
}
