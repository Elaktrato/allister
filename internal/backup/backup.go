// Package backup writes and restores the universal backup archive: a zip
// with backup.json (every item of every plugin, enabled or not, with its
// parts and list entry) and covers/<name> for every local item cover.
//
// Archive format (format "allister.backup", format_version 1):
//
//	backup.json  {"format": "allister.backup", "format_version": 1,
//	              "created_at": "<RFC 3339 UTC>",
//	              "plugins": [{"id": ..., "version": ...}],   (informational)
//	              "items": [library.BackupItem, ...]}
//	covers/<name>  the file of every item whose cover is a local file
//
// A remote cover URL is not retained once a cover has been downloaded, so
// every local item cover is included (not only uploaded ones). Person photos
// are not included: the sync after a restore fetches credits and photos
// again. An item whose local cover file is missing at backup time carries
// "cover": "" and "cover_missing": true.
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

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
)

const (
	// Format is the value of backup.json's "format" member.
	Format = "allister.backup"
	// FormatVersion is the backup format version this code writes and the
	// only one it restores.
	FormatVersion = 1
	// MaxListed caps every row list of a Report.
	MaxListed = 200

	jsonName   = "backup.json"
	coversDirZ = "covers/"
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
}

// DefaultLimits returns the production limits.
func DefaultLimits() Limits {
	return Limits{MaxJSONBytes: 256 << 20, MaxTotalBytes: 2 << 30, MaxEntries: 200_000, MaxItems: 100_000}
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
	return l
}

// Write streams the backup of every item in lib as a zip to w: backup.json
// (deflated, written item by item in batches, never held in memory) followed
// by covers/<name> (stored: images are already compressed) for every local
// cover file found in coversDir. A local cover whose name is not acceptable
// or whose file is missing is written as "cover": "" with
// "cover_missing": true. A write error stops the stream and is returned (the
// caller has usually sent headers already and can only log it).
func Write(ctx context.Context, w io.Writer, lib *library.Library, plugins []PluginVersion, coversDir string, now time.Time) error {
	zw := zip.NewWriter(w)
	jw, err := zw.CreateHeader(&zip.FileHeader{Name: jsonName, Method: zip.Deflate, Modified: now.UTC()})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	bw := bufio.NewWriterSize(jw, 64<<10)
	if plugins == nil {
		plugins = []PluginVersion{}
	}
	head, err := json.Marshal(struct {
		Format        string          `json:"format"`
		FormatVersion int             `json:"format_version"`
		CreatedAt     string          `json:"created_at"`
		Plugins       []PluginVersion `json:"plugins"`
	}{Format, FormatVersion, now.UTC().Format(time.RFC3339), plugins})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	// the surrounding object is written by hand so items can stream
	bw.Write(head[:len(head)-1])
	bw.WriteString(`,"items":[`)
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
				bi.Cover, bi.CoverMissing = "", true
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
	bw.WriteString("]}\n")
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
	f, err := os.Open(filepath.Join(coversDir, name))
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
	Ignored                                 int
	Aborted                                 string
}

func addRow(list *[]Row, more *int, r Row) {
	if len(*list) < MaxListed {
		*list = append(*list, r)
	} else {
		*more++
	}
}

var (
	errJSONTooLarge  = errors.New("backup.json too large")
	errTotalTooLarge = errors.New("archive too large")
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
	opts   Options
	lim    Limits
	covers map[string]*zip.File
	done   map[string]coverResult
	budget *budget
	rep    *Report
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
	if n, ok := entryCount(ra, size); ok && n > uint64(rs.lim.MaxEntries) {
		return rs.rep, reject("The archive holds more than %d files: it is not an Allister backup.", rs.lim.MaxEntries)
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
	in := &capReader{r: budgetReader{r: rc, b: rs.budget}, left: rs.lim.MaxJSONBytes}
	if err := rs.decode(ctx, json.NewDecoder(in)); err != nil && !errors.Is(err, errStopped) {
		return rs.rep, err
	}
	return rs.rep, nil
}

// index maps the readable entries: the first regular backup.json and the
// first regular covers/<valid name> of each name. Everything else
// (directories, symlinks, nested or unsafe names, duplicates) is ignored.
func (rs *restorer) index(zr *zip.Reader) *zip.File {
	var jsonFile *zip.File
	rs.covers = map[string]*zip.File{}
	for _, f := range zr.File {
		regular := f.Mode().IsRegular() && !strings.HasSuffix(f.Name, "/")
		name, isCover := strings.CutPrefix(f.Name, coversDirZ)
		switch {
		case regular && f.Name == jsonName && jsonFile == nil:
			jsonFile = f
		case regular && isCover && library.ValidFileName(name) && rs.covers[name] == nil:
			rs.covers[name] = f
		default:
			rs.rep.Ignored++
		}
	}
	return jsonFile
}

// decode reads backup.json token by token: the header members must come
// before "items" (as Write puts them); items are decoded one by one.
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
			return reject("This backup has no format version; this version of Allister restores backups of format version %d.", FormatVersion)
		}
		if version != fmt.Sprint(FormatVersion) {
			return reject("This backup has format version %s; this version of Allister restores only format version %d. Use a newer Allister to restore it.",
				clip(version, 20), FormatVersion)
		}
		return nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return rs.syntax(err)
		}
		key, _ := tok.(string)
		switch key {
		case "format":
			var s any
			if err := dec.Decode(&s); err != nil {
				return rs.syntax(err)
			}
			format, _ = s.(string)
		case "format_version":
			var v any
			if err := dec.Decode(&v); err != nil {
				return rs.syntax(err)
			}
			haveVersion = true
			if n, ok := v.(json.Number); ok {
				version = n.String()
			} else {
				version = "of an unknown kind"
			}
		case "items":
			if err := header(); err != nil {
				return err
			}
			if err := rs.items(ctx, dec); err != nil {
				return err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return rs.syntax(err)
			}
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return rs.syntax(err)
	}
	if err := header(); err != nil {
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
		if err := dec.Decode(&raw); err != nil {
			return rs.syntax(err)
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

// item restores one decoded item value.
func (rs *restorer) item(ctx context.Context, index int, raw json.RawMessage) error {
	if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		rs.rep.Failed++
		addRow(&rs.rep.FailedRows, &rs.rep.MoreFailed, Row{Index: index, Reason: "the item is not a JSON object"})
		return nil
	}
	var bi library.BackupItem
	if err := json.Unmarshal(raw, &bi); err != nil {
		// identify the row as far as the identity members decode
		var id struct {
			PluginID string `json:"plugin_id"`
			Kind     string `json:"kind"`
			Source   string `json:"source"`
			SourceID string `json:"source_id"`
		}
		json.Unmarshal(raw, &id)
		rs.rep.Failed++
		addRow(&rs.rep.FailedRows, &rs.rep.MoreFailed, Row{Index: index, PluginID: clip(id.PluginID, 60), Kind: clip(id.Kind, 60),
			Source: clip(id.Source, 60), SourceID: clip(id.SourceID, 100), Reason: clip("the item cannot be read: "+err.Error(), 300)})
		return nil
	}
	res, err := rs.opts.Library.RestoreItem(ctx, bi, library.RestoreOptions{ResolveCover: rs.resolver()})
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
	stored, err := covers.Save(rs.opts.CoversDir, budgetReader{r: rc, b: rs.budget})
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	switch {
	case err == nil:
		return coverResult{stored: stored}, nil
	case errors.Is(err, errTotalTooLarge), errors.As(err, &pathErr), errors.As(err, &linkErr):
		return coverResult{}, err
	case errors.Is(err, covers.ErrNotImage):
		return coverResult{note: what + " is not a supported image (jpeg, png, webp or gif): restored without a cover"}, nil
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

// entryCount reads the entry count from the zip's end of central directory
// record (zip64 aware) without loading the directory, so a directory with
// millions of entries is refused before zip.NewReader allocates for it.
// ok is false when the record cannot be found (zip.NewReader then decides).
func entryCount(ra io.ReaderAt, size int64) (n uint64, ok bool) {
	const eocdLen, maxComment = 22, 65535
	tail := min(size, eocdLen+maxComment)
	if tail < eocdLen {
		return 0, false
	}
	buf := make([]byte, tail)
	if _, err := ra.ReadAt(buf, size-tail); err != nil && err != io.EOF {
		return 0, false
	}
	le := binary.LittleEndian
	for i := len(buf) - eocdLen; i >= 0; i-- {
		// the record ends the file: its comment runs exactly to the end
		if le.Uint32(buf[i:]) != 0x06054b50 || i+eocdLen+int(le.Uint16(buf[i+20:])) != len(buf) {
			continue
		}
		total := uint64(le.Uint16(buf[i+10:]))
		if total != 0xFFFF {
			return total, true
		}
		// zip64: the locator sits right before the record
		if i < 20 || le.Uint32(buf[i-20:]) != 0x07064b50 {
			return 0, false
		}
		off := int64(le.Uint64(buf[i-20+8:]))
		rec := make([]byte, 56)
		if off < 0 || off > size-56 {
			return 0, false
		}
		if _, err := ra.ReadAt(rec, off); err != nil || le.Uint32(rec) != 0x06064b50 {
			return 0, false
		}
		return le.Uint64(rec[32:]), true
	}
	return 0, false
}
