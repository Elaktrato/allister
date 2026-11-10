package web

import (
	"html/template"
	"log"
	"net/http"

	"github.com/allister/allister/internal/settings"
)

type langOption struct{ Value, Label string }

var baseLangOptions = []langOption{
	{"ja", "Japanese (native)"},
	{"ja-latn", "Romaji"},
	{"en", "English"},
}

var langNames = map[string]string{"de": "Deutsch", "fr": "Français", "es": "Español"}

// titleLangOptions returns the built-in options followed by other language
// tags found in stored titles.
func (s *server) titleLangOptions(r *http.Request) ([]langOption, error) {
	known, err := s.deps.Settings.KnownLanguages(r.Context())
	if err != nil {
		return nil, err
	}
	opts := append([]langOption(nil), baseLangOptions...)
	have := map[string]bool{}
	for _, o := range opts {
		have[o.Value] = true
	}
	for _, l := range known {
		if have[l] {
			continue
		}
		label := l
		if n, ok := langNames[l]; ok {
			label = n
		}
		opts = append(opts, langOption{l, label})
	}
	return opts, nil
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	opts, err := s.titleLangOptions(r)
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "settings", map[string]any{
		"Options":  opts,
		"Selected": s.deps.Settings.TitleLang(r.Context()),
	})
}

// getPlugins renders the plugins section of Settings: every installed plugin
// with its Disable/Enable button, and the refused ones.
func (s *server) getPlugins(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings_plugins_page", map[string]any{
		"Plugins":  s.pluginRows(),
		"Problems": s.deps.Registry.Problems(),
	})
}

func (s *server) postSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lang := r.PostForm.Get("title_lang")
	opts, err := s.titleLangOptions(r)
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	valid := false
	for _, o := range opts {
		valid = valid || o.Value == lang
	}
	if !valid {
		http.Error(w, "unknown language", http.StatusBadRequest)
		return
	}
	if err := s.deps.Settings.Set(r.Context(), settings.KeyTitleLang, lang); err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// importAvailable reports whether Settings offers the import page: there is
// one and an enabled plugin has an importer.
func (s *server) importAvailable() bool {
	return s.deps.Imports != nil && len(s.importerOptions("")) > 0
}

// settingsTabsView is the data of the settings section tabs.
type settingsTabsView struct {
	Current string // the section of the page shown (settingsSection)
	Import  bool   // the Import tab is offered
}

// settingsSections are the paths of the settings section pages.
var settingsSections = []string{"/settings", "/settings/plugins", "/settings/import", "/settings/backup"}

// settingsSection is the section a settings path belongs to: the longest
// section path it is or lies under, so a page answering a POST below its
// section (/settings/backup/restore, /settings/import/resume) marks its tab.
func settingsSection(path string) string {
	best := ""
	for _, p := range settingsSections {
		if underPrefix(path, p) && len(p) > len(best) {
			best = p
		}
	}
	return best
}

// settingsTabs prepares the section tabs for r. The Import tab follows
// importAvailable, and stays while an import page is shown (a running
// import of a plugin switched off meanwhile is still reported there).
func (s *server) settingsTabs(r *http.Request) settingsTabsView {
	cur := settingsSection(r.URL.Path)
	return settingsTabsView{Current: cur, Import: s.importAvailable() || (s.deps.Imports != nil && cur == "/settings/import")}
}

// settingsTab is one rendered section tab.
type settingsTab struct {
	Href, Label, Icon string
	Active            bool
}

// settingsTabsMarker is emitted by a settings page where its section tabs go
// (filled by fillMarkers, like navMarker).
const settingsTabsMarker = "<!--allister-settings-tabs-->"

var settingsTabsTemplate = template.Must(template.New("settings_tabs.html").Funcs(template.FuncMap{
	"tab": func(href, label, icon, current string) settingsTab {
		return settingsTab{Href: href, Label: label, Icon: icon, Active: href == current}
	},
}).ParseFS(templateFS, "templates/settings_tabs.html"))
