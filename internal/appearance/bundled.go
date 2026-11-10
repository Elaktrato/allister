package appearance

// Unicode ranges of the Latin and Latin Extended subsets, as Google Fonts
// (and the Fontsource packages the files come from) split them: a browser
// downloads the Latin Extended file only for text that needs it.
const (
	rangeLatin    = "U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2191, U+2193, U+2212, U+2215, U+FEFF, U+FFFD"
	rangeLatinExt = "U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF, U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020, U+20A0-20AB, U+20AD-20C0, U+2113, U+2C60-2C7F, U+A720-A7FF"
	// Source Serif 4's Latin Extended file also covers U+20C1-20C4.
	rangeLatinExtSerif = "U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF, U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020, U+20A0-20AB, U+20AD-20C4, U+2113, U+2C60-2C7F, U+A720-A7FF"
)

// bundled builds a bundled font: a variable woff2 per subset in
// /static/fonts/ (versioned file names, pinned in PINS.md, licences in
// LICENSES/). The family name is prefixed so an installed font of the same
// name never stands in for the bundled file.
func bundled(id, name, family, weight, version, extRange, fallback string) FontChoice {
	f := "Allister " + family
	return FontChoice{
		ID: id, Name: name, Kind: "bundled", Stack: `"` + f + `", ` + fallback,
		faces: []face{
			{Family: f, URL: "/static/fonts/" + id + "-latin-wght-v" + version + ".woff2", Format: "woff2", Weight: weight, UnicodeRange: rangeLatin},
			{Family: f, URL: "/static/fonts/" + id + "-latin-ext-wght-v" + version + ".woff2", Format: "woff2", Weight: weight, UnicodeRange: extRange},
		},
	}
}

// BundledFonts are the fonts shipped in the binary, all under the SIL Open
// Font License 1.1.
var BundledFonts = []FontChoice{
	bundled("inter", "Inter", "Inter", "100 900", "5.3.0", rangeLatinExt, defaultStack),
	bundled("source-serif-4", "Source Serif 4", "Source Serif 4", "200 900", "5.3.1", rangeLatinExtSerif, `ui-serif, Georgia, Cambria, "Times New Roman", Times, serif`),
	bundled("jetbrains-mono", "JetBrains Mono", "JetBrains Mono", "100 800", "5.3.0", rangeLatinExt, `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace`),
}
