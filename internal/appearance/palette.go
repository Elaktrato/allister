// Package appearance holds the user's look of the app: the colour palette
// (presets and a freely edited palette), the app font (system stacks,
// bundled fonts and uploaded fonts) and the size scale, and renders them
// into the stylesheet /appearance.css.
//
// Colours are semantic tokens. Each token becomes a CSS custom property
// --al-<token> holding an "r g b" triplet; the Tailwind config maps the
// colour classes the templates use to these properties
// (tailwind-map.json), so changing a token re-themes every page without
// touching a template.
//
// Everything that reaches the stylesheet is validated data: hex colours
// (#rrggbb), font stacks from the tables in this package and sizes from
// Scales. Nothing typed by the user is ever copied into the CSS (an
// uploaded font's display name is not used there; its family name is
// derived from its row id).
package appearance

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Token is one colour of the palette.
type Token struct {
	Name  string // the CSS property is --al-<Name>
	Label string // shown in the editor
	Group string // the editor's section
}

// Tokens are the palette's colours, in editor order. The default preset
// keeps the exact Tailwind shades the templates used before theming, with
// four shades merged into their nearest token (text-slate-200 into text,
// text-indigo-200 into link, ring-indigo-400 into focus and
// border-amber-800 into warning-border).
var Tokens = []Token{
	{"bg", "Page background", "Surfaces"},
	{"surface", "Panels and sidebar", "Surfaces"},
	{"raised", "Raised and hovered rows", "Surfaces"},
	{"control", "Selected controls", "Surfaces"},
	{"control-hover", "Hovered controls", "Surfaces"},
	{"overlay", "Dialog backdrop", "Surfaces"},
	{"text-strong", "Strong text", "Text"},
	{"text", "Body text", "Text"},
	{"text-secondary", "Secondary text", "Text"},
	{"text-muted", "Muted text", "Text"},
	{"text-faint", "Faint text and icons", "Text"},
	{"border", "Input and control borders", "Borders"},
	{"border-muted", "Subtle dividers", "Borders"},
	{"border-strong", "Strong borders", "Borders"},
	{"accent", "Accent buttons", "Accent"},
	{"accent-hover", "Accent buttons, hovered", "Accent"},
	{"accent-text", "Text on accent buttons", "Accent"},
	{"link", "Links and accent text", "Accent"},
	{"accent-icon", "Accent icons", "Accent"},
	{"focus", "Focus ring", "Accent"},
	{"warning", "Warning text", "Status"},
	{"warning-bg", "Notice background", "Status"},
	{"warning-border", "Notice border", "Status"},
	{"warning-text", "Notice text", "Status"},
	{"success", "Success", "Status"},
	{"info", "Information", "Status"},
	{"danger", "Danger", "Status"},
}

// IsToken reports whether name is a token of the palette.
func IsToken(name string) bool {
	for _, t := range Tokens {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Palette maps every token to a normalised #rrggbb colour.
type Palette map[string]string

// Pair is a foreground and background token checked for contrast. Min is
// 4.5 (WCAG AA text) unless Reason documents why a lower one is enough.
type Pair struct {
	Fg, Bg string
	Min    float64
	Reason string
}

const (
	reasonNonText = "non-text: icons, focus rings and similar UI parts need 3:1 (WCAG 1.4.11)"
	reasonInput   = "the border of inputs and selects is their visible boundary: 3:1 against the field (WCAG 1.4.11)"
	reasonHover   = "hover state only: the resting accent button meets 4.5:1 and keeps the same label"
)

// Pairs are the contrast pairs every shipped preset must meet and the
// editor warns about.
var Pairs = []Pair{
	{"text", "bg", 4.5, ""},
	{"text", "surface", 4.5, ""},
	{"text", "raised", 4.5, ""},
	{"text", "control", 4.5, ""},
	{"text-strong", "bg", 4.5, ""},
	{"text-strong", "raised", 4.5, ""},
	{"text-strong", "control", 4.5, ""},
	{"text-secondary", "bg", 4.5, ""},
	{"text-secondary", "surface", 4.5, ""},
	{"text-secondary", "raised", 4.5, ""},
	{"text-muted", "bg", 4.5, ""},
	{"text-muted", "surface", 4.5, ""},
	{"text-muted", "raised", 4.5, ""},
	{"text-faint", "bg", 4.5, ""},
	{"text-faint", "surface", 4.5, ""},
	{"link", "bg", 4.5, ""},
	{"link", "surface", 4.5, ""},
	{"link", "raised", 4.5, ""},
	{"accent-text", "accent", 4.5, ""},
	{"accent-text", "accent-hover", 3, reasonHover},
	{"accent-icon", "bg", 3, reasonNonText},
	{"border", "bg", 3, reasonInput},
	{"border", "surface", 3, reasonInput},
	{"focus", "bg", 3, reasonNonText},
	{"focus", "surface", 3, reasonNonText},
	{"warning", "bg", 4.5, ""},
	{"warning", "surface", 4.5, ""},
	{"warning-text", "warning-bg", 4.5, ""},
	{"success", "bg", 4.5, ""},
	{"success", "surface", 4.5, ""},
	{"info", "bg", 4.5, ""},
	{"danger", "bg", 4.5, ""},
	{"danger", "surface", 4.5, ""},
}

// Warning is a pair of a palette below its minimum contrast.
type Warning struct {
	Fg, Bg     string
	Ratio, Min float64
}

// Message words the warning for the editor.
func (w Warning) Message() string {
	return fmt.Sprintf("%s on %s: contrast %.2f:1, below %.1f:1", label(w.Fg), label(w.Bg), w.Ratio, w.Min)
}

func label(token string) string {
	for _, t := range Tokens {
		if t.Name == token {
			return t.Label
		}
	}
	return token
}

// Shortfall is a pair whose exact colours stay below the pair's minimum
// on purpose.
type Shortfall struct{ Fg, Bg, FgColor, BgColor string }

// Shortfalls are the pairs where Midnight, kept identical to the look
// before theming, misses the minimum: the old slate-500 faint text (real
// labels such as "disabled" and column hints, 4.24:1 on the page and
// 3.75:1 on panels) and the old slate-700 input borders (1.95:1 and
// 1.72:1). Every other preset meets the minimums; TestPresetsAreReadable
// pins these ratios. Warnings stays quiet for exactly these colours, so
// the default does not warn about itself, while any edit is checked.
var Shortfalls = []Shortfall{
	{"text-faint", "bg", "#64748b", "#020617"},
	{"text-faint", "surface", "#64748b", "#0f172a"},
	{"border", "bg", "#334155", "#020617"},
	{"border", "surface", "#334155", "#0f172a"},
}

func knownShortfall(fg, bg, fgColor, bgColor string) bool {
	for _, sf := range Shortfalls {
		if sf.Fg == fg && sf.Bg == bg && sf.FgColor == fgColor && sf.BgColor == bgColor {
			return true
		}
	}
	return false
}

// Warnings lists the pairs of p below their minimum contrast, except the
// known Shortfalls. Missing or invalid colours are skipped (ParsePalette
// refuses them anyway).
func Warnings(p Palette) []Warning {
	var out []Warning
	for _, pr := range Pairs {
		fg, err1 := NormalizeHex(p[pr.Fg])
		bg, err2 := NormalizeHex(p[pr.Bg])
		if err1 != nil || err2 != nil || knownShortfall(pr.Fg, pr.Bg, fg, bg) {
			continue
		}
		if r := Contrast(fg, bg); r < pr.Min {
			out = append(out, Warning{Fg: pr.Fg, Bg: pr.Bg, Ratio: r, Min: pr.Min})
		}
	}
	return out
}

// NormalizeHex accepts #rrggbb or #rgb (any case, surrounding spaces
// trimmed; no other white space) and returns lower-case #rrggbb. Everything else is an error:
// named colours, functions, and anything carrying CSS syntax.
func NormalizeHex(s string) (string, error) {
	s = strings.Trim(s, " ")
	if len(s) != 4 && len(s) != 7 || s[0] != '#' {
		return "", errors.New("not a #rrggbb colour")
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return "", errors.New("not a #rrggbb colour")
		}
	}
	s = strings.ToLower(s)
	if len(s) == 4 {
		s = string([]byte{'#', s[1], s[1], s[2], s[2], s[3], s[3]})
	}
	return s, nil
}

// ParsePalette validates a complete palette: every token present with a
// valid colour (normalised), no unknown key.
func ParsePalette(m map[string]string) (Palette, error) {
	if len(m) == 0 {
		return nil, errors.New("no colours")
	}
	out := Palette{}
	for k, v := range m {
		if !IsToken(k) {
			return nil, fmt.Errorf("unknown colour %q", clip(k))
		}
		h, err := NormalizeHex(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a colour like #1e293b", label(k), clip(v))
		}
		out[k] = h
	}
	for _, t := range Tokens {
		if _, ok := out[t.Name]; !ok {
			return nil, fmt.Errorf("%s is missing", t.Label)
		}
	}
	return out, nil
}

// clip shortens a refused value for an error message (it is escaped where
// shown).
func clip(s string) string {
	if r := []rune(s); len(r) > 30 {
		return string(r[:30]) + "…"
	}
	return s
}

// DecodePalette reads a stored palette (a JSON object) and validates it.
func DecodePalette(s string) (Palette, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return ParsePalette(m)
}

// rgb returns the channels of a normalised #rrggbb colour.
func rgb(hex string) (r, g, b uint8) {
	v, _ := strconv.ParseUint(hex[1:], 16, 32)
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

// triplet is the "r g b" form used by the CSS properties.
func triplet(hex string) string {
	r, g, b := rgb(hex)
	return fmt.Sprintf("%d %d %d", r, g, b)
}

// luminance is the WCAG relative luminance of a normalised colour.
func luminance(hex string) float64 {
	r, g, b := rgb(hex)
	lin := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// Contrast is the WCAG contrast ratio of two normalised colours (1 to 21).
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// SchemeOf is "light" when the palette's background is closer to white
// than to black (more contrast against black), else "dark". It sets the
// stylesheet's color-scheme, so native controls match.
func SchemeOf(p Palette) string {
	bg, err := NormalizeHex(p["bg"])
	if err != nil {
		return "dark"
	}
	if Contrast(bg, "#000000") > Contrast(bg, "#ffffff") {
		return "light"
	}
	return "dark"
}

// LoadTailwindMap reads the class map the Tailwind config uses: utility
// (backgroundColor, textColor, ...) to class colour name (slate-950) to
// token. Tests use it to keep the templates, the config and the tokens in
// step.
func LoadTailwindMap(path string) (map[string]map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
