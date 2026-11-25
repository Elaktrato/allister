# Pause idle games on import (design, 2026-11-22, approved)

## Intent

The Steam account import (often scheduled daily/weekly) marks every game with any playtime as Playing and, depending on the import mode, either never updates existing games or overwrites their status each run, so the Playing list fills with games last touched years ago and manual fixes get undone. The owner wants games not played for a while to move to "on hold" (Paused) automatically, without manual work. Steam only for now.

## Goals

- A game whose last play is longer ago than the cutoff is imported as, or moved to, **Paused**; played within the cutoff it is **Playing**; a Paused or Planned game played again goes back to Playing on the next import.
- The cutoff is a setting on the Games plugin's settings page, default **30 days**, adjustable; 0 = off (today's behaviour).
- Steam imports never change the status of a **Completed** or **Dropped** game (playtime still updates); replaying stays the owner's "Replay".
- Games with 0 minutes stay Planned (as today).
- Manual edits are no longer undone by a re-import that has nothing new.

## Non-goals

- Other plugins or importers (AniList etc.), a general "no activity in Allister" rule, a separate daily job without an import, notifications.

## Approach (owner chose A)

A general import rule an importer opts into:

- **Contract (data only):** `plugin.ParsedEntry.LastActive *time.Time` (`last_active`, the source's last-activity time of the row) and `plugin.ImporterInfo.IdlePause bool` (`idle_pause`: rows carry LastActive; the plugin gets the cutoff setting and the rule applies). JSON round-trip tested; validation: IdlePause requires an account importer.
- **Steam importer:** parse `rtime_last_played` (Unix seconds; 0 = never/unknown -> LastActive nil); set `LastActive` and `UpdatedAt` to it, so the existing "only if newer" mode updates a game only when it was played after the entry's last change. Manifest sets `IdlePause: true`.
- **Core rule (internal/importer, generic, no media words):** for rows of an IdlePause importer with cutoff N > 0 days (setting `plugin.<id>.idle_days`, default 30):
  - a row whose status is Active and whose LastActive is more than N calendar days before today (configured time zone) gets status Paused;
  - for an existing entry whose shown status is Completed or Dropped, the import keeps the status (rating/progress as today);
  - the Active -> Paused move for an existing entry applies even when the row is not "newer" (it is derived from the cutoff, not a source change); a Paused -> Active move needs LastActive within the cutoff;
  - changes are logged with origin `import` (not on the activity heatmap); imports still never start runs.
- **Settings UI:** a number field "Pause games not played for … days (0 = off)" on the plugin settings page, shown only for plugins with an IdlePause importer; label text comes from the manifest/kind labels, not media words in core.
- Docs (usage.md import section, plugins.md contract fields), CHANGELOG Unreleased.

Rejected: B (all inside the Steam importer: it can't see settings or stored entries, so it can't keep Completed games or undo only its own pauses); C (a separate daily job with a stored last-played field: two moving parts and the import must still be stopped from flipping games back).

## Left to the builder

Exact setting key handling and form markup; how the runner reads the setting and today's date; test fixtures.
