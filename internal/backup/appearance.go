package backup

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/appearance"
)

// appearanceMember is backup.json's "appearance": the stored appearance
// settings (as they are; a restore validates them again) and the uploaded
// fonts, whose files are the archive's fonts/<file> entries.
//
//	{"preset": "forest", "palette": {"bg": "#020617", ...} | null,
//	 "font": "upload-3", "font_scale": "large",
//	 "fonts": [{"id": 3, "name": "My Font", "file": "<sha256>.woff2", "format": "woff2"}]}
type appearanceMember struct {
	Preset    string             `json:"preset"`
	Palette   appearance.Palette `json:"palette"`
	Font      string             `json:"font"`
	FontScale string             `json:"font_scale"`
	Fonts     []backupFont       `json:"fonts"`
}

type backupFont struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	File   string `json:"file"`
	Format string `json:"format"`
}

// maxBackupFonts bounds the font list a restore looks at (a real backup has
// at most appearance.MaxFonts).
const maxBackupFonts = 64

// appearanceOf returns the member and the fonts whose files go into the
// archive (nil, nil without a service).
func appearanceOf(ctx context.Context, app *appearance.Service) (*appearanceMember, []appearance.Font) {
	if app == nil {
		return nil, nil
	}
	c := app.Config(ctx)
	m := &appearanceMember{Preset: c.Preset, Palette: c.Palette, Font: c.Font, FontScale: c.Scale, Fonts: []backupFont{}}
	var files []appearance.Font
	for _, f := range app.Uploads(ctx) {
		if !appearance.ValidFontFile(f.File, f.Format) {
			continue
		}
		m.Fonts = append(m.Fonts, backupFont{ID: f.ID, Name: f.Name, File: f.File, Format: f.Format})
		files = append(files, f)
	}
	return m, files
}

// writeFonts adds fonts/<file> for every uploaded font (stored: font files
// are compressed already or small). A file that vanished is skipped with a
// log line; the restore then notes it missing.
func writeFonts(ctx context.Context, zw *zip.Writer, app *appearance.Service, fonts []appearance.Font, now time.Time) error {
	if app == nil || app.Fonts() == nil {
		return nil
	}
	for _, f := range fonts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		p := app.Fonts().Path(f.File)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			log.Printf("backup: font %s skipped: not a regular file (%v)", f.File, err)
			continue
		}
		file, err := os.Open(p)
		if err != nil {
			log.Printf("backup: font %s skipped: %v", f.File, err)
			continue
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fontsDirZ + f.File, Method: zip.Store, Modified: now.UTC()})
		if err == nil {
			_, err = io.Copy(w, file)
		}
		file.Close()
		if err != nil {
			return fmt.Errorf("backup: font %s: %w", f.File, err)
		}
	}
	return nil
}

func (rs *restorer) note(format string, a ...any) {
	if len(rs.rep.Appearance) < MaxListed {
		rs.rep.Appearance = append(rs.rep.Appearance, clip(fmt.Sprintf(format, a...), 300))
	}
}

// plural is "1 font" or "2 fonts".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// userMessage is an appearance error's message without its "invalid: "
// prefix.
func userMessage(err error) string {
	return strings.TrimPrefix(err.Error(), appearance.ErrInvalid.Error()+": ")
}

// restoreAppearance merges the appearance member: every listed font whose
// file is in the archive is stored like an upload (sniffed, capped,
// deduplicated by content, stored under its own hash: the archive's names
// are never used as paths), and the settings are restored only when this
// installation has none of its own (a restore never overwrites); each is
// validated again. Refusals are notes. Only the first member is read.
func (rs *restorer) restoreAppearance(ctx context.Context, raw json.RawMessage) error {
	if rs.looked {
		rs.note("backup.json has more than one appearance member: only the first was read")
		return nil
	}
	rs.looked = true
	app := rs.opts.Appearance
	if app == nil {
		rs.note("The appearance settings and fonts in the backup were not restored (not available here).")
		return nil
	}
	var m appearanceMember
	if err := json.Unmarshal(raw, &m); err != nil {
		rs.note("The appearance settings in the backup cannot be read: not restored.")
		return nil
	}
	ids := map[int64]int64{}
	restored, already := 0, 0
	if len(m.Fonts) > maxBackupFonts {
		rs.note("The backup lists more than %d fonts: only the first %d were read.", maxBackupFonts, maxBackupFonts)
		m.Fonts = m.Fonts[:maxBackupFonts]
	}
	for _, f := range m.Fonts {
		label := clip(strings.TrimSpace(f.Name), 60)
		if label == "" {
			label = "A font"
		}
		if !appearance.IsStoredFontName(f.File) {
			rs.note("%s: not a font file name of an Allister backup: skipped", label)
			continue
		}
		zf := rs.fonts[f.File]
		if zf == nil {
			rs.note("%s: the font file is missing from the backup: skipped", label)
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			rs.note("%s: the font file cannot be read: skipped", label)
			continue
		}
		font, existing, err := app.AddFont(ctx, f.Name, budgetReader{r: rc, b: rs.budget})
		rc.Close()
		var corrupt flate.CorruptInputError
		switch {
		case err == nil && existing:
			already++
			ids[f.ID] = font.ID
		case err == nil:
			restored++
			ids[f.ID] = font.ID
		case errors.Is(err, errTotalTooLarge):
			return rs.abort(rs.limitMessage(err))
		case errors.Is(err, appearance.ErrInvalid):
			rs.note("%s: %s: skipped", label, userMessage(err))
		case errors.Is(err, zip.ErrChecksum), errors.Is(err, zip.ErrFormat), errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &corrupt):
			rs.note("%s: the font file is damaged: skipped", label)
		default: // the disk, the database
			return err
		}
	}
	if restored > 0 {
		rs.note("%s restored.", plural(restored, "uploaded font", "uploaded fonts"))
	}
	if already > 0 {
		rs.note("%s of the backup already uploaded here.", plural(already, "font was", "fonts were"))
	}
	return rs.restoreAppearanceSettings(ctx, app, m, ids)
}

// restoreAppearanceSettings applies the member's settings when this
// installation has none of its own.
func (rs *restorer) restoreAppearanceSettings(ctx context.Context, app *appearance.Service, m appearanceMember, ids map[int64]int64) error {
	if m.Preset == "" && m.Palette == nil && m.Font == "" && m.FontScale == "" {
		return nil
	}
	c := app.Config(ctx)
	if c.Preset != "" || c.Palette != nil || c.Font != "" || c.Scale != "" {
		rs.note("Appearance settings kept: this installation has its own.")
		return nil
	}
	applied := false
	apply := func(what string, err error) error {
		switch {
		case err == nil:
			applied = true
		case errors.Is(err, appearance.ErrInvalid):
			rs.note("The %s of the backup was not restored: %s.", what, userMessage(err))
		default:
			return err
		}
		return nil
	}
	if m.Preset != "" {
		if err := apply("colour preset", app.SetPreset(ctx, m.Preset)); err != nil {
			return err
		}
	}
	if m.Palette != nil {
		if err := apply("custom palette", app.SetPalette(ctx, m.Palette)); err != nil {
			return err
		}
	}
	if m.FontScale != "" {
		if err := apply("text size", app.SetScale(ctx, m.FontScale)); err != nil {
			return err
		}
	}
	if m.Font != "" {
		font := m.Font
		if rest, ok := strings.CutPrefix(font, "upload-"); ok {
			old, err := strconv.ParseInt(rest, 10, 64)
			if id, found := ids[old]; err == nil && found {
				font = "upload-" + strconv.FormatInt(id, 10)
			} else {
				rs.note("The app font of the backup is an uploaded font that was not restored: the default font is used.")
				font = ""
			}
		}
		if font != "" {
			if err := apply("app font", app.SetFont(ctx, font)); err != nil {
				return err
			}
		}
	}
	if applied {
		rs.note("Appearance settings restored.")
	}
	return nil
}
