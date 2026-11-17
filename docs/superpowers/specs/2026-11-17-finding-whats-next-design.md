# Finding what's next: design

Issue #35, project 3 of the next version. Decisions by the owner are marked **(owner)**, everything else is the author's call and listed again at the end.

## Intent

The owner opens Allister mostly to see **what they are currently watching, reading or playing**; newly aired releases matter less. #35 makes that first view sharper, extends the calendar, and adds a way to pick something from the backlog.

## Goals

1. **Up next (home page) (owner: option 1A).** The "In progress" section becomes "Up next": the same cards (cover, +1, rewatch marker, series badge), sorted by last activity, and each card names the next unit ("Episode 7", "Chapter 12") and, when stored schedules know of aired releases beyond the current progress, a hint such as "2 aired". "Coming up", "Series in progress" and "Recently updated" stay unchanged.
2. **Calendar (owner: option 2).** `/calendar` gets a week view, a per-plugin filter, and a "Mark watched" button on aired releases.
3. **Backlog randomiser (owner: option 3A).** A "Pick one" button on a plugin's list draws one entry at random from the current filter (default status: planned), with a length filter declared by each kind as data, and a "Roll again".

## Non-goals and anti-goals

- No streaks, no pace, no recommendation scoring: the pick is uniformly random among matches.
- No new page for the randomiser (`/pick` was rejected: it duplicates the filter bar).
- No new schedule sources (games, books and movies stay out of the calendar, see `docs/usage.md`).
- No media words in the core (`internal/guard`): length measures come from manifests.
- No inline scripts or inline styles; only theme-token colour classes.

## Constraints

- Plugins declare data only; contract types stay serialisable (JSON round trip tested). A new `KindDef` field must be optional so existing manifests stay valid.
- htmx rules from `AGENTS.md` (Vary headers, no swaps over focused inputs, page handlers in `static/app.js`).
- No migration is expected (all three parts read existing tables). If one turns out to be needed, the number is 00021.

## Approaches considered

| Part | Chosen | Rejected and why |
|---|---|---|
| Up next | **A.** Sharpen the cards in place | B. A separate row list: loses the cover and the list's +1, bigger rework |
| Randomiser | **A.** "Pick one" on the existing list | B. `/pick` page: duplicates the filter bar and the URL parameters |
| Length | Kinds declare length measures as data; filtering in Go over the planned set | Filtering in SQL on a computed total time: needs per-kind expressions in the query engine for a bounded set |

## Design

### 1. Up next

- Section id and heading change to "Up next"; the data is the existing `homeEntries(..., Active, homeActiveLimit)`.
- Per card, a small line **Next: `<progress label> <n+1>`**, derived from the kind's primary progress track (`progressLabel(k)` plus current value + 1). Hidden when the track is unbounded-complete (current >= total > 0), for duration/percent tracks (no discrete next unit) and when no primary track is active.
- **Aired hint.** One bounded query (new `library.AiredSince`-style read over `airings` joined to active entries, limited to the cards shown) counts, per item, airings with `airs_at <= now` that lie beyond the entry's progress. For a kind with season and episode tracks the comparison is ordered by (season, number); for one count track by number. Rendered as "N aired" next to the Next line, only for N >= 1.
- The card template (`list_card`) is shared with the list; the new lines are added through optional `rowView` fields set only by the home handler, so the list is untouched.
- The rewatch marker already shows (`repeatBadges`); a test pins that it stays.

### 2. Calendar

- **Week view:** `?view=week&week=YYYY-MM-DD` (any day of the week; the week starts on Monday as the month grid does). Seven day columns/rows with the day's releases; previous/next/Today links; a Month/Week toggle that keeps the plugin filter. Days and times use the configured time zone (same `s.location`).
- **Plugin filter:** `?plugin=<id>` is plugin-level only and limits both views to one plugin's releases; the existing per-source schedule capability check (`sourceSchedules`) is reused unchanged. The select lists the enabled plugins that have at least one schedule-capable source. Unknown id: ignored with the full view (not an error).
- **Mark watched:** `POST /calendar/watched` with the item and the release's season and number. The button shows only on releases that have aired (`airs_at <= now`) and lie beyond the entry's progress. The handler sets the progress to **at least** that release: it never lowers progress and does nothing (still answers success) when progress is already at or beyond it, so a double click cannot double count. It goes through the same library path as +1 (one transaction, an `entry_events` row with origin `manual`, so the heatmap counts it). For a season-aware kind both the season and the episode track are set; if the kind's active tracks do not include the release's tracks, the button is not offered. A completed or dropped entry is left alone: the calendar only lists active entries today.
- **Repeat runs.** The aired hint and the Mark watched button are computed against the entry's display run. While the display run is an active **repeat** run (run number above 1), both are suppressed: stored airings cannot say which episodes a rewatch has seen, so "24 aired" would be wrong. The Next line still shows (it only reads the run's progress).
- **Numbering.** Mark watched is offered only when the entry's active tracks include every track the release sets (season-aware kind: season and episode; anime: episode, season 0 means "no season track"). The builder verifies that AniList (absolute numbers, anime has no season track) and TVmaze (season and number per season, matching the TV tracks) agree with the tracks, and gates the button per source if one does not.
- **Past airings:** the calendar reads stored airings of active entries. AniList's `airingSchedule` and TVmaze episode lists are expected to include aired episodes, but AniList pages its schedule (25 nodes by default). The builder verifies this against the clients and, if long shows lose their older episodes, requests a larger page (data-only change in the client); the spec accepts "only what the source reports".

### 3. Randomiser

- **Length declaration.** New optional `KindDef.Length []LengthDef` (data only):
  `LengthDef{Key, Label, Field, UnitsField, PerUnitField}`. The unit is derived from which fields are set (no separate unit property): `Field` alone is minutes (a game's `time_main`, a movie's `runtime`); `UnitsField` with `PerUnitField` (`episodes` x `runtime`/`duration`) is a total in minutes; `UnitsField` alone is a unit count, labelled with that field's `Unit` (for example "eps"). Any other combination is invalid. The filter's bound inputs are labelled "min" or the field's unit accordingly. Each measure has a label shown in the filter ("Main story", "Runtime", "Total time", "Episodes"). Validation (manifest tests): referenced fields exist and are numbers.
  - Games: `time_main`, `time_extra`, `time_complete` (minutes).
  - Movies: `runtime`.
  - TV: total time (`episodes` x `runtime`) and `episodes`.
  - Anime: total time (`episodes` x `duration`) and `episodes`.
  - Manga, books, light novels: none declared (no length filter shown).
- **Filter.** Parameters `len=<measure key>`, `len_min`, `len_max` (minutes or units; either bound may be empty). They are parsed in `internal/web` like the other list parameters, validated against the kind, bookmarkable, shown in the filter panel only for kinds that declare a length, and applied **only by the pick action** (and not to the table/card list, which already has `f.<field>` range filters for stored fields). Entries lacking the data (field unset) are **excluded** when a bound is set.
- **Pick.** `GET /{plugin}/pick?<the list's query parameters>` loads the matching entries (the list's query; when the request has **no** `status` parameter the pick uses planned, an explicit status, including "all", is honoured; the list page's "Pick one" button therefore carries the list's explicit status or `status=planned` when the list has none, because the list's own default comes from the per-plugin home setting; the list's `Rows` machinery, so genre, tags, own tags, `f.` filters and search all work), applies the length filter in Go, and picks one uniformly with `math/rand/v2`. It answers a **partial** (htmx, target a result panel on the list page) showing the pick as a card with title, cover, length, genres, a link to the entry, a **Roll again** button (same URL, new draw) and, only when the picked entry is planned, a **Start** button (status to active through the existing entry-change path). A full-page GET without htmx renders the list page with the panel filled, so a link works. No match: an empty-state message that names the filters.
- Bounded: the planned set of one kind is loaded in full for the draw (single user; a cap of 20,000 like `StatsRows` with a notice above it).
- Randomness comes from a new injectable server field (`rand`, defaulting to `math/rand/v2`; the server has none today), so tests draw deterministically. It touches the server constructor and the test helpers.

## Tests (outline, the plan details them)

- Manifest: `Length` validation, JSON round trip, guard test still green.
- Library: aired-beyond-progress read (display run aware, nothing for an active repeat run) (single track, season+episode ordering, nothing aired, completed entries excluded).
- Web: Up next heading and Next/aired lines, rewatch marker kept, hidden for percent/duration tracks; calendar week view bounds and Monday start, plugin filter, unknown plugin id; mark watched sets progress, is idempotent, never lowers, writes a `manual` event; pick: uniform over matches (seeded), length bounds, missing data excluded, empty state, htmx partial and full page, Roll again; docs test (README cap).
- Browser smoke test with Playwright against a seeded instance for all three parts.

## Docs

`docs/usage.md` (routes, URL parameters, home section, calendar, pick), `docs/plugins.md` (`Length`), `docs/handoff.md`, `CHANGELOG.md`. CSS cannot be verified locally: only classes from `tailwind-map.json`, styling unchecked until CI.

## Left to the builder

Exact markup and wording; the aired-count query shape; whether the calendar's week and month share one grid builder; the page size for AniList's schedule.

## Decisions taken on the owner's behalf

1. Length as an optional `KindDef.Length` declaration instead of reusing `StatsDef` (stats time means "time of a completed item", not the length of an unstarted one).
2. Length filtered in Go over the planned set, not in SQL.
3. Entries without length data are excluded when a bound is set.
4. The pick defaults to status planned and offers "Start".
5. "Mark watched" sets progress to at least the release (idempotent) instead of +1.
6. The aired hint and the Next line come only from stored schedules and the primary progress track; none for duration/percent tracks.
7. An unknown calendar plugin id is ignored.
8. A repeat run suppresses the aired hint and Mark watched; the pick's status default and the button's explicit status; `?plugin=` plugin-level; `LengthDef` unit derived from the fields set; a new injectable `rand` field.
9. v0.4.0 is tagged after #35 is merged **(owner)**; the `CHANGELOG.md` Unreleased items from projects 1 to 3 fold into the v0.4.0 section at that point.
