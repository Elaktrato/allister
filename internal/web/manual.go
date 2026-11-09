package web

import (
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/ids"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
)

const (
	maxManualBody     = 12 << 20
	maxManualMemory   = 1 << 20
	maxTitleRunes     = 300
	maxStudioRunes    = 200
	maxManualSynopsis = 5000
	maxManualEpisodes = 10000
	minManualYear     = 1900
	maxManualYear     = 2100
)

// manualTypes is the AniList format vocabulary, so manual entries sort and
// filter like sourced ones.
var manualTypes = []langOption{
	{"TV", "TV"}, {"TV_SHORT", "TV short"}, {"MOVIE", "Movie"}, {"SPECIAL", "Special"},
	{"OVA", "OVA"}, {"ONA", "ONA"}, {"MUSIC", "Music"},
}

// manualForm is the form page's data; string fields echo the user's input.
type manualForm struct {
	addForm
	Title, TitleLang, Type string
	EpisodesIn, Year       string
	Studio, Synopsis       string
	Langs                  []langOption
	Types                  []langOption
	FileDropped            bool
	CoversEnabled          bool
}

func (s *server) newManualForm(r *http.Request) (manualForm, error) {
	opts, err := s.titleLangOptions(r)
	if err != nil {
		return manualForm{}, err
	}
	f := manualForm{
		addForm:       addForm{Status: string(store.PlanToWatch)},
		TitleLang:     s.deps.Settings.TitleLang(r.Context()),
		Langs:         opts,
		Types:         manualTypes,
		CoversEnabled: s.deps.CoversDir != "",
	}
	f.addForm = f.addForm.withStatuses()
	return f, nil
}

func (s *server) getManualNew(w http.ResponseWriter, r *http.Request) {
	f, err := s.newManualForm(r)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "manual_form", f)
}

func hasOption(opts []langOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// optInt parses an optional integer in [lo, hi]; blank gives nil.
func optInt(v, label string, lo, hi int) (*int, string) {
	if v == "" {
		return nil, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return nil, fmt.Sprintf("%s must be a whole number from %d to %d, or blank.", label, lo, hi)
	}
	return &n, ""
}

func tooLong(v, label string, max int) string {
	if utf8.RuneCountInString(v) > max {
		return fmt.Sprintf("%s must be at most %d characters.", label, max)
	}
	return ""
}

// parseMultipart limits and parses the body (multipart, or urlencoded as a
// fallback). On failure it has written the response and returns false.
func parseMultipart(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxManualBody)
	err := r.ParseMultipartForm(maxManualMemory)
	if err == nil || errors.Is(err, http.ErrNotMultipart) {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "bad request", http.StatusBadRequest)
	}
	return false
}

func (s *server) postManualNew(w http.ResponseWriter, r *http.Request) {
	if !parseMultipart(w, r) {
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	ctx := r.Context()
	f, err := s.newManualForm(r)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	fail := func(msg string) {
		f.Error = msg
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "manual_form", f)
	}

	f.Title = strings.TrimSpace(r.PostFormValue("title"))
	if v, ok := r.PostForm["title_lang"]; ok && len(v) > 0 && v[0] != "" {
		f.TitleLang = v[0]
	}
	f.Type = r.PostFormValue("type")
	f.EpisodesIn = strings.TrimSpace(r.PostFormValue("episodes"))
	f.Year = strings.TrimSpace(r.PostFormValue("year"))
	f.Studio = strings.TrimSpace(r.PostFormValue("studio"))
	f.Synopsis = strings.TrimSpace(r.PostFormValue("synopsis"))

	var fh *multipart.FileHeader
	if r.MultipartForm != nil {
		if fhs := r.MultipartForm.File["cover"]; len(fhs) > 0 {
			fh = fhs[0]
			f.FileDropped = true
		}
	}
	f.addForm, _, _ = parseEntryForm(r, f.addForm) // echo only; validated below
	f.addForm = f.addForm.withStatuses()

	var episodes, year *int
	var msg string
	switch {
	case f.Title == "":
		msg = "Please enter a title."
	case tooLong(f.Title, "Title", maxTitleRunes) != "":
		msg = tooLong(f.Title, "Title", maxTitleRunes)
	case !hasOption(f.Langs, f.TitleLang):
		msg = "Please choose a title language from the list."
	case !hasOption(manualTypes, f.Type):
		msg = "Please choose a type."
	}
	if msg == "" {
		episodes, msg = optInt(f.EpisodesIn, "Episodes", 1, maxManualEpisodes)
	}
	if msg == "" {
		year, msg = optInt(f.Year, "Year", minManualYear, maxManualYear)
	}
	if msg == "" {
		if msg = tooLong(f.Studio, "Studio", maxStudioRunes); msg == "" {
			msg = tooLong(f.Synopsis, "Synopsis", maxManualSynopsis)
		}
	}
	var entry store.ListEntry
	if msg == "" {
		f.addForm, entry, msg = parseEntryForm(r, f.addForm)
		f.addForm = f.addForm.withStatuses()
	}
	if msg == "" {
		if err := store.ValidateListEntry(entry, episodes); err != nil {
			msg = invalidMessage(err)
		}
	}
	if msg == "" && fh != nil && s.deps.CoversDir == "" {
		msg = "Cover upload is not available on this server."
	}
	if msg != "" {
		if f.FileDropped {
			msg += " Please choose the cover file again, browsers cannot keep it."
		}
		fail(msg)
		return
	}

	// Everything is valid: write the cover (content-checked), then the rows.
	cover := ""
	if fh != nil {
		file, err := fh.Open()
		if err != nil {
			s.fail(w, r, err, "")
			return
		}
		cover, err = covers.Save(s.deps.CoversDir, file)
		file.Close()
		if err != nil {
			switch {
			case errors.Is(err, covers.ErrNotImage):
				fail("The cover must be a JPEG, PNG, WebP or GIF image. Please choose the cover file again.")
			case errors.Is(err, covers.ErrTooLarge):
				fail("The cover image must be smaller than 10 MiB. Please choose the cover file again.")
			default:
				s.fail(w, r, err, "")
			}
			return
		}
	}
	id, err := ids.NewManualID()
	if err != nil {
		s.cleanupCover(r, cover)
		s.fail(w, r, err, "")
		return
	}
	a := provider.Anime{SearchResult: provider.SearchResult{
		Source: provider.Manual, ID: id,
		Titles: []provider.Title{{Lang: f.TitleLang, Text: f.Title}},
		Type:   f.Type, Episodes: episodes, Year: year, Studio: f.Studio, Synopsis: f.Synopsis,
		CoverURL: cover,
	}}
	// No sync job: manual entries have nothing to sync.
	if _, err := s.deps.Store.AddToList(ctx, a, entry, store.WithSyncState("done")); err != nil {
		s.cleanupCover(r, cover)
		if errors.Is(err, store.ErrInvalid) {
			f.FileDropped = f.FileDropped || fh != nil
			fail(invalidMessage(err))
			return
		}
		s.fail(w, r, err, "")
		return
	}
	http.Redirect(w, r, animeURL(string(provider.Manual), id), http.StatusSeeOther)
}

// cleanupCover removes a just-written cover file after a failed save, unless
// another row references the same content-hash name (then it stays). If the
// check or removal fails the orphaned file is harmless and only logged.
func (s *server) cleanupCover(r *http.Request, name string) {
	if name == "" || s.deps.CoversDir == "" {
		return
	}
	used, err := s.deps.Store.CoverReferenced(r.Context(), name)
	if err != nil {
		log.Printf("manual cover cleanup %s: %v", name, err)
		return
	}
	if used {
		return
	}
	if err := os.Remove(filepath.Join(s.deps.CoversDir, name)); err != nil && !os.IsNotExist(err) {
		log.Printf("manual cover cleanup %s: %v", name, err)
	}
}
