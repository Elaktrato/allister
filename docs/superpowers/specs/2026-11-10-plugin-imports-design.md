# Plugin imports and the scheduled AniList import (design, 2026-11-10)

## Intent

Two anime importers, in their own Import section on the anime plugin's
settings page: AniList **by username** (public list, no credentials) and the
MyAnimeList **XML export file**. The user chooses what happens to entries
that already exist locally, and the AniList import can run on a schedule so
the local list follows the account.

## Contract (`internal/plugin`)

- Optional `AccountImporter { ImportAccount(ctx, account string)
  (ParseResult, error) }`; `ImporterInfo.AccountLabel` (data; non-empty means
  the UI asks for a name instead of a file).
- `ParsedEntry` gains `UpdatedAt *time.Time`: the entry's source-side
  modification time when the source knows one (AniList does, the MyAnimeList
  export does not).

## Import modes — the user's choice

Stored per importer as `plugin.<id>.import.<importer>.mode` and saved with
each import (migration 00015 adds `imports.mode`), so resume and scheduled
runs keep it:

- **skip** (default): entries already in the list stay as they are.
- **newer**: update status, score and primary progress only when the source
  entry was modified after the local entry; rows without `UpdatedAt` are
  skipped (the MyAnimeList export has none).
- **always**: update them unconditionally.

Updates write only the three list fields (status, rating, primary progress) —
never item data, covers or credits. A run reports updated rows separately
from imported and skipped ones (`Report.Updated`).

## Importers

- **AniList username** (`anilist-user`, account importer): one GraphQL
  `MediaListCollection(userName:, type: ANIME)` request; statuses
  CURRENT/REPEATING → active, PLANNING → planned, COMPLETED → completed,
  PAUSED → paused, DROPPED → dropped; the account's score format is
  normalised to the core's 0–10 (0 unrated); per-entry `updatedAt`; rows are
  keyed by the AniList id, so the source gains an `anilist` lookup type.
  An unknown user is a clear import error, not a failure per row.
- **MyAnimeList XML export** (`mal-xml`, file importer): parses the export's
  `myanimelist/anime` entries (id, title, watched count, score, status
  1/2/3/4/6 → active/completed/paused/dropped/planned); rows are keyed by the
  existing `mal` lookup type.

## UI

- The plugin settings page gains an **Import** section, one form per
  importer: the account field or the file picker, the mode select (the two
  unsupported options hidden or annotated for importers without
  timestamps), and — for the AniList importer — the schedule select
  (off / daily / weekly). The section shows the last run and links to the
  import queue. It posts to `POST /settings/plugins/{id}/import/{importer}`.
- The generic `/settings/import` page keeps serving the other plugins'
  importers and the queue/history.

## Scheduler

- Settings: `plugin.<id>.import.<importer>.account` (the stored username) and
  `.schedule` (off/daily/weekly); the schedule select is only offered for
  account importers.
- Core job `scheduled_import` {plugin, importer}: runs the stored account
  through the runner with the stored mode, then re-enqueues itself with
  `run_after` = now + interval, as long as the schedule is still enabled.
  "Another import is active" skips this round and re-enqueues; the pending
  run survives restarts because it is a normal job row. Saving a schedule
  enqueues the first run.
- No new cron infrastructure: it is a self-rescheduling job like any other.

## Non-goals

MyAnimeList by username (needs OAuth); two-way sync; per-field rules;
importing anything the source does not list.

## Tests

Contract round trip; AniList mapping (statuses, score normalisation,
updatedAt, unknown user); XML parsing (status codes, missing fields); account
submit; the mode matrix (skip/newer/always, no-timestamp rows); update
writes only list fields and bumps the entry timestamp; the scheduled job
(runs, re-enqueues with the interval, stops when disabled, steps aside while
another import runs); the settings forms (right controls per importer, mode
and schedule persisted, invalid values refused); the report and import page
counters.
