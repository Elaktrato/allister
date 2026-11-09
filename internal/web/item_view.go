package web

// View building of the generic item overview: the view structs fed to
// item.html and sections.html, the building-block sections and the value
// formatting (all text is escaped by html/template).

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

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

// maxShownSeconds caps a displayed duration at 999:59:59.
const maxShownSeconds = 999*3600 + 59*60 + 59

// fmtDuration formats whole seconds as h:mm:ss, clamped to 0..999:59:59.
func fmtDuration(v float64) string {
	sec := int64(maxShownSeconds)
	if v < maxShownSeconds {
		sec = int64(max(v, 0))
	}
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

// fmtProgress formats one progress value: count and pages "N / total" ("?"
// when the total field is declared but unknown, just "N" without one),
// percent "N%", duration h:mm:ss. A negative stored value shows as 0 and a
// duration is capped at 999:59:59, so a bad value never prints garbage.
func fmtProgress(def plugin.ProgressDef, v float64, fields map[string]plugin.FieldValue) string {
	if !(v > 0) { // also NaN
		v = 0
	}
	switch def.Type {
	case plugin.Percent:
		return fmtNum(v) + "%"
	case plugin.Duration:
		return fmtDuration(v)
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
		// the displayed title (ResolveTitle's choice) first, the rest in order
		first := library.ResolveTitleIndex(d.Titles, lang)
		if first >= 0 {
			t := d.Titles[first]
			sv.Titles = append(sv.Titles, titleRow{Label: langLabel(t.Lang), Text: t.Text})
		}
		for i, t := range d.Titles {
			if i != first {
				sv.Titles = append(sv.Titles, titleRow{Label: langLabel(t.Lang), Text: t.Text})
			}
		}
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
