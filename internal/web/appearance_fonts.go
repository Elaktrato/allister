package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/appearance"
)

// maxFontUpload caps the upload request: the font plus the form's other
// field and the multipart framing.
const maxFontUpload = appearance.MaxFontBytes + 64<<10

// mountFonts mounts the uploaded font routes when the server has a font
// store (Deps.Appearance built with one).
func (s *server) mountFonts(mux *http.ServeMux) {
	if s.appearance.Fonts() == nil {
		return
	}
	mux.HandleFunc("GET /fonts", s.getFont)
	mux.HandleFunc("GET /fonts/", s.getFont)
	mux.HandleFunc("POST /settings/appearance/fonts", s.postFontUpload)
	mux.HandleFunc("POST /settings/appearance/fonts/{id}/delete", s.postFontDelete)
}

// uploadRow is one uploaded font in the list of the appearance page.
type uploadRow struct {
	ID           int64
	Name, Format string
	Size         string
	Active       bool
}

// formatNames words a stored font format.
var formatNames = map[string]string{"woff2": "WOFF2", "woff": "WOFF", "truetype": "TrueType", "opentype": "OpenType"}

func kib(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// getFont serves an uploaded font file: only a name of a font listed in
// the fonts table (a content hash and the extension of its format), never a
// directory or another file of the fonts dir. The name is a content hash,
// so the file is cached forever. Every answer carries nosniff.
func (s *server) getFont(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name, ok := strings.CutPrefix(r.URL.Path, "/fonts/")
	if !ok || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	store := s.appearance.Fonts()
	f, ok, err := store.ByFile(r.Context(), name)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(store.Path(f.File))
	if err != nil {
		log.Printf("font %s: %v", f.File, err)
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", appearance.Formats[f.Format].MIME)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", fi.ModTime(), file)
}

var errNoFontFile = fmt.Errorf("%w: please choose a font file (woff2, woff, ttf or otf)", appearance.ErrInvalid)

// postFontUpload stores an uploaded font (multipart fields "name" and
// "file"). The request is capped (413 with the page); a refused font is
// 422 with the page and the reason. The form is parsed into memory only
// (the cap is below the memory limit), and the file's type is decided by
// its first bytes, never by its name or the browser's content type.
func (s *server) postFontUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFontUpload)
	err := r.ParseMultipartForm(maxFontUpload)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		s.fontRefused(w, r, appearance.ErrFontTooLarge)
		return
	case err != nil:
		s.fontRefused(w, r, errNoFontFile)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.fontRefused(w, r, errNoFontFile)
		return
	}
	defer file.Close()
	_, _, err = s.appearance.AddFont(r.Context(), r.FormValue("name"), file)
	if err != nil {
		s.fontRefused(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/appearance", http.StatusSeeOther)
}

// fontRefused answers a refused upload: 413 for a font over the limit,
// 422 for anything else the user can fix, 500 otherwise.
func (s *server) fontRefused(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appearance.ErrFontTooLarge):
		s.renderStatus(w, r, http.StatusRequestEntityTooLarge, "settings_appearance", s.appearancePage(r.Context(), invalidMessage(err), nil))
	case errors.Is(err, appearance.ErrInvalid):
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings_appearance", s.appearancePage(r.Context(), invalidMessage(err), nil))
	default:
		s.internalError(w, r, err)
	}
}

// postFontDelete removes an uploaded font (the app font falls back to the
// default when it was this one).
func (s *server) postFontDelete(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if !isASCIIDigits(raw, maxIDDigits) || err != nil || id <= 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such font.", "")
		return
	}
	switch err := s.appearance.DeleteFont(r.Context(), id); {
	case errors.Is(err, appearance.ErrNoFont):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such font.", "")
	case err != nil:
		s.internalError(w, r, err)
	default:
		http.Redirect(w, r, "/settings/appearance", http.StatusSeeOther)
	}
}
