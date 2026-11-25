package importer

import (
	"context"
	"fmt"
	"time"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/settings"
)

// The idle rule of an importer with ImporterInfo.IdlePause: with the
// plugin's cutoff of N > 0 days (settings IdleDays), a row whose status is
// Active and whose LastActive lies more than N calendar days before today
// (the configured time zone) is treated as Paused, for a new entry and for
// an update. For an existing entry the import keeps a shown Completed or
// Dropped status, keeps a Paused one unless the row's last activity is
// known (and so within the cutoff), and under every mode but skip pauses a
// shown Active entry whose row is idle even when the row is not newer: the
// change follows from the cutoff, not from the source.

// idleState is the idle rule's view of one row.
type idleState struct {
	days   int  // the cutoff in days; 0 when the rule does not apply
	paused bool // the row was Active and is idle: its status is now Paused
}

// applyIdle evaluates the idle rule for rc's row and, for an idle Active
// row, changes its status to Paused.
func (r *Runner) applyIdle(ctx context.Context, rc *rowCtx) {
	if !rc.ref.info.IdlePause {
		return
	}
	days, loc := settings.DefaultIdleDays, time.UTC
	if r.d.Settings != nil {
		days, loc = r.d.Settings.IdleDays(ctx, rc.pluginID()), r.d.Settings.TimeLocation(ctx)
	}
	if days <= 0 {
		return
	}
	rc.idle.days = days
	if rc.entry.Status != plugin.Active || rc.entry.LastActive == nil {
		return
	}
	today := plugin.DateOnly(r.now().In(loc))
	last := plugin.DateOnly(rc.entry.LastActive.In(loc))
	if last.Before(today.AddDate(0, 0, -days)) {
		rc.entry.Status = plugin.Paused
		rc.idle.paused = true
	}
}

// idleNote is the row message part that explains an idle pause ("" when
// the row was not paused).
func (rc *rowCtx) idleNote() string {
	if !rc.idle.paused {
		return ""
	}
	return fmt.Sprintf("paused: no activity for more than %d days", rc.idle.days)
}

// idleStatus is the status an update applies to an existing entry shown as
// shown: the row's, or "" (keep the entry's) when the idle rule protects it.
func (rc *rowCtx) idleStatus(shown library.Status) library.Status {
	st := library.Status(rc.entry.Status)
	if rc.idle.days == 0 {
		return st
	}
	switch {
	case shown == library.Completed || shown == library.Dropped:
		return ""
	case shown == library.Paused && st == library.Active && rc.entry.LastActive == nil:
		return "" // no known activity: not proven to be within the cutoff
	}
	return st
}
