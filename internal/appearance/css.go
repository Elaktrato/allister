package appearance

import (
	"bytes"
	"fmt"
)

// Config is the stored appearance. Every field may be empty or invalid
// (read from the database as it is): CSS falls back to the default of
// each one.
type Config struct {
	Preset string
	// Palette is the freely edited palette; nil means the preset's.
	Palette Palette
	Font    string
	Scale   string
}

// Resolved is a Config with every fallback applied.
type Resolved struct {
	Preset string
	// Colors is the palette in use; for the system preset (without a
	// custom palette) it is the dark one, and Light the light one.
	Colors, Light Palette
	Custom        bool
	Font          FontChoice
	Scale         Scale
}

// Resolve applies the fallbacks: an unknown preset is the default, an
// invalid palette is ignored, an unknown font or scale is the default.
func Resolve(c Config, uploads []Font) Resolved {
	r := Resolved{Preset: c.Preset}
	if !ValidPreset(r.Preset) {
		r.Preset = DefaultPreset
	}
	if c.Palette != nil {
		if m, err := ParsePalette(c.Palette); err == nil {
			r.Colors, r.Custom = m, true
		}
	}
	if !r.Custom {
		if r.Preset == SystemPreset {
			d, _ := PresetByID(SystemDark)
			l, _ := PresetByID(SystemLight)
			r.Colors, r.Light = d.Colors, l.Colors
		} else {
			p, _ := PresetByID(r.Preset)
			r.Colors = p.Colors
		}
	}
	var ok bool
	if r.Font, ok = FontByID(c.Font, uploads); !ok {
		r.Font, _ = FontByID(DefaultFont, nil)
	}
	if r.Scale, ok = scaleByID(c.Scale); !ok {
		r.Scale, _ = scaleByID(DefaultScale)
	}
	return r
}

// CSS renders the stylesheet of c: the @font-face rules of the bundled and
// uploaded fonts (browsers download a font only when text uses it), the
// palette and font as custom properties on :root (for the system preset
// the light palette inside a prefers-color-scheme media query), and one
// .al-font-<id> class per font for the picker's samples. The output holds
// only validated colours, table font stacks and table sizes.
func CSS(c Config, uploads []Font) []byte {
	r := Resolve(c, uploads)
	var b bytes.Buffer
	b.WriteString("/* Allister appearance: generated from the appearance settings, do not edit. */\n")
	fonts := Fonts(uploads)
	for _, f := range fonts {
		for _, fc := range f.faces {
			fmt.Fprintf(&b, "@font-face {\n  font-family: \"%s\";\n  font-style: normal;\n  font-weight: %s;\n  font-display: swap;\n  src: url(\"%s\") format(\"%s\");\n", fc.Family, fc.Weight, fc.URL, fc.Format)
			if fc.UnicodeRange != "" {
				fmt.Fprintf(&b, "  unicode-range: %s;\n", fc.UnicodeRange)
			}
			b.WriteString("}\n")
		}
	}
	b.WriteString(":root {\n")
	writeColors(&b, "  ", r.Colors)
	fmt.Fprintf(&b, "  --al-font: %s;\n  --al-root-size: %s;\n}\n", r.Font.Stack, r.Scale.Size)
	if r.Light != nil {
		b.WriteString("@media (prefers-color-scheme: light) {\n  :root {\n")
		writeColors(&b, "    ", r.Light)
		b.WriteString("  }\n}\n")
	}
	for _, f := range fonts {
		fmt.Fprintf(&b, ".al-font-%s { font-family: %s; }\n", f.ID, f.Stack)
	}
	return b.Bytes()
}

// writeColors writes color-scheme and one property per token of p (a
// validated palette).
func writeColors(b *bytes.Buffer, indent string, p Palette) {
	fmt.Fprintf(b, "%scolor-scheme: %s;\n", indent, SchemeOf(p))
	for _, t := range Tokens {
		fmt.Fprintf(b, "%s--al-%s: %s;\n", indent, t.Name, triplet(p[t.Name]))
	}
}
