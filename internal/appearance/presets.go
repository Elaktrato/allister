package appearance

// Preset is a named, complete palette. Scheme is "dark" or "light" (the
// stylesheet's color-scheme). TestPresetsAreReadable keeps every preset
// above the minimum contrast of each pair in Pairs.
type Preset struct {
	ID, Name, Scheme string
	Colors           Palette
}

const (
	// DefaultPreset is today's dark look.
	DefaultPreset = "midnight"
	// SystemPreset is not a palette: it follows the browser's
	// prefers-color-scheme, SystemDark by default and SystemLight when
	// the system asks for light.
	SystemPreset = "system"
	SystemDark   = "midnight"
	SystemLight  = "paper"
)

// Presets are the shipped palettes, in the order the picker lists them
// (the system preset comes first in the picker, see PresetChoices).
var Presets = []Preset{
	{ID: "midnight", Name: "Midnight", Scheme: "dark", Colors: Palette{
		"bg": "#020617", "surface": "#0f172a", "raised": "#1e293b", "control": "#334155", "control-hover": "#475569", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#f1f5f9", "text-secondary": "#cbd5e1", "text-muted": "#94a3b8", "text-faint": "#64748b",
		"border": "#334155", "border-muted": "#1e293b", "border-strong": "#475569",
		"accent": "#4f46e5", "accent-hover": "#6366f1", "accent-text": "#ffffff", "link": "#a5b4fc", "accent-icon": "#818cf8", "focus": "#6366f1",
		"warning": "#fcd34d", "warning-bg": "#451a03", "warning-border": "#b45309", "warning-text": "#fde68a",
		"success": "#34d399", "info": "#7dd3fc", "danger": "#fca5a5",
	}},
	{ID: "graphite", Name: "Graphite", Scheme: "dark", Colors: Palette{
		"bg": "#09090b", "surface": "#18181b", "raised": "#27272a", "control": "#3f3f46", "control-hover": "#52525b", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#f4f4f5", "text-secondary": "#d4d4d8", "text-muted": "#a1a1aa", "text-faint": "#71717a",
		"border": "#3f3f46", "border-muted": "#27272a", "border-strong": "#52525b",
		"accent": "#0369a1", "accent-hover": "#0284c7", "accent-text": "#ffffff", "link": "#7dd3fc", "accent-icon": "#38bdf8", "focus": "#0ea5e9",
		"warning": "#fcd34d", "warning-bg": "#422006", "warning-border": "#a16207", "warning-text": "#fde68a",
		"success": "#4ade80", "info": "#93c5fd", "danger": "#fca5a5",
	}},
	{ID: "forest", Name: "Forest", Scheme: "dark", Colors: Palette{
		"bg": "#03140e", "surface": "#0a2219", "raised": "#133327", "control": "#1d4a3a", "control-hover": "#2a5e4c", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#ecfdf5", "text-secondary": "#c6e7d8", "text-muted": "#93c4ae", "text-faint": "#5f8f7b",
		"border": "#1d4a3a", "border-muted": "#133327", "border-strong": "#2a5e4c",
		"accent": "#047857", "accent-hover": "#059669", "accent-text": "#ffffff", "link": "#6ee7b7", "accent-icon": "#34d399", "focus": "#10b981",
		"warning": "#fcd34d", "warning-bg": "#422006", "warning-border": "#a16207", "warning-text": "#fde68a",
		"success": "#86efac", "info": "#7dd3fc", "danger": "#fca5a5",
	}},
	{ID: "rose", Name: "Rose", Scheme: "dark", Colors: Palette{
		"bg": "#12060a", "surface": "#1f0d14", "raised": "#2e1520", "control": "#44202f", "control-hover": "#5c2c40", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#fff1f2", "text-secondary": "#fecdd3", "text-muted": "#d8a4b0", "text-faint": "#9d6d7a",
		"border": "#44202f", "border-muted": "#2e1520", "border-strong": "#5c2c40",
		"accent": "#be123c", "accent-hover": "#e11d48", "accent-text": "#ffffff", "link": "#fda4af", "accent-icon": "#fb7185", "focus": "#fb7185",
		"warning": "#fcd34d", "warning-bg": "#451a03", "warning-border": "#b45309", "warning-text": "#fde68a",
		"success": "#6ee7b7", "info": "#7dd3fc", "danger": "#fecaca",
	}},
	{ID: "contrast", Name: "High contrast", Scheme: "dark", Colors: Palette{
		"bg": "#000000", "surface": "#0a0a0a", "raised": "#1f1f1f", "control": "#2e2e2e", "control-hover": "#454545", "overlay": "#000000",
		"text-strong": "#ffffff", "text": "#ffffff", "text-secondary": "#f0f0f0", "text-muted": "#d4d4d4", "text-faint": "#b3b3b3",
		"border": "#8a8a8a", "border-muted": "#5c5c5c", "border-strong": "#bfbfbf",
		"accent": "#ffd60a", "accent-hover": "#ffe45c", "accent-text": "#000000", "link": "#ffe45c", "accent-icon": "#ffd60a", "focus": "#ffd60a",
		"warning": "#ffc04d", "warning-bg": "#2b1a00", "warning-border": "#ffb020", "warning-text": "#ffe2a8",
		"success": "#6bff9e", "info": "#9edcff", "danger": "#ff9d9d",
	}},
	{ID: "sand", Name: "Sand", Scheme: "light", Colors: Palette{
		"bg": "#f7f3ea", "surface": "#fffdf8", "raised": "#ece4d4", "control": "#e2d7c2", "control-hover": "#d6c8ad", "overlay": "#1c1917",
		"text-strong": "#1c1917", "text": "#292524", "text-secondary": "#44403c", "text-muted": "#57534e", "text-faint": "#78716c",
		"border": "#d6c8ad", "border-muted": "#e7dfcf", "border-strong": "#a8a29e",
		"accent": "#b45309", "accent-hover": "#92400e", "accent-text": "#ffffff", "link": "#9a3412", "accent-icon": "#c2410c", "focus": "#c2410c",
		"warning": "#92400e", "warning-bg": "#fef3c7", "warning-border": "#d97706", "warning-text": "#78350f",
		"success": "#047857", "info": "#0369a1", "danger": "#b91c1c",
	}},
	{ID: "paper", Name: "Paper", Scheme: "light", Colors: Palette{
		"bg": "#f8fafc", "surface": "#ffffff", "raised": "#e9eef5", "control": "#dbe3ee", "control-hover": "#cbd5e1", "overlay": "#0f172a",
		"text-strong": "#020617", "text": "#0f172a", "text-secondary": "#334155", "text-muted": "#475569", "text-faint": "#64748b",
		"border": "#cbd5e1", "border-muted": "#e2e8f0", "border-strong": "#94a3b8",
		"accent": "#4f46e5", "accent-hover": "#4338ca", "accent-text": "#ffffff", "link": "#4338ca", "accent-icon": "#4f46e5", "focus": "#4f46e5",
		"warning": "#92400e", "warning-bg": "#fffbeb", "warning-border": "#f59e0b", "warning-text": "#78350f",
		"success": "#047857", "info": "#0369a1", "danger": "#b91c1c",
	}},
}

// PresetByID returns a shipped palette (not the system pseudo preset).
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// ValidPreset reports whether id can be chosen: a shipped palette or the
// system preset.
func ValidPreset(id string) bool {
	_, ok := PresetByID(id)
	return ok || id == SystemPreset
}

// PresetChoice is one entry of the preset picker.
type PresetChoice struct{ ID, Name string }

// PresetChoices lists the picker's entries: the system preset, then the
// palettes.
func PresetChoices() []PresetChoice {
	d, _ := PresetByID(SystemDark)
	l, _ := PresetByID(SystemLight)
	out := []PresetChoice{{SystemPreset, "System (" + d.Name + " or " + l.Name + ")"}}
	for _, p := range Presets {
		out = append(out, PresetChoice{p.ID, p.Name})
	}
	return out
}
