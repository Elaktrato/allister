package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// Backup records (format_version 1 of the universal backup). The JSON names
// are part of the backup format: change them only together with the format
// version. sync_state is deliberately not stored: a restore decides it.

// BackupItem is one item of a backup with all its parts and, when the item is
// in the list, its entry.
//
// Cover holds the base name of a local cover file (the archive carries it as
// covers/<name>); CoverURL holds a remote cover URL that was never
// downloaded. At most one of them is set. CoverMissing is set by the archive
// writer when the item referenced a local cover that could not be included
// (file missing or name not acceptable); Cover is then "".
type BackupItem struct {
	PluginID     string                       `json:"plugin_id"`
	Kind         string                       `json:"kind"`
	Source       string                       `json:"source"`
	SourceID     string                       `json:"source_id"`
	Titles       []plugin.Title               `json:"titles"`
	Fields       map[string]plugin.FieldValue `json:"fields,omitempty"`
	Genres       []string                     `json:"genres,omitempty"`
	Tags         []plugin.Tag                 `json:"tags,omitempty"`
	ExternalIDs  map[string]string            `json:"external_ids,omitempty"`
	ExternalURL  string                       `json:"external_url,omitempty"`
	Relations    []plugin.Relation            `json:"relations,omitempty"`
	Credits      []BackupCredit               `json:"credits,omitempty"`
	Cover        string                       `json:"cover,omitempty"`
	CoverURL     string                       `json:"cover_url,omitempty"`
	CoverMissing bool                         `json:"cover_missing,omitempty"`
	ProgressKeys []string                     `json:"progress_keys"`
	CreatedAt    string                       `json:"created_at,omitempty"` // RFC 3339 UTC
	UpdatedAt    string                       `json:"updated_at,omitempty"` // RFC 3339 UTC
	Entry        *BackupEntry                 `json:"entry,omitempty"`
}

// BackupCredit is a credit of a backed-up item.
type BackupCredit struct {
	Role     string        `json:"role"`
	Detail   string        `json:"detail,omitempty"`
	Person   BackupPerson  `json:"person"`
	With     *BackupPerson `json:"with,omitempty"`
	WithRole string        `json:"with_role,omitempty"`
}

// BackupPerson is a credited person. ImageURL is only a REMOTE photo URL:
// downloaded photos are not part of a backup (the sync after a restore
// fetches them again).
type BackupPerson struct {
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// BackupEntry is the list entry of a backed-up item. Rating is null when
// unrated; the dates are YYYY-MM-DD or null; Progress holds EVERY stored
// value, also those of keys that are not active.
type BackupEntry struct {
	Status     Status             `json:"status"`
	Rating     *int               `json:"rating"`
	Comment    string             `json:"comment"`
	StartedOn  *string            `json:"started_on"`
	FinishedOn *string            `json:"finished_on"`
	Progress   map[string]float64 `json:"progress"`
	UpdatedAt  string             `json:"updated_at,omitempty"` // RFC 3339 UTC
}

// fileNameRE is the shape of a local image file name in a backup.
var fileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidFileName reports whether s is acceptable as a local image file name
// in a backup: 1 to 128 ASCII letters, digits, '.', '_' or '-', starting with
// a letter or digit, without "..". It never contains a path separator.
func ValidFileName(s string) bool {
	return fileNameRE.MatchString(s) && !strings.Contains(s, "..")
}

const (
	maxRestoreTitles = 200
	maxRestoreExtIDs = 100
	stampLayout      = "2006-01-02T15:04:05Z"
)

// BackupItems calls fn for every stored item, in ascending id order,
// regardless of whether its plugin is enabled or even installed (the backup
// selects by data). Items are read in batches of batch ids (default 200 when
// batch <= 0) with one IN (...) query per child table and bound parameters;
// only one batch is held in memory. An error from fn stops the iteration and
// is returned.
//
// Consistency: every transaction of this library is a write lock (the
// database opens transactions IMMEDIATE), so a long read transaction would
// block every edit for the whole download. Instead each batch is read with
// its own queries over an id range (id > last id of the previous batch).
// Each item is internally consistent as far as one batch's queries see it;
// edits made while a backup runs may or may not be included, and an item
// edited between two child queries of its batch can mix old and new parts.
// That is accepted for a single-user application.
func (l *Library) BackupItems(ctx context.Context, batch int, fn func(BackupItem) error) error {
	if batch <= 0 {
		batch = 200
	}
	var last int64
	for {
		items, ids, err := l.backupBatch(ctx, last, batch)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		for i := range items {
			if err := fn(items[i]); err != nil {
				return err
			}
		}
		last = ids[len(ids)-1]
		if len(items) < batch {
			return nil
		}
	}
}

// backupBatch reads up to n items with id > after and all their parts.
func (l *Library) backupBatch(ctx context.Context, after int64, n int) ([]BackupItem, []int64, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, plugin_id, kind, source, source_id, cover_path, external_url, progress_keys, created_at, updated_at
		FROM items WHERE id > ? ORDER BY id LIMIT ?`, after, n)
	if err != nil {
		return nil, nil, fmt.Errorf("backup items: %w", err)
	}
	var items []BackupItem
	var ids []int64
	for rows.Next() {
		var id int64
		var bi BackupItem
		var cover, pk string
		if err := rows.Scan(&id, &bi.PluginID, &bi.Kind, &bi.Source, &bi.SourceID, &cover, &bi.ExternalURL, &pk, &bi.CreatedAt, &bi.UpdatedAt); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("backup items: %w", err)
		}
		if err := decodeKeys(pk, &bi.ProgressKeys); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if bi.ProgressKeys == nil {
			bi.ProgressKeys = []string{}
		}
		if isRemote(cover) {
			bi.CoverURL = cover
		} else {
			bi.Cover = cover
		}
		items = append(items, bi)
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, nil, fmt.Errorf("backup items: %w", err)
	}
	if len(items) == 0 {
		return nil, nil, nil
	}
	if err := l.backupParts(ctx, ids, items); err != nil {
		return nil, nil, err
	}
	return items, ids, nil
}

func isRemote(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// eachRow runs query over the batch's ids (bound as IN (...) parameters) and
// calls scan for every row with the index of its item in the batch.
func (l *Library) eachRow(ctx context.Context, query string, ids []int64, scan func(rows *sql.Rows) (int64, error), add func(i int) error) error {
	args := make([]any, len(ids))
	pos := make(map[int64]int, len(ids))
	for i, id := range ids {
		args[i] = id
		pos[id] = i
	}
	rows, err := l.db.QueryContext(ctx, strings.Replace(query, "(?)", "("+placeholders(len(ids))+")", 1), args...)
	if err != nil {
		return fmt.Errorf("backup items: %w", err)
	}
	for rows.Next() {
		id, err := scan(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("backup items: %w", err)
		}
		if i, ok := pos[id]; ok {
			if err := add(i); err != nil {
				rows.Close()
				return fmt.Errorf("backup items: %w", err)
			}
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("backup items: %w", err)
	}
	return nil
}

// backupParts fills the child parts of a batch: one query per table.
func (l *Library) backupParts(ctx context.Context, ids []int64, items []BackupItem) error {
	var id int64
	var a, b string
	if err := l.eachRow(ctx, `SELECT item_id, lang, text FROM item_titles WHERE item_id IN (?) ORDER BY item_id, rowid`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &a, &b) },
		func(i int) error {
			items[i].Titles = append(items[i].Titles, plugin.Title{Lang: a, Text: b})
			return nil
		}); err != nil {
		return err
	}

	accs := make([]*fieldAcc, len(items))
	var key, vtype string
	var text, date sql.NullString
	var num sql.NullFloat64
	if err := l.eachRow(ctx, `SELECT item_id, key, vtype, text, num, date FROM item_fields WHERE item_id IN (?) ORDER BY item_id, key, seq`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &key, &vtype, &text, &num, &date) },
		func(i int) error {
			if accs[i] == nil {
				accs[i] = newFieldAcc()
			}
			var tp, dp *string
			var np *float64
			if text.Valid {
				s := text.String
				tp = &s
			}
			if date.Valid {
				s := date.String
				dp = &s
			}
			if num.Valid {
				f := num.Float64
				np = &f
			}
			return accs[i].add(key, vtype, tp, np, dp)
		}); err != nil {
		return err
	}
	for i, acc := range accs {
		if acc != nil && len(acc.vals) > 0 {
			items[i].Fields = acc.vals
		}
	}

	if err := l.eachRow(ctx, `SELECT item_id, type, value FROM external_ids WHERE item_id IN (?) ORDER BY item_id, type`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &a, &b) },
		func(i int) error {
			if items[i].ExternalIDs == nil {
				items[i].ExternalIDs = map[string]string{}
			}
			items[i].ExternalIDs[a] = b
			return nil
		}); err != nil {
		return err
	}

	var rel plugin.Relation
	var tj, fj string
	if err := l.eachRow(ctx, `SELECT item_id, kind, source, source_id, kind_item, cover_url, url, titles_json, fields_json
		FROM `+tableRelations+` WHERE item_id IN (?) ORDER BY item_id, seq`, ids,
		func(r *sql.Rows) (int64, error) {
			rel = plugin.Relation{}
			return id, r.Scan(&id, &rel.Kind, &rel.Item.Source, &rel.Item.ID, &rel.Item.Kind, &rel.Item.CoverURL, &rel.Item.URL, &tj, &fj)
		},
		func(i int) error {
			if err := json.Unmarshal([]byte(tj), &rel.Item.Titles); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(fj), &rel.Item.Fields); err != nil {
				return err
			}
			if len(rel.Item.Titles) == 0 {
				rel.Item.Titles = nil
			}
			if len(rel.Item.Fields) == 0 {
				rel.Item.Fields = nil
			}
			items[i].Relations = append(items[i].Relations, rel)
			return nil
		}); err != nil {
		return err
	}

	var termKind, term string
	var rank sql.NullInt64
	var spoiler int
	if err := l.eachRow(ctx, `SELECT item_id, kind, term, rank, spoiler FROM item_terms WHERE item_id IN (?) ORDER BY item_id, kind, seq`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &termKind, &term, &rank, &spoiler) },
		func(i int) error {
			if termKind == "genre" {
				items[i].Genres = append(items[i].Genres, term)
				return nil
			}
			t := plugin.Tag{Name: term, Spoiler: spoiler != 0}
			if rank.Valid {
				r := int(rank.Int64)
				t.Rank = &r
			}
			items[i].Tags = append(items[i].Tags, t)
			return nil
		}); err != nil {
		return err
	}

	var c BackupCredit
	var pimg string
	var wsid, wname, wurl, wimg sql.NullString
	if err := l.eachRow(ctx, `SELECT c.item_id, c.role, c.detail, c.counterpart_role,
		p.source_id, p.name, p.external_url, p.image_path, w.source_id, w.name, w.external_url, w.image_path
		FROM credits c JOIN `+tablePeople+` p ON p.id = c.person_id
		LEFT JOIN `+tablePeople+` w ON w.id = c.counterpart_person_id
		WHERE c.item_id IN (?) ORDER BY c.item_id, c.seq`, ids,
		func(r *sql.Rows) (int64, error) {
			c = BackupCredit{}
			return id, r.Scan(&id, &c.Role, &c.Detail, &c.WithRole, &c.Person.SourceID, &c.Person.Name, &c.Person.URL, &pimg,
				&wsid, &wname, &wurl, &wimg)
		},
		func(i int) error {
			if isRemote(pimg) {
				c.Person.ImageURL = pimg
			}
			if wsid.Valid {
				c.With = &BackupPerson{SourceID: wsid.String, Name: wname.String, URL: wurl.String}
				if isRemote(wimg.String) {
					c.With.ImageURL = wimg.String
				}
			}
			items[i].Credits = append(items[i].Credits, c)
			return nil
		}); err != nil {
		return err
	}

	var status, updated string
	var rating sql.NullInt64
	var comment string
	var started, finished sql.NullString
	if err := l.eachRow(ctx, `SELECT item_id, status, rating, comment, started_on, finished_on, updated_at FROM `+tableEntries+` WHERE item_id IN (?)`, ids,
		func(r *sql.Rows) (int64, error) {
			return id, r.Scan(&id, &status, &rating, &comment, &started, &finished, &updated)
		},
		func(i int) error {
			e := &BackupEntry{Status: Status(status), Comment: comment, UpdatedAt: updated, Progress: map[string]float64{}}
			if rating.Valid {
				r := int(rating.Int64)
				e.Rating = &r
			}
			if started.Valid && started.String != "" {
				s := started.String
				e.StartedOn = &s
			}
			if finished.Valid && finished.String != "" {
				s := finished.String
				e.FinishedOn = &s
			}
			items[i].Entry = e
			return nil
		}); err != nil {
		return err
	}

	var pkey string
	var pval float64
	return l.eachRow(ctx, `SELECT item_id, key, value FROM `+tableEntryProgress+` WHERE item_id IN (?)`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &pkey, &pval) },
		func(i int) error {
			// values without an entry are not part of a backup
			if items[i].Entry != nil {
				items[i].Entry.Progress[pkey] = pval
			}
			return nil
		})
}

// RestoreOutcome is what RestoreItem did with one backup item.
type RestoreOutcome string

const (
	RestoreCreated       RestoreOutcome = "created"
	RestoreSkipped       RestoreOutcome = "skipped"
	RestoreFailed        RestoreOutcome = "failed"
	RestoreUnknownPlugin RestoreOutcome = "unknown_plugin"
)

// RestoreOptions customises RestoreItem.
type RestoreOptions struct {
	// ResolveCover stores the backup's local cover file name and returns the
	// name it was stored under ("" with a note when it cannot be restored,
	// for example missing from the archive or not an image). It is called
	// only for an item that passed validation and does not exist yet. An
	// error is fatal: RestoreItem returns it and writes nothing. When nil,
	// local covers are left empty with a note.
	ResolveCover func(ctx context.Context, name string) (stored, note string, err error)
	// ReleaseCover is called with a name ResolveCover returned when the
	// item was not written after all (skipped, failed or an error) and no
	// item or person references the stored file, so the caller can delete
	// it. It may be nil. Residual race (accepted for a single-user app): a concurrent
	// save or sync that stores byte-identical content between the
	// ImageReferenced check and the caller's delete can lose its file.
	ReleaseCover func(ctx context.Context, stored string)
}

// RestoreResult reports one restored backup item. ItemID and SyncPending are
// set only for RestoreCreated; Reason only for RestoreFailed and
// RestoreUnknownPlugin. Notes lists what was restored differently from the
// backup (a cover left empty).
type RestoreResult struct {
	Outcome     RestoreOutcome
	ItemID      int64
	PluginID    string
	SyncPending bool
	Reason      string
	Notes       []string
}

// restorePlan is a validated backup item ready to be written.
type restorePlan struct {
	p                    *prepared
	data                 plugin.ItemData
	credits              []plugin.Credit
	entry                *Entry
	entryStamp           string
	createdAt, updatedAt string
}

// RestoreItem MERGES one backup item into the library: it never overwrites
// or deletes anything. The item is validated with the same rules as SaveItem
// (titles, fields, terms, external ids, relations, cover), SaveCredits and
// entries (status, rating, comment, dates, progress of every stored key:
// active keys like ValidateEntry, inactive ones must be eligible and in
// range), plus caps on titles and external ids and RFC 3339 time stamps.
// Manual items additionally need their required fields (as ValidateItem),
// and every credited person needs a name.
//
// Outcomes: a plugin that is not installed is RestoreUnknownPlugin (nothing
// written); invalid data is RestoreFailed with the reason (nothing written);
// an item whose (plugin, kind, source, source id) exists is RestoreSkipped
// and left untouched (no entry is added either); otherwise the item with all
// its parts, its credits (existing people are linked unchanged, missing
// ones created), its entry with all progress values and its time stamps is
// written in ONE transaction and the result is RestoreCreated. Its sync
// state is "done" for manual items without a remote cover and "pending"
// otherwise (SyncPending), so the normal sync refetches credits' photos,
// downloads remote covers and fills gaps. When the item is not written
// after its cover was stored, ReleaseCover gets the unreferenced file.
// The plugin need not be enabled.
//
// The returned error is reserved for failures that are not about the item
// (database errors, a ResolveCover error, context cancellation).
func (l *Library) RestoreItem(ctx context.Context, bi BackupItem, opts RestoreOptions) (RestoreResult, error) {
	res := RestoreResult{PluginID: bi.PluginID}
	if _, ok := l.reg.Manifest(bi.PluginID); !ok {
		res.Outcome, res.Reason = RestoreUnknownPlugin, "the plugin is not installed"
		return res, nil
	}
	plan, err := l.planRestore(bi)
	if err != nil {
		return failedRestore(res, err)
	}
	var existing int64
	err = l.db.QueryRowContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?`,
		bi.PluginID, bi.Kind, plan.p.source, plan.p.sourceID).Scan(&existing)
	if err == nil {
		res.Outcome = RestoreSkipped
		return res, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return res, fmt.Errorf("restore item: %w", err)
	}
	stored := ""
	if bi.Cover != "" {
		note := "the cover file is not part of the backup: restored without a cover"
		if opts.ResolveCover != nil {
			if stored, note, err = opts.ResolveCover(ctx, bi.Cover); err != nil {
				return res, err
			}
		}
		if stored != "" && checkImageRef("cover", stored) == nil {
			plan.data.CoverURL = stored
		} else {
			stored = ""
			if note != "" {
				res.Notes = append(res.Notes, note)
			}
		}
	}
	// manual items have nothing to sync, except a remote cover to download
	syncState := "pending"
	if plan.p.source == manualSource && !isRemote(plan.data.CoverURL) {
		syncState = "done"
	}
	id, err := l.writeRestore(ctx, bi, plan, syncState)
	if err != nil && stored != "" {
		l.releaseCover(ctx, opts, stored)
	}
	var dup *AlreadySavedError
	switch {
	case errors.As(err, &dup):
		res.Outcome, res.Notes = RestoreSkipped, nil
		return res, nil
	case errors.Is(err, ErrInvalid):
		return failedRestore(res, err)
	case err != nil:
		return res, err
	}
	res.Outcome, res.ItemID, res.SyncPending = RestoreCreated, id, syncState == "pending"
	return res, nil
}

// releaseCover hands a stored cover back to the caller when nothing
// references it (the item that would have used it was not written).
func (l *Library) releaseCover(ctx context.Context, opts RestoreOptions, stored string) {
	if opts.ReleaseCover == nil {
		return
	}
	// the request context may be the reason the write failed
	if ref, err := l.ImageReferenced(context.WithoutCancel(ctx), stored); err == nil && !ref {
		opts.ReleaseCover(ctx, stored)
	}
}

func failedRestore(res RestoreResult, err error) (RestoreResult, error) {
	if !errors.Is(err, ErrInvalid) {
		return res, err
	}
	res.Outcome, res.Reason = RestoreFailed, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
	return res, nil
}

// planRestore validates everything of bi without touching the database.
func (l *Library) planRestore(bi BackupItem) (*restorePlan, error) {
	if len(bi.Titles) > maxRestoreTitles {
		return nil, invalid("more than %d titles", maxRestoreTitles)
	}
	if len(bi.ExternalIDs) > maxRestoreExtIDs {
		return nil, invalid("more than %d external ids", maxRestoreExtIDs)
	}
	switch {
	case bi.Cover != "" && bi.CoverURL != "":
		return nil, invalid("an item has both a cover file and a cover URL")
	case bi.Cover != "" && !ValidFileName(bi.Cover):
		return nil, invalid("cover %q is not a plain file name", truncateRunes(bi.Cover, 80))
	case bi.CoverURL != "":
		if err := checkHTTPURL("cover url", bi.CoverURL); err != nil {
			return nil, err
		}
		if !isRemote(bi.CoverURL) {
			return nil, invalid("cover url must be an http(s) URL")
		}
	}
	d := plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: bi.Source, ID: bi.SourceID, Kind: bi.Kind, Titles: bi.Titles, Fields: bi.Fields, CoverURL: bi.CoverURL},
		ExternalURL: bi.ExternalURL, ExternalIDs: bi.ExternalIDs, Relations: bi.Relations, Genres: bi.Genres, Tags: bi.Tags,
	}
	p, err := l.prepare(bi.PluginID, bi.Kind, d, bi.ProgressKeys)
	if err != nil {
		return nil, err
	}
	// the manual rules (ValidateItem): a manual item needs its required
	// fields; sourced items may lack them, as in a normal save
	if p.source == manualSource {
		if miss := requiredMissing(p.def, p.fields); len(miss) > 0 {
			return nil, invalid("required field(s) missing: %s", strings.Join(miss, ", "))
		}
	}
	plan := &restorePlan{p: p, data: d}
	if plan.createdAt, err = restoreStamp("created_at", bi.CreatedAt); err != nil {
		return nil, err
	}
	if plan.updatedAt, err = restoreStamp("updated_at", bi.UpdatedAt); err != nil {
		return nil, err
	}
	for _, c := range bi.Credits {
		// every person needs a name: a new one is created from it, and the
		// transaction must not fail on it after the cover was stored
		if strings.TrimSpace(c.Person.Name) == "" {
			return nil, invalid("credited person %q needs a name", truncateRunes(c.Person.SourceID, 40))
		}
		if c.With != nil && strings.TrimSpace(c.With.Name) == "" {
			return nil, invalid("credit counterpart person %q needs a name", truncateRunes(c.With.SourceID, 40))
		}
		pc := plugin.Credit{Role: c.Role, Detail: c.Detail, WithRole: c.WithRole, Person: personRef(c.Person)}
		if err := checkRemoteImage("credit person image", c.Person.ImageURL); err != nil {
			return nil, err
		}
		if c.With != nil {
			w := personRef(*c.With)
			pc.With = &w
			if err := checkRemoteImage("credit counterpart person image", c.With.ImageURL); err != nil {
				return nil, err
			}
		}
		plan.credits = append(plan.credits, pc)
	}
	if err := checkCredits(plan.credits); err != nil {
		return nil, err
	}
	if bi.Entry != nil {
		e, err := l.restoreEntry(p, d, *bi.Entry)
		if err != nil {
			return nil, err
		}
		plan.entry = e
		if plan.entryStamp, err = restoreStamp("entry updated_at", bi.Entry.UpdatedAt); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func personRef(p BackupPerson) plugin.PersonRef {
	return plugin.PersonRef{ID: p.SourceID, Name: p.Name, URL: p.URL, ImageURL: p.ImageURL}
}

// checkRemoteImage accepts "" or an http(s) URL (never a local file name:
// a backup does not carry person photos).
func checkRemoteImage(path, s string) error {
	if s == "" {
		return nil
	}
	if err := checkHTTPURL(path, s); err != nil {
		return err
	}
	if !isRemote(s) {
		return invalid("%s must be empty or an http(s) URL", path)
	}
	return nil
}

// restoreStamp checks an RFC 3339 time stamp and returns it in the stored
// form (UTC, seconds); "" stays "" (the database then uses now).
func restoreStamp(path, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
		return "", invalid("%s %q is not an RFC 3339 time", path, truncateRunes(s, 40))
	}
	return t.UTC().Format(stampLayout), nil
}

// restoreDay parses a YYYY-MM-DD calendar day (nil stays nil).
func restoreDay(path string, s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, *s)
	if err != nil || t.Format(dateLayout) != *s {
		return nil, invalid("%s %q is not a YYYY-MM-DD date", path, truncateRunes(*s, 40))
	}
	return &t, nil
}

// restoreEntry validates a backup entry against the prepared item: the
// active keys' values like ValidateEntry, the other stored values must be
// eligible progress of the kind and within their bounds.
func (l *Library) restoreEntry(p *prepared, d plugin.ItemData, be BackupEntry) (*Entry, error) {
	started, err := restoreDay("started_on", be.StartedOn)
	if err != nil {
		return nil, err
	}
	finished, err := restoreDay("finished_on", be.FinishedOn)
	if err != nil {
		return nil, err
	}
	item := &Item{ProgressKeys: p.progress, Fields: d.Fields}
	active := map[string]float64{}
	keys := make([]string, 0, len(be.Progress))
	for k := range be.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if slices.Contains(p.progress, k) {
			active[k] = be.Progress[k]
			continue
		}
		def, ok := progressDef(p.def, k)
		if !ok {
			return nil, invalid("progress %q is not a progress of kind %q", truncateRunes(k, 40), p.def.Key)
		}
		total, bounded := progressBound(def, item)
		if err := checkProgressValue(def, be.Progress[k], total, bounded); err != nil {
			return nil, err
		}
	}
	e := Entry{Status: be.Status, Rating: be.Rating, Comment: be.Comment, StartedOn: started, FinishedOn: finished, Progress: active}
	if err := l.ValidateEntry(p.def, item, e); err != nil {
		return nil, err
	}
	e.Progress = be.Progress
	return &e, nil
}

// writeRestore writes the planned item in one transaction.
func (l *Library) writeRestore(ctx context.Context, bi BackupItem, plan *restorePlan, syncState string) (int64, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("restore item: %w", err)
	}
	defer tx.Rollback()
	id, err := insertItem(ctx, tx, bi.PluginID, bi.Kind, plan.data, syncState, plan.p)
	if err != nil {
		return 0, err
	}
	if plan.createdAt != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET created_at = ? WHERE id = ?`, plan.createdAt, id); err != nil {
			return 0, fmt.Errorf("restore item: %w", err)
		}
	}
	if plan.updatedAt != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET updated_at = ? WHERE id = ?`, plan.updatedAt, id); err != nil {
			return 0, fmt.Errorf("restore item: %w", err)
		}
	}
	if err := restoreCredits(ctx, tx, id, bi.PluginID, plan.p.source, plan.credits); err != nil {
		return 0, err
	}
	if plan.entry != nil {
		if _, err := writeEntryMode(ctx, tx, id, *plan.entry, true); err != nil {
			return 0, err
		}
		if plan.entryStamp != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE `+tableEntries+` SET updated_at = ? WHERE item_id = ?`, plan.entryStamp, id); err != nil {
				return 0, fmt.Errorf("restore item: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("restore item: %w", err)
	}
	return id, nil
}

// restoreCredits writes the credits of a restored item. Unlike SaveCredits
// it never changes an existing person: a person with the same (plugin,
// source, source id) is linked as it is (name, URL and image stay; the
// normal sync refreshes sourced people), a missing one is created from the
// backup's data.
func restoreCredits(ctx context.Context, tx *sql.Tx, itemID int64, pluginID, source string, credits []plugin.Credit) error {
	for i, c := range credits {
		pid, err := restorePerson(ctx, tx, pluginID, source, c.Person)
		if err != nil {
			return err
		}
		var with any
		if c.With != nil {
			id, err := restorePerson(ctx, tx, pluginID, source, *c.With)
			if err != nil {
				return err
			}
			with = id
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO credits(item_id, seq, role, detail, person_id, counterpart_person_id, counterpart_role)
			VALUES (?,?,?,?,?,?,?)`, itemID, i, c.Role, c.Detail, pid, with, c.WithRole); err != nil {
			return fmt.Errorf("restore credits: %w", err)
		}
	}
	return nil
}

func restorePerson(ctx context.Context, tx *sql.Tx, pluginID, source string, p plugin.PersonRef) (int64, error) {
	sid := strings.TrimSpace(p.ID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+tablePeople+`(plugin_id, source, source_id, name, image_path, external_url)
		VALUES (?,?,?,?,?,?) ON CONFLICT(plugin_id, source, source_id) DO NOTHING`,
		pluginID, source, sid, strings.TrimSpace(p.Name), p.ImageURL, p.URL); err != nil {
		return 0, fmt.Errorf("restore person: %w", err)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM `+tablePeople+` WHERE plugin_id = ? AND source = ? AND source_id = ?`,
		pluginID, source, sid).Scan(&id); err != nil {
		return 0, fmt.Errorf("restore person: %w", err)
	}
	return id, nil
}
