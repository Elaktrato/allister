package web

// The home page, GET /: one page across every enabled plugin with four
// sections, each hidden when empty: Up next (active entries as list
// cards naming the next unit and the releases aired beyond progress), Coming up (the next 7 days of stored releases, the calendar's
// data), Series in progress and Recently updated. Every section is assembled
// from bounded per-kind queries merged in Go; rows, cards, +1, status labels
// and URLs come from the list's own builders.

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// The section limits and the Coming up window.
const (
	homeActiveLimit = 12
	homeComingLimit = 10
	homeSeriesLimit = 6
	homeRecentLimit = 10
	homeWindow      = 7 * 24 * time.Hour
)

// homeView is the data of home.html.
type homeView struct {
	Cards       []rowView  // Up next
	ActiveAll   []homeLink // a link per plugin to its active entries
	Days        []homeDay  // Coming up, grouped by day
	Series      []homeSeries
	Recent      []homeRecent
	CalendarURL string
}

type homeLink struct{ Label, Href string }

// homeDay is one day of Coming up.
type homeDay struct {
	Label    string
	Releases []homeRelease
}

type homeRelease struct {
	Title, Label, Href string
	Season, Number     int
	At                 time.Time
}

type homeSeries struct {
	Title, Href, Cover, Progress string
	BadgeIcon, BadgeLabel        string
	BarPct                       int // -1: no parts
}

type homeRecent struct {
	Title, Href, Status, StatusKey, Updated string
	BadgeIcon, BadgeLabel                   string
	RepeatMarker                            string // an active repeat run's badge
}

// badge is the plugin icon and kind label shown on a home entry.
func badge(m plugin.Manifest, k plugin.KindDef) (icon, label string) {
	icon = m.Icon
	if icon == "" {
		icon = "category"
	}
	return icon, k.Label
}

// homeEntry is one entry of the merged cross-plugin sections.
type homeEntry struct {
	m   plugin.Manifest
	k   plugin.KindDef
	row library.Row
}

// newestFirst orders entries by update time, then item id (the list's order).
func newestFirst(a, b homeEntry) int {
	return cmp.Or(b.row.Entry.UpdatedAt.Compare(a.row.Entry.UpdatedAt), cmp.Compare(b.row.Item.ID, a.row.Item.ID))
}

// enabledManifests lists the enabled plugins in registry order.
func (s *server) enabledManifests() []plugin.Manifest {
	var out []plugin.Manifest
	for _, m := range s.deps.Registry.Manifests() {
		if s.deps.Registry.IsEnabled(m.ID) {
			out = append(out, m)
		}
	}
	return out
}

// homeEntries loads at most limit newest entries per kind (of the status, or
// any when nil) and returns the newest limit across all kinds.
func (s *server) homeEntries(ctx context.Context, ms []plugin.Manifest, status *library.Status, limit int) ([]homeEntry, error) {
	var all []homeEntry
	for _, m := range ms {
		lang := s.titleLang(ctx, m)
		for _, k := range m.Kinds {
			rows, err := s.deps.Library.Rows(ctx, library.Query{PluginID: m.ID, Kind: k.Key, Status: status}, lang, limit, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				all = append(all, homeEntry{m: m, k: k, row: r})
			}
		}
	}
	slices.SortStableFunc(all, newestFirst)
	return all[:min(limit, len(all))], nil
}

// nextLabel names the unit after the entry's progress ("<label> <n+1>") for
// an active count or pages primary track that is not at its total; "" for
// every other kind of track.
func nextLabel(k plugin.KindDef, keys []string, e library.Entry, fields map[string]plugin.FieldValue) string {
	def, ok := primaryTrack(k, keys)
	if !ok {
		return ""
	}
	cur := e.Progress[def.Key]
	if total, bounded := progressTotal(def, fields); bounded && cur >= total {
		return ""
	}
	return progressLabel(k) + " " + strconv.Itoa(int(cur)+1)
}

// applyUpNext sets the Up next fields of a home card: the next unit and the
// releases aired beyond progress (absent from aired: none). The home page and
// the +1 answer on a home card share it.
func applyUpNext(rv *rowView, k plugin.KindDef, row library.Row, aired map[int64]int) {
	rv.NextLabel = nextLabel(k, row.Item.ProgressKeys, row.Entry, row.Item.Fields)
	rv.Aired = aired[row.Item.ID]
}

// getHome renders the home page; with no plugin enabled it sends the owner to
// Settings > Plugins.
func (s *server) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ms := s.enabledManifests()
	if len(ms) == 0 {
		http.Redirect(w, r, "/settings/plugins", http.StatusSeeOther)
		return
	}
	v := homeView{CalendarURL: "/calendar"}
	active := library.Active
	entries, err := s.homeEntries(ctx, ms, &active, homeActiveLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ids := make([]int64, 0, len(entries))
	for _, he := range entries {
		ids = append(ids, he.row.Item.ID)
	}
	aired, err := s.deps.Library.AiredCounts(ctx, ids, s.now())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	linked := map[string]bool{}
	for _, he := range entries {
		rv := s.rowView(he.m, he.row, "/", nil)
		applyUpNext(&rv, he.k, he.row, aired)
		rv.BadgeIcon, rv.BadgeLabel = badge(he.m, he.k)
		v.Cards = append(v.Cards, rv)
		if key := he.m.ID + "/" + he.k.Key; !linked[key] {
			linked[key] = true
			label := he.k.LabelPlural
			if label == "" {
				label = he.m.Name
			}
			v.ActiveAll = append(v.ActiveAll, homeLink{Label: label, Href: listURL(he.m, he.k.Key, string(library.Active), "", 1)})
		}
	}
	if err := s.markHomeSeries(ctx, v.Cards, entries); err != nil {
		s.internalError(w, r, err)
		return
	}
	if v.Days, err = s.homeComingUp(ctx, ms, s.now().In(s.location(ctx))); err != nil {
		s.internalError(w, r, err)
		return
	}
	if v.Series, err = s.homeSeries(ctx, ms); err != nil {
		s.internalError(w, r, err)
		return
	}
	recent, err := s.homeEntries(ctx, ms, nil, homeRecentLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, he := range recent {
		icon, label := badge(he.m, he.k)
		title := he.row.Title
		if title == "" {
			title = "(untitled)"
		}
		hr := homeRecent{Title: title, Href: itemURL(he.m, he.row.Item.Source, he.row.Item.SourceID, he.row.Item.Kind),
			Status: statusLabel(he.k, he.row.Entry.Status), StatusKey: string(he.row.Entry.Status), BadgeIcon: icon, BadgeLabel: label}
		hr.RepeatMarker, _, _ = repeatBadges(he.k, he.row.Entry)
		if !he.row.Entry.UpdatedAt.IsZero() {
			hr.Updated = he.row.Entry.UpdatedAt.Format("2006-01-02")
		}
		v.Recent = append(v.Recent, hr)
	}
	w.Header().Set("Vary", "HX-Request, HX-Target, HX-History-Restore-Request")
	s.render(w, r, "home", v)
}

// markHomeSeries adds the series part badge to the cards, per plugin (the
// list's markSeries works on one manifest's rows).
func (s *server) markHomeSeries(ctx context.Context, cards []rowView, entries []homeEntry) error {
	for _, m := range s.enabledManifests() {
		var views []rowView
		var rows []library.Row
		var idx []int
		for i, he := range entries {
			if he.m.ID == m.ID {
				views, rows, idx = append(views, cards[i]), append(rows, he.row), append(idx, i)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if err := s.markSeries(ctx, m, views, rows); err != nil {
			return err
		}
		for j, i := range idx {
			cards[i] = views[j]
		}
	}
	return nil
}

// homeComingUp lists the stored releases of active entries airing in the next
// 7 days (the calendar feed's data), grouped by day: one bounded query over
// the enabled plugins' schedule-capable sources.
func (s *server) homeComingUp(ctx context.Context, ms []plugin.Manifest, now time.Time) ([]homeDay, error) {
	var sources []library.ScheduleSource
	byID := map[string]plugin.Manifest{}
	for _, m := range ms {
		byID[m.ID] = m
		for _, si := range m.Sources {
			if si.SupportsSchedule {
				sources = append(sources, library.ScheduleSource{PluginID: m.ID, Source: si.ID})
			}
		}
	}
	rows, err := s.deps.Library.ScheduleBetween(ctx, now, now.Add(homeWindow), sources, homeComingLimit)
	if err != nil {
		return nil, err
	}
	langs := map[string]string{} // one title language read per plugin
	var days []homeDay
	for _, row := range rows {
		m, ok := byID[row.PluginID]
		if !ok {
			continue
		}
		k, ok := kindDef(m, row.Kind)
		if !ok {
			continue
		}
		lang, ok := langs[m.ID]
		if !ok {
			lang = s.titleLang(ctx, m)
			langs[m.ID] = lang
		}
		title := library.ResolveTitle(row.Titles, lang, titleFallback(m, k, row.Kind))
		if title == "" {
			title = "(untitled)"
		}
		at := row.AirsAt.In(now.Location())
		label := at.Format("Monday, 2 January")
		if len(days) == 0 || days[len(days)-1].Label != label {
			days = append(days, homeDay{Label: label})
		}
		d := &days[len(days)-1]
		d.Releases = append(d.Releases, homeRelease{Title: title, Label: releaseLabel(k, row.Season, row.Number),
			Href: itemURL(m, row.Source, row.SourceID, row.Kind), Season: row.Season, Number: row.Number, At: at})
	}
	return days, nil
}

// homeSeries returns the newest series with derived status active across
// the plugins (one bounded library query).
func (s *server) homeSeries(ctx context.Context, ms []plugin.Manifest) ([]homeSeries, error) {
	byID := map[string]plugin.Manifest{}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		byID[m.ID] = m
		ids = append(ids, m.ID)
	}
	rows, err := s.deps.Library.ActiveSeries(ctx, ids, homeSeriesLimit)
	if err != nil {
		return nil, err
	}
	var out []homeSeries
	for _, row := range rows {
		m, ok := byID[row.Series.PluginID]
		if !ok {
			continue
		}
		k, ok := kindDef(m, row.Series.Kind)
		if !ok {
			k = m.Kinds[0]
		}
		icon, label := badge(m, k)
		hs := homeSeries{Title: row.Series.Title, Href: seriesURL(m.ID, row.Series.UID), Cover: covers.Src(row.Cover),
			Progress: strconv.Itoa(row.Completed) + "/" + strconv.Itoa(row.Parts), BadgeIcon: icon, BadgeLabel: label, BarPct: -1}
		if row.Parts > 0 {
			hs.BarPct = row.Completed * 100 / row.Parts
		}
		out = append(out, hs)
	}
	return out, nil
}
