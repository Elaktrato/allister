// Package backup writes and restores the universal backup archive: a zip
// with backup.json (every item of every plugin, enabled or not, with its
// parts and list entry) and covers/<name> for every local item cover.
//
// Archive format (format "allister.backup", format_version 3):
//
//	backup.json  {"format": "allister.backup", "format_version": 3,
//	              "created_at": "<RFC 3339 UTC>",
//	              "plugins": [{"id": ..., "version": ...}],   (informational)
//	              "appearance": {...},                        (optional)
//	              "time_zone": "Europe/Berlin",               (optional)
//	              "items": [library.BackupItem, ...],
//	              "series": [library.BackupSeries, ...],      (optional, after items)
//	              "lists": [library.BackupList, ...],         (optional, after items)
//	              "aliases": [library.BackupAlias, ...],      (optional, after items)
//	              "dismissed_duplicates": [library.BackupDismissedPair, ...]}
//	                                                          (optional, after items)
//	covers/<name>  the file of every item whose cover is a local file
//	fonts/<name>   the file of every uploaded font
//
// The appearance member (see appearance.go) holds the appearance settings
// and the list of uploaded fonts; it and the fonts/ entries are ignored by
// a restore of an older Allister (an unknown member, unread entries). The
// member may come before the header: it is
// held (within Limits.MaxMemberBytes) and applied only after "format" and
// "format_version" were accepted, so a refused archive stores nothing.
//
// A remote cover URL is not retained once a cover has been downloaded, so
// every local item cover is included (not only uploaded ones). Person photos
// are not included: the sync after a restore fetches credits and photos
// again. An item whose local cover file is missing at backup time carries
// "cover": "" and "cover_missing": true.
//
// Format 2 changed the item's entry: notes, runs (each with its progress,
// unit notes and change log), own tags and orphaned events instead of one
// state with a comment (library.BackupEntry). A restore reads format 1 too:
// its entry (library.BackupEntryV1) becomes run 1, the comment the notes.
//
// Format 3 added the event origin "bulk" (a change made for several items at
// once); an older Allister refuses such an event, so it refuses the format
// instead of dropping those items. Format 3 also carries the aliases
// (identities kept by a merge), the dismissed duplicate pairs and the
// covers' origins; a backup without them restores as before. A restore
// reads formats 1, 2 and 3.
//
// In backup.json "format" and "format_version" must come before "items",
// "series", "lists", "aliases" and "dismissed_duplicates" after it, and each
// of these seven members appears once; a restore
// refuses anything else (before restoring an item where it can be detected
// that early). One item's JSON is limited to Limits.MaxItemBytes.
//
// Before the archive is opened its central directory is walked the way
// archive/zip will read it, and an archive with more entries than
// Limits.MaxEntries, or whose directory does not match its end record, is
// refused without loading it.
//
// A restore MERGES (see library.RestoreItem): existing items are skipped,
// nothing is overwritten or deleted. Only backup.json and covers/<name>
// entries are read; names must satisfy library.ValidFileName. Nothing is
// ever extracted by its archive name: cover bytes go through covers.Save,
// which sniffs the type and stores them under their content hash.
package backup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/allister/allister/internal/appearance"
	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
)

const (
	// Format is the value of backup.json's "format" member.
	Format = "allister.backup"
	// FormatVersion is the backup format version this code writes. A
	// restore reads it and every older version (MinFormatVersion on).
	FormatVersion = 3
	// MinFormatVersion is the oldest format version a restore reads.
	MinFormatVersion = 1
	// MaxListed caps every row list of a Report.
	MaxListed = 200

	jsonName   = "backup.json"
	coversDirZ = "covers/"
	fontsDirZ  = "fonts/"
	batchSize  = 200
)

// PluginVersion is one installed plugin listed in backup.json
// (informational only: a restore never depends on it).
type PluginVersion struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Limits bounds what a restore reads. A zero field means its default.
type Limits struct {
	// MaxJSONBytes caps the uncompressed backup.json (default 256 MiB).
	MaxJSONBytes int64
	// MaxTotalBytes caps all uncompressed bytes read from the archive
	// (default 2 GiB): the zip bomb guard.
	MaxTotalBytes int64
	// MaxEntries caps the number of archive entries (default 200,000).
	MaxEntries int
	// MaxItems caps the number of items in backup.json (default 100,000).
	MaxItems int
	// MaxItemBytes caps one item's JSON (default 4 MiB).
	MaxItemBytes int64
	// MaxMemberBytes caps one unknown top-level member of backup.json
	// (default 1 MiB).
	MaxMemberBytes int64
}

// DefaultLimits returns the production limits.
func DefaultLimits() Limits {
	return Limits{MaxJSONBytes: 256 << 20, MaxTotalBytes: 2 << 30, MaxEntries: 200_000, MaxItems: 100_000, MaxItemBytes: 4 << 20, MaxMemberBytes: 1 << 20}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxJSONBytes <= 0 {
		l.MaxJSONBytes = d.MaxJSONBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = d.MaxEntries
	}
	if l.MaxItems <= 0 {
		l.MaxItems = d.MaxItems
	}
	if l.MaxItemBytes <= 0 {
		l.MaxItemBytes = d.MaxItemBytes
	}
	if l.MaxMemberBytes <= 0 {
		l.MaxMemberBytes = d.MaxMemberBytes
	}
	return l
}

// Write streams the backup of every item in lib as a zip to w: backup.json
// (deflated, written item by item in batches, never held in memory) followed
// by covers/<name> (stored: images are already compressed) for every local
// cover file found in coversDir, then fonts/<name> for every uploaded font
// of app (nil: no appearance member and no fonts). timeZone, when not empty,
// is the stored time zone setting ("time_zone" member). A local cover whose
// name is not acceptable or whose file is missing is written as
// "cover": "" with "cover_missing": true. A write error stops the stream
// and is returned (the caller has usually sent headers already and can
// only log it).
func Write(ctx context.Context, w io.Writer, lib *library.Library, plugins []PluginVersion, coversDir string, app *appearance.Service, timeZone string, now time.Time) error {
	zw := zip.NewWriter(w)
	jw, err := zw.CreateHeader(&zip.FileHeader{Name: jsonName, Method: zip.Deflate, Modified: now.UTC()})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	bw := bufio.NewWriterSize(jw, 64<<10)
	if plugins == nil {
		plugins = []PluginVersion{}
	}
	look, fonts := appearanceOf(ctx, app)
	head, err := json.Marshal(struct {
		Format        string            `json:"format"`
		FormatVersion int               `json:"format_version"`
		CreatedAt     string            `json:"created_at"`
		Plugins       []PluginVersion   `json:"plugins"`
		Appearance    *appearanceMember `json:"appearance,omitempty"`
		TimeZone      string            `json:"time_zone,omitempty"`
	}{Format, FormatVersion, now.UTC().Format(time.RFC3339), plugins, look, timeZone})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	// the surrounding object is written by hand so items can stream
	// bufio errors are sticky: the Flush below reports a failed write
	_, _ = bw.Write(head[:len(head)-1])
	_, _ = bw.WriteString(`,"items":[`)
	enc := json.NewEncoder(bw)
	var names []string
	seen := map[string]bool{}
	first := true
	err = lib.BackupItems(ctx, batchSize, func(bi library.BackupItem) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bi.Cover != "" {
			if coverFile(coversDir, bi.Cover) {
				if !seen[bi.Cover] {
					seen[bi.Cover] = true
					names = append(names, bi.Cover)
				}
			} else {
				bi.Cover, bi.CoverSource, bi.CoverMissing = "", "", true
			}
		}
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(bi)
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = bw.WriteString(`],"series":[`)
	first = true
	err = lib.BackupSeries(ctx, func(bs library.BackupSeries) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(bs)
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = bw.WriteString(`],"lists":[`)
	first = true
	err = lib.BackupLists(ctx, func(bl library.BackupList) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(bl)
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = bw.WriteString(`],"aliases":[`)
	first = true
	err = lib.BackupAliases(ctx, func(ba library.BackupAlias) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(ba)
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = bw.WriteString(`],"dismissed_duplicates":[`)
	first = true
	err = lib.BackupDismissedPairs(ctx, func(bp library.BackupDismissedPair) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !first {
			if _, err := bw.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(bp)
	})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = bw.WriteString("]}\n") // sticky, reported by Flush
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if err := writeCover(zw, coversDir, name, now); err != nil {
			return err
		}
	}
	if err := writeFonts(ctx, zw, app, fonts, now); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// coverFile reports whether name is an acceptable cover name with a regular
// file (not a symlink) in coversDir.
func coverFile(coversDir, name string) bool {
	if coversDir == "" || !library.ValidFileName(name) {
		return false
	}
	fi, err := os.Lstat(filepath.Join(coversDir, name))
	return err == nil && fi.Mode().IsRegular()
}

// writeCover adds covers/<name>. A file that vanished since it was checked
// is skipped with a log line (the restore then notes the missing cover).
func writeCover(zw *zip.Writer, coversDir, name string, now time.Time) error {
	f, err := os.Open(filepath.Join(coversDir, name)) //nolint:gosec // G304: name passed coverFile, a plain file name inside coversDir
	if err != nil {
		log.Printf("backup: cover %s skipped: %v", name, err)
		return nil
	}
	defer f.Close()
	cw, err := zw.CreateHeader(&zip.FileHeader{Name: coversDirZ + name, Method: zip.Store, Modified: now.UTC()})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := io.Copy(cw, f); err != nil {
		return fmt.Errorf("backup: cover %s: %w", name, err)
	}
	return nil
}

// RejectError reports an archive that was refused before anything was
// restored. Its message is meant for the user.
type RejectError struct{ Msg string }

func (e *RejectError) Error() string { return e.Msg }

func reject(format string, a ...any) error { return &RejectError{Msg: fmt.Sprintf(format, a...)} }

const notBackup = "This file is not an Allister backup"

// Options configures Restore.
type Options struct {
	Library *library.Library
	// CoversDir is DATA_DIR/covers, where restored covers are stored under
	// their content hash. When empty, covers are not restored (noted).
	CoversDir string
	Limits    Limits
	// Enqueue is called for every created item whose sync state is
	// pending, so the caller can queue its sync job.
	Enqueue func(ctx context.Context, itemID int64, pluginID string)
	// Appearance restores the appearance member and the uploaded fonts;
	// when nil they are skipped (noted).
	Appearance *appearance.Service
	// TimeZone restores the time zone setting; when nil it is skipped.
	TimeZone TimeZoneStore
}

// TimeZoneStore is the stored time zone setting a restore may fill.
type TimeZoneStore interface {
	StoredTimeZone(ctx context.Context) (string, error)
	SetTimeZone(ctx context.Context, name string) error
}

// Row identifies one backup item in a report. Index is its 1-based position
// in backup.json. Every text is cleaned (no control characters) and
// shortened, because it comes from the uploaded file.
type Row struct {
	Index                            int
	PluginID, Kind, Source, SourceID string
	Title, Reason                    string
}

// Report summarises a restore. The row lists hold at most MaxListed rows;
// the More counters tell how many were left out. Notes are created items
// restored differently from the backup (a cover left empty). Ignored counts
// archive entries that are not read (only backup.json and covers/<name>
// are). Aborted is set when the restore stopped part way (damaged or
// oversized backup.json): the items before that point were restored, each
// in its own transaction.
type Report struct {
	Created, Skipped, Failed, UnknownPlugin int
	FailedRows, UnknownRows, SkippedRows    []Row
	MoreFailed, MoreUnknown, MoreSkipped    int
	Notes                                   []Row
	MoreNotes                               int
	// SeriesCreated and SeriesSkipped count the series of the backup that
	// were created or left alone (the uid exists, or the series is invalid
	// or has no part that can be restored); parts left out are Notes.
	SeriesCreated, SeriesSkipped int
	// Lists counts the custom lists of the backup that were restored
	// (created, or merged into the list of the same name); ListItemsAdded the
	// entries added to them and ListItemsLeftOut those whose item is not in
	// the library (named per list in Notes, like lists that are not valid).
	Lists, ListItemsAdded, ListItemsLeftOut int
	// Aliases counts the merged-away identities of the backup that were
	// added; AliasesSkipped those whose identity is an item or an alias
	// already, AliasesLeftOut those whose survivor is not in the library.
	Aliases, AliasesSkipped, AliasesLeftOut int
	// Dismissed counts the dismissed duplicate pairs that were added;
	// DismissedSkipped those present already, DismissedLeftOut those whose
	// items are not both in the library (or are one item).
	Dismissed, DismissedSkipped, DismissedLeftOut int
	Ignored                                       int
	Aborted                                       string
	// Appearance notes what the appearance member restored or skipped
	// (cleaned like the rows).
	Appearance []string
}

func addRow(list *[]Row, more *int, r Row) {
	if len(*list) < MaxListed {
		*list = append(*list, r)
	} else {
		*more++
	}
}

var (
	errJSONTooLarge   = errors.New("backup.json too large")
	errTotalTooLarge  = errors.New("archive too large")
	errItemTooLarge   = errors.New("item too large")
	errMemberTooLarge = errors.New("member too large")
	// errStopped unwinds the decoder after abort recorded Report.Aborted.
	errStopped = errors.New("restore stopped")
)

// budget counts the uncompressed bytes read from the archive.
type budget struct{ left int64 }

type budgetReader struct {
	r io.Reader
	b *budget
}

func (br budgetReader) Read(p []byte) (int, error) {
	n, err := br.r.Read(p)
	br.b.left -= int64(n)
	if br.b.left < 0 {
		return n, errTotalTooLarge
	}
	return n, err
}

// capReader fails once more than left bytes were read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, errJSONTooLarge
	}
	return n, err
}

// restorer holds the state of one restore.
type restorer struct {
	opts    Options
	lim     Limits
	covers  map[string]*zip.File
	fonts   map[string]*zip.File
	looked  bool // the appearance member was handled
	zoned   bool // the time zone member was handled
	version int  // the accepted format version
	done    map[string]coverResult
	budget  *budget
	rep     *Report
	limiter *itemLimiter
}

// itemLimiter bounds the bytes the decoder may read while one item is being
// decoded: reads are shortened so the decoder never gets more than limit
// bytes in total (stream offset), and an item that does not end before the
// limit fails with errItemTooLarge. A negative limit means no limit.
type itemLimiter struct {
	r           io.Reader
	read, limit int64
	err         error // returned when the limit is reached
}

func (l *itemLimiter) Read(p []byte) (int, error) {
	if l.limit >= 0 {
		allowed := l.limit - l.read
		if allowed <= 0 {
			return 0, l.err
		}
		if int64(len(p)) > allowed {
			p = p[:allowed]
		}
	}
	n, err := l.r.Read(p)
	l.read += int64(n)
	return n, err
}

type coverResult struct{ stored, note string }

// Restore merges the backup archive in ra (size bytes) into the library.
// It returns a *RejectError when the archive is refused before anything was
// restored (not a zip, too many entries, no backup.json, wrong format or
// format version). A damaged or oversized backup.json found part way stops
// the restore with Report.Aborted set and a nil error. Other errors (the
// database, writing covers, ctx cancellation) are returned with the report
// of what was restored so far. Restore writes only into opts.CoversDir.
func Restore(ctx context.Context, ra io.ReaderAt, size int64, opts Options) (*Report, error) {
	rs := &restorer{opts: opts, lim: opts.Limits.withDefaults(), done: map[string]coverResult{}, rep: &Report{}}
	rs.budget = &budget{left: rs.lim.MaxTotalBytes}
	walked, declared, err := scanDirectory(ra, size, rs.lim.MaxEntries)
	switch {
	case errors.Is(err, errNotZip):
		return rs.rep, reject("%s (it is not a zip archive).", notBackup)
	case errors.Is(err, errPrefix):
		return rs.rep, reject("This file is not an Allister backup archive (unexpected data before the zip directory). Only archives written by Allister are accepted.")
	case err != nil:
		return rs.rep, err
	case walked > uint64(rs.lim.MaxEntries): //nolint:gosec // G115: MaxEntries is a positive limit
		return rs.rep, reject("The archive holds more than %d files: it is not an Allister backup.", rs.lim.MaxEntries)
	case walked != declared:
		return rs.rep, reject("The archive's file directory does not match its end record (%d entries, %d declared): it is not an Allister backup.", walked, declared)
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && zr != nil) {
		return rs.rep, reject("%s (it is not a zip archive).", notBackup)
	}
	if len(zr.File) > rs.lim.MaxEntries {
		return rs.rep, reject("The archive holds more than %d files: it is not an Allister backup.", rs.lim.MaxEntries)
	}
	jsonFile := rs.index(zr)
	if jsonFile == nil {
		return rs.rep, reject("%s: the archive has no backup.json.", notBackup)
	}
	rc, err := jsonFile.Open()
	if err != nil {
		return rs.rep, reject("%s: backup.json cannot be read.", notBackup)
	}
	defer rc.Close()
	rs.limiter = &itemLimiter{r: &capReader{r: budgetReader{r: rc, b: rs.budget}, left: rs.lim.MaxJSONBytes}, limit: -1}
	if err := rs.decode(ctx, json.NewDecoder(rs.limiter)); err != nil && !errors.Is(err, errStopped) {
		return rs.rep, err
	}
	return rs.rep, nil
}

// index maps the readable entries: the first regular backup.json, the
// first regular covers/<valid name> and fonts/<stored font name> of each
// name. Everything else (directories, symlinks, nested or unsafe names,
// duplicates) is ignored.
func (rs *restorer) index(zr *zip.Reader) *zip.File {
	var jsonFile *zip.File
	rs.covers = map[string]*zip.File{}
	rs.fonts = map[string]*zip.File{}
	for _, f := range zr.File {
		regular := f.Mode().IsRegular() && !strings.HasSuffix(f.Name, "/")
		name, isCover := strings.CutPrefix(f.Name, coversDirZ)
		font, isFont := strings.CutPrefix(f.Name, fontsDirZ)
		switch {
		case regular && f.Name == jsonName && jsonFile == nil:
			jsonFile = f
		case regular && isCover && library.ValidFileName(name) && rs.covers[name] == nil:
			rs.covers[name] = f
		case regular && isFont && appearance.IsStoredFontName(font) && rs.fonts[font] == nil:
			rs.fonts[font] = f
		default:
			rs.rep.Ignored++
		}
	}
	return jsonFile
}

// decode reads backup.json token by token: "format" and "format_version"
// must come before "items", "series", "lists", "aliases" and
// "dismissed_duplicates" after it (as Write puts them), and each of the
// seven may appear once; items are decoded one by one.
func (rs *restorer) decode(ctx context.Context, dec *json.Decoder) error {
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		if lim := rs.limitMessage(err); lim != "" {
			return reject("%s", lim)
		}
		return reject("%s: backup.json is not a JSON object.", notBackup)
	}
	var format string
	version, haveVersion := "", false
	header := func() error {
		if format != Format {
			return reject("%s.", notBackup)
		}
		if !haveVersion {
			return reject("This backup has no format version; this version of Allister restores backups of format versions %d to %d.", MinFormatVersion, FormatVersion)
		}
		for v := MinFormatVersion; v <= FormatVersion; v++ {
			if version == fmt.Sprint(v) {
				rs.version = v
				return nil
			}
		}
		return reject("This backup has format version %s; this version of Allister restores only format versions %d to %d. Use a newer Allister to restore it.",
			clip(version, 20), MinFormatVersion, FormatVersion)
	}
	seen := map[string]bool{}
	// pending holds appearance and time zone members read before the header
	// was checked
	type member struct {
		key string
		raw json.RawMessage
	}
	var pending []member
	flush := func() error {
		for _, m := range pending {
			var err error
			if m.key == "time_zone" {
				err = rs.restoreTimeZone(ctx, m.raw)
			} else {
				err = rs.restoreAppearance(ctx, m.raw)
			}
			if err != nil {
				return err
			}
		}
		pending = nil
		return nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return rs.syntax(err)
		}
		key, _ := tok.(string)
		switch key {
		case "format", "format_version", "items", "series", "lists", "aliases", "dismissed_duplicates":
			if seen[key] {
				msg := fmt.Sprintf("backup.json has more than one %q member", key)
				if !seen["items"] {
					return reject("%s: %s.", notBackup, msg)
				}
				return rs.abort(msg) // items were restored already
			}
			seen[key] = true
		}
		switch key {
		case "format":
			var s any
			if err := rs.member(dec, &s, seen["items"]); err != nil {
				return err
			}
			format, _ = s.(string)
		case "format_version":
			var v any
			if err := rs.member(dec, &v, seen["items"]); err != nil {
				return err
			}
			haveVersion = true
			if n, ok := v.(json.Number); ok {
				version = n.String()
			} else {
				version = "of an unknown kind"
			}
		case "items":
			// refused before any item is restored
			if !seen["format"] || !seen["format_version"] {
				return reject(`%s: "format" and "format_version" must come before "items".`, notBackup)
			}
			if err := header(); err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			if err := rs.items(ctx, dec); err != nil {
				return err
			}
		case "series":
			// series refer to restored items by identity: they come after "items"
			if !seen["items"] {
				return reject(`%s: "series" must come after "items".`, notBackup)
			}
			if err := rs.series(ctx, dec); err != nil {
				return err
			}
		case "lists":
			// lists refer to restored items by identity: they come after "items"
			if !seen["items"] {
				return reject(`%s: "lists" must come after "items".`, notBackup)
			}
			if err := rs.lists(ctx, dec); err != nil {
				return err
			}
		case "aliases", "dismissed_duplicates":
			// they refer to restored items by identity: they come after "items"
			if !seen["items"] {
				return reject(`%s: %q must come after "items".`, notBackup, key)
			}
			if err := rs.merges(ctx, dec, key); err != nil {
				return err
			}
		case "appearance", "time_zone":
			var raw json.RawMessage
			if err := rs.member(dec, &raw, seen["items"]); err != nil {
				return err
			}
			// applied only once the header is known to be acceptable
			// (before "items", or at the end of an archive without them),
			// so a refused archive stores nothing
			pending = append(pending, member{key, raw})
			if seen["items"] {
				if err := flush(); err != nil {
					return err
				}
			}
		default:
			var skip json.RawMessage
			if err := rs.member(dec, &skip, seen["items"]); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return rs.syntax(err)
	}
	if err := header(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		var se *json.SyntaxError
		if err == nil || errors.As(err, &se) {
			return rs.abort("backup.json has unexpected data after the end of the backup")
		}
		return rs.syntax(err) // a read error: limits, damaged archive entry
	}
	return nil
}

// decodeCapped decodes the next value with at most limit bytes read for it
// (tooLarge reports an overrun).
func (rs *restorer) decodeCapped(dec *json.Decoder, v any, limit int64, tooLarge error) error {
	rs.limiter.limit, rs.limiter.err = dec.InputOffset()+limit, tooLarge
	err := dec.Decode(v)
	rs.limiter.limit = -1
	return err
}

// member decodes a top-level member other than "items" under
// Limits.MaxMemberBytes: too large is refused before any item was restored
// and stops the restore after.
func (rs *restorer) member(dec *json.Decoder, v any, afterItems bool) error {
	err := rs.decodeCapped(dec, v, rs.lim.MaxMemberBytes, errMemberTooLarge)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errMemberTooLarge) && !afterItems:
		return reject("%s: %s.", notBackup, rs.limitMessage(err))
	}
	return rs.syntax(err)
}

// items restores the items array; the decoder stands before '['.
func (rs *restorer) items(ctx context.Context, dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return rs.syntax(err)
	}
	if tok != json.Delim('[') {
		return rs.abort("backup.json is damaged: \"items\" is not a list")
	}
	n := 0
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n >= rs.lim.MaxItems {
			return rs.abort(fmt.Sprintf("backup.json holds more than %d items", rs.lim.MaxItems))
		}
		var raw json.RawMessage
		err := rs.decodeCapped(dec, &raw, rs.lim.MaxItemBytes, errItemTooLarge)
		if err != nil {
			return rs.syntax(err)
		}
		if int64(len(raw)) > rs.lim.MaxItemBytes {
			return rs.abort(rs.limitMessage(errItemTooLarge))
		}
		n++
		if err := rs.item(ctx, n, raw); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return rs.syntax(err)
	}
	return nil
}

// series restores the series array (after all items); the decoder stands
// before '['. One series is one transaction.
func (rs *restorer) series(ctx context.Context, dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return rs.syntax(err)
	}
	if tok != json.Delim('[') {
		return rs.abort("backup.json is damaged: \"series\" is not a list")
	}
	n := 0
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := rs.decodeCapped(dec, &raw, rs.lim.MaxItemBytes, errItemTooLarge); err != nil {
			return rs.syntax(err)
		}
		if int64(len(raw)) > rs.lim.MaxItemBytes {
			return rs.abort(rs.limitMessage(errItemTooLarge))
		}
		n++
		var bs library.BackupSeries
		if err := json.Unmarshal(raw, &bs); err != nil {
			rs.seriesSkipped(Row{Index: n, Reason: clip("the series cannot be read: "+err.Error(), 300)})
			continue
		}
		row := Row{Index: n, Title: clip(bs.Title, 120), PluginID: clip(bs.PluginID, 60), Kind: clip(bs.Kind, 60)}
		created, leftOut, err := rs.opts.Library.RestoreSeries(ctx, bs)
		switch {
		case errors.Is(err, library.ErrInvalid):
			row.Reason = clip("the series is not valid: "+err.Error(), 300)
			rs.seriesSkipped(row)
			continue
		case err != nil:
			return err
		case created:
			rs.rep.SeriesCreated++
		default:
			rs.rep.SeriesSkipped++
			switch {
			case len(leftOut) > 0: // nothing could be restored
				row.Reason = "not created: none of its parts could be restored: " + leftOutList(leftOut)
				addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
			case len(bs.Parts) == 0:
				row.Reason = "not created: it has no parts"
				addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
			}
			continue
		}
		if len(leftOut) > 0 {
			row.Reason = fmt.Sprintf("%d parts left out: %s", len(leftOut), leftOutList(leftOut))
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
		}
	}
	if _, err := dec.Token(); err != nil {
		return rs.syntax(err)
	}
	return nil
}

// lists restores the lists array (after all items); the decoder stands
// before '['. One list is one transaction.
func (rs *restorer) lists(ctx context.Context, dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return rs.syntax(err)
	}
	if tok != json.Delim('[') {
		return rs.abort("backup.json is damaged: \"lists\" is not a list")
	}
	n := 0
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := rs.decodeCapped(dec, &raw, rs.lim.MaxItemBytes, errItemTooLarge); err != nil {
			return rs.syntax(err)
		}
		if int64(len(raw)) > rs.lim.MaxItemBytes {
			return rs.abort(rs.limitMessage(errItemTooLarge))
		}
		n++
		var bl library.BackupList
		if err := json.Unmarshal(raw, &bl); err != nil {
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, Row{Index: n, Reason: clip("the list cannot be read: "+err.Error(), 300)})
			continue
		}
		row := Row{Index: n, Title: clip(bl.Name, 120)}
		added, leftOut, err := rs.opts.Library.RestoreList(ctx, bl)
		switch {
		case errors.Is(err, library.ErrInvalid):
			row.Reason = clip("the list is not valid: "+err.Error(), 300)
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
			continue
		case err != nil:
			return err
		}
		rs.rep.Lists++
		rs.rep.ListItemsAdded += added
		rs.rep.ListItemsLeftOut += leftOut
		if leftOut > 0 {
			row.Reason = fmt.Sprintf("%d entries left out: their items are not in the library", leftOut)
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
		}
	}
	if _, err := dec.Token(); err != nil {
		return rs.syntax(err)
	}
	return nil
}

// merges restores the aliases or dismissed_duplicates array (after all
// items); the decoder stands before '['. One row is one transaction.
func (rs *restorer) merges(ctx context.Context, dec *json.Decoder, key string) error {
	tok, err := dec.Token()
	if err != nil {
		return rs.syntax(err)
	}
	if tok != json.Delim('[') {
		return rs.abort(fmt.Sprintf("backup.json is damaged: %q is not a list", key))
	}
	what, added, skipped, leftOut := "alias", &rs.rep.Aliases, &rs.rep.AliasesSkipped, &rs.rep.AliasesLeftOut
	if key == "dismissed_duplicates" {
		what, added, skipped, leftOut = "dismissed duplicate pair", &rs.rep.Dismissed, &rs.rep.DismissedSkipped, &rs.rep.DismissedLeftOut
	}
	n := 0
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := rs.decodeCapped(dec, &raw, rs.lim.MaxItemBytes, errItemTooLarge); err != nil {
			return rs.syntax(err)
		}
		if int64(len(raw)) > rs.lim.MaxItemBytes {
			return rs.abort(rs.limitMessage(errItemTooLarge))
		}
		n++
		var outcome library.RestoreMerge
		var row Row
		var readErr error
		if key == "aliases" {
			var ba library.BackupAlias
			if readErr = json.Unmarshal(raw, &ba); readErr == nil {
				row = refRow(n, ba.Alias)
				outcome, err = rs.opts.Library.RestoreAlias(ctx, ba)
			}
		} else {
			var bp library.BackupDismissedPair
			if readErr = json.Unmarshal(raw, &bp); readErr == nil {
				row = refRow(n, bp.A)
				outcome, err = rs.opts.Library.RestoreDismissedPair(ctx, bp)
			}
		}
		switch {
		case readErr != nil:
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, Row{Index: n, Reason: clip("the "+what+" cannot be read: "+readErr.Error(), 300)})
		case errors.Is(err, library.ErrInvalid):
			row.Reason = clip("the "+what+" is not valid: "+err.Error(), 300)
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, row)
		case err != nil:
			return err
		case outcome == library.MergeAdded:
			*added++
		case outcome == library.MergeSkipped:
			*skipped++
		default:
			*leftOut++
		}
	}
	if _, err := dec.Token(); err != nil {
		return rs.syntax(err)
	}
	return nil
}

// refRow identifies a row of the backup by an item identity (cleaned).
func refRow(index int, r library.BackupPartRef) Row {
	return Row{Index: index, PluginID: clip(r.PluginID, 60), Kind: clip(r.Kind, 60), Source: clip(r.Source, 60), SourceID: clip(r.SourceID, 120)}
}

// maxNamedParts is how many left-out parts a series note names.
const maxNamedParts = 5

// leftOutList names the first parts of left (source:id, with the title when
// known, and why), then "and N more".
func leftOutList(left []library.LeftOutPart) string {
	var parts []string
	for i, p := range left {
		if i == maxNamedParts {
			parts = append(parts, fmt.Sprintf("and %d more", len(left)-maxNamedParts))
			break
		}
		name := clip(p.Ref.Source, 30) + ":" + clip(p.Ref.SourceID, 40)
		if p.Title != "" {
			name = clip(p.Title, 40) + " " + name
		}
		parts = append(parts, name+" ("+p.Reason+")")
	}
	return strings.Join(parts, ", ")
}

func (rs *restorer) seriesSkipped(r Row) {
	rs.rep.SeriesSkipped++
	addRow(&rs.rep.Notes, &rs.rep.MoreNotes, r)
}

// item restores one decoded item value.
func (rs *restorer) item(ctx context.Context, index int, raw json.RawMessage) error {
	if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		rs.rep.Failed++
		addRow(&rs.rep.FailedRows, &rs.rep.MoreFailed, Row{Index: index, Reason: "the item is not a JSON object"})
		return nil
	}
	bi, err := rs.decodeItem(raw)
	if err != nil {
		// identify the row as far as the identity members decode
		var id struct {
			PluginID string `json:"plugin_id"`
			Kind     string `json:"kind"`
			Source   string `json:"source"`
			SourceID string `json:"source_id"`
		}
		_ = json.Unmarshal(raw, &id) // best effort: the ids only label the failed row
		rs.rep.Failed++
		addRow(&rs.rep.FailedRows, &rs.rep.MoreFailed, Row{Index: index, PluginID: clip(id.PluginID, 60), Kind: clip(id.Kind, 60),
			Source: clip(id.Source, 60), SourceID: clip(id.SourceID, 100), Reason: clip("the item cannot be read: "+err.Error(), 300)})
		return nil
	}
	res, err := rs.opts.Library.RestoreItem(ctx, bi, library.RestoreOptions{ResolveCover: rs.resolver(), ReleaseCover: rs.release})
	if err != nil {
		if errors.Is(err, errTotalTooLarge) {
			return rs.abort(rs.limitMessage(err))
		}
		return err
	}
	row := identity(index, bi)
	switch res.Outcome {
	case library.RestoreCreated:
		rs.rep.Created++
		for _, note := range res.Notes {
			r := row
			r.Reason = clip(note, 300)
			addRow(&rs.rep.Notes, &rs.rep.MoreNotes, r)
		}
		if res.SyncPending && rs.opts.Enqueue != nil {
			rs.opts.Enqueue(ctx, res.ItemID, res.PluginID)
		}
	case library.RestoreSkipped:
		rs.rep.Skipped++
		addRow(&rs.rep.SkippedRows, &rs.rep.MoreSkipped, row)
	case library.RestoreUnknownPlugin:
		rs.rep.UnknownPlugin++
		row.Reason = clip(res.Reason, 300)
		addRow(&rs.rep.UnknownRows, &rs.rep.MoreUnknown, row)
	default:
		rs.rep.Failed++
		row.Reason = clip(res.Reason, 300)
		addRow(&rs.rep.FailedRows, &rs.rep.MoreFailed, row)
	}
	return nil
}

// decodeItem decodes one item of the accepted format version; a format-1
// entry is upgraded to its format-2 form (run 1).
func (rs *restorer) decodeItem(raw json.RawMessage) (library.BackupItem, error) {
	if rs.version >= 2 {
		var bi library.BackupItem
		err := json.Unmarshal(raw, &bi)
		return bi, err
	}
	var v1 struct {
		library.BackupItem
		Entry *library.BackupEntryV1 `json:"entry,omitempty"` // shadows the format-2 entry
	}
	if err := json.Unmarshal(raw, &v1); err != nil {
		return library.BackupItem{}, err
	}
	bi := v1.BackupItem
	if v1.Entry != nil {
		e := v1.Entry.Upgrade()
		bi.Entry = &e
	}
	return bi, nil
}

func identity(index int, bi library.BackupItem) Row {
	r := Row{Index: index, PluginID: clip(bi.PluginID, 60), Kind: clip(bi.Kind, 60), Source: clip(bi.Source, 60), SourceID: clip(bi.SourceID, 100)}
	if len(bi.Titles) > 0 {
		r.Title = clip(bi.Titles[0].Text, 120)
	}
	return r
}

// resolver stores archive covers on first use and remembers the result per
// archive name.
func (rs *restorer) resolver() func(ctx context.Context, name string) (string, string, error) {
	if rs.opts.CoversDir == "" {
		return nil
	}
	return func(ctx context.Context, name string) (string, string, error) {
		if r, ok := rs.done[name]; ok {
			return r.stored, r.note, nil
		}
		r, err := rs.storeCover(name)
		if err != nil {
			return "", "", err
		}
		rs.done[name] = r
		return r.stored, r.note, nil
	}
}

// release deletes a stored cover the library found unreferenced after the
// item that wanted it was not written, and forgets it, so a later item with
// the same archive cover stores it again.
//
// Accepted residual race: between the library's ImageReferenced check and
// this os.Remove, a concurrent save or sync could store byte-identical
// content under the same content-hash name and reference it; that file
// would then be removed. For a single-user app this window is accepted.
func (rs *restorer) release(_ context.Context, stored string) {
	if rs.opts.CoversDir == "" || !library.ValidFileName(stored) {
		return
	}
	if err := os.Remove(filepath.Join(rs.opts.CoversDir, stored)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("backup restore: remove unused cover %s: %v", stored, err)
	}
	for name, r := range rs.done {
		if r.stored == stored {
			delete(rs.done, name)
		}
	}
}

func (rs *restorer) storeCover(name string) (coverResult, error) {
	what := "the cover file covers/" + name
	f := rs.covers[name]
	if f == nil {
		return coverResult{note: what + " is missing from the backup: restored without a cover"}, nil
	}
	rc, err := f.Open()
	if err != nil {
		return coverResult{note: what + " cannot be read: restored without a cover"}, nil
	}
	defer rc.Close()
	stored, err := covers.SaveRestored(rs.opts.CoversDir, budgetReader{r: rc, b: rs.budget})
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	switch {
	case err == nil:
		return coverResult{stored: stored}, nil
	case errors.Is(err, errTotalTooLarge), errors.As(err, &pathErr), errors.As(err, &linkErr):
		return coverResult{}, err
	case errors.Is(err, covers.ErrNotImage):
		return coverResult{note: what + " is not a supported image (jpeg, png, webp, gif or avif): restored without a cover"}, nil
	case errors.Is(err, covers.ErrTooLarge):
		return coverResult{note: fmt.Sprintf("%s is larger than %d MiB: restored without a cover", what, covers.MaxUploadBytes>>20)}, nil
	}
	// damaged entry (checksum, truncated or corrupt data)
	return coverResult{note: what + " is damaged: restored without a cover"}, nil
}

// limitMessage returns the user message of a size limit error, or "".
func (rs *restorer) limitMessage(err error) string {
	switch {
	case errors.Is(err, errJSONTooLarge):
		return fmt.Sprintf("backup.json is larger than %s", size(rs.lim.MaxJSONBytes))
	case errors.Is(err, errTotalTooLarge):
		return fmt.Sprintf("the archive unpacks to more than %s", size(rs.lim.MaxTotalBytes))
	case errors.Is(err, errItemTooLarge):
		return fmt.Sprintf("an item in backup.json is larger than %s", size(rs.lim.MaxItemBytes))
	case errors.Is(err, errMemberTooLarge):
		return fmt.Sprintf("a top-level member of backup.json is larger than %s", size(rs.lim.MaxMemberBytes))
	}
	return ""
}

// syntax turns a decode error into the abort message (or a reject when
// nothing was read yet).
func (rs *restorer) syntax(err error) error {
	if lim := rs.limitMessage(err); lim != "" {
		return rs.abort(lim)
	}
	var se *json.SyntaxError
	reason := "it is cut off or not valid JSON"
	if errors.As(err, &se) {
		reason = fmt.Sprintf("invalid JSON at byte %d", se.Offset)
	}
	return rs.abort("backup.json is damaged (" + reason + ")")
}

// abort records why the restore stopped and what was done before.
func (rs *restorer) abort(msg string) error {
	n := rs.rep.Created + rs.rep.Skipped + rs.rep.Failed + rs.rep.UnknownPlugin
	rs.rep.Aborted = fmt.Sprintf("The restore stopped: %s. It stopped after %d item(s); every item before that point was handled on its own (created ones stay), nothing after it was read.", msg, n)
	return errStopped
}

func size(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// clip makes text from the uploaded file safe to show and log: invalid
// UTF-8 and control characters become U+FFFD, and it is shortened to n runes.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return '�'
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n]) + "…"
	}
	return s
}

// errNotZip: archive/zip would not read the archive either.
var errNotZip = errors.New("not a zip archive")

// errPrefix: the archive has data before its zip directory (base offset).
var errPrefix = errors.New("data before the zip directory")

// scanDirectory finds the end of central directory record exactly the way
// zip.NewReader does (the last signature within the last 1 KiB, then 65 KiB,
// whose comment fits; bytes after it are allowed; zip64 records, also when
// the writer emits them without marker values since Go 1.27; the base
// offset of prepended data) and walks the central directory headers the
// way it reads them (until the first header without the signature), without
// keeping anything. It returns the number of headers walked (it stops
// counting at max+1) and the number the end record declares. archive/zip
// compares only the low 16 bits of the two, so a directory can hold far
// more headers than declared; the caller refuses both a count over the cap
// and a mismatch before zip.NewReader allocates anything.
func scanDirectory(ra io.ReaderAt, size int64, max int) (walked, declared uint64, err error) {
	le := binary.LittleEndian
	var buf []byte
	var endOff int64
	for i, bLen := range []int64{1024, 65 * 1024} {
		bLen = min(bLen, size)
		buf = make([]byte, bLen)
		if _, err := ra.ReadAt(buf, size-bLen); err != nil && err != io.EOF {
			return 0, 0, err
		}
		if p := findEndSignature(buf); p >= 0 {
			buf, endOff = buf[p:], size-bLen+int64(p)
			break
		}
		if i == 1 || bLen == size {
			return 0, 0, errNotZip
		}
	}
	declared = uint64(le.Uint16(buf[10:]))
	dirSize := uint64(le.Uint32(buf[12:]))
	dirOffset := uint64(le.Uint32(buf[16:]))
	// The zip64 end record a locator right before the end record names.
	// archive/zip consults it when the end record's fields are zip64 markers
	// (0xffff entries, 0xffffffff size or offset; its directorySize check was
	// 0xffff before Go 1.27), and since Go 1.27 the writer also emits it
	// whenever any entry needed a zip64 extra, with real values instead of
	// markers in the plain end record. The locator is therefore followed
	// whenever one is present; markers without a locator keep the marker
	// values, which archive/zip (and the checks below) refuse.
	if p, ok := zip64Locator(ra, endOff); ok {
		rec := make([]byte, 56)
		if p < 0 || p > size-56 {
			return 0, 0, errNotZip
		}
		if _, err := ra.ReadAt(rec, p); err != nil || le.Uint32(rec) != 0x06064b50 {
			return 0, 0, errNotZip
		}
		endOff = p
		declared, dirSize, dirOffset = le.Uint64(rec[32:]), le.Uint64(rec[40:]), le.Uint64(rec[48:])
	}
	const maxInt64 = 1<<63 - 1
	if dirSize > maxInt64 || dirOffset > maxInt64 {
		return 0, 0, errNotZip
	}
	base := endOff - int64(dirSize) - int64(dirOffset)
	if o := base + int64(dirOffset); o < 0 || o >= size {
		return 0, 0, errNotZip
	}
	// archive/zip accepts data before the archive (a base offset, with a
	// fallback to 0 when a header parses at the plain offset). Following
	// that fallback exactly would mean re-implementing its full header
	// parser, and any difference lets the two walk different directories.
	// zip.Writer never prepends data, so only base 0 (an archive written by
	// this app) is accepted.
	if base != 0 {
		return 0, 0, errPrefix
	}
	start := int64(dirOffset)
	br := bufio.NewReaderSize(io.NewSectionReader(ra, start, size-start), 64<<10)
	for walked <= uint64(max) { //nolint:gosec // G115: max is the positive entry limit
		if _, ok := nextHeader(br); !ok {
			break
		}
		walked++
	}
	return walked, declared, nil
}

// findEndSignature is archive/zip's search: the last end record signature
// in b; when that record's comment does not fit, there is none.
func findEndSignature(b []byte) int {
	for i := len(b) - 22; i >= 0; i-- {
		if b[i] == 'P' && b[i+1] == 'K' && b[i+2] == 0x05 && b[i+3] == 0x06 {
			if n := int(b[i+20]) | int(b[i+21])<<8; n+22+i > len(b) {
				return -1
			}
			return i
		}
	}
	return -1
}

// zip64Locator returns the offset of the zip64 end record named by the
// locator right before the end record, as archive/zip validates it.
func zip64Locator(ra io.ReaderAt, endOff int64) (int64, bool) {
	le := binary.LittleEndian
	if endOff < 20 {
		return 0, false
	}
	loc := make([]byte, 20)
	if _, err := ra.ReadAt(loc, endOff-20); err != nil {
		return 0, false
	}
	if le.Uint32(loc) != 0x07064b50 || le.Uint32(loc[4:]) != 0 || le.Uint32(loc[16:]) != 1 {
		return 0, false
	}
	return int64(le.Uint64(loc[8:])), true //nolint:gosec // G115: the caller refuses p < 0
}

// nextHeader reads one central directory header (46 bytes plus name, extra
// and comment) and reports whether it was complete and signed.
func nextHeader(br *bufio.Reader) (int, bool) {
	var h [46]byte
	if _, err := io.ReadFull(br, h[:]); err != nil || binary.LittleEndian.Uint32(h[:]) != 0x02014b50 {
		return 0, false
	}
	le := binary.LittleEndian
	n := int(le.Uint16(h[28:])) + int(le.Uint16(h[30:])) + int(le.Uint16(h[32:]))
	if d, err := br.Discard(n); err != nil || d != n {
		return 0, false
	}
	return 46 + n, true
}
