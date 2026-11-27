package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

// Backup records (format_version 4 of the universal backup; BackupEntryV1 is
// the entry of format 1). The JSON names are part of the backup format:
// change them only together with the format version. sync_state is
// deliberately not stored: a restore decides it.

// BackupItem is one item of a backup with all its parts and, when the item is
// in the list, its entry.
//
// Cover holds the base name of a local cover file (the archive carries it as
// covers/<name>); CoverURL holds a remote cover URL that was never
// downloaded. At most one of them is set. CoverMissing is set by the archive
// writer when the item referenced a local cover that could not be included
// (file missing or name not acceptable); Cover is then "". CoverSource is
// the remote URL a local cover was downloaded from (restored only with the
// cover file).
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
	CoverSource  string                       `json:"cover_source,omitempty"`
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

// BackupEntry is the list entry of a backed-up item (format 2): its notes,
// every run in number order (1..n), its own tags (names ordered by key) and
// its stamps. The display run is not stored: a restore recomputes it.
// OrphanEvents are the changes whose run was deleted (they still feed the
// activity history); events of the runs are in the runs. Together the entry
// carries the newest maxBackupEvents changes; the run (or the orphans) whose
// older changes stayed behind is marked EventsTruncated.
type BackupEntry struct {
	Notes                 string        `json:"notes"`
	Runs                  []BackupRun   `json:"runs"`
	UserTags              []string      `json:"user_tags,omitempty"`
	OrphanEvents          []BackupEvent `json:"orphan_events,omitempty"`
	OrphanEventsTruncated bool          `json:"orphan_events_truncated,omitempty"`
	AddedAt               string        `json:"added_at,omitempty"`   // RFC 3339 UTC
	UpdatedAt             string        `json:"updated_at,omitempty"` // RFC 3339 UTC
}

// BackupRun is one run of a backed-up entry. Rating is null when unrated;
// the dates are YYYY-MM-DD or null; Progress holds EVERY stored value of the
// run, also those of keys that are not active. Events holds the run's
// changes, newest first.
type BackupRun struct {
	Number          int                `json:"number"`
	Status          Status             `json:"status"`
	Rating          *int               `json:"rating"`
	Review          string             `json:"review"`
	StartedOn       *string            `json:"started_on"`
	FinishedOn      *string            `json:"finished_on"`
	Progress        map[string]float64 `json:"progress"`
	UnitNotes       []BackupUnitNote   `json:"unit_notes,omitempty"`
	Events          []BackupEvent      `json:"events,omitempty"`
	EventsTruncated bool               `json:"events_truncated,omitempty"`
	CreatedAt       string             `json:"created_at,omitempty"` // RFC 3339 UTC
	UpdatedAt       string             `json:"updated_at,omitempty"` // RFC 3339 UTC
}

// BackupUnitNote is the note on one unit of a run's progress track.
type BackupUnitNote struct {
	ProgressKey string `json:"progress_key"`
	Unit        int    `json:"unit"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at,omitempty"` // RFC 3339 UTC
	UpdatedAt   string `json:"updated_at,omitempty"` // RFC 3339 UTC
}

// BackupEntryV1 is the list entry of a format-1 backup: one state with a
// comment, all stored progress values and the change log (newest first).
// Upgrade turns it into the format-2 entry a restore writes.
type BackupEntryV1 struct {
	Status          Status             `json:"status"`
	Rating          *int               `json:"rating"`
	Comment         string             `json:"comment"`
	StartedOn       *string            `json:"started_on"`
	FinishedOn      *string            `json:"finished_on"`
	Progress        map[string]float64 `json:"progress"`
	UpdatedAt       string             `json:"updated_at,omitempty"` // RFC 3339 UTC
	Events          []BackupEvent      `json:"events,omitempty"`
	EventsTruncated bool               `json:"events_truncated,omitempty"`
}

// Upgrade returns the format-2 entry of a format-1 entry: its state, progress
// and change log become run 1, the comment the entry's notes, and its stamp
// the entry's and the run's stamps (as a format-1 restore wrote them).
func (e BackupEntryV1) Upgrade() BackupEntry {
	return BackupEntry{Notes: e.Comment, AddedAt: e.UpdatedAt, UpdatedAt: e.UpdatedAt, Runs: []BackupRun{{
		Number: 1, Status: e.Status, Rating: e.Rating, StartedOn: e.StartedOn, FinishedOn: e.FinishedOn, Progress: e.Progress,
		Events: e.Events, EventsTruncated: e.EventsTruncated, CreatedAt: e.UpdatedAt, UpdatedAt: e.UpdatedAt}}}
}

// BackupEvent is one change of an entry's log, as stored: OccurredAt is an RFC
// 3339 UTC time, Type is progress, status or rating, OldNum/NewNum carry the
// progress values or the rating (rating null is "unrated"), OldText/NewText
// the statuses, and Origin says who made the change.
type BackupEvent struct {
	OccurredAt  string   `json:"occurred_at"`
	Type        string   `json:"type"`
	ProgressKey string   `json:"progress_key,omitempty"`
	OldNum      *float64 `json:"old_num,omitempty"`
	NewNum      *float64 `json:"new_num,omitempty"`
	OldText     string   `json:"old_text,omitempty"`
	NewText     string   `json:"new_text,omitempty"`
	Origin      string   `json:"origin,omitempty"`
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
	// maxBackupEvents caps the change log an entry carries in a backup: the
	// newest events are kept, older ones stay in the database behind
	// EventsTruncated. Restore accepts no more than this per entry, so every
	// backup the writer produces can be restored.
	maxBackupEvents = 5000
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
	rows, err := l.db.QueryContext(ctx, `SELECT id, plugin_id, kind, source, source_id, cover_path, cover_source, external_url, progress_keys, created_at, updated_at
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
		if err := rows.Scan(&id, &bi.PluginID, &bi.Kind, &bi.Source, &bi.SourceID, &cover, &bi.CoverSource, &bi.ExternalURL, &pk, &bi.CreatedAt, &bi.UpdatedAt); err != nil {
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
			bi.CoverURL, bi.CoverSource = cover, ""
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
			l.listCompat(items[i].PluginID, items[i].Kind, items[i].Fields)
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

	return l.backupEntries(ctx, ids, items)
}

// backupEntries fills the entries of a batch: the entry rows, their runs with
// every stored progress value and unit note, their own tags and the change
// log. Rows of an item without an entry (its entry was removed) have no
// place in a backup.
func (l *Library) backupEntries(ctx context.Context, ids []int64, items []BackupItem) error {
	var id int64
	var notes, added, updated string
	if err := l.eachRow(ctx, `SELECT item_id, notes, added_at, updated_at FROM `+tableEntries+` WHERE item_id IN (?)`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &notes, &added, &updated) },
		func(i int) error {
			items[i].Entry = &BackupEntry{Notes: notes, AddedAt: added, UpdatedAt: updated, Runs: []BackupRun{}}
			return nil
		}); err != nil {
		return err
	}

	// where each run sits: its item's index in the batch and its index in
	// the entry's runs
	type place struct{ item, run int }
	runs := map[int64]place{}
	var runID int64
	var br BackupRun
	var status string
	var rating sql.NullInt64
	var started, finished sql.NullString
	if err := l.eachRow(ctx, `SELECT item_id, id, number, status, rating, review, started_on, finished_on, created_at, updated_at
		FROM `+tableRuns+` WHERE item_id IN (?) ORDER BY item_id, number`, ids,
		func(r *sql.Rows) (int64, error) {
			br = BackupRun{}
			return id, r.Scan(&id, &runID, &br.Number, &status, &rating, &br.Review, &started, &finished, &br.CreatedAt, &br.UpdatedAt)
		},
		func(i int) error {
			e := items[i].Entry
			if e == nil {
				return nil
			}
			br.Status, br.Progress = Status(status), map[string]float64{}
			if rating.Valid {
				v := int(rating.Int64)
				br.Rating = &v
			}
			if started.Valid && started.String != "" {
				v := started.String
				br.StartedOn = &v
			}
			if finished.Valid && finished.String != "" {
				v := finished.String
				br.FinishedOn = &v
			}
			runs[runID] = place{i, len(e.Runs)}
			e.Runs = append(e.Runs, br)
			return nil
		}); err != nil {
		return err
	}
	run := func(runID int64) *BackupRun {
		p, ok := runs[runID]
		if !ok {
			return nil
		}
		return &items[p.item].Entry.Runs[p.run]
	}

	// every stored value of every run, active or not
	var pkey string
	var pval float64
	if err := l.eachRow(ctx, `SELECT r.item_id, p.run_id, p.key, p.value FROM `+tableRunProgress+` p
		JOIN `+tableRuns+` r ON r.id = p.run_id WHERE r.item_id IN (?)`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &runID, &pkey, &pval) },
		func(int) error {
			if r := run(runID); r != nil {
				r.Progress[pkey] = pval
			}
			return nil
		}); err != nil {
		return err
	}

	var un BackupUnitNote
	if err := l.eachRow(ctx, `SELECT r.item_id, n.run_id, n.progress_key, n.unit, n.body, n.created_at, n.updated_at FROM `+tableUnitNotes+` n
		JOIN `+tableRuns+` r ON r.id = n.run_id WHERE r.item_id IN (?) ORDER BY r.item_id, r.number, n.progress_key, n.unit`, ids,
		func(r *sql.Rows) (int64, error) {
			un = BackupUnitNote{}
			return id, r.Scan(&id, &runID, &un.ProgressKey, &un.Unit, &un.Body, &un.CreatedAt, &un.UpdatedAt)
		},
		func(int) error {
			if r := run(runID); r != nil {
				r.UnitNotes = append(r.UnitNotes, un)
			}
			return nil
		}); err != nil {
		return err
	}

	var tag string
	if err := l.eachRow(ctx, `SELECT x.item_id, u.name FROM entry_user_tags x JOIN user_tags u ON u.id = x.tag_id
		WHERE x.item_id IN (?) ORDER BY x.item_id, u.name_key`, ids,
		func(r *sql.Rows) (int64, error) { return id, r.Scan(&id, &tag) },
		func(i int) error {
			if e := items[i].Entry; e != nil {
				e.UserTags = append(e.UserTags, tag)
			}
			return nil
		}); err != nil {
		return err
	}

	// The change log is read newest first; an entry keeps maxBackupEvents of
	// them, each in its run (or among the orphans when its run was deleted),
	// and marks where older ones stayed behind.
	kept := make([]int, len(items))
	var evRun sql.NullInt64
	var occurred, evType, evKey, evOrigin string
	var oldNum, newNum sql.NullFloat64
	var oldText, newText string
	return l.eachRow(ctx, `SELECT item_id, run_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin
		FROM `+tableEntryEvents+` WHERE item_id IN (?) ORDER BY item_id, id DESC`, ids,
		func(r *sql.Rows) (int64, error) {
			return id, r.Scan(&id, &evRun, &occurred, &evType, &evKey, &oldNum, &newNum, &oldText, &newText, &evOrigin)
		},
		func(i int) error {
			e := items[i].Entry
			if e == nil {
				return nil
			}
			var r *BackupRun
			if evRun.Valid {
				r = run(evRun.Int64)
			}
			if kept[i] >= maxBackupEvents {
				if r != nil {
					r.EventsTruncated = true
				} else {
					e.OrphanEventsTruncated = true
				}
				return nil
			}
			kept[i]++
			ev := BackupEvent{OccurredAt: occurred, Type: evType, ProgressKey: evKey, OldText: oldText, NewText: newText, Origin: evOrigin}
			if oldNum.Valid {
				v := oldNum.Float64
				ev.OldNum = &v
			}
			if newNum.Valid {
				v := newNum.Float64
				ev.NewNum = &v
			}
			if r != nil {
				r.Events = append(r.Events, ev)
			} else {
				e.OrphanEvents = append(e.OrphanEvents, ev)
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
	// AppliedUpgrades lists the progress upgrades the backup's database had
	// applied (its "progress_upgrades" member; nil for a backup without it,
	// which was written before any ran). Every upgrade of the item's kind
	// not listed here is applied to the restored item's keys.
	AppliedUpgrades []BackupProgressUpgrade
}

// RestoreResult reports one restored backup item. ItemID and SyncPending are
// set only for RestoreCreated; Reason only for RestoreFailed and
// RestoreUnknownPlugin. Notes lists what was restored differently from the
// backup (a cover left empty, a truncated change log).
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
	entry                *entryPlan
	createdAt, updatedAt string
	coverSource          string // kept when the cover file was stored
}

// entryPlan is a validated backup entry: its runs in number order, the
// change log oldest first (each event with the index of its run, -1 for an
// orphan) and the own tags. The stamps are in the stored form ("" is now).
type entryPlan struct {
	notes              string
	addedAt, updatedAt string
	runs               []runPlan
	events             []planEvent
	tags               []tagName
	truncated          bool // the backup kept only the newest events
}

type runPlan struct {
	run                  BackupRun
	createdAt, updatedAt string
	notes                []BackupUnitNote
}

type planEvent struct {
	run int
	ev  BackupEvent
}

type tagName struct{ name, key string }

// RestoreItem MERGES one backup item into the library: it never overwrites
// or deletes anything. The item is validated with the same rules as SaveItem
// (titles, fields, terms, external ids, relations, cover), SaveCredits and
// entries (notes; per run status, rating, review, dates, progress of every
// stored key, see restoreRun; run numbers 1..n; unit notes as SetUnitNote
// but not bounded by the total; own tags as SetEntryTags),
// plus caps on titles, external ids and events and RFC 3339 time stamps.
// Manual items additionally need their required fields (as ValidateItem),
// and every credited person needs a name.
//
// Outcomes: a plugin that is not installed is RestoreUnknownPlugin (nothing
// written); invalid data is RestoreFailed with the reason (nothing written);
// an item whose (plugin, kind, source, source id) exists is RestoreSkipped
// and left untouched (no entry is added either); otherwise the item with all
// its parts, its credits (existing people are linked unchanged, missing
// ones created), its entry with every run (numbers, progress values, unit
// notes, change log), its own tags (an existing tag keeps its spelling) and
// its time stamps is written in ONE transaction, the display run recomputed,
// and the result is RestoreCreated. Its sync
// state is "done" for manual items without a remote cover and "pending"
// otherwise (SyncPending), so the normal sync refetches credits' photos,
// downloads remote covers and fills gaps. When the item is not written
// after its cover was stored, ReleaseCover gets the unreferenced file.
// The plugin need not be enabled. Progress upgrades of the kind that the
// backup did not record as applied (RestoreOptions.AppliedUpgrades) switch
// the item's keys first, as ApplyProgressUpgrades would have.
//
// The returned error is reserved for failures that are not about the item
// (database errors, a ResolveCover error, context cancellation).
func (l *Library) RestoreItem(ctx context.Context, bi BackupItem, opts RestoreOptions) (RestoreResult, error) {
	res := RestoreResult{PluginID: bi.PluginID}
	if _, ok := l.reg.Manifest(bi.PluginID); !ok {
		res.Outcome, res.Reason = RestoreUnknownPlugin, "the plugin is not installed"
		return res, nil
	}
	if def, err := l.kindDef(bi.PluginID, bi.Kind); err == nil { // an unknown kind fails in planRestore
		bi.ProgressKeys = restoredKeys(def, bi.PluginID, bi.ProgressKeys, opts.AppliedUpgrades)
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
			// the origin of a downloaded cover, kept only with that file
			plan.data.CoverURL, plan.coverSource = stored, bi.CoverSource
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
	// the writer keeps only the newest part of the log; say so rather than
	// silently restoring a gap
	if plan.entry != nil && plan.entry.truncated {
		res.Notes = append(res.Notes, "the change log of this entry was truncated in the backup (newest 5,000 events restored); older events are not in the archive")
	}
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
	if err := checkHTTPURL("cover source", bi.CoverSource); err != nil {
		return nil, err
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
		if plan.entry, err = l.restoreEntry(p, d, *bi.Entry); err != nil {
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

// restoreEntry validates a backup entry against the prepared item and plans
// its write.
func (l *Library) restoreEntry(p *prepared, d plugin.ItemData, be BackupEntry) (*entryPlan, error) {
	item := &Item{ProgressKeys: p.progress, Fields: d.Fields}
	if err := l.ValidateEntry(p.def, item, Entry{Status: Planned, Notes: be.Notes}); err != nil {
		return nil, err
	}
	plan := &entryPlan{notes: be.Notes}
	var err error
	if plan.addedAt, err = restoreStamp("entry added_at", be.AddedAt); err != nil {
		return nil, err
	}
	if plan.updatedAt, err = restoreStamp("entry updated_at", be.UpdatedAt); err != nil {
		return nil, err
	}
	if len(be.Runs) == 0 {
		return nil, invalid("an entry needs at least one run")
	}
	runs := slices.Clone(be.Runs)
	slices.SortStableFunc(runs, func(a, b BackupRun) int { return a.Number - b.Number })
	for i, r := range runs {
		if r.Number != i+1 {
			return nil, invalid("run numbers must be 1 to %d without gaps or repeats", len(runs))
		}
	}
	total := len(be.OrphanEvents)
	for _, r := range runs {
		total += len(r.Events)
	}
	if total > maxBackupEvents {
		return nil, invalid("entry has more than %d events", maxBackupEvents)
	}
	plan.truncated = be.OrphanEventsTruncated
	orphans, err := restoreEvents(p, be.OrphanEvents)
	if err != nil {
		return nil, err
	}
	// the log oldest first: orphans (their runs were deleted, so they are
	// older as a rule), then the runs in order, then by time (stable)
	for i := len(orphans) - 1; i >= 0; i-- {
		plan.events = append(plan.events, planEvent{run: -1, ev: orphans[i]})
	}
	display := displayRunIndex(runs)
	for i, r := range runs {
		rp, err := l.restoreRun(p, item, r, i == display)
		if err != nil {
			return nil, err
		}
		plan.runs = append(plan.runs, rp)
		plan.truncated = plan.truncated || r.EventsTruncated
		evs, err := restoreEvents(p, r.Events)
		if err != nil {
			return nil, err
		}
		for j := len(evs) - 1; j >= 0; j-- {
			plan.events = append(plan.events, planEvent{run: i, ev: evs[j]})
		}
	}
	slices.SortStableFunc(plan.events, func(a, b planEvent) int { return strings.Compare(a.ev.OccurredAt, b.ev.OccurredAt) })
	for _, raw := range be.UserTags {
		name, key, err := userTagName(raw)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(plan.tags, func(t tagName) bool { return t.key == key }) {
			continue
		}
		if len(plan.tags) == maxEntryTags {
			return nil, invalid("more than %d tags", maxEntryTags)
		}
		plan.tags = append(plan.tags, tagName{name, key})
	}
	return plan, nil
}

// displayRunIndex applies refreshDisplayRun's rule to runs (in number
// order): the latest run, except that a dropped repeat gives way to the
// latest completed run.
func displayRunIndex(runs []BackupRun) int {
	last := len(runs) - 1
	if runs[last].Number > 1 && runs[last].Status == Dropped {
		for i := last - 1; i >= 0; i-- {
			if runs[i].Status == Completed {
				return i
			}
		}
	}
	return last
}

// restoreRun validates one run: its state like ValidateEntry, and its stored
// progress values, which must be eligible progress of the kind. The display
// run's values are checked against the item's totals (the active keys like
// ValidateEntry); the other runs are stored as written, so their values are
// bounded only by the largest value the library takes (a total can be
// lowered after a run was finished). The status of a past run is not
// checked either: the app can store any. Unit notes are checked like
// SetUnitNote except for the total (see checkUnitNote), then the stamps.
func (l *Library) restoreRun(p *prepared, item *Item, r BackupRun, display bool) (runPlan, error) {
	at := fmt.Sprintf("run %d ", r.Number)
	started, err := restoreDay(at+"started_on", r.StartedOn)
	if err != nil {
		return runPlan{}, err
	}
	finished, err := restoreDay(at+"finished_on", r.FinishedOn)
	if err != nil {
		return runPlan{}, err
	}
	active := map[string]float64{}
	keys := make([]string, 0, len(r.Progress))
	for k := range r.Progress {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if display && slices.Contains(p.progress, k) {
			active[k] = r.Progress[k]
			continue
		}
		def, ok := progressDef(p.def, k)
		if !ok {
			return runPlan{}, invalid("progress %q is not a progress of kind %q", truncateRunes(k, 40), p.def.Key)
		}
		total, bounded := progressBound(def, item)
		if !display {
			total, bounded = 0, false
		}
		if err := checkProgressValue(def, r.Progress[k], total, bounded); err != nil {
			return runPlan{}, err
		}
	}
	if err := l.ValidateEntry(p.def, item, Entry{Status: r.Status, Rating: r.Rating, Review: r.Review,
		StartedOn: started, FinishedOn: finished, Progress: active}); err != nil {
		return runPlan{}, err
	}
	rp := runPlan{run: r}
	if rp.run.Progress == nil {
		rp.run.Progress = map[string]float64{}
	}
	if rp.createdAt, err = restoreStamp(at+"created_at", r.CreatedAt); err != nil {
		return runPlan{}, err
	}
	if rp.updatedAt, err = restoreStamp(at+"updated_at", r.UpdatedAt); err != nil {
		return runPlan{}, err
	}
	seen := map[string]bool{}
	for _, n := range r.UnitNotes {
		if err := checkUnitNote(p.def, n); err != nil {
			return runPlan{}, err
		}
		id := fmt.Sprintf("%s %d", n.ProgressKey, n.Unit)
		if seen[id] {
			return runPlan{}, invalid("the unit note on %s %d appears twice", truncateRunes(n.ProgressKey, 40), n.Unit)
		}
		seen[id] = true
		if n.CreatedAt, err = restoreStamp("unit note created_at", n.CreatedAt); err != nil {
			return runPlan{}, err
		}
		if n.UpdatedAt, err = restoreStamp("unit note updated_at", n.UpdatedAt); err != nil {
			return runPlan{}, err
		}
		rp.notes = append(rp.notes, n)
	}
	return rp, nil
}

// checkUnitNote applies SetUnitNote's rules to a backed-up unit note, except
// the item's total: a note is stored as written even when the total was
// lowered after it (a conversion clamps progress, never notes), so the unit
// is bounded only by the largest value the library takes. A blank note
// (SetUnitNote deletes it) is refused.
func checkUnitNote(kind plugin.KindDef, n BackupUnitNote) error {
	def, ok := progressDef(kind, n.ProgressKey)
	if !ok {
		return invalid("unit note progress %q is not a progress of kind %q", truncateRunes(n.ProgressKey, 40), kind.Key)
	}
	if def.Type != plugin.Count && def.Type != plugin.Pages {
		return invalid("notes can only be added to count or pages progress, not %q", n.ProgressKey)
	}
	if n.Unit < 1 {
		return invalid("the unit must be at least 1")
	}
	if float64(n.Unit) > maxProgressValue {
		return invalid("the unit %d is too large", n.Unit)
	}
	if strings.TrimSpace(n.Body) == "" {
		return invalid("a unit note is empty")
	}
	if utf8.RuneCountInString(n.Body) > MaxUnitNoteRunes {
		return invalid("the note is longer than %d characters", MaxUnitNoteRunes)
	}
	return cleanText("note", n.Body, multiLine)
}

// restoreEvents validates a backed-up change log against the prepared item:
// at most maxBackupEvents, a known type, origin and progress key (only a
// progress event carries one), an RFC 3339 time and finite numbers. It
// returns the events with their time stamps in the stored form. The order is
// kept as backed up (newest first), restoreEntry reverses it.
func restoreEvents(p *prepared, events []BackupEvent) ([]BackupEvent, error) {
	if len(events) > maxBackupEvents {
		return nil, invalid("entry has more than %d events", maxBackupEvents)
	}
	out := make([]BackupEvent, 0, len(events))
	for _, ev := range events {
		switch ev.Type {
		case eventProgress:
			if ev.ProgressKey == "" {
				return nil, invalid("a progress event needs a progress key")
			}
			if _, ok := progressDef(p.def, ev.ProgressKey); !ok {
				return nil, invalid("event progress %q is not a progress of kind %q", truncateRunes(ev.ProgressKey, 40), p.def.Key)
			}
		case eventStatus, eventRating:
			if ev.ProgressKey != "" {
				return nil, invalid("a %s event must not carry a progress key", ev.Type)
			}
		default:
			return nil, invalid("event type %q must be one of progress, status, rating", truncateRunes(ev.Type, 40))
		}
		if !validOrigin(EventOrigin(ev.Origin)) {
			return nil, invalid("event origin %q must be one of manual, import, restore, bulk", truncateRunes(ev.Origin, 40))
		}
		stamp, err := restoreStamp("event occurred_at", ev.OccurredAt)
		if err != nil {
			return nil, err
		}
		if stamp == "" {
			return nil, invalid("event occurred_at is missing")
		}
		for _, n := range []*float64{ev.OldNum, ev.NewNum} {
			if n != nil && (math.IsNaN(*n) || math.IsInf(*n, 0)) {
				return nil, invalid("event numbers must be finite")
			}
		}
		ev.OccurredAt = stamp
		out = append(out, ev)
	}
	return out, nil
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
	if plan.coverSource != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET cover_source = ? WHERE id = ?`, plan.coverSource, id); err != nil {
			return 0, fmt.Errorf("restore item: %w", err)
		}
	}
	if err := restoreCredits(ctx, tx, id, bi.PluginID, plan.p.source, plan.credits); err != nil {
		return 0, err
	}
	if plan.entry != nil {
		if err := writeRestoredEntry(ctx, tx, id, plan.entry); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("restore item: %w", err)
	}
	return id, nil
}

// orNow is the SQL value of a stored stamp: NULL for "" (the column then
// takes now through COALESCE).
func orNow(stamp string) any {
	if stamp == "" {
		return nil
	}
	return stamp
}

// writeRestoredEntry writes a planned entry of item itemID: the entry row,
// its runs with their numbers, progress values and unit notes, the change
// log oldest first, the own tags, then the display run.
func writeRestoredEntry(ctx context.Context, tx *sql.Tx, itemID int64, e *entryPlan) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntries+`(item_id, notes, added_at, updated_at)
		VALUES (?, ?, COALESCE(?, `+nowSQL+`), COALESCE(?, `+nowSQL+`))`, itemID, e.notes, orNow(e.addedAt), orNow(e.updatedAt)); err != nil {
		return fmt.Errorf("restore entry: %w", err)
	}
	runIDs := make([]int64, len(e.runs))
	for i, rp := range e.runs {
		r := rp.run
		var rating, started, finished any
		if r.Rating != nil {
			rating = *r.Rating
		}
		if r.StartedOn != nil {
			started = *r.StartedOn
		}
		if r.FinishedOn != nil {
			finished = *r.FinishedOn
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO `+tableRuns+`(item_id, number, status, rating, review, started_on, finished_on, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,COALESCE(?, `+nowSQL+`),COALESCE(?, `+nowSQL+`))`,
			itemID, r.Number, string(r.Status), rating, r.Review, started, finished, orNow(rp.createdAt), orNow(rp.updatedAt))
		if err != nil {
			return fmt.Errorf("restore run: %w", err)
		}
		if runIDs[i], err = res.LastInsertId(); err != nil {
			return fmt.Errorf("restore run: %w", err)
		}
		keys := make([]string, 0, len(r.Progress))
		for k := range r.Progress {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableRunProgress+`(run_id, key, value) VALUES (?,?,?)`, runIDs[i], k, r.Progress[k]); err != nil {
				return fmt.Errorf("restore run progress: %w", err)
			}
		}
		for _, n := range rp.notes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableUnitNotes+`(run_id, progress_key, unit, body, created_at, updated_at)
				VALUES (?,?,?,?,COALESCE(?, `+nowSQL+`),COALESCE(?, `+nowSQL+`))`,
				runIDs[i], n.ProgressKey, n.Unit, n.Body, orNow(n.CreatedAt), orNow(n.UpdatedAt)); err != nil {
				return fmt.Errorf("restore unit note: %w", err)
			}
		}
	}
	for _, pe := range e.events {
		var runID any
		if pe.run >= 0 {
			runID = runIDs[pe.run]
		}
		if err := restoreEvent(ctx, tx, itemID, runID, pe.ev); err != nil {
			return err
		}
	}
	for _, t := range e.tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_tags(name, name_key, created_at) VALUES (?, ?, `+nowSQL+`)
			ON CONFLICT(name_key) DO NOTHING`, t.name, t.key); err != nil {
			return fmt.Errorf("restore tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entry_user_tags(item_id, tag_id)
			SELECT ?, id FROM user_tags WHERE name_key = ?`, itemID, t.key); err != nil {
			return fmt.Errorf("restore tags: %w", err)
		}
	}
	return refreshDisplayRun(ctx, tx, itemID)
}

// restoreEvent appends one backed-up event of run runID (nil: its run was
// deleted) with its stored time stamp and origin, so the restored log keeps
// the history of the backup.
func restoreEvent(ctx context.Context, tx *sql.Tx, itemID int64, runID any, ev BackupEvent) error {
	var oldV, newV any
	if ev.OldNum != nil {
		oldV = *ev.OldNum
	}
	if ev.NewNum != nil {
		newV = *ev.NewNum
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+tableEntryEvents+`(item_id, run_id, occurred_at, event_type, progress_key, old_num, new_num, old_text, new_text, origin)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		itemID, runID, ev.OccurredAt, ev.Type, ev.ProgressKey, oldV, newV, ev.OldText, ev.NewText, ev.Origin); err != nil {
		return fmt.Errorf("restore event: %w", err)
	}
	return nil
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

// BackupSeries is one series of a backup: its own values and its parts
// (main parts first, then spin-off parts, each block in position order),
// identified like items everywhere in the backup (no numeric ids). Cover,
// CoverURL and CoverMissing work like BackupItem's; StartedOn and EndedOn are
// calendar days (YYYY-MM-DD); Linked is the identity of the linked entry and
// Parent the uid of the parent series. A reader that does not know the value
// members ignores them.
type BackupSeries struct {
	UID          string                       `json:"uid"`
	PluginID     string                       `json:"plugin_id"`
	Kind         string                       `json:"kind"`
	Title        string                       `json:"title"`
	CreatedAt    time.Time                    `json:"created_at"`
	UpdatedAt    time.Time                    `json:"updated_at"`
	Cover        string                       `json:"cover,omitempty"`
	CoverURL     string                       `json:"cover_url,omitempty"`
	CoverMissing bool                         `json:"cover_missing,omitempty"`
	Description  string                       `json:"description,omitempty"`
	StartedOn    string                       `json:"started_on,omitempty"`
	EndedOn      string                       `json:"ended_on,omitempty"`
	Finished     bool                         `json:"finished,omitempty"`
	PartsTotal   int                          `json:"parts_total,omitempty"`
	Fields       map[string]plugin.FieldValue `json:"fields,omitempty"`
	Linked       *BackupItemRef               `json:"linked,omitempty"`
	Parent       string                       `json:"parent,omitempty"`
	Parts        []BackupPartRef              `json:"parts"`
}

// BackupItemRef identifies an item by its source identity.
type BackupItemRef struct {
	PluginID string `json:"plugin_id"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	SourceID string `json:"source_id"`
}

// BackupPartRef identifies a series part by its item's source identity and
// says whether it is a spin-off part.
type BackupPartRef struct {
	PluginID string `json:"plugin_id"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	SourceID string `json:"source_id"`
	SpinOff  bool   `json:"spin_off,omitempty"`
}

// BackupSeries calls fn for every series (ordered by id) with its values and
// its parts (main block first, then spin-offs, each in position order),
// streamed from one query; the fields of each series are read before fn.
func (l *Library) BackupSeries(ctx context.Context, fn func(BackupSeries) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT `+seriesCols+`, li.plugin_id, li.kind, li.source, li.source_id, ps.uid,
		i.plugin_id, i.kind, i.source, i.source_id, p.spin_off
		FROM series s LEFT JOIN series_parts p ON p.series_id = s.id LEFT JOIN items i ON i.id = p.item_id
		LEFT JOIN items li ON li.id = s.linked_item_id LEFT JOIN series ps ON ps.id = s.parent_id
		ORDER BY s.id, p.spin_off, p.position`)
	if err != nil {
		return fmt.Errorf("backup series: %w", err)
	}
	defer rows.Close()
	var cur *BackupSeries
	var curID int64
	flush := func() error {
		if cur == nil {
			return nil
		}
		s := &Series{ID: curID}
		if err := attachSeriesFields(ctx, l.db, s); err != nil {
			return fmt.Errorf("backup series: %w", err)
		}
		cur.Fields = s.Fields
		err := fn(*cur)
		cur = nil
		return err
	}
	for rows.Next() {
		// the part columns are NULL for a series without parts
		var lPlugin, lKind, lSource, lID, parent sql.NullString
		var pPlugin, pKind, pSource, pID sql.NullString
		var pSpin sql.NullBool
		s, err := scanSeries(rows, &lPlugin, &lKind, &lSource, &lID, &parent,
			&pPlugin, &pKind, &pSource, &pID, &pSpin)
		if err != nil {
			return fmt.Errorf("backup series: %w", err)
		}
		if cur == nil || s.ID != curID {
			if err := flush(); err != nil {
				return err
			}
			curID = s.ID
			cur = backupSeriesOf(s, parent.String)
			if lPlugin.Valid {
				cur.Linked = &BackupItemRef{PluginID: lPlugin.String, Kind: lKind.String, Source: lSource.String, SourceID: lID.String}
			}
		}
		if pPlugin.Valid {
			cur.Parts = append(cur.Parts, BackupPartRef{PluginID: pPlugin.String, Kind: pKind.String, Source: pSource.String,
				SourceID: pID.String, SpinOff: pSpin.Bool})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup series: %w", err)
	}
	return flush()
}

// backupSeriesOf is the backup record of s without its parts, link and fields.
func backupSeriesOf(s *Series, parentUID string) *BackupSeries {
	bs := &BackupSeries{UID: s.UID, PluginID: s.PluginID, Kind: s.Kind, Title: s.Title,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Description: s.Description,
		Finished: s.Finished, PartsTotal: s.PartsTotal, Parent: parentUID, Parts: []BackupPartRef{}}
	if isRemote(s.CoverPath) {
		bs.CoverURL = s.CoverPath
	} else {
		bs.Cover = s.CoverPath
	}
	if s.StartedOn != nil {
		bs.StartedOn = s.StartedOn.Format(dateLayout)
	}
	if s.EndedOn != nil {
		bs.EndedOn = s.EndedOn.Format(dateLayout)
	}
	return bs
}

// Why a part of a restored series was left out (LeftOutPart.Reason).
const (
	LeftOutMissing   = "missing"
	LeftOutInSeries  = "in another series"
	LeftOutOtherKind = "other plugin or kind"
	LeftOutDuplicate = "duplicate"
)

// LeftOutPart is a part of a backup series that RestoreSeries could not
// restore: its reference, why, and the item's title when the item exists.
type LeftOutPart struct {
	Ref    BackupPartRef
	Reason string
	Title  string
}

// SeriesRestore reports one restored backup series. LeftOut lists the parts
// left out (also when the series was not created); Notes the values restored
// differently from the backup (only for a created series); Parent the
// parent uid that RestoreSeriesParent must resolve once every series of the
// backup is restored ("" for none).
//
// Empty marks a series created without parts and without own values: it is
// only worth keeping when a spin-off series of the backup names it as its
// parent, so the caller passes it to DeleteSeriesIfEmpty once every parent
// is set.
type SeriesRestore struct {
	Created bool
	LeftOut []LeftOutPart
	Notes   []string
	Parent  string
	Empty   bool
}

// RestoreSeries merges one series of a backup in a single transaction. A
// series whose uid exists is skipped (not created, nothing left out). The
// parts are resolved by identity and keep their block (main or spin-off) in
// backup order; a part whose item is missing, has no entry, is of another
// plugin or kind than the series, is listed twice, or already belongs to a
// series is left out (named in LeftOut with the reason). A series none of
// whose archived parts resolves is not created unless it holds own values;
// one archived without parts (kept for its data or its spin-offs) is. The
// uid, title and stamps of the backup are kept; an invalid title, uid or
// kind is ErrInvalid.
//
// The values are checked one by one with the rules of UpdateSeriesDetails
// against the installed kind; a value that fails, a linked entry that is
// missing or refused, and a cover that cannot be restored (opts.ResolveCover
// as for RestoreItem) are left empty with a note, the rest is restored. The
// parent is not set here (see SeriesRestore.Parent).
func (l *Library) RestoreSeries(ctx context.Context, bs BackupSeries, opts RestoreOptions) (SeriesRestore, error) {
	var res SeriesRestore
	title, err := cleanSeriesTitle(bs.Title)
	if err != nil {
		return res, err
	}
	if bs.UID == "" || len(bs.UID) > 100 || bs.PluginID == "" || bs.Kind == "" {
		return res, invalid("series without a uid, plugin or kind")
	}
	if err := cleanText("series uid", bs.UID, singleLine); err != nil {
		return res, err
	}
	now := time.Now().UTC()
	if bs.CreatedAt.IsZero() {
		bs.CreatedAt = now
	}
	if bs.UpdatedAt.IsZero() {
		bs.UpdatedAt = bs.CreatedAt
	}
	kind, kindErr := l.kindDef(bs.PluginID, bs.Kind)
	d, fieldRows, notes := restoreSeriesValues(kind, kindErr, title, bs)
	parent, parentNote := restoreParentUID(bs.Parent)
	if parentNote != "" {
		notes = append(notes, parentNote)
	}
	// an existing uid is skipped before anything is stored
	var n int
	if err := l.db.QueryRowContext(ctx, `SELECT count(*) FROM series WHERE uid = ?`, bs.UID).Scan(&n); err != nil {
		return res, err
	}
	if n > 0 {
		return res, nil
	}
	stored := ""
	committed := false
	defer func() {
		if stored != "" && !committed {
			l.releaseCover(ctx, opts, stored)
		}
	}()
	// the cover is stored before the write transaction, like RestoreItem
	if kindErr == nil && bs.Cover != "" {
		var note string
		if stored, note, err = restoreSeriesCover(ctx, opts, bs.Cover); err != nil {
			return res, err
		}
		if stored != "" {
			d.CoverPath = stored
		} else if note != "" {
			notes = append(notes, note)
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM series WHERE uid = ?`, bs.UID).Scan(&n); err != nil {
		return res, err
	}
	if n > 0 {
		return res, nil
	}
	var parts []SeriesPart
	taken := map[int64]bool{}
	for _, ref := range bs.Parts {
		if ref.PluginID != bs.PluginID || ref.Kind != bs.Kind {
			res.LeftOut = append(res.LeftOut, LeftOutPart{Ref: ref, Reason: LeftOutOtherKind})
			continue
		}
		var id int64
		var inSeries bool
		var itemTitle sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT i.id, EXISTS (SELECT 1 FROM series_parts p WHERE p.item_id = i.id),
			(SELECT t.text FROM item_titles t WHERE t.item_id = i.id ORDER BY t.lang, t.text LIMIT 1)
			FROM items i JOIN `+tableEntries+` e ON e.item_id = i.id
			WHERE i.plugin_id = ? AND i.kind = ? AND i.source = ? AND i.source_id = ?`,
			ref.PluginID, ref.Kind, ref.Source, ref.SourceID).Scan(&id, &inSeries, &itemTitle)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res.LeftOut = append(res.LeftOut, LeftOutPart{Ref: ref, Reason: LeftOutMissing})
			continue
		case err != nil:
			return res, err
		case taken[id]:
			res.LeftOut = append(res.LeftOut, LeftOutPart{Ref: ref, Reason: LeftOutDuplicate, Title: itemTitle.String})
			continue
		case inSeries:
			res.LeftOut = append(res.LeftOut, LeftOutPart{Ref: ref, Reason: LeftOutInSeries, Title: itemTitle.String})
			continue
		}
		taken[id] = true
		parts = append(parts, SeriesPart{ItemID: id, SpinOff: ref.SpinOff})
	}
	if kindErr == nil && bs.Linked != nil {
		id, note, err := restoreSeriesLink(ctx, tx, bs.PluginID, kind, *bs.Linked)
		if err != nil {
			return res, err
		}
		d.LinkedItemID = id
		if note != "" {
			notes = append(notes, note)
		}
	}
	// a series whose parts all failed is kept only for its own data (R6); one
	// archived without parts was kept for its data or its spin-offs (Empty)
	if len(parts) == 0 && len(bs.Parts) > 0 && !d.holdsOwn() {
		return res, nil
	}
	res.Empty = len(parts) == 0 && !d.holdsOwn()
	ins, err := tx.ExecContext(ctx, `INSERT INTO series(uid, plugin_id, kind, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		bs.UID, bs.PluginID, bs.Kind, title, bs.CreatedAt.UTC().Format(time.RFC3339), bs.UpdatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return res, err
	}
	sid, err := ins.LastInsertId()
	if err != nil {
		return res, err
	}
	if err := writeSeriesValues(ctx, tx, sid, d, fieldRows); err != nil {
		return res, err
	}
	pos := map[bool]int{}
	for _, p := range parts {
		pos[p.SpinOff]++
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_parts(series_id, item_id, position, spin_off) VALUES (?, ?, ?, ?)`,
			sid, p.ItemID, pos[p.SpinOff], p.SpinOff); err != nil {
			return res, mapPartInsertErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	committed = true
	res.Created, res.Notes, res.Parent = true, notes, parent
	return res, nil
}

// restoreSeriesValues returns the values of bs that pass
// prepareSeriesDetails one by one (with the field rows to store) and a note
// for every value left out. Without an installed kind only the title and
// the finished marker are restored.
func restoreSeriesValues(kind plugin.KindDef, kindErr error, title string, bs BackupSeries) (SeriesDetails, []fieldRow, []string) {
	d := SeriesDetails{Title: title, Finished: bs.Finished}
	var notes []string
	if kindErr != nil {
		if bs.Cover != "" || bs.CoverURL != "" || bs.Description != "" || bs.StartedOn != "" || bs.EndedOn != "" ||
			bs.PartsTotal != 0 || len(bs.Fields) > 0 || bs.Linked != nil {
			notes = append(notes, "the series data is not restored: "+noteReason(kindErr))
		}
		return d, nil, notes
	}
	try := func(what string, set func(*SeriesDetails)) {
		t := d
		set(&t)
		if _, err := prepareSeriesDetails(kind, &t); err != nil {
			notes = append(notes, what+" is not restored: "+noteReason(err))
			return
		}
		d = t
	}
	if bs.Description != "" {
		try("the description", func(t *SeriesDetails) { t.Description = bs.Description })
	}
	for _, v := range []struct {
		name, value string
		set         func(*SeriesDetails, *time.Time)
	}{
		{"started_on", bs.StartedOn, func(t *SeriesDetails, v *time.Time) { t.StartedOn = v }},
		{"ended_on", bs.EndedOn, func(t *SeriesDetails, v *time.Time) { t.EndedOn = v }},
	} {
		if v.value == "" {
			continue
		}
		day, err := time.Parse(dateLayout, v.value)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s is not restored: %q is not a calendar day (YYYY-MM-DD)", v.name, v.value))
			continue
		}
		try(v.name, func(t *SeriesDetails) { v.set(t, &day) })
	}
	if bs.PartsTotal != 0 {
		try("parts_total", func(t *SeriesDetails) { t.PartsTotal = bs.PartsTotal })
	}
	if bs.Cover == "" && bs.CoverURL != "" {
		try("the cover", func(t *SeriesDetails) { t.CoverPath = bs.CoverURL })
	}
	keys := make([]string, 0, len(bs.Fields))
	for k := range bs.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	accepted := map[string]plugin.FieldValue{}
	for _, k := range keys {
		t := d
		t.Fields = map[string]plugin.FieldValue{k: bs.Fields[k]}
		if _, err := prepareSeriesDetails(kind, &t); err != nil {
			notes = append(notes, fmt.Sprintf("the field %q is not restored: %s", k, noteReason(err)))
			continue
		}
		accepted[k] = bs.Fields[k]
	}
	if len(accepted) > 0 {
		d.Fields = accepted
	}
	rows, err := prepareSeriesDetails(kind, &d)
	if err != nil { // the accepted fields together fail
		notes = append(notes, "the fields are not restored: "+noteReason(err))
		d.Fields = nil
		if rows, err = prepareSeriesDetails(kind, &d); err != nil {
			return SeriesDetails{Title: title, Finished: bs.Finished}, nil, append(notes, "the series data is not restored: "+noteReason(err))
		}
	}
	return d, rows, notes
}

// noteReason is err as the reason of a restore note: without the "invalid: "
// prefix of ErrInvalid, like the web's messages.
func noteReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
}

// restoreParentUID returns the parent uid to resolve later, or a note when
// it cannot be a uid.
func restoreParentUID(uid string) (string, string) {
	if uid == "" {
		return "", ""
	}
	if len(uid) > 100 || cleanText("parent", uid, singleLine) != nil {
		return "", "the parent series is not restored: its uid is not valid"
	}
	return uid, ""
}

// restoreSeriesLink resolves the linked entry by identity and checks it like
// UpdateSeriesDetails; a missing or refused entry is 0 with a note.
func restoreSeriesLink(ctx context.Context, tx *sql.Tx, pluginID string, kind plugin.KindDef, ref BackupItemRef) (int64, string, error) {
	what := "the linked entry " + ref.Source + ":" + ref.SourceID
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE plugin_id = ? AND kind = ? AND source = ? AND source_id = ?`,
		ref.PluginID, ref.Kind, ref.Source, ref.SourceID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, what + " is not in the library: restored without the link", nil
	case err != nil:
		return 0, "", err
	}
	if err := checkSeriesLink(ctx, tx, pluginID, kind, id, true); err != nil {
		if errors.Is(err, ErrInvalid) {
			return 0, what + " is not restored: " + noteReason(err), nil
		}
		return 0, "", err
	}
	return id, "", nil
}

// restoreSeriesCover stores the backup's local cover name through
// opts.ResolveCover like RestoreItem; "" with a note when it cannot be
// restored.
func restoreSeriesCover(ctx context.Context, opts RestoreOptions, name string) (string, string, error) {
	if !ValidFileName(name) {
		return "", "the cover is not restored: it is not a plain file name", nil
	}
	if opts.ResolveCover == nil {
		return "", "the cover file is not part of the backup: restored without a cover", nil
	}
	stored, note, err := opts.ResolveCover(ctx, name)
	if err != nil {
		return "", "", err
	}
	if stored == "" || checkImageRef("cover", stored) != nil {
		if note == "" {
			note = "the cover is not restored"
		}
		return "", note, nil
	}
	return stored, "", nil
}

// DeleteSeriesIfEmpty deletes the series uid of the plugin when it has no
// parts, holds nothing of its own and has no spin-off series, the rule of
// renumberSeries, and reports whether it did.
func (l *Library) DeleteSeriesIfEmpty(ctx context.Context, pluginID, uid string) (bool, error) {
	var id int64
	switch err := l.db.QueryRowContext(ctx, `SELECT id FROM series WHERE plugin_id = ? AND uid = ?
		AND NOT EXISTS (SELECT 1 FROM series_parts p WHERE p.series_id = series.id)`, pluginID, uid).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("delete empty series: %w", err)
	}
	res, err := l.db.ExecContext(ctx, deleteEmptySeriesSQL+` AND NOT EXISTS (SELECT 1 FROM series_parts p WHERE p.series_id = ?1)`, id)
	if err != nil {
		return false, fmt.Errorf("delete empty series: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RestoreSeriesParent sets the parent of the restored series uid (of the
// plugin) to the series parentUID, checked like UpdateSeriesDetails. It runs
// once every series of a backup is restored. A parent that is missing or
// refused leaves the series without a parent and returns a note; a series
// that does not exist or already has a parent is left alone ("", nil).
func (l *Library) RestoreSeriesParent(ctx context.Context, pluginID, uid, parentUID string) (string, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id int64
	var has sql.NullInt64
	switch err := tx.QueryRowContext(ctx, `SELECT id, parent_id FROM series WHERE plugin_id = ? AND uid = ?`, pluginID, uid).Scan(&id, &has); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	}
	if has.Valid {
		return "", nil
	}
	what := "the parent series " + parentUID
	var pid int64
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE uid = ?`, parentUID).Scan(&pid); {
	case errors.Is(err, sql.ErrNoRows):
		return what + " is not in the library: restored without a parent", nil
	case err != nil:
		return "", err
	}
	if err := checkSeriesParent(ctx, tx, id, pluginID, pid); err != nil {
		if errors.Is(err, ErrInvalid) {
			return what + " is not restored: " + noteReason(err), nil
		}
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE series SET parent_id = ? WHERE id = ?`, pid, id); err != nil {
		return "", err
	}
	return "", tx.Commit()
}

// BackupList is one custom list of a backup: its name, order mode, creation
// stamp and entries in position order, identified like items everywhere in
// the backup (no numeric ids).
type BackupList struct {
	Name        string           `json:"name"`
	ManualOrder bool             `json:"manual_order"`
	CreatedAt   string           `json:"created_at,omitempty"` // RFC 3339 UTC
	Items       []BackupListItem `json:"items"`
}

// BackupListItem is one place of a backed-up list.
type BackupListItem struct {
	BackupPartRef
	Position int    `json:"position"`
	AddedAt  string `json:"added_at,omitempty"` // RFC 3339 UTC
}

// BackupLists calls fn for every custom list (ordered by name key) with its
// entries in position order, streamed from one query.
func (l *Library) BackupLists(ctx context.Context, fn func(BackupList) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT c.id, c.name, c.manual_order, c.created_at,
		i.plugin_id, i.kind, i.source, i.source_id, li.position, li.added_at
		FROM lists c LEFT JOIN list_items li ON li.list_id = c.id LEFT JOIN items i ON i.id = li.item_id
		ORDER BY c.name_key, c.id, li.position`)
	if err != nil {
		return fmt.Errorf("backup lists: %w", err)
	}
	defer rows.Close()
	var cur *BackupList
	var curID int64
	for rows.Next() {
		var id int64
		var bl BackupList
		var pluginID, kind, source, sourceID, added sql.NullString
		var pos sql.NullInt64
		if err := rows.Scan(&id, &bl.Name, &bl.ManualOrder, &bl.CreatedAt, &pluginID, &kind, &source, &sourceID, &pos, &added); err != nil {
			return fmt.Errorf("backup lists: %w", err)
		}
		if cur == nil || id != curID {
			if cur != nil {
				if err := fn(*cur); err != nil {
					return err
				}
			}
			curID, bl.Items = id, []BackupListItem{}
			cur = &bl
		}
		if pluginID.Valid {
			cur.Items = append(cur.Items, BackupListItem{BackupPartRef: BackupPartRef{PluginID: pluginID.String, Kind: kind.String,
				Source: source.String, SourceID: sourceID.String}, Position: int(pos.Int64), AddedAt: added.String})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup lists: %w", err)
	}
	if cur != nil {
		return fn(*cur)
	}
	return nil
}

// RestoreList merges one list of a backup in a single transaction. A list
// whose name exists (ignoring case) keeps its name, order mode and places;
// the backup's entries it lacks are added at its end. A missing list is
// created with the backup's name, order mode and creation stamp. Entries are
// taken in the backup's position order and resolved by identity; an entry
// whose item is missing or has no entry is left out (counted in leftOut),
// one listed twice or in the list already is passed over. added counts the
// places written. An invalid name or stamp is ErrInvalid (nothing written).
func (l *Library) RestoreList(ctx context.Context, bl BackupList) (added int, leftOut int, err error) {
	name, key, err := listName(bl.Name)
	if err != nil {
		return 0, 0, err
	}
	created, err := restoreStamp("list created_at", bl.CreatedAt)
	if err != nil {
		return 0, 0, err
	}
	places := slices.Clone(bl.Items)
	slices.SortStableFunc(places, func(a, b BackupListItem) int { return a.Position - b.Position })
	stamps := make([]string, len(places))
	for i, p := range places {
		if stamps[i], err = restoreStamp("list entry added_at", p.AddedAt); err != nil {
			return 0, 0, err
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("restore list: %w", err)
	}
	defer tx.Rollback()
	var listID int64
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM lists WHERE name_key = ?`, key).Scan(&listID); {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `INSERT INTO lists(name, name_key, manual_order, created_at, updated_at)
			VALUES (?1, ?2, ?3, COALESCE(?4, `+nowSQL+`), COALESCE(?4, `+nowSQL+`))`, name, key, bl.ManualOrder, orNow(created))
		if err != nil {
			return 0, 0, fmt.Errorf("restore list: %w", err)
		}
		if listID, err = res.LastInsertId(); err != nil {
			return 0, 0, fmt.Errorf("restore list: %w", err)
		}
	case err != nil:
		return 0, 0, fmt.Errorf("restore list: %w", err)
	}
	for i, p := range places {
		var itemID int64
		var in bool
		err := tx.QueryRowContext(ctx, `SELECT i.id, EXISTS (SELECT 1 FROM list_items x WHERE x.list_id = ? AND x.item_id = i.id)
			FROM items i JOIN `+tableEntries+` e ON e.item_id = i.id
			WHERE i.plugin_id = ? AND i.kind = ? AND i.source = ? AND i.source_id = ?`,
			listID, p.PluginID, p.Kind, p.Source, p.SourceID).Scan(&itemID, &in)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			leftOut++
			continue
		case err != nil:
			return 0, 0, fmt.Errorf("restore list: %w", err)
		case in:
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO list_items(list_id, item_id, position, added_at)
			SELECT ?1, ?2, COALESCE((SELECT MAX(position) FROM list_items WHERE list_id = ?1), 0) + 1, COALESCE(?3, `+nowSQL+`)`,
			listID, itemID, orNow(stamps[i])); err != nil {
			return 0, 0, fmt.Errorf("restore list: %w", err)
		}
		added++
	}
	if added > 0 {
		if err := touchList(ctx, tx, listID, ""); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("restore list: %w", err)
	}
	return added, leftOut, nil
}

// BackupAlias is a merged-away identity of a backup: the identity of the item
// that kept it (Item) and the identity it stands for (Alias).
type BackupAlias struct {
	Item  BackupPartRef `json:"item"`
	Alias BackupPartRef `json:"alias"`
}

// BackupDismissedPair is a pair of items the duplicate finder must not offer
// again, both by identity.
type BackupDismissedPair struct {
	A           BackupPartRef `json:"a"`
	B           BackupPartRef `json:"b"`
	DismissedAt string        `json:"dismissed_at"` // RFC 3339 UTC
}

// What RestoreAlias and RestoreDismissedPair did with a row.
type RestoreMerge int

// The outcomes of RestoreAlias and RestoreDismissedPair.
const (
	MergeAdded   RestoreMerge = iota // written
	MergeSkipped                     // present already
	MergeLeftOut                     // its items are not in the library
)

// BackupAliases calls fn for every alias (ordered by its identity) with the
// identity of the item it belongs to.
func (l *Library) BackupAliases(ctx context.Context, fn func(BackupAlias) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT i.plugin_id, i.kind, i.source, i.source_id, a.plugin_id, a.kind, a.source, a.source_id
		FROM item_aliases a JOIN items i ON i.id = a.item_id ORDER BY a.plugin_id, a.kind, a.source, a.source_id`)
	if err != nil {
		return fmt.Errorf("backup aliases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ba BackupAlias
		if err := rows.Scan(&ba.Item.PluginID, &ba.Item.Kind, &ba.Item.Source, &ba.Item.SourceID,
			&ba.Alias.PluginID, &ba.Alias.Kind, &ba.Alias.Source, &ba.Alias.SourceID); err != nil {
			return fmt.Errorf("backup aliases: %w", err)
		}
		if err := fn(ba); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup aliases: %w", err)
	}
	return nil
}

// BackupDismissedPairs calls fn for every dismissed duplicate pair (ordered
// by item ids) with both identities.
func (l *Library) BackupDismissedPairs(ctx context.Context, fn func(BackupDismissedPair) error) error {
	rows, err := l.db.QueryContext(ctx, `SELECT x.plugin_id, x.kind, x.source, x.source_id, y.plugin_id, y.kind, y.source, y.source_id, d.dismissed_at
		FROM dismissed_duplicates d JOIN items x ON x.id = d.item_a JOIN items y ON y.id = d.item_b ORDER BY d.item_a, d.item_b`)
	if err != nil {
		return fmt.Errorf("backup dismissed duplicates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bp BackupDismissedPair
		if err := rows.Scan(&bp.A.PluginID, &bp.A.Kind, &bp.A.Source, &bp.A.SourceID,
			&bp.B.PluginID, &bp.B.Kind, &bp.B.Source, &bp.B.SourceID, &bp.DismissedAt); err != nil {
			return fmt.Errorf("backup dismissed duplicates: %w", err)
		}
		if err := fn(bp); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup dismissed duplicates: %w", err)
	}
	return nil
}

// checkRef validates an identity that is written to the database: every
// part set, single-line text.
func checkRef(path string, r BackupPartRef) error {
	for _, v := range []struct{ name, s string }{{"plugin", r.PluginID}, {"kind", r.Kind}, {"source", r.Source}, {"source id", r.SourceID}} {
		if v.s == "" {
			return invalid("%s has no %s", path, v.name)
		}
		if err := cleanText(path+" "+v.name, v.s, singleLine); err != nil {
			return err
		}
	}
	return nil
}

// RestoreAlias merges one alias of a backup. Its survivor is resolved by
// identity (an item, or the item that identity is an alias of); a missing
// survivor leaves it out. An alias identity that is an item or an alias
// already is skipped. The alias must be of the survivor's plugin and kind,
// and a manual identity is never an alias (ErrInvalid otherwise).
func (l *Library) RestoreAlias(ctx context.Context, ba BackupAlias) (RestoreMerge, error) {
	if err := checkRef("alias", ba.Alias); err != nil {
		return 0, err
	}
	if err := checkRef("alias item", ba.Item); err != nil {
		return 0, err
	}
	if ba.Alias.PluginID != ba.Item.PluginID || ba.Alias.Kind != ba.Item.Kind {
		return 0, invalid("an alias must be of its item's plugin and kind")
	}
	if ba.Alias.Source == manualSource {
		return 0, invalid("a manual identity is not an alias")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("restore alias: %w", err)
	}
	defer tx.Rollback()
	a := ba.Alias
	taken, err := identityOwner(ctx, tx, a.PluginID, a.Kind, a.Source, a.SourceID)
	if err != nil {
		return 0, fmt.Errorf("restore alias: %w", err)
	}
	if taken != 0 {
		return MergeSkipped, nil
	}
	owner, err := identityOwner(ctx, tx, ba.Item.PluginID, ba.Item.Kind, ba.Item.Source, ba.Item.SourceID)
	if err != nil {
		return 0, fmt.Errorf("restore alias: %w", err)
	}
	if owner == 0 {
		return MergeLeftOut, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_aliases(item_id, plugin_id, kind, source, source_id) VALUES (?,?,?,?,?)`,
		owner, a.PluginID, a.Kind, a.Source, a.SourceID); err != nil {
		return 0, fmt.Errorf("restore alias: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("restore alias: %w", err)
	}
	return MergeAdded, nil
}

// RestoreDismissedPair merges one dismissed duplicate pair of a backup. Both
// items are resolved by identity (an item, or the item that identity is an
// alias of); a pair with a missing item, or whose identities resolve to one
// item, is left out, and one present already is skipped. An invalid identity
// or stamp is ErrInvalid.
func (l *Library) RestoreDismissedPair(ctx context.Context, bp BackupDismissedPair) (RestoreMerge, error) {
	if err := checkRef("dismissed pair", bp.A); err != nil {
		return 0, err
	}
	if err := checkRef("dismissed pair", bp.B); err != nil {
		return 0, err
	}
	stamp, err := restoreStamp("dismissed_at", bp.DismissedAt)
	if err != nil {
		return 0, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("restore dismissed pair: %w", err)
	}
	defer tx.Rollback()
	var ids [2]int64
	for i, r := range []BackupPartRef{bp.A, bp.B} {
		if ids[i], err = identityOwner(ctx, tx, r.PluginID, r.Kind, r.Source, r.SourceID); err != nil {
			return 0, fmt.Errorf("restore dismissed pair: %w", err)
		}
	}
	a, b := min(ids[0], ids[1]), max(ids[0], ids[1])
	if a == 0 || a == b {
		return MergeLeftOut, nil
	}
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO dismissed_duplicates(item_a, item_b, dismissed_at)
		VALUES (?1, ?2, COALESCE(?3, `+nowSQL+`))`, a, b, orNow(stamp))
	if err != nil {
		return 0, fmt.Errorf("restore dismissed pair: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("restore dismissed pair: %w", err)
	}
	if n == 0 {
		return MergeSkipped, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("restore dismissed pair: %w", err)
	}
	return MergeAdded, nil
}
