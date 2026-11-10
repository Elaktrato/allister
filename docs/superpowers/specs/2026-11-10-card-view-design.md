# Card view for the core list (design, 2026-11-10)

## Intent and why

The list today is one table. The owner wants a second, visual view for
browsing: a responsive grid of cover cards with the title on a semi-opaque
overlay, a usable progress control (the table's +1 on the card), and a small
colored corner marking the status — everything else the table shows is
omitted. The plugin's list page (what `/` redirects to, the "home") opens in
this view by default, and per-plugin settings decide what the home shows
(example: only the things being watched right now).

## Goals

- Card grid view of any kind's list, driven by the same query engine,
  filters, tabs, sorting and pagination as the table.
- Usable progress: a +1 on the card, same POST and swap semantics as the
  table's +1 (the response re-renders that one card).
- Status as a colored corner element; colors fixed per core status class.
- Per-plugin home settings: default view and a default status filter.
- Everything URL-bookmarkable exactly like the current list; the htmx
  contract (rows region, Vary, out-of-band swaps, no focused-input swaps)
  unchanged.

## Non-goals

- Multi-status home filters (the engine filters by one status class; a set
  would widen `library.Query` — a small later step if wanted).
- Card-specific columns or fields; the card shows cover, title, progress
  and status only.
- Any plugin code: cards are core, driven by the kind's declared progress
  and the stored cover.

## Approaches considered

- A separate "home" route rendering its own card page: rejected — it
  duplicates the query, filter, tab and pagination wiring for no gain; the
  view rides the existing list instead.
- Progress opens the entry dialog instead of a +1: rejected — heavier than
  a quick bump; the dialog stays reachable through the card's title link
  and the entry routes.
- One JSON settings key for the whole home configuration: rejected — the
  settings store uses one flat, documented key per choice
  (`plugin.<id>.*`).

## Design

- **View param.** `view=cards|table` on the list URL. Absent or invalid →
  the plugin's `home.view` setting (default `cards`), an invalid value is
  named in the existing ignored-parameters notice. Every generated list URL
  (tabs, sort, filters, pagination, view toggle) carries the view along,
  like `status` and `kind` today.
- **Home settings** (`/settings/plugins/{id}`, one new form section,
  POST `/settings/plugins/{id}/home`): `plugin.<id>.home.view`
  (`cards` default, or `table`) and `plugin.<id>.home.statuses` (one core
  status class, or empty for all). Labels come from the kind's declared
  status labels.
- **Home status semantics.** A list URL without `status` uses the home
  setting's status; `status=all` always means all statuses; the "All" tab
  links to `status=all` so it stays reachable with a filtered home. The
  effective status rides in the filter bar's hidden field, so filter and
  sort requests preserve it.
- **Cards.** In the rows region, `view=cards` renders a grid
  (2 columns on a phone to 6 on a wide screen) of portrait
  (`aspect-[2/3]`) cards instead of the table. Each card: the stored cover
  (`object-cover`; a neutral placeholder without one), a bottom dark
  gradient overlay with the title (two-line clamp, linking to the
  overview), the progress text from the kind's primary progress (count
  `12/24`, unbounded `12`, percent `50%` — the table's progress renderer),
  and a +1 button posting the same entry-progress form as the table row;
  the +1 response swaps that card. Finished or ineligible progress hides
  the +1. A top-right corner triangle carries the status color with the
  status label as its accessible name: slate planned, indigo active, amber
  paused, emerald completed, rose dropped.
- **Sorting without the table head.** In card view the compact "Sort by"
  control (today small-screens only, inside the filter panel) renders
  outside the collapsed panel, always visible; its out-of-band option
  updates keep working.
- **View toggle.** Two icon buttons (grid/table) in the list header, plain
  links carrying the current query with the view swapped — no htmx, a view
  switch is rare.

## Tests

Web: view parsing (invalid → notice + plugin default), home defaults
applied and `status=all` reachable, both settings POSTs, card rendering
(grid, overlay, corner classes per status, +1 form), a +1 from a card
swaps exactly that card, and the htmx/Vary assertions unchanged. Guard:
the two new toggle icons are in the pinned font's name list.
