package web

import (
	"html/template"
	"net/http"
)

// langNames are display names of common title language tags; any other tag
// is shown as it is.
var langNames = map[string]string{
	"ja": "Japanese (native)", "ja-latn": "Romaji", "en": "English",
	"de": "Deutsch", "fr": "Français", "es": "Español",
}

// generalPlugin links one installed plugin's own settings from General.
type generalPlugin struct{ Name, Href string }

// getSettings renders the General section. Display settings (title
// language, table columns) belong to each plugin, so it links their pages.
func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	var ps []generalPlugin
	for _, m := range s.deps.Registry.Manifests() {
		ps = append(ps, generalPlugin{Name: m.Name, Href: pluginSettingsURL(m.ID)})
	}
	s.render(w, r, "settings", map[string]any{"Plugins": ps})
}

// getPlugins renders the plugins section of Settings: every installed plugin
// with its Disable/Enable button, and the refused ones.
func (s *server) getPlugins(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings_plugins_page", map[string]any{
		"Plugins":  s.pluginRows(),
		"Problems": s.deps.Registry.Problems(),
	})
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
var settingsSections = []string{"/settings", "/settings/appearance", "/settings/plugins", "/settings/import", "/settings/backup"}

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
