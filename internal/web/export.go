package web

import (
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/allister/allister/internal/plugin"
)

// extRe accepts the file extensions an export file name may carry: a dot and
// 1 to 10 lower-case letters or digits (checked after lower-casing).
var extRe = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

// exportLink is one exporter of a plugin, linked from the list and Settings.
type exportLink struct {
	Name, URL string
}

// exportLinks lists the exporters the plugin declares AND provides, with the
// URL of their export route (always the whole list of the plugin).
func (s *server) exportLinks(m plugin.Manifest) []exportLink {
	p, ok := s.deps.Registry.Get(m.ID)
	if !ok {
		return nil
	}
	var out []exportLink
	for _, ex := range m.Exporters {
		if p.Exporter(ex.ID) == nil {
			continue
		}
		out = append(out, exportLink{Name: ex.Name, URL: "/" + m.ID + "/export/" + ex.ID})
	}
	return out
}

// exportFileName is "<plugin>-<exporter>-<YYYY-MM-DD><ext>", built only from
// the registry-validated slugs, the date and the manifest extension when it
// is a plain short extension (else none).
func exportFileName(pluginID, exporterID, date, ext string) string {
	ext = strings.ToLower(ext)
	if !extRe.MatchString(ext) {
		ext = ""
	}
	return pluginID + "-" + exporterID + "-" + date + ext
}

// exportContentType is the manifest MIME type when it parses as a media type
// (re-formatted, so nothing but type and parameters reaches the header), else
// application/octet-stream.
func exportContentType(raw string) string {
	if strings.ContainsAny(raw, "\r\n") {
		return "application/octet-stream"
	}
	mt, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return "application/octet-stream"
	}
	if out := mime.FormatMediaType(mt, params); out != "" {
		return out
	}
	return "application/octet-stream"
}

// startedWriter remembers whether anything was written.
type startedWriter struct {
	w       http.ResponseWriter
	started bool
}

// Write forwards p; an empty write is not forwarded, so it cannot commit
// the 200 status before the exporter produced anything.
func (s *startedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.started = true
	return s.w.Write(p)
}

// getExport streams the plugin's entries through one of its exporters
// (GET /{plugin}/export/{exporter}, optionally ?kind=). The exporter id is
// matched against the manifest; an unknown id, an exporter the plugin does
// not provide or an unknown kind answer 404. A failure before the first byte
// is a 500; a failure while streaming just ends the response (logged).
func (s *server) getExport(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	p, pok := s.deps.Registry.Get(pluginID)
	if !ok || !pok {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such export.", "")
		return
	}
	var info plugin.ExporterInfo
	found := false
	for _, ex := range m.Exporters {
		if ex.ID == r.PathValue("exporter") {
			info, found = ex, true
			break
		}
	}
	var exp plugin.Exporter
	if found {
		exp = p.Exporter(info.ID)
	}
	kind := r.URL.Query().Get("kind")
	if _, kok := kindDef(m, kind); exp == nil || (kind != "" && !kok) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such export.", "")
		return
	}
	entries, err := s.deps.Library.ExportEntries(r.Context(), pluginID, kind)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", exportContentType(info.MIME))
	h.Set("Content-Disposition", `attachment; filename="`+exportFileName(m.ID, info.ID, s.now().Format("2006-01-02"), info.Extension)+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	sw := &startedWriter{w: w}
	if err := exp.Export(r.Context(), entries, sw); err != nil {
		log.Printf("export %s/%s: %v", m.ID, info.ID, err)
		if !sw.started {
			h.Del("Content-Disposition")
			h.Del("Cache-Control")
			http.Error(w, "the export failed", http.StatusInternalServerError)
		}
	}
}
