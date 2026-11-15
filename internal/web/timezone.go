package web

// The time zone setting (Settings > General): the zone the home page's
// Coming up and the calendar page group their days and show their times in.
// The .ics feed stays UTC.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/allister/allister/internal/settings"
)

// location is the effective time zone (UTC without a settings store).
func (s *server) location(ctx context.Context) *time.Location {
	if s.deps.Settings == nil {
		return time.UTC
	}
	return s.deps.Settings.TimeLocation(ctx)
}

// timeZoneView is the time zone form of the General page.
type timeZoneView struct {
	Value, Default, Error string
}

// timeZoneForm builds the form: the stored zone (empty field when none, the
// default shown as placeholder). typed, when set, is a rejected input shown again.
func (s *server) timeZoneForm(ctx context.Context, typed, problem string) timeZoneView {
	v := timeZoneView{Default: settings.DefaultTimeZone(), Error: problem}
	if s.deps.Settings != nil {
		v.Value, _ = s.deps.Settings.StoredTimeZone(ctx)
	}
	if problem != "" {
		v.Value = typed
	}
	return v
}

// postTimeZone stores the zone (POST /settings/time-zone, field time_zone). An
// empty value restores the default; an unknown name is refused with 422.
func (s *server) postTimeZone(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("time_zone"))
	if name != "" && !settings.ValidTimeZone(name) {
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "settings",
			s.generalView(r, s.timeZoneForm(r.Context(), truncateRunes(name, 100), "“"+truncateRunes(name, 100)+"” is not a time zone. Use an IANA name such as Europe/Berlin.")))
		return
	}
	if s.deps.Settings == nil {
		s.internalError(w, r, errors.New("no settings store"))
		return
	}
	if err := s.deps.Settings.SetTimeZone(r.Context(), name); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
