package appearance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxFontBytes caps one uploaded font file (2 MiB): a Latin font is
	// usually far smaller, and the file is served to every page.
	MaxFontBytes = 2 << 20
	// MaxFonts caps the number of uploaded fonts.
	MaxFonts = 8
	// MaxFontName caps an uploaded font's display name, in characters.
	MaxFontName = 40
)

var (
	// ErrNotFont: the content is not a woff2, woff, TrueType or OpenType
	// font (decided by its first four bytes only).
	ErrNotFont = fmt.Errorf("%w: the file is not a font (woff2, woff, ttf or otf)", ErrInvalid)
	// ErrFontTooLarge: over MaxFontBytes.
	ErrFontTooLarge = fmt.Errorf("%w: the font is larger than %d MiB", ErrInvalid, MaxFontBytes>>20)
	// ErrTooManyFonts: MaxFonts are uploaded already.
	ErrTooManyFonts = fmt.Errorf("%w: %d fonts are uploaded already; delete one first", ErrInvalid, MaxFonts)
	// ErrNoFont: no uploaded font has the id.
	ErrNoFont = errors.New("no such font")
)

// SniffFont returns the format of a font file from its first four bytes:
// wOF2 (woff2), wOFF (woff), 0x00010000 or "true" (TrueType), OTTO
// (OpenType with CFF outlines). Nothing else is accepted (no font
// collections, EOT or SVG fonts), and nothing beyond the magic is read:
// font internals are never decoded here.
func SniffFont(head []byte) (format string, ok bool) {
	if len(head) < 4 {
		return "", false
	}
	switch string(head[:4]) {
	case "wOF2":
		return "woff2", true
	case "wOFF":
		return "woff", true
	case "\x00\x01\x00\x00", "true":
		return "truetype", true
	case "OTTO":
		return "opentype", true
	}
	return "", false
}

// CheckFontName validates an uploaded font's display name: trimmed of
// spaces, 1 to MaxFontName characters of valid UTF-8 without control or
// format characters (no newlines, NUL, bidi overrides or zero-width
// characters).
func CheckFontName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: the name is not valid text", ErrInvalid)
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", fmt.Errorf("%w: the name contains a control character (U+%04X)", ErrInvalid, r)
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: please give the font a name", ErrInvalid)
	}
	if utf8.RuneCountInString(s) > MaxFontName {
		return "", fmt.Errorf("%w: the name is longer than %d characters", ErrInvalid, MaxFontName)
	}
	return s, nil
}

// FontStore keeps uploaded fonts: a row per font in the fonts table and
// the file in dir (DATA_DIR/fonts) under its content hash.
type FontStore struct {
	db  *sql.DB
	dir string
}

// NewFontStore returns the store over the database and the fonts dir.
func NewFontStore(db *sql.DB, dir string) *FontStore { return &FontStore{db: db, dir: dir} }

const fontColumns = `id, name, file, format, size, created_at`

func scanFont(sc interface{ Scan(...any) error }) (Font, error) {
	var f Font
	var created string
	if err := sc.Scan(&f.ID, &f.Name, &f.File, &f.Format, &f.Size, &created); err != nil {
		return Font{}, err
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return f, nil
}

// List returns the uploaded fonts in upload order.
func (s *FontStore) List(ctx context.Context) ([]Font, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fontColumns+` FROM fonts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Font
	for rows.Next() {
		f, err := scanFont(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ByFile finds the font stored as file; ok is false for a name that is not
// a listed, valid stored font.
func (s *FontStore) ByFile(ctx context.Context, file string) (f Font, ok bool, err error) {
	if !fontFile.MatchString(file) {
		return Font{}, false, nil
	}
	f, err = scanFont(s.db.QueryRowContext(ctx, `SELECT `+fontColumns+` FROM fonts WHERE file = ?`, file))
	if errors.Is(err, sql.ErrNoRows) {
		return Font{}, false, nil
	}
	if err != nil {
		return Font{}, false, err
	}
	return f, ValidFontFile(f.File, f.Format), nil
}

// Path is the file of a stored font name, or "" for a name that is not a
// stored font name.
func (s *FontStore) Path(file string) string {
	if !fontFile.MatchString(file) {
		return ""
	}
	return filepath.Join(s.dir, file)
}

// Add stores an uploaded font under name. The content's first bytes decide
// the format (SniffFont), at most MaxFontBytes are accepted, and the file
// is written through a temp file and a rename as <sha256><ext>. A font
// whose content is uploaded already is not stored again: that font is
// returned with existing set. Errors the user can fix (name, not a font,
// too large, too many fonts) wrap ErrInvalid.
func (s *FontStore) Add(ctx context.Context, name string, r io.Reader) (f Font, existing bool, err error) {
	if name, err = CheckFontName(name); err != nil {
		return Font{}, false, err
	}
	file, format, size, fresh, err := s.write(r)
	if err != nil {
		return Font{}, false, err
	}
	keep := !fresh // a file that was there already is never removed here
	defer func() {
		if !keep {
			os.Remove(filepath.Join(s.dir, file))
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Font{}, false, err
	}
	defer tx.Rollback()
	old, err := scanFont(tx.QueryRowContext(ctx, `SELECT `+fontColumns+` FROM fonts WHERE file = ?`, file))
	switch {
	case err == nil:
		keep = true
		return old, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Font{}, false, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fonts`).Scan(&n); err != nil {
		return Font{}, false, err
	}
	if n >= MaxFonts {
		return Font{}, false, ErrTooManyFonts
	}
	f, err = scanFont(tx.QueryRowContext(ctx, `INSERT INTO fonts(name, file, format, size) VALUES(?, ?, ?, ?) RETURNING `+fontColumns,
		name, file, format, size))
	if err != nil {
		return Font{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Font{}, false, err
	}
	keep = true
	return f, false, nil
}

// write stores r in dir under its content hash and returns the name, the
// sniffed format, the size and whether the file was newly written (false:
// the same content was stored already and is kept as it is). Nothing is
// left behind on error.
func (s *FontStore) write(r io.Reader) (file, format string, size int64, fresh bool, err error) {
	r = io.LimitReader(r, MaxFontBytes+1)
	head := make([]byte, 4)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", 0, false, err
	}
	format, ok := SniffFont(head[:n])
	if !ok {
		return "", "", 0, false, ErrNotFont
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", "", 0, false, err
	}
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", "", 0, false, err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	h := sha256.New()
	w := io.MultiWriter(tmp, h)
	if _, err = w.Write(head); err == nil {
		size, err = io.Copy(w, r)
		size += int64(len(head))
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", 0, false, err
	}
	if size > MaxFontBytes {
		return "", "", 0, false, ErrFontTooLarge
	}
	file = hex.EncodeToString(h.Sum(nil)) + Formats[format].Ext
	dst := filepath.Join(s.dir, file)
	if fi, err := os.Lstat(dst); err == nil && fi.Mode().IsRegular() {
		return file, format, size, false, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", 0, false, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", "", 0, false, err
	}
	return file, format, size, true, nil
}

// Delete removes an uploaded font's row and file (ErrNoFont when there is
// no such font).
func (s *FontStore) Delete(ctx context.Context, id int64) error {
	var file string
	err := s.db.QueryRowContext(ctx, `DELETE FROM fonts WHERE id = ? RETURNING file`, id).Scan(&file)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoFont
	}
	if err != nil {
		return err
	}
	if p := s.Path(file); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
