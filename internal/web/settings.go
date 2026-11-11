package web

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strings"
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
	s.renderPlugins(w, r, http.StatusOK, "")
}

// renderPlugins renders the plugins page with the source key card. status and
// keyError report a rejected key form, re-rendering the page it came from.
func (s *server) renderPlugins(w http.ResponseWriter, r *http.Request, status int, keyError string) {
	keys, err := s.sourceKeyViews(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	view := map[string]any{
		"Plugins":    s.pluginRows(),
		"Problems":   s.deps.Registry.Problems(),
		"SourceKeys": keys,
	}
	if keyError != "" {
		view["KeyError"] = keyError
	}
	s.renderStatus(w, r, status, "settings_plugins_page", view)
}

// sourceKeyView is one source's API key form on the plugins page.
type sourceKeyView struct {
	ID, Name, Label, Help, Action string
	Configured                    bool // an effective key is set (stored or from the environment)
	Stored                        bool // this install stores a key, so it can be removed
}

// sourceKeyViews builds the API key forms of the plugins page: one per source
// that declares a key label, in registration order, each source once (two
// plugins may share one source and so one key).
func (s *server) sourceKeyViews(ctx context.Context) ([]sourceKeyView, error) {
	var out []sourceKeyView
	seen := map[string]bool{}
	for _, m := range s.deps.Registry.Manifests() {
		for _, src := range m.Sources {
			if src.KeyLabel == "" || seen[src.ID] {
				continue
			}
			seen[src.ID] = true
			key, err := s.sourceKey(ctx, src.ID)
			if err != nil {
				return nil, err
			}
			stored := ""
			if s.deps.Settings != nil {
				if stored, err = s.deps.Settings.SourceKey(ctx, src.ID); err != nil {
					return nil, err
				}
			}
			out = append(out, sourceKeyView{ID: src.ID, Name: src.Name, Label: src.KeyLabel, Help: src.KeyHelp,
				Action: sourceKeyURL(src.ID), Configured: key != "", Stored: stored != ""})
		}
	}
	return out, nil
}

// sourceKey is the effective API key of source id: the Deps.SourceKey resolver
// (which may answer with a key from the environment) when wired, else the
// stored key, else "".
func (s *server) sourceKey(ctx context.Context, id string) (string, error) {
	if s.deps.SourceKey != nil {
		return s.deps.SourceKey(ctx, id)
	}
	if s.deps.Settings != nil {
		return s.deps.Settings.SourceKey(ctx, id)
	}
	return "", nil
}

// sourceKeyURL is the form action that saves the key of source id.
func sourceKeyURL(id string) string { return "/settings/plugins/" + url.PathEscape(id) + "/key" }

// keyedSource reports whether an installed manifest declares source id with a
// key label (only such a source can have a key).
func (s *server) keyedSource(id string) bool {
	for _, m := range s.deps.Registry.Manifests() {
		for _, src := range m.Sources {
			if src.ID == id && src.KeyLabel != "" {
				return true
			}
		}
	}
	return false
}

// maxSourceKey is the longest API key accepted, in characters.
const maxSourceKey = 500

// sourceKeyProblem reports why key is not acceptable, "" when it is: 1 to 500
// printable ASCII characters (0x21-0x7E), without spaces.
func sourceKeyProblem(key string) string {
	switch {
	case key == "":
		return "Please enter the key."
	case len(key) > maxSourceKey:
		return "The key is longer than 500 characters."
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x21 || c > 0x7e {
			return "The key must be printable ASCII characters without spaces."
		}
	}
	return ""
}

// postSourceKey saves or removes the API key of a source
// (POST /settings/plugins/{source}/key: field key, or remove=1). The key is
// never echoed: the page only reports whether one is configured. Sources no
// installed manifest declares with a key label answer 404.
func (s *server) postSourceKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("source")
	if !s.keyedSource(id) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such source.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if r.PostForm.Get("remove") == "1" {
		if err := s.deps.Settings.ClearSourceKey(ctx, id); err != nil {
			s.internalError(w, r, err)
			return
		}
		http.Redirect(w, r, "/settings/plugins#source-keys", http.StatusSeeOther)
		return
	}
	key := strings.TrimSpace(r.PostForm.Get("key"))
	if problem := sourceKeyProblem(key); problem != "" {
		s.renderPlugins(w, r, http.StatusBadRequest, problem)
		return
	}
	if err := s.deps.Settings.SetSourceKey(ctx, id, key); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plugins#source-keys", http.StatusSeeOther)
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
