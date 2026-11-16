# Managing entries (design, 2026-11-16)

Project 2 of the next version (issue #34): update from source with a merge
view, bulk edit, and a duplicate finder with merge. Builds on the tracking
depth work (runs, notes, own tags, custom lists; migration 00019).

## Intent

The owner keeps a large library by hand and wants three tools to look after
it:

- **Update from source with a merge view** (the owner's own request): "If I
  know an item has been updated on the source lately I should be able to
  manually request an update from source where I get a merge view of what
  changed and pick and choose what I'd like to bring over to my allister
  entry." Today "Update from source" only fills empty fields, so a changed
  title, synopsis or total never arrives; nothing else refreshes a saved
  item's data.
- **Bulk edit**: change many entries at once.
- **Duplicate finder and merge**: find items that are the same thing (for
  example one book from Open Library and Google Books) and merge them
  without losing runs, notes, tags or memberships.

## Goals

- A **comparison page** that shows, for one item, the stored data next to an
  incoming version and lets the owner tick what to take. Rows appear only
  where something differs. Fields are compared one by one; titles, genres,
  tags, the cover, relations and credits (cast and crew) each as a whole
  ("keep mine" / "take theirs"). Rows where the stored side is empty start
  ticked; all others start unticked. "Apply selected" writes all ticked rows
  in one transaction; nothing else changes. With no differences the page
  says the item is up to date.
- **Update from source** opens that page with the source's current answer
  (Details plus People for credits) as the incoming version.
- **Bulk edit** in every plugin list (table and cards): a checkbox per row
  and card, a bar when anything is selected ("N selected" with Set status,
  Add to list, Tags, Add to series, Remove), and "Select all N matching" that
  selects everything the current filter matches, across pages.
- **Duplicate finder**: a "Possible duplicates" page per plugin listing pairs
  of items of the same kind that share an external id value of the same type,
  or have the same title and year; each pair has "Compare & merge" and "Not a
  duplicate" (hides the pair for good).
- **Merge**: the owner picks which item survives; the comparison page shows
  the survivor against the other item; applying it also moves the other
  item's entry data onto the survivor and deletes the other item.

## Non-goals

- Remembering "keep mine" choices between updates (owner decision: don't
  remember; every update shows every current difference).
- Tracking which values were edited by hand.
- Automatic or scheduled updates from sources (sequel/follow-up alerts are
  issue #38).
- Picking single genres, tags, titles, relations or credits out of a list
  (lists are taken or kept as a whole).
- Merging across plugins or kinds; merging more than two items at once
  (merge repeatedly).
- Start again in bulk (it stays one entry at a time).
- Fuzzy title matching in the finder (same normalised title and year only).

## Anti-goals

- An update or merge that changes anything the owner did not tick.
- A merge that loses a run, a note, a review, a unit note, a tag, a list or
  series membership, or activity history.
- A bulk action that applies to a different set than the count shown before
  confirming.
- Media words in core code, plugin HTML/JS, inline scripts or styles; the
  htmx, colour-token and CSP rules in AGENTS.md stay.

## Constraints

- One new migration, **00020** (dismissed duplicate pairs). No other schema
  change is expected; if the builder needs one, it goes in 00020 too.
- Every write goes through the library and its validation (text hygiene,
  field bounds, the runs rules: older runs only completed or dropped, the
  display-run rule, imports never start runs).
- The contract stays data-only; no plugin code changes are needed (sources
  already provide Details and People).
- Docker may be unavailable to the builder: tests run with
  `CGO_ENABLED=0 go test`; golangci-lint can be built from the pinned
  version (v2.15.0) with `GOTOOLCHAIN=go1.27.4`.

## Approaches

- **A. One shared compare-and-apply core (chosen by the owner).** A library
  function compares a stored item with an incoming `plugin.ItemData` (plus
  credits) and returns the differences; another applies a selection of them
  in one transaction. One comparison page serves both features; merge adds
  its entry steps on top of the same apply. One code path to test, both
  features behave the same.
- **B. Two implementations** (extend fill-missing for updates, a separate
  merge): quicker to start, two diff/apply paths that drift.
- **C. Reuse the Edit details form pre-filled with source values:** cannot
  show both sides and does not cover the cover, relations or credits.

## Compare and apply (library)

- `ItemChanges(ctx, itemID, incoming plugin.ItemData, credits []plugin.Credit,
  withCredits bool) ([]Change, error)`: one `Change` per differing part, with
  a stable key (`field.<key>`, `titles`, `genres`, `tags`, `cover`,
  `relations`, `credits`), a label (the field's label from the manifest), the
  stored and the incoming value for display, and `StoredEmpty`. Compared
  after the same normalisation the library applies on save (so a value that
  would be stored identically is not a difference). Non-editable fields are
  included (a source is allowed to change them); unknown keys from a source
  are dropped as SaveItem does.
- `ApplyChanges(ctx, itemID, incoming, credits, keys []string) (applied int,
  err error)`: re-computes the differences inside one transaction and writes
  only the selected keys that still differ (a key no longer differing is
  skipped, not an error). Writes reuse the existing item write paths
  (titles, fields, terms, relations, credits, cover path or URL), with the
  same validation; a source value the library cannot store is ErrInvalid
  (web: 502 page, as today).
- The cover: taking the source's cover stores its URL and lets the existing
  cover download (sync) fetch it, as an add does; a local cover file stays
  in the covers folder (cleanup is issue #37).
- `FillMissingFields` is replaced by this path; callers move over.

## The comparison page (web)

- `GET /{plugin}/{source}/{id}/refresh` asks the source (Details and, when
  the plugin's sections show credits, People) and renders the page; `POST`
  re-asks the source and applies the ticked keys (the incoming data is never
  trusted from the form; the form only carries keys). Manual items have no
  update (404, as today).
- One row per Change: label, stored value, incoming value, a checkbox
  (pre-ticked when stored is empty). Long text shows truncated with a
  "show all" `<details>`; lists show counts and the added/removed entries.
- Applying redirects to the overview with "Updated N" (today's notice).
- The item page's "Update from source" button links to this page.

## Bulk edit

- Lists (both views) get a checkbox per row/card and a select-all for the
  page; when the page holds every match on it, "Select all N matching"
  switches the selection to "everything this filter matches". The bar shows
  the count and the actions.
- The bar posts either the ticked item ids or the current filter (the list's
  own URL parameters); a filter selection is re-run at apply time and the
  confirm step shows the count it will change ("Change 120 entries?"); a
  count that differs from what the owner saw is shown again before applying
  (anti-goal).
- Actions, each one transaction through the same library calls the single
  dialogs use: set status (as a correction of the display run: no question,
  no new run; refused rows, e.g. a past display run behind a dropped
  repeat, are skipped and counted), add to a custom list, add or remove own
  tags, add to a series (series rules apply: same plugin and kind, an entry
  in another series is skipped and counted), remove from the library
  (confirmation names the count).
- After applying: the list reloads with a notice ("Changed 118, skipped 2:
  …").
- Routes: `POST /{plugin}/bulk` with `action`, `ids` or the filter, and a
  `confirm` step. htmx rules unchanged; the bar is plain HTML with handlers in
  app.js.

## Duplicate finder

- `GET /{plugin}/duplicates`: pairs of items of the same plugin and kind
  where an external id type/value is shared, or the normalised display
  title (case and Unicode folded, punctuation and whitespace collapsed) and
  the year field (where the kind has one) are equal. Each pair shows both
  items (cover, title, source, status) and the reason ("same ISBN",
  "same title and year").
- "Not a duplicate" stores the pair (migration 00020:
  `dismissed_duplicates(item_a, item_b, dismissed_at)`, a < b, both FK items
  cascade); dismissed pairs never show again.
- A link to the page from the plugin's list (only when pairs exist).

## Merge

- "Compare & merge" asks which item survives (default: the one with a source
  over a manual one, else the older), then shows the comparison page with
  the other item as the incoming version (its titles, fields, terms, cover,
  relations and credits).
- Applying, in one transaction: the ticked item changes on the survivor;
  then the other item's entry data moves to the survivor:
  - **Runs** of both are combined in date order (started date, then
    created) and renumbered; if more than one would be planned/active/paused,
    every one but the latest becomes dropped (older runs may only be
    completed or dropped). Unit notes and events move with their runs;
    the other item's orphan events move too. The display run is refreshed.
  - **Notes**: both kept, the other's appended after a divider line.
  - **Own tags** and **custom list** memberships: union (lists keep the
    survivor's place; the other's place is used when only it was a member).
  - **Series**: the survivor keeps its own; when only the other item is a
    part, the survivor takes its place.
  - The other item's **external ids** that the survivor lacks are added.
  - When only the other item is in the library, the survivor is added with
    that entry.
- Then the other item is deleted. The result page is the survivor's
  overview with a notice.

## Backup and export

- `dismissed_duplicates` goes into the backup (pairs by item identity, as
  series and lists do) and restores by merge; no change to the export.

## What is left to the builder

- Exact markup and wording within the existing styles and tokens.
- How a list's filter is carried in the bulk form (hidden inputs or the
  query string) as long as it is re-run at apply time.
- Display of long values and of list differences on the comparison page.
- The title normalisation details and the finder's query shape, within a few
  hundred ms for a library of a few thousand entries.
- Splitting into reviewable, green commits. Suggested order: compare/apply
  core and the update page; bulk edit; duplicate finder; merge; backup; docs
  and CHANGELOG.
