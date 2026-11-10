# Subscribable calendar (design, 2026-11-10)

## Intent

A calendar feed the owner subscribes to locally (Thunderbird/KOrganizer over a
Headscale tailnet): every entry on the **watching** list contributes its
upcoming episode air dates as timed events, so "new episode tonight" appears
in the calendar apps. No public exposure, no accounts — same posture as the
rest of the app.

## Goals

- An optional source capability for release schedules, declared as data and
  implemented by the anime plugin against AniList's airing schedule.
- A core endpoint `GET /calendar.ics` serving a valid iCalendar feed of the
  events, never hitting the source while serving.
- Schedules cached in the database and refreshed through the normal paced job
  queue (rate limits, plugin gate and retries all reused).
- Core stays media-agnostic: no "episode" wording in core code or templates
  (the guard enforces it); the event summary takes the release label from the
  kind's primary progress definition.

## Non-goals

- CalDAV write access, per-entry calendars, alarms/reminders (calendar apps
  set their own), authentication, events for statuses other than active.
- Finished/past schedules kept forever (only a one-day look-back is served).

## Approaches considered

- Serving the feed from live source queries: rejected — calendar clients
  poll, and the AniList rate limit (30/min) plus latency make polls fragile.
- All-day events on the air date: rejected in favour of timed events at the
  known air time (the owner's choice).
- Fetching schedules inside the feed request instead of a job: rejected —
  jobs are the app's pattern (paced, retried, held while the plugin is off).

## Design

- **Contract** (`internal/plugin`): `Airing struct { Number int; AirsAt
  time.Time }` and an optional `ScheduleSource interface { Schedule(ctx,
  kind, id string) ([]Airing, error) }`. `SourceInfo` gains
  `SupportsSchedule bool` so the core knows which sources can answer without
  type assertions at every corner. Airing errors follow the existing
  source-error contract.
- **Anime plugin**: `Schedule` queries AniList
  `Media(id: $id) { airingSchedule { nodes { episode airingAt } } }` and maps
  each node to `Airing{Number: episode, AirsAt: airingAt}`; errors map like
  the other source methods. `SourceInfo.SupportsSchedule = true`.
- **Storage** (migration 00014): `item_schedules(item_id PRIMARY KEY,
  synced_at)` and `airings(item_id, number, airs_at, PRIMARY KEY(item_id,
  number))`, both `ON DELETE CASCADE` from items. A schedule row marks "this
  item was asked" even when the answer is empty (a finished show must not be
  re-fetched on every poll).
- **Refresh**: a new job kind `sync_schedule`, payload `{item_id, plugin_id}`
  (plugin gate like the sync job). Its handler loads the item, skips unless
  the entry is still active and the source supports schedules, calls
  `Schedule`, and replaces the stored rows. Rate limits and outages map to
  `jobs.ErrRetryLater`, a bad answer to `ErrPermanent`, matching the People
  fix. Two producers: the existing `sync_item` job also refreshes the
  schedule when the entry is active (so adding a watching entry populates it
  immediately), and the feed enqueues refreshes for active items whose
  schedule is missing or older than 12 hours (at most 20 per request, best
  effort) — polls then self-heal without ever blocking.
- **Feed** (`internal/web`, `GET /calendar.ics`, `text/calendar;
  charset=utf-8`): events for stored airings of active entries of
  schedule-capable sources, from 24 hours ago onward. One timed VEVENT per
  airing: `DTSTART`/`DTEND` at the air time (30 minutes), `UID
  <item>-<number>@allister`, `SUMMARY "<title> — <progress label> <number>"`
  (label from the kind's primary progress, so the core says no media word;
  "Release" when the kind has none), `DESCRIPTION` with the source's link
  label and the absolute item URL (request host), `DTSTAMP`, and a calendar
  `PRODID`/`X-WR-CALNAME "Allister"`. Proper iCalendar escaping (backslash,
  semicolon, comma, newline) and 75-octet line folding with CRLF endings.
  Titles resolve like the list (the plugin's title language, then the kind's
  fallback order).

## Tests

Contract: `Airing` JSON round trip is covered by the existing contract test
sweep; the plugin fixtures gain a schedule so the optional interface is
proven end to end. Anime: the AniList response maps to `Airing`s, errors map
like the other methods. Library: replace semantics (even empty), cascade on
item delete, the feed query returns active entries only. Jobs: the handler
stores only for active entries, maps retry/permanent errors, refreshes on
`sync_item`. Web: the feed's content type and calendar structure (BEGIN/
END, folding, escaping), active-only and schedule-capable-only events, past
airings filtered, the bounded refresh enqueues, and that no core file gains a
media word (guard).
