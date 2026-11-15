# Home page (design, 2026-11-15)

The owner asked for a home page across all plugins and approved the outline
("the way you picture it is great, just do that"); every detail below is the
builder's call, made for the owner.

## Intent

One page at `/` that answers "what am I in the middle of, what changed, what
is coming" across every enabled plugin, instead of redirecting to the first
plugin's list.

## Goals

- `GET /` renders the home page when at least one plugin is enabled (no
  plugin enabled: redirect to `/settings/plugins`, as today).
- Four sections, each hidden when it has nothing to show, in this order:
  1. **In progress** — entries with status class `active` across all enabled
     plugins and kinds, most recently updated first, at most 12; rendered as
     the existing list cards (cover, title, progress bar, working **+1**),
     each with its plugin's icon and kind label; a link per plugin to its
     list filtered to active.
  2. **Coming up** — the next 7 days of releases from the calendar data the
     iCalendar feed already uses (stored schedules of active entries of
     sources that support schedules), at most 10, grouped by day, each
     linking to its item; a link to the full calendar page.
  3. **Series in progress** — series whose derived status is active, across
     plugins, most recently updated first, at most 6, with "completed/parts"
     and links to the series page; hidden when there are none.
  4. **Recently updated** — the 10 most recently updated entries of any
     status across plugins, a compact list (title, plugin icon, status label,
     relative time), each linking to its item.
- A **Home** entry (icon `home`) at the top of the sidebar / phone top bar,
  marked current on `/`.
- Works on a phone: sections stack; the In progress cards use the list's
  card grid.

## Non-goals

- Configurable sections, widgets or per-plugin home settings for this page
  (the existing per-plugin home settings stay for the plugin lists).
- New data sources, new tables or migrations, stats on the home page.

## Anti-goals

- A slow `/`: at most one bounded query per enabled kind per section (all
  with LIMIT), no full-library scans.
- Duplicated list logic: cards, +1, status labels and URLs reuse the list's
  row builder and templates.

## Constraints

- Core only (`internal/web`, `internal/library` if a cross-plugin query is
  needed); no media words; theme colour classes only; no inline
  script/style; htmx rules unchanged; the +1 on a home card swaps the card
  in place like on a list.
- README ≤ 150 lines; `docs/usage.md` route table gets `/`.

## Approaches

- **A. A core page assembled from existing per-kind queries (chosen).**
  Query each enabled kind with a LIMIT and merge in Go (sort by update time,
  cut to the section limit). Reuses the list's row builder and the calendar
  and series code.
- **B. New cross-plugin SQL queries.** One query per section across all
  plugins. Fewer round trips, but duplicates the list query engine's
  filtering and title resolution. Not worth it for a handful of kinds.
- **C. Redirect `/` to a configurable "start list".** Cheapest, but does not
  give the cross-plugin view the owner asked for.

## Left to the builder

Exact markup and wording within the existing styles, the relative-time
format (reuse the list's "updated" text), the per-plugin "show all" link
wording.
