# Tracking depth (design, 2026-11-16)

First of five projects for the next version (tracking depth; managing
entries; finding what's next; looking back; running it). It covers five
features: repeat viewings, a read-only timeline, notes and reviews, the
owner's own tags, and custom lists. Authentication was split off to issue
#33.

## Intent

The owner uses Allister daily and wants each entry to carry its whole
history, not one overwritten state. In their words: rewatches and rereads
should be "as deep as possible, so we get new dates, maybe even a new score
and a count on top of it". Alongside that: notes on the entry, a review per
viewing and notes on single episodes or chapters ("having both would be
neat"); personal labels ("comfort", "with-partner"); and custom lists for
what no media type tracks on its own, in their words "multimedia
franchises, Kingdom Hearts has games, manga and novels" and "which games
... I've 100% completed and got all achievements for".

The owner is the only user and accepts breaking changes before a full
release; **their existing data must survive the upgrade**, and their older
backups must still restore.

## Goals

- **Runs.** Every viewing / reading / playthrough of an item is a *run*
  with its own status, score, start and finish date, review and progress.
  A first watch is run 1.
- **Start again** on a finished entry starts run n+1: status Active
  (shown in the plugin's "watching" status with a repeat marker),
  progress 0, started today, the previous run's score prefilled, review
  empty.
- A **repeat count** ("x3", the number of completed runs) on cards, rows
  and the item page once an item was completed at least twice.
- The **item page** shows every run (newest first) with dates, score,
  status and review; past runs can be edited (dates, score, review) and
  deleted.
- A **read-only timeline** per run, built from the existing change log
  (`entry_events`): progress steps, status and score changes, with notes
  on single units shown in place. Runs with no logged events say so.
- **Entry notes** (general, the same across runs; today's comment becomes
  them), a **review per run**, and **unit notes** (one note per unit
  number of a count or pages track within a run, e.g. "episode 7 of run
  2"). All three use a small formatting language with spoilers.
- **Own tags**: personal labels shared across all plugins, set in the entry
  dialog, filterable in every list.
- **Custom lists**: named, cross-plugin collections with their own page,
  optional manual order, a progress line ("6 of 14 done") and adding from
  the library or straight from search.
- Backup/restore and the per-plugin JSON export keep all of it.

## Non-goals

- Editing or backdating the timeline (owner decision: read-only is enough);
  events stay append-only. Unit notes are editable; the events are not.
- Reading repeat counts out of imports (AniList `repeat`, Letterboxd
  rewatches): importers keep writing what they write today, into the
  current run. No new import functionality in this version.
- Rule-based ("smart") lists, nested lists, sharing lists.
- Tags on a single run, tag colours, tag hierarchies.
- A repeat-count filter or sort (can come later; nothing here prevents it).
- Statistics beyond what is said under Statistics (the year in review and
  goals are project 4).

## Anti-goals

- Losing or altering any existing entry data in the upgrade (status,
  score, dates, comment, every progress value including inactive keys, the
  event log).
- Media words in core code, plugin HTML/JS, inline scripts or inline
  styles; the strict-CSP path, theme tokens and htmx rules in AGENTS.md
  stay.
- A slower list: list pages must stay within a few ms for a normal
  library (a few thousand entries).
- A rewatch that makes a show look dropped: abandoning a repeat run must
  not change how the entry presents itself (see Display run).

## Constraints

- Migrations: one new migration, **00019**; never edit applied ones; the
  00008 gap stays.
- The plugin contract stays serialisable data (JSON round trip tested);
  only `cmd/allister` imports plugins; the guard tests stay green.
- No new Go module dependency (owner's pins policy; today only goose and
  SQLite).
- Statuses stay the closed core enum of five; plugins only supply labels.
- Dates are calendar days; NULL score and 0 are different.

## Approaches

The central choice was how runs are stored.

- **A. Current run on the entry, past runs in a history table.** Least
  code churn (about 90 places read entry fields), but a run lives in two
  shapes and needs merging code everywhere history matters.
- **B. Every run in a runs table; the entry only means "in my library"
  (chosen by the owner).** One shape for every run; the cleanest model for
  history, stats per run and the timeline. Cost: every reader of entry
  status/score/dates/progress (list, filters, sorts, stats, home,
  calendar, series, imports, backup, export) moves to the runs table. The
  owner accepted the breakage risk ("we have no users aside from myself").
- **C. Rebuild history from the change log.** No new tables, but no room
  for per-run reviews or unit notes and nothing before the log existed;
  fails "as deep as possible".

Smaller calls (made by Claude, owner may correct):

- **Formatting** is a small in-house renderer, not a Markdown library plus
  a sanitiser: no new dependencies, output safe by construction.
- **Tags, lists and unit notes** get their own tables in the same
  migration.

## Data model (migration 00019)

All in one migration that converts existing data.

- `entries` becomes library membership: `item_id` (PK, FK items, cascade),
  `notes` TEXT (from today's `comment`), `added_at`, `updated_at`,
  `display_run_id` (FK entry_runs; see Display run). The status, rating,
  dates and comment columns go (SQLite table rebuild; the builder chooses
  the SQL).
- `entry_runs`: `id`, `item_id` (FK entries, cascade), `number` (1..n,
  dense, UNIQUE with item_id), `status` (the five statuses), `rating`
  (NULL or 0..10), `review` TEXT, `started_on`, `finished_on`,
  `created_at`, `updated_at`.
- `run_progress` replaces `entry_progress`: `run_id`, `key`, `value`,
  PK (run_id, key). Inactive keys' values are kept, per run, as today.
  The active progress keys stay on the item (`items.progress_keys`), so
  every run of an item tracks the same keys.
- `entry_events` gains `run_id` (nullable FK entry_runs, ON DELETE SET
  NULL; existing rows get their item's run 1). Deleting a run keeps its
  events, so past activity in the heatmap never changes (Claude's call);
  removing the entry still cascades through `item_id` as today. No new event types: starting a run is logged as a
  status change (completed -> active) on the new run.
- `unit_notes`: `run_id`, `progress_key`, `unit` (integer >= 1), `body`,
  `created_at`, `updated_at`; PK (run_id, progress_key, unit). Only for
  progress tracks of type count or pages.
- `user_tags`: `id`, `name` (unique, case-insensitive), `created_at`;
  `entry_user_tags`: `item_id` (FK entries, cascade), `tag_id` (FK,
  cascade), PK both.
- `lists`: `id`, `name` (unique, case-insensitive), `manual_order` (bool),
  `created_at`, `updated_at`; `list_items`: `list_id` (cascade), `item_id`
  (FK entries, cascade), `position`, `added_at`, PK (list_id, item_id).
  An entry removed from the library leaves every list; deleting a list
  never touches entries.

Conversion: each existing entry becomes run 1 with its status, rating,
dates, progress (all keys) and `updated_at`; its comment becomes the
entry's notes; its events point at run 1. A migration test seeds the 00018
schema with real-shaped rows (several statuses, NULL vs 0 rating,
inactive progress keys, events) and checks every value after 00019.

## Display run

What the list, cards, filters, sorts, home page, calendar, series status
and statistics' status/score treat as "the entry's state":

- the **latest run**, except
- when the latest run is a **repeat run (number > 1) with status dropped**:
  then the latest **completed** run (the abandoned repeat stays in the
  history as dropped; the entry presents as completed with that run's
  score and dates). If no earlier run is completed, the latest run.

A paused repeat run stays paused and shows under Paused, resumable (Claude's
call; an earlier message suggested treating a pause like a drop, which
would lose a resumable state).

`entries.display_run_id` stores this, maintained in the same transaction
as every run write, so list queries join one row instead of computing a
max per item. A test recomputes the rule for every entry and compares.

Cards, rows, the entry dialog and +1 all show and act on the **display
run**, so what you see is what you edit (Claude's call). An abandoned
repeat is only reachable from the run history, where it can be edited or
deleted. "Start again" is offered when the display run is completed.

## Repeats

- Plugin contract: `KindDef.Repeat *RepeatDef` with `Action` (button,
  e.g. "Rewatch", "Reread", "Replay") and `Marker` (badge on an active
  repeat run, e.g. "Rewatching"). Optional; the core falls back to a
  neutral "Start again" / "Again". Every shipped plugin declares it.
  JSON round-trip test.
- Starting again: new run n+1, status active, `started_on` today (in the
  configured time zone), rating copied from the previous completed run,
  progress empty, review empty; logged as a status event with origin
  manual (counts as activity only as the heatmap already defines it).
- Repeat count = completed runs; shown when >= 2 as "x<n>" (the builder
  picks the icon/wording).
- The item page's run history: newest first; each run shows number,
  status (plugin label), dates, score, review, a link to its timeline;
  past runs have Edit (dates, score, review, and status completed or
  dropped). Any run can be deleted while the entry has two or more,
  including the latest (the undo for a mistaken "Start again"), with
  confirmation; later runs renumber densely and the display run is
  recomputed. Deleting the only run means removing the entry, so it is
  not offered.

## Timeline (read-only)

On the item page, per run, collapsed by default:

- progress events as unit steps: a step from 4 to 5 reads "Episode 5"
  (the track's label from the plugin), a jump from 4 to 7 one line
  "Episodes 5-7" (builder chooses how labels pluralise; plugins already
  declare track labels, a plural label may need a contract field);
  percent and duration tracks show the value change;
- status and score changes;
- the time in the configured time zone; import and restore events are
  marked as such;
- unit notes appear at their unit, even when no event exists for it;
- a run with no events: "Nothing was logged for this run".

## Notes, reviews, unit notes

- Entry notes (max 10,000 runes) are edited in the entry dialog; the
  review (max 10,000) belongs to a run and is edited in the dialog for
  the latest run, in the run history for past runs; unit notes (max
  2,000) are added or edited from the timeline, and after a +1 the
  response offers a small "Note on <unit>" link for the unit just
  logged.
- Formatting language (new generic package, e.g. `internal/textfmt`):
  paragraphs and line breaks, `**bold**`, `*italic*`, `- ` and `1. `
  lists, `[text](https://...)` links (https only, `rel="noopener
  noreferrer nofollow"`), and `||spoiler||` spans (may cover a whole
  paragraph) hidden until clicked (handler in `app.js`, no inline script;
  without JS they stay readable on focus or hover is the builder's call
  as long as they are hidden by default). Everything else is escaped.
  Fuzz test: no output ever contains a tag or attribute outside the
  allowed set.
- Input hygiene as for comments today (control characters rejected).

## Own tags

- Set in the entry dialog: a text input with suggestions from existing
  tags (a `datalist`), several per entry; a new name creates the tag.
  Names: single line, 1-40 runes, case-insensitively unique, stored as
  first typed.
- Filter in every list next to genres/tags, any/all like them (the
  builder picks the URL parameter and documents it in `docs/usage.md`).
- Shown on the item page; a table column option.
- Manage (rename, merge by renaming onto an existing name, delete) on a
  Settings > Tags page.

## Custom lists

- Sidebar: a "Lists" group under the plugins with each list and "New
  list". `/lists` (overview) and `/lists/{id}`.
- A list page: name, progress line "<completed> of <n> done" (display
  status completed), entries as the existing cards (mixed plugins, each
  with its plugin's status labels and +1), rename, delete (entries stay),
  order mode: manual (move up/down like series parts) or sorted (title,
  date added, status).
- Adding: from an item page ("Add to list", choose one or more lists);
  on the list page, a search over the whole library; and "Add from
  search", going through a plugin's normal search/add and adding the new
  entry as planned (reusing the series "add from search" return-flow
  pattern).
- An item can be in any number of lists.

## Statistics

- Time and units: every **run** counts the way an entry counts today
  (its progress, whatever its status), each filtered and bucketed by its
  own finish date; a run without a finish date counts only in an
  all-time report, as an undated entry does today. A 2026 rewatch adds
  to 2026. The status filter narrows the runs summed by each run's own
  status (Claude's call).
- Entry counts, the score average and distribution use the display run,
  once per entry, filtered by the display run's finish date and status
  exactly as entries are today. Numbers for a library without repeats
  must equal today's (regression test on a seeded library).
- The activity heatmap is unchanged (event log).

## Backup, restore, export

- Universal backup `format_version` 2: an entry carries `notes`, `runs`
  (number, status, rating, review, dates, progress, unit notes, events
  per run), `user_tags`; top-level `lists` after `items` (items
  identified as everywhere in the backup, like `series`).
- Restore reads format 1 (entry -> run 1, comment -> notes, events ->
  run 1) and 2. Merge rules unchanged: an item that exists is skipped as
  a whole; a list whose name exists is merged by adding missing items
  (Claude's call; the builder may report instead).
- Per-plugin JSON export: entries gain `notes`, `runs`, `user_tags`,
  `lists`; `plugin.ExportEntry` gains matching optional fields (JSON
  round trip). The existing top-level entry fields keep carrying the
  display run's values so the export stays readable.
- Importers keep their `plugin` entry type; the core writes it into the
  latest run (a new entry: run 1).

## What is left to the builder

- Exact SQL of the table rebuild and the Down migration.
- Template markup and wording within the existing styles and theme
  tokens; icons.
- Whether the timeline loads lazily (htmx fragment) or inline.
- URL parameters for the tag filter and list sorting (documented in
  `docs/usage.md`).
- Splitting into reviewable, green commits. Suggested order: migration and
  library (runs, display run) with every existing reader moved over;
  repeats and run history; timeline; notes and formatting; tags; lists;
  backup/export; docs and CHANGELOG.
