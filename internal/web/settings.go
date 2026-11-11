package web

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/plugin"
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
	Configured                    bool // every effective credential is set (stored or from the environment)
	Stored                        bool // this install stores a credential, so it can be removed
	// Fields are the named inputs of a multi-field source; empty for a
	// single-key source.
	Fields []sourceKeyFieldView
}

// sourceKeyFieldView is one named credential input of a multi-field source.
type sourceKeyFieldView struct {
	Name, Label string
	Secret      bool // render the input as a password field
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
			v := sourceKeyView{ID: src.ID, Name: src.Name, Label: src.KeyLabel, Help: src.KeyHelp,
				Action: sourceKeyURL(src.ID)}
			if len(src.KeyFields) > 0 {
				v.Configured = true
				for _, f := range src.KeyFields {
					v.Fields = append(v.Fields, sourceKeyFieldView{Name: f.Name, Label: f.Label, Secret: f.Secret})
					effective, err := s.sourceKeyField(ctx, src.ID, f.Name)
					if err != nil {
						return nil, err
					}
					if effective == "" {
						v.Configured = false
					}
					stored, err := s.storedSourceKeyField(ctx, src.ID, f.Name)
					if err != nil {
						return nil, err
					}
					v.Stored = v.Stored || stored != ""
				}
			} else {
				key, err := s.sourceKey(ctx, src.ID)
				if err != nil {
					return nil, err
				}
				stored, err := s.storedSourceKeyField(ctx, src.ID, "key")
				if err != nil {
					return nil, err
				}
				v.Configured, v.Stored = key != "", stored != ""
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// sourceKey is the effective API key of source id: the Deps.SourceKey resolver
// (which may answer with a key from the environment) when wired, else the
// effective "key" field.
func (s *server) sourceKey(ctx context.Context, id string) (string, error) {
	if s.deps.SourceKey != nil {
		return s.deps.SourceKey(ctx, id)
	}
	return s.sourceKeyField(ctx, id, "key")
}

// sourceKeyField is the effective value of one named credential field of
// source id: the Deps.SourceKeyField resolver (which may answer with a value
// from the environment) when wired, else the stored field, else "".
func (s *server) sourceKeyField(ctx context.Context, id, field string) (string, error) {
	if s.deps.SourceKeyField != nil {
		return s.deps.SourceKeyField(ctx, id, field)
	}
	return s.storedSourceKeyField(ctx, id, field)
}

// storedSourceKeyField is the value this install stores for one field of
// source id (never one the environment supplies), so the page can tell a
// configured source from a removable one.
func (s *server) storedSourceKeyField(ctx context.Context, id, field string) (string, error) {
	if s.deps.Settings == nil {
		return "", nil
	}
	return s.deps.Settings.SourceKeyField(ctx, id, field)
}

// sourceConfigured reports whether si has everything it needs: a source that
// declares no key label is always configured, a single-key source needs a
// non-empty effective key, and a multi-field source needs every effective
// field.
func (s *server) sourceConfigured(ctx context.Context, si plugin.SourceInfo) bool {
	if si.KeyLabel == "" {
		return true
	}
	if len(si.KeyFields) == 0 {
		key, err := s.sourceKey(ctx, si.ID)
		if err != nil {
			log.Printf("source %s: read key: %v", si.ID, err)
		}
		return key != ""
	}
	for _, f := range si.KeyFields {
		value, err := s.sourceKeyField(ctx, si.ID, f.Name)
		if err != nil {
			log.Printf("source %s: read %s: %v", si.ID, f.Name, err)
			return false
		}
		if value == "" {
			return false
		}
	}
	return true
}

// sourceKeyURL is the form action that saves the key of source id.
func sourceKeyURL(id string) string { return "/settings/plugins/" + url.PathEscape(id) + "/key" }

// keyedSourceInfo is the installed source info of source id: the first
// manifest entry that declares it with a key label. ok is false for unknown
// and unkeyed sources (only such a source can have a key).
func (s *server) keyedSourceInfo(id string) (plugin.SourceInfo, bool) {
	for _, m := range s.deps.Registry.Manifests() {
		for _, src := range m.Sources {
			if src.ID == id && src.KeyLabel != "" {
				return src, true
			}
		}
	}
	return plugin.SourceInfo{}, false
}

// maxSourceKey is the longest API key accepted, in characters.
const maxSourceKey = 500

// validCredential reports whether value is 1 to maxSourceKey printable ASCII
// characters (0x21-0x7E), without spaces; single keys and named fields share
// the rule.
func validCredential(value string) bool {
	if value == "" || len(value) > maxSourceKey {
		return false
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// sourceKeyProblem reports why key is not acceptable, "" when it is: 1 to 500
// printable ASCII characters (0x21-0x7E), without spaces.
func sourceKeyProblem(key string) string {
	switch {
	case key == "":
		return "Please enter the key."
	case len(key) > maxSourceKey:
		return "The key is longer than 500 characters."
	case !validCredential(key):
		return "The key must be printable ASCII characters without spaces."
	}
	return ""
}

// sourceFieldProblem reports why value is not acceptable for the named
// credential field label, "" when it is; the wording names the field instead of
// "the key".
func sourceFieldProblem(label, value string) string {
	switch {
	case value == "":
		return "Please enter " + label + "."
	case len(value) > maxSourceKey:
		return label + " is longer than 500 characters."
	case !validCredential(value):
		return label + " must be printable ASCII characters without spaces."
	}
	return ""
}

// postSourceKey saves or removes the API key of a source
// (POST /settings/plugins/{source}/key). A single-key source posts field key; a
// multi-field source posts every field.<name>. A credential is never echoed:
// the page only reports whether one is configured. Remove clears either the
// one key or every named field. Sources no installed manifest declares with a
// key label answer 404.
func (s *server) postSourceKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("source")
	si, ok := s.keyedSourceInfo(id)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such source.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if r.PostForm.Get("remove") == "1" {
		var err error
		if len(si.KeyFields) > 0 {
			err = s.deps.Settings.ClearSourceKeys(ctx, id)
		} else {
			err = s.deps.Settings.ClearSourceKey(ctx, id)
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		http.Redirect(w, r, "/settings/plugins#source-keys", http.StatusSeeOther)
		return
	}
	if len(si.KeyFields) > 0 {
		s.storeSourceKeyFields(w, r, si)
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

// storeSourceKeyFields saves every named credential field of si. Every field
// is required; a rejected form re-renders the page and stores nothing (all
// fields are checked before the first write).
func (s *server) storeSourceKeyFields(w http.ResponseWriter, r *http.Request, si plugin.SourceInfo) {
	values := make(map[string]string, len(si.KeyFields))
	for _, f := range si.KeyFields {
		value := strings.TrimSpace(r.PostForm.Get("field." + f.Name))
		if problem := sourceFieldProblem(f.Label, value); problem != "" {
			s.renderPlugins(w, r, http.StatusBadRequest, problem)
			return
		}
		values[f.Name] = value
	}
	for _, f := range si.KeyFields {
		if err := s.deps.Settings.SetSourceKeyField(r.Context(), si.ID, f.Name, values[f.Name]); err != nil {
			s.internalError(w, r, err)
			return
		}
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
