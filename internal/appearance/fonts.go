package appearance

import (
	"regexp"
	"strconv"
	"time"
)

// FontChoice is one entry of the font picker. Stack is the CSS font-family
// value; it only ever comes from the tables here or is derived from an
// uploaded font's row id.
type FontChoice struct {
	ID, Name, Stack string
	// Kind is "system", "bundled" or "uploaded".
	Kind  string
	faces []face
}

// face is one @font-face rule of a bundled or uploaded font.
type face struct {
	Family, URL, Format, Weight, UnicodeRange string
}

// defaultStack is Tailwind's default sans-serif stack: the app font before
// theming, and the fallback behind every bundled and uploaded font.
const defaultStack = `ui-sans-serif, system-ui, sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji"`

// DefaultFont is the system sans-serif stack (today's look).
const DefaultFont = "system-sans"

// SystemFonts are font stacks of fonts the operating system has; they need
// no download.
var SystemFonts = []FontChoice{
	{ID: "system-sans", Name: "System sans-serif", Kind: "system", Stack: defaultStack},
	{ID: "system-serif", Name: "System serif", Kind: "system", Stack: `ui-serif, Georgia, Cambria, "Times New Roman", Times, serif`},
	{ID: "system-mono", Name: "System monospace", Kind: "system", Stack: `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace`},
	{ID: "system-humanist", Name: "Humanist", Kind: "system", Stack: `Seravek, "Gill Sans Nova", Ubuntu, Calibri, "DejaVu Sans", source-sans-pro, sans-serif`},
	{ID: "system-rounded", Name: "Rounded", Kind: "system", Stack: `ui-rounded, "Hiragino Maru Gothic ProN", Quicksand, Comfortaa, Manjari, "Arial Rounded MT", "Arial Rounded MT Bold", Calibri, source-sans-pro, sans-serif`},
}

// Font is an uploaded font (a row of the fonts table). File is the stored
// file's name (<sha256>.<ext>), Format one of the Formats keys.
type Font struct {
	ID        int64
	Name      string
	File      string
	Format    string
	Size      int64
	CreatedAt time.Time
}

// Formats maps an uploaded font's stored format to its file extension, the
// CSS format() hint and the MIME type it is served with.
var Formats = map[string]struct{ Ext, CSS, MIME string }{
	"woff2":    {".woff2", "woff2", "font/woff2"},
	"woff":     {".woff", "woff", "font/woff"},
	"truetype": {".ttf", "truetype", "font/ttf"},
	"opentype": {".otf", "opentype", "font/otf"},
}

// fontFile is a stored font file name: a sha256 and a known extension.
var fontFile = regexp.MustCompile(`^[0-9a-f]{64}\.(woff2|woff|ttf|otf)$`)

// ValidFontFile reports whether name is a stored font file name whose
// extension matches format.
func ValidFontFile(name, format string) bool {
	f, ok := Formats[format]
	return ok && fontFile.MatchString(name) && name[64:] == f.Ext
}

// uploadedID is the picker id of an uploaded font.
func uploadedID(id int64) string { return "upload-" + strconv.FormatInt(id, 10) }

// uploadedChoice is the picker entry of an uploaded font; ok is false for
// a row that is not valid (it is then left out everywhere).
func uploadedChoice(f Font) (FontChoice, bool) {
	if f.ID <= 0 || !ValidFontFile(f.File, f.Format) {
		return FontChoice{}, false
	}
	family := "Allister Font " + strconv.FormatInt(f.ID, 10)
	return FontChoice{
		ID: uploadedID(f.ID), Name: f.Name, Kind: "uploaded",
		Stack: `"` + family + `", ` + defaultStack,
		faces: []face{{Family: family, URL: "/fonts/" + f.File, Format: Formats[f.Format].CSS, Weight: "100 900"}},
	}, true
}

// Fonts lists the picker's entries: the system stacks, the bundled fonts
// and the valid uploaded fonts.
func Fonts(uploads []Font) []FontChoice {
	out := append([]FontChoice{}, SystemFonts...)
	out = append(out, BundledFonts...)
	for _, f := range uploads {
		if c, ok := uploadedChoice(f); ok {
			out = append(out, c)
		}
	}
	return out
}

// FontByID finds a picker entry.
func FontByID(id string, uploads []Font) (FontChoice, bool) {
	for _, c := range Fonts(uploads) {
		if c.ID == id {
			return c, true
		}
	}
	return FontChoice{}, false
}

// Scale is a size step: the root font size every rem-based size follows.
type Scale struct{ ID, Label, Size string }

// DefaultScale keeps today's sizes.
const DefaultScale = "default"

// Scales are the size steps.
var Scales = []Scale{
	{"small", "Small", "87.5%"},
	{"default", "Default", "100%"},
	{"large", "Large", "112.5%"},
}

func scaleByID(id string) (Scale, bool) {
	for _, s := range Scales {
		if s.ID == id {
			return s, true
		}
	}
	return Scale{}, false
}

// ValidScale reports whether id is a size step.
func ValidScale(id string) bool {
	_, ok := scaleByID(id)
	return ok
}
