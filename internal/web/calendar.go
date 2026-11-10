package web

// The calendar feed, GET /calendar.ics: one timed event per stored airing of
// an active entry whose source declares the schedule capability. It reads
// only the database; stale schedules are enqueued for the sync_schedule job
// (through the plugin's ScheduleJobs entry) so calendar polls never hit a
// source themselves.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// calendarLookBack is how far behind now the feed lists airings, so an
// release that aired yesterday still appears.
const calendarLookBack = 24 * time.Hour

// calendarStaleAfter is the age at which a stored schedule is refreshed.
const calendarStaleAfter = 12 * time.Hour

// calendarRefreshCap bounds the refresh jobs one feed request enqueues.
const calendarRefreshCap = 20

// icsLayout is the iCalendar timestamp form (UTC).
const icsLayout = "20060102T150405Z"

// getCalendar serves the iCalendar feed.
func (s *server) getCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.deps.Library.ScheduleEntries(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := s.now()
	var b strings.Builder
	icsLine(&b, "BEGIN:VCALENDAR")
	icsLine(&b, "VERSION:2.0")
	icsLine(&b, "PRODID:-//Allister//Calendar//EN")
	icsLine(&b, "CALSCALE:GREGORIAN")
	icsLine(&b, "METHOD:PUBLISH")
	icsLine(&b, "X-WR-CALNAME:Allister")
	for _, row := range rows {
		if row.AirsAt.Before(now.Add(-calendarLookBack)) {
			continue
		}
		m, ok := s.deps.Registry.Manifest(row.PluginID)
		if !ok || !sourceSchedules(m, row.Source) {
			continue
		}
		k, ok := kindDef(m, row.Kind)
		if !ok {
			continue
		}
		title := s.resolveTitle(ctx, m, k, row.Kind, row.Titles)
		if title == "" {
			title = "(untitled)"
		}
		icsLine(&b, "BEGIN:VEVENT")
		icsLine(&b, fmt.Sprintf("UID:%d-%d@allister", row.ItemID, row.Number))
		icsLine(&b, "DTSTAMP:"+now.UTC().Format(icsLayout))
		icsLine(&b, "DTSTART:"+row.AirsAt.UTC().Format(icsLayout))
		icsLine(&b, "DTEND:"+row.AirsAt.Add(30*time.Minute).UTC().Format(icsLayout))
		icsLine(&b, "SUMMARY:"+icsText(title+" — "+progressLabel(k)+" "+fmt.Sprint(row.Number)))
		link := m.Sources[0].LinkLabel
		for _, si := range m.Sources {
			if si.ID == row.Source {
				link = si.LinkLabel
			}
		}
		if link != "" {
			link += ": "
		}
		icsLine(&b, "DESCRIPTION:"+icsText(link+"http://"+r.Host+itemURL(m, row.Source, row.SourceID, row.Kind)))
		icsLine(&b, "END:VEVENT")
	}
	icsLine(&b, "END:VCALENDAR")

	s.enqueueStaleSchedules(ctx, now)

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=300")
	w.Write([]byte(b.String()))
}

// sourceSchedules reports whether the manifest's source declares the
// schedule capability.
func sourceSchedules(m plugin.Manifest, source string) bool {
	for _, si := range m.Sources {
		if si.ID == source {
			return si.SupportsSchedule
		}
	}
	return false
}

// progressLabel is the kind's primary progress label (the feed's word for a
// release, supplied by the plugin data); "Release" when the kind has none.
func progressLabel(k plugin.KindDef) string {
	if def, ok := progressDefOf(k, k.Progress.PrimaryKey); ok && def.Label != "" {
		return def.Label
	}
	return "Release"
}

// enqueueStaleSchedules asks the sync_schedule job to refresh active entries
// whose schedule was never stored or is old (best effort, bounded).
func (s *server) enqueueStaleSchedules(ctx context.Context, now time.Time) {
	stale, err := s.deps.Library.StaleSchedules(ctx, now.Add(-calendarStaleAfter))
	if err != nil {
		log.Printf("calendar: stale schedules: %v", err)
		return
	}
	sent := 0
	for _, st := range stale {
		if sent >= calendarRefreshCap {
			break
		}
		m, ok := s.deps.Registry.Manifest(st.PluginID)
		if !ok || !sourceSchedules(m, st.Source) {
			continue
		}
		mk := s.deps.ScheduleJobs[st.PluginID]
		if mk == nil {
			return
		}
		kind, payload := mk(st.ItemID)
		if err := s.deps.Jobs.Enqueue(ctx, kind, payload); err != nil {
			log.Printf("calendar: enqueue schedule for %d: %v", st.ItemID, err)
			return
		}
		sent++
	}
}

// icsLine writes one unfolded content line (escaping happens in the values).
func icsLine(b *strings.Builder, line string) {
	foldICS(b, line)
}

// icsText escapes a text value per RFC 5545.
func icsText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldICS appends line with CRLF, folding at 75 octets on rune boundaries so
// multi-byte characters are never split; continuation lines start with one
// space.
func foldICS(b *strings.Builder, line string) {
	const max = 75
	width := 0
	for _, r := range line {
		w := len(string(r))
		if width+w > max {
			b.WriteString("\r\n ")
			width = 1
		}
		b.WriteRune(r)
		width += w
	}
	b.WriteString("\r\n")
}
