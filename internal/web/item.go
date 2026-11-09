package web

// The generic, schema-driven item overview: GET /{plugin}/{source}/{id}
// renders a saved item from the library or an unsaved one live from the
// plugin's source. The kind's Sections list decides which building blocks
// appear and in which order; every block is a fixed template in
// sections.html fed with plain view structs built here (all text is escaped
// by html/template; plugins never supply markup).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

var (
	// errNoSuchItem: no saved item and nothing that can be fetched live.
	errNoSuchItem = errors.New("no such item")
	// errNoSource: the plugin declares the source but does not provide it.
	errNoSource = fmt.Errorf("%w: source not provided by the plugin", plugin.ErrUnavailable)
)

// manualSource is the source of items typed in by the user; they exist only
// in the library.
const manualSource = "manual"

// maxPathIDBytes is the longest source or item id accepted from a URL.
const maxPathIDBytes = 200

// coreRoutes returns the generic routes every plugin gets, mounted under its
// literal prefix /{plugin-id} and behind its enabled guard like any route set.
func (s *server) coreRoutes(pluginID string) routeSet {
	return func(reg routeRegistrar) {
		reg.Handle("GET /{source}/{id}", func(w http.ResponseWriter, r *http.Request) { s.getItem(w, r, pluginID) })
	}
}

// itemRef names the item of a request. Kind is "" when not given.
type itemRef struct{ Plugin, Source, ID, Kind string }

// validPathID accepts a non-empty valid UTF-8 value of at most
// maxPathIDBytes without control characters.
func validPathID(s string) bool {
	if s == "" || len(s) > maxPathIDBytes || !utf8.ValidString(s) {
		return false
	}
	return strings.IndexFunc(s, unicode.IsControl) < 0
}

func kindDef(m plugin.Manifest, key string) (plugin.KindDef, bool) {
	for _, k := range m.Kinds {
		if k.Key == key {
			return k, true
		}
	}
	return plugin.KindDef{}, false
}

func sourceInfo(m plugin.Manifest, id string) (plugin.SourceInfo, bool) {
	for _, si := range m.Sources {
		if si.ID == id {
			return si, true
		}
	}
	return plugin.SourceInfo{}, false
}

func hasKind(m plugin.Manifest, key string) bool { _, ok := kindDef(m, key); return ok }

func hasSource(m plugin.Manifest, id string) bool { _, ok := sourceInfo(m, id); return ok }

func hasSection(k plugin.KindDef, t plugin.SectionType) bool {
	return slices.ContainsFunc(k.Sections, func(d plugin.SectionDef) bool { return d.Type == t })
}

// itemPath is /{plugin}/{source}/{id} with every part path escaped.
func itemPath(pluginID, source, id string) string {
	return "/" + url.PathEscape(pluginID) + "/" + url.PathEscape(source) + "/" + url.PathEscape(id)
}

// itemURL is itemPath plus ?kind= when the plugin has several kinds.
func itemURL(m plugin.Manifest, source, id, kind string) string {
	u := itemPath(m.ID, source, id)
	if len(m.Kinds) > 1 && kind != "" {
		u += "?kind=" + url.QueryEscape(kind)
	}
	return u
}

// source returns the plugin's source implementation, or nil.
func (s *server) source(pluginID, sourceID string) plugin.Source {
	p, ok := s.deps.Registry.Get(pluginID)
	if !ok {
		return nil
	}
	return p.Source(sourceID)
}

func (s *server) getItem(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	ref := itemRef{Plugin: pluginID, Source: r.PathValue("source"), ID: r.PathValue("id"), Kind: r.URL.Query().Get("kind")}
	if !ok || !validPathID(ref.Source) || !validPathID(ref.ID) || (ref.Kind != "" && !hasKind(m, ref.Kind)) {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
		return
	}
	s.showItem(w, r, ref, itemExtras{}, http.StatusOK)
}

// itemExtras are the legacy dialogs an overview can be rendered with (no
// JavaScript fallback of the add and match dialogs). UseCache lets a fresh
// cache entry replace the source calls of an unsaved item.
type itemExtras struct {
	Dialog   *addForm
	Match    *matchView
	UseCache bool
}

// showItem renders the overview of ref (with a dialog open when x says so).
func (s *server) showItem(w http.ResponseWriter, r *http.Request, ref itemRef, x itemExtras, status int) {
	m, _ := s.deps.Registry.Manifest(ref.Plugin)
	li, err := s.loadItem(r.Context(), m, ref, x.UseCache)
	if err != nil {
		s.itemFail(w, r, m, ref.Source, err, itemURL(m, ref.Source, ref.ID, ref.Kind))
		return
	}
	v := s.itemView(r.Context(), li)
	if x.Dialog != nil {
		f := *x.Dialog
		if f.Episodes == nil {
			f.Episodes = primaryTotal(li)
		}
		f = f.withStatuses()
		v.Dialog = &f
	}
	v.Match = x.Match
	s.renderStatus(w, r, status, "item", v)
}

// itemFail writes the error page of a load error.
func (s *server) itemFail(w http.ResponseWriter, r *http.Request, m plugin.Manifest, source string, err error, retry string) {
	switch {
	case errors.Is(err, errNoSuchItem), errors.Is(err, plugin.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such item.", "")
	case errors.Is(err, plugin.ErrRateLimited), errors.Is(err, plugin.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		s.errorPage(w, r, http.StatusServiceUnavailable, "Temporarily unavailable", sourceMessage(m, source, err), retry)
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// sourceMessage is the readable outage message naming the source.
func sourceMessage(m plugin.Manifest, source string, err error) string {
	name := "The source"
	if si, ok := sourceInfo(m, source); ok && si.Name != "" {
		name = si.Name
	}
	if errors.Is(err, plugin.ErrRateLimited) {
		return name + " is rate limiting requests, please wait a moment and try again."
	}
	return name + " is unavailable right now, please try again."
}

// loadedItem is everything the overview shows, saved or live.
type loadedItem struct {
	man           plugin.Manifest
	kind          plugin.KindDef
	data          plugin.ItemData // identity (Source, ID, Kind) always set
	saved         *library.Item   // nil when live
	entry         *library.Entry  // nil when not in the list
	credits       []creditView
	creditsFailed bool
	recs          []plugin.ItemSummary
}

// loadItem finds the saved item of ref (the kind decides between several
// kinds; without one the first kind of the manifest wins), else fetches it
// live. Unsaved manual items, undeclared sources and an unknown kind of a
// plugin with several kinds are errNoSuchItem.
func (s *server) loadItem(ctx context.Context, m plugin.Manifest, ref itemRef, useCache bool) (*loadedItem, error) {
	items, err := s.deps.Library.ItemByPluginSource(ctx, ref.Plugin, ref.Source, ref.ID)
	if err != nil {
		return nil, err
	}
	for _, k := range m.Kinds {
		if ref.Kind != "" && k.Key != ref.Kind {
			continue
		}
		for i := range items {
			if items[i].Kind == k.Key {
				return s.loadSaved(ctx, m, k, &items[i])
			}
		}
	}
	return s.loadLive(ctx, m, ref, useCache)
}

func (s *server) loadSaved(ctx context.Context, m plugin.Manifest, k plugin.KindDef, it *library.Item) (*loadedItem, error) {
	li := &loadedItem{man: m, kind: k, saved: it, data: plugin.ItemData{
		ItemSummary: plugin.ItemSummary{Source: it.Source, ID: it.SourceID, Kind: it.Kind, Titles: it.Titles, Fields: it.Fields, CoverURL: it.CoverPath},
		ExternalURL: it.ExternalURL, ExternalIDs: it.ExternalIDs, Relations: it.Relations, Genres: it.Genres, Tags: it.Tags,
	}}
	var err error
	if li.entry, err = s.deps.Library.Entry(ctx, it.ID); err != nil {
		return nil, err
	}
	if hasSection(k, plugin.SectionPeople) {
		cs, err := s.deps.Library.Credits(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			cv := creditView{Role: c.Role, Person: savedPersonView(c.Person), Detail: c.Detail, WithRole: c.WithRole}
			if c.With != nil {
				p := savedPersonView(*c.With)
				cv.With = &p
			}
			li.credits = append(li.credits, cv)
		}
	}
	// recommendations are not stored: fetched live, failures omit the row
	if hasSection(k, plugin.SectionRecommendations) && hasSource(m, it.Source) {
		if src := s.source(m.ID, it.Source); src != nil {
			if recs, err := src.Recommendations(ctx, it.Kind, it.SourceID); err == nil {
				li.recs = recs
			} else {
				log.Printf("recommendations %s/%s/%s: %v", m.ID, it.Source, it.SourceID, err)
			}
		}
	}
	return li, nil
}

func (s *server) loadLive(ctx context.Context, m plugin.Manifest, ref itemRef, useCache bool) (*loadedItem, error) {
	if ref.Source == manualSource || !hasSource(m, ref.Source) {
		return nil, errNoSuchItem
	}
	kindKey := ref.Kind
	if kindKey == "" {
		if len(m.Kinds) != 1 {
			return nil, errNoSuchItem
		}
		kindKey = m.Kinds[0].Key
	}
	k, _ := kindDef(m, kindKey)
	key := liveKey(m.ID, ref.Source, kindKey, ref.ID)
	c, hit := cachedDetail{}, false
	if useCache {
		c, hit = s.cache.get(key)
	}
	if !hit {
		src := s.source(m.ID, ref.Source)
		if src == nil {
			return nil, errNoSource
		}
		d, err := src.Details(ctx, kindKey, ref.ID)
		if err != nil {
			return nil, err
		}
		c = cachedDetail{Data: d}
		if hasSection(k, plugin.SectionPeople) {
			if c.Credits, err = src.People(ctx, kindKey, ref.ID); err != nil {
				log.Printf("people %s: %v", key, err)
				c.Credits, c.CreditsFailed = nil, true
			}
		}
		if hasSection(k, plugin.SectionRecommendations) {
			if recs, err := src.Recommendations(ctx, kindKey, ref.ID); err == nil {
				c.Recs = recs
			} else {
				log.Printf("recommendations %s: %v", key, err)
			}
		}
		s.cache.put(key, c)
	}
	li := &loadedItem{man: m, kind: k, data: c.Data, creditsFailed: c.CreditsFailed, recs: c.Recs}
	// the identity comes from the URL (matched against the manifest), not
	// from what the source returned
	li.data.Source, li.data.ID, li.data.Kind = ref.Source, ref.ID, kindKey
	for _, cr := range c.Credits {
		cv := creditView{Role: cr.Role, Person: livePersonView(cr.Person), Detail: cr.Detail, WithRole: cr.WithRole}
		if cr.With != nil {
			p := livePersonView(*cr.With)
			cv.With = &p
		}
		li.credits = append(li.credits, cv)
	}
	return li, nil
}

// primaryTotal is the total of the kind's primary progress track (the
// legacy add dialog's episode total), nil when unknown.
func primaryTotal(li *loadedItem) *int {
	for _, d := range li.kind.Progress.Eligible {
		if d.Key != li.kind.Progress.PrimaryKey || d.TotalField == "" {
			continue
		}
		if v := li.data.Fields[d.TotalField].Num; v != nil {
			n := int(*v)
			return &n
		}
	}
	return nil
}

// --- view ---

type personView struct {
	Name, Image, ExtURL, LocalURL string
}

func livePersonView(p plugin.PersonRef) personView {
	return personView{Name: p.Name, Image: httpURL(p.ImageURL), ExtURL: httpsURL(p.URL)}
}

func savedPersonView(p library.Person) personView {
	return personView{Name: p.Name, Image: covers.Src(p.ImagePath), ExtURL: httpsURL(p.ExternalURL),
		LocalURL: "/people/" + strconv.FormatInt(p.ID, 10)}
}

// creditView is one credit: the person, the role qualifier (Detail) and the
// optional counterpart with its role label (WithRole, e.g. "Voice actor").
type creditView struct {
	Role     string
	Person   personView
	Detail   string
	With     *personView
	WithRole string
}

type titleRow struct{ Label, Text string }

type linkCard struct {
	Title, Type, Href, Cover string
	External                 bool
}

type infoRow struct{ Label, Text, Href string }

type relatedRow struct {
	Kind, Title, Type, Href string
	External                bool
}

type chipView struct{ Name, Rank string }

type extLink struct{ Label, Href string }

// sectionView is one rendered building block; only the members of its Type
// are set. Sections without data are never built.
type sectionView struct {
	Type          plugin.SectionType
	Heading       string
	Info          []infoRow
	Paragraphs    []string
	Titles        []titleRow
	Related       []relatedRow
	Credits       []creditView
	CreditsFailed bool
	Chips         []chipView
	Spoilers      []chipView
	Cards         []linkCard
	Links         []extLink
}

type progressView struct{ Label, Value string }

// entryView is the list summary of an item in the list.
type entryView struct {
	Status, Rating, Comment, EditURL string
	Progress                         []progressView
}

type itemView struct {
	Title, Cover string
	PartialSync  bool
	Entry        *entryView
	// AddURL opens the add dialog (items not in the list).
	AddURL string
	// MatchURL, MatchLabel and UnmatchedHint: a manual item that can be
	// matched to the plugin's source.
	MatchURL, MatchLabel, UnmatchedHint string
	Sections                            []sectionView
	Dialog                              *addForm
	Match                               *matchView
}

func (s *server) itemView(ctx context.Context, li *loadedItem) itemView {
	lang := s.deps.Settings.TitleLang(ctx)
	m, k, d := li.man, li.kind, li.data
	v := itemView{Title: library.ResolveTitle(d.Titles, lang)}
	if v.Title == "" {
		v.Title = "(untitled)"
	}
	path := itemPath(m.ID, d.Source, d.ID)
	if it := li.saved; it != nil {
		v.Cover = covers.Src(it.CoverPath)
		v.PartialSync = it.SyncState != "done"
		if it.Source == manualSource && len(m.Sources) > 0 {
			v.MatchURL, v.MatchLabel = path+"/match", "Match to "+m.Sources[0].Name
			v.UnmatchedHint = unmatchedHint(m, it)
		}
		if li.entry != nil {
			v.Entry = entrySummary(k, it, li.entry)
		}
	} else {
		v.Cover = httpURL(d.CoverURL)
	}
	if v.Entry == nil {
		v.AddURL = path + "/add"
	}
	for _, sec := range k.Sections {
		if sv, ok := s.section(li, sec, lang); ok {
			v.Sections = append(v.Sections, sv)
		}
	}
	return v
}

func statusLabel(k plugin.KindDef, st library.Status) string {
	l := k.Statuses
	switch st {
	case library.Planned:
		return l.Planned
	case library.Active:
		return l.Active
	case library.Paused:
		return l.Paused
	case library.Completed:
		return l.Completed
	case library.Dropped:
		return l.Dropped
	}
	return string(st)
}

func entrySummary(k plugin.KindDef, it *library.Item, e *library.Entry) *entryView {
	v := &entryView{Status: statusLabel(k, e.Status), Rating: "–", Comment: e.Comment,
		EditURL: "/list/" + strconv.FormatInt(it.ID, 10) + "/edit"}
	if e.Rating != nil {
		v.Rating = strconv.Itoa(*e.Rating)
	}
	for _, key := range it.ProgressKeys {
		for _, def := range k.Progress.Eligible {
			if def.Key == key {
				v.Progress = append(v.Progress, progressView{Label: def.Label, Value: fmtProgress(def, e.Progress[key], it.Fields)})
			}
		}
	}
	return v
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// fmtProgress formats one progress value: count and pages "N / total" ("?"
// when the total field is declared but unknown, just "N" without one),
// percent "N%", duration h:mm:ss.
func fmtProgress(def plugin.ProgressDef, v float64, fields map[string]plugin.FieldValue) string {
	switch def.Type {
	case plugin.Percent:
		return fmtNum(v) + "%"
	case plugin.Duration:
		sec := int64(v)
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	out := fmtNum(v)
	if def.TotalField != "" {
		if t := fields[def.TotalField].Num; t != nil {
			out += " / " + fmtNum(*t)
		} else {
			out += " / ?"
		}
	}
	return out
}

// sourceLink is the "View on ..." link of the item's external page (https
// only), ok false when there is none.
func sourceLink(m plugin.Manifest, d plugin.ItemData) (extLink, bool) {
	href := httpsURL(d.ExternalURL)
	if href == "" {
		return extLink{}, false
	}
	label := "External page"
	if si, ok := sourceInfo(m, d.Source); ok {
		label = si.LinkLabel
		if label == "" {
			label = "View on " + si.Name
		}
	}
	return extLink{Label: label, Href: href}, true
}

// fieldText formats a field value by its type; href is set for an https url.
func fieldText(fd plugin.FieldDef, v plugin.FieldValue) (text, href string) {
	switch fd.Type {
	case plugin.FieldNumber:
		if v.Num != nil {
			text = fmtNum(*v.Num)
		}
	case plugin.FieldDate:
		if v.Date != nil {
			text = v.Date.Format("2006-01-02")
		}
	case plugin.FieldList:
		text = strings.Join(v.List, ", ")
	case plugin.FieldURL:
		text, href = v.Text, httpsURL(v.Text)
	default:
		text = v.Text
	}
	return text, href
}

// relationLabel is a relation kind key as a label: "side_story" -> "Side story".
func relationLabel(kind string) string {
	s := strings.ToLower(strings.ReplaceAll(kind, "_", " "))
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func orID(title, id string) string {
	if title != "" {
		return title
	}
	if id != "" {
		return id
	}
	return "(untitled)"
}

// section builds one building block; ok is false when it has no data.
func (s *server) section(li *loadedItem, sec plugin.SectionDef, lang string) (sectionView, bool) {
	m, k, d := li.man, li.kind, li.data
	sv := sectionView{Type: sec.Type, Heading: sec.Label}
	heading := func(def string) {
		if sv.Heading == "" {
			sv.Heading = def
		}
	}
	switch sec.Type {
	case plugin.SectionInfo:
		heading("Information")
		for _, fd := range k.Fields {
			if !fd.Show {
				continue
			}
			if text, href := fieldText(fd, d.Fields[fd.Key]); text != "" {
				sv.Info = append(sv.Info, infoRow{Label: fd.Label, Text: text, Href: href})
			}
		}
		// the source link, unless a links section shows it
		if l, ok := sourceLink(m, d); ok && !hasSection(k, plugin.SectionLinks) {
			sv.Links = []extLink{l}
		}
		return sv, len(sv.Info) > 0 || len(sv.Links) > 0
	case plugin.SectionText:
		fd, ok := fieldDef(k, sec.Field)
		if !ok {
			return sv, false
		}
		heading(fd.Label)
		for _, line := range strings.Split(d.Fields[fd.Key].Text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				sv.Paragraphs = append(sv.Paragraphs, line)
			}
		}
		return sv, len(sv.Paragraphs) > 0
	case plugin.SectionTitles:
		heading("Titles")
		var rest []titleRow
		for _, t := range d.Titles {
			row := titleRow{Label: langLabel(t.Lang), Text: t.Text}
			if t.Lang == lang {
				sv.Titles = append(sv.Titles, row)
			} else {
				rest = append(rest, row)
			}
		}
		sv.Titles = append(sv.Titles, rest...)
		return sv, len(sv.Titles) > 0
	case plugin.SectionRelated:
		heading("Related")
		for _, r := range d.Relations {
			row := relatedRow{Kind: relationLabel(r.Kind), Title: orID(library.ResolveTitle(r.Item.Titles, lang), r.Item.ID),
				Type: r.Item.Fields["type"].Text}
			switch {
			case r.Item.ID != "" && hasKind(m, r.Item.Kind) && hasSource(m, r.Item.Source):
				row.Href = itemURL(m, r.Item.Source, r.Item.ID, r.Item.Kind)
			case httpsURL(r.Item.URL) != "":
				row.Href, row.External = r.Item.URL, true
			}
			sv.Related = append(sv.Related, row)
		}
		return sv, len(sv.Related) > 0
	case plugin.SectionPeople:
		heading("People")
		for _, c := range li.credits {
			if len(sec.Roles) == 0 || slices.Contains(sec.Roles, c.Role) {
				sv.Credits = append(sv.Credits, c)
			}
		}
		sv.CreditsFailed = li.creditsFailed
		return sv, len(sv.Credits) > 0 || sv.CreditsFailed
	case plugin.SectionGenres:
		heading(orDefault(k.Genres.Label, "Genres"))
		for _, g := range d.Genres {
			sv.Chips = append(sv.Chips, chipView{Name: g})
		}
		return sv, len(sv.Chips) > 0
	case plugin.SectionTags:
		heading(orDefault(k.Tags.Label, "Tags"))
		for _, t := range d.Tags {
			c := chipView{Name: t.Name}
			if t.Rank != nil {
				c.Rank = strconv.Itoa(*t.Rank) + "%"
			}
			if t.Spoiler {
				sv.Spoilers = append(sv.Spoilers, c)
			} else {
				sv.Chips = append(sv.Chips, c)
			}
		}
		return sv, len(sv.Chips) > 0 || len(sv.Spoilers) > 0
	case plugin.SectionRecommendations:
		heading("Recommendations")
		for _, rc := range li.recs {
			kind := rc.Kind
			if kind == "" {
				kind = k.Key
			}
			if rc.ID == "" || !hasSource(m, rc.Source) || !hasKind(m, kind) {
				continue
			}
			sv.Cards = append(sv.Cards, linkCard{Title: orID(library.ResolveTitle(rc.Titles, lang), rc.ID),
				Href: itemURL(m, rc.Source, rc.ID, kind), Cover: httpURL(rc.CoverURL)})
		}
		return sv, len(sv.Cards) > 0
	case plugin.SectionLinks:
		if l, ok := sourceLink(m, d); ok {
			sv.Links = []extLink{l}
		}
		return sv, len(sv.Links) > 0
	}
	return sv, false
}

func fieldDef(k plugin.KindDef, key string) (plugin.FieldDef, bool) {
	if key == "" {
		return plugin.FieldDef{}, false
	}
	for _, fd := range k.Fields {
		if fd.Key == key {
			return fd, true
		}
	}
	return plugin.FieldDef{}, false
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
