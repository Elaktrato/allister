# Series groups (design, 2026-11-15)

Issue #1 ("model 3"), reshaped with the owner: not a books-only series ->
volume hierarchy, but a **core series group** that works the same in every
list.

## Intent

The owner tracks things that come in parts (book series, film trilogies,
game series, seasons of a franchise) and wants to see them as one thing:
one row in the list with progress over its parts, opening into the parts,
while every part stays a full entry with its own status, rating, dates and
cover. No source knows such a group reliably (AniList has one record per
light-novel series, Open Library and Google Books one per book, no series
id), so groups are built **by hand**; the owner is fine with nothing being
filled in automatically.

The owner's flow, in their words: "I can turn one entry into a series making
that entry the first in said series but I can adjust that whenever I feel
like it. When adding a new entry to the series I can choose from ones I've
already got in my list or I can search for them like any normal entry."
And: "we can add this feature to all lists".

## Goals

- In any plugin's list: "Make this a series" on an entry creates a series
  with that entry as part 1.
- Add a part from the entries already in the list, or by searching the
  plugin's sources like a normal add (the found item is added to the list
  and attached in one step).
- Reorder parts and take a part out again at any time; rename the series.
- A **Series tab** in each list shows a series as one row (cover of its
  first part, its title, progress over the parts such as "3/7", an expand
  control); expanded, it shows its parts in order. The other tabs keep
  showing every entry, parts marked with their series.
- A series page with its parts, actions to add, reorder, remove, rename and
  delete.
- A part's overview says which series it belongs to and at what position,
  with links to the series and the previous/next part.
- Backup/restore and the per-plugin JSON export keep the grouping.

## Non-goals

- Filling a series automatically from a source (volume lists, collections,
  franchises), and any guessing from a volume count.
- Groups across plugins (a book series with its anime) or nested groups.
- A rating, comment, dates or source metadata of the series itself.
- Changing the books plugin's light-novel kind (model 1 counters) beyond
  its label; it is relabelled "Light novels" and otherwise stays as it is.
- Statistics for series (they count the parts, which are normal entries).

## Anti-goals

- A part must never lose its entry, progress or history because of a
  series action: ungrouping, reordering, renaming or deleting a series
  (without "also remove the parts") only changes the grouping.
- No plugin HTML/JS, no media words in core code, no inline scripts; the
  existing htmx rules (`hx-history="false"` on `#list-rows`, `Vary`) stay.
- The list must not get slower than a few ms per page for a normal library.

## Constraints

- Plugins are data only, so the feature is core (`internal/library`,
  `internal/web`, `internal/backup`); it needs no plugin declaration and is
  on for every kind of every plugin.
- Migrations are strictly increasing; this is **00018**. Foreign keys are
  on.
- Contract types stay JSON-serialisable; the contract changes are the
  reserved source id `series`, the optional `KindDef.SeriesHint` and two
  optional `ExportEntry` fields.
- Text hygiene as everywhere: the series title is a SingleLine (max 300
  runes), refused when empty or invalid.
- README stays at or under 150 lines.

## Approaches

1. **Core series group (chosen by the owner).** A new core concept: a named
   group of entries of one plugin and kind, with ordered members. No
   source data; progress and status are derived from the parts.
2. **A container kind per plugin** ("Collection", "Franchise", ...): full
   items with fields, rating and possibly source data (TMDB collections,
   IGDB franchises). Richer, but every plugin needs a new kind with data,
   and it raises questions like rating a franchise separately. Can still be
   added later where a source has real collections.

Storage, the builder's choice confirmed in the conversation: a **separate
membership table** rather than a parent column on `items`, so matching,
sync, imports and edits (which all rewrite items) cannot break a group,
deleting either side cleans up through foreign keys, and backup gets a
self-contained section. A parent link on `items`, and building on the
`relations` snapshots (replaced on every sync, no foreign key), were
rejected.

## Data model (migration 00018)

```sql
CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,      -- stable id for URLs and backups (ids.NewManualID style)
    plugin_id  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    title      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE series_parts (
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL UNIQUE REFERENCES items(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,           -- 1..n, dense, rewritten on reorder
    PRIMARY KEY (series_id, item_id)
);
CREATE INDEX series_parts_order ON series_parts(series_id, position);
```

- **One kind per series**: a part must be an item of the series' plugin and
  kind (checked in `internal/library`). This keeps the list query (which is
  per kind) simple; in the books plugin a series of books lives on the Book
  tab.
- **One series per item** (`UNIQUE(item_id)`).
- **Only entries**: an item can be added only while it has an entry (is in
  the list). Removing a part from the list deletes the item (existing
  `DeleteItem`), and the cascade removes its membership.
- **An empty series is deleted** (builder's call): when the last part goes,
  the series row goes in the same transaction.
- Positions are dense 1..n; every change (add, remove, move) renumbers in
  one transaction.

## Derived values (computed on read, never stored)

- **Progress**: completed parts / all parts, e.g. "3/7"; the bar uses the
  same ratio.
- **Status** (for the row's status corner and label), from the parts'
  status classes, first rule that applies (builder's call, confirmed by the
  review):
  1. all dropped -> dropped;
  2. every part that is not dropped is completed (at least one completed)
     -> completed (2 read + 1 dropped is a finished series);
  3. all planned -> planned;
  4. any active -> active;
  5. any paused -> paused;
  6. otherwise (completed and planned parts, nothing started) -> active.
  Dropped parts count as parts but not as completed.
- **Cover**: the first part's cover; **updated**: the newest part update.

## The list

Decided with the owner: **a separate Series tab**, the other tabs unchanged.

- **The entry tabs stay as today**: every entry is its own row, parts of a
  series included. A part gets a small badge "2 · The Expanse" (position and
  series title) linking to the series page. No grouping logic in the
  existing list query.
- **A "Series" tab** appears next to the kind tabs once the plugin has at
  least one series. In a plugin with one kind (anime, movies, games, ...)
  the bar reads "All | Series"; in books it reads "Books | Light novels |
  Series" (see the relabel below). URL: the list's existing view parameters
  plus `tab=series` (builder's choice of the exact name, bookmarkable like
  the other parameters).
- The Series tab lists the plugin's series (every kind of the plugin), one
  row each: cover of part 1, series title, derived progress "3/7", derived
  status, last update, and an **expand control** that loads the parts (in
  position order, the normal row templates) with htmx below the row.
  Collapsed is the default and is not remembered; without htmx the control
  is a link to the series page. In the card view a series card links to the
  series page.
- **Status tabs on the Series tab filter by the series' derived status**,
  so the counts add up again ("All" is their sum, as today). The text
  search matches the series title and the parts' titles. Other filters are
  not offered on the Series tab (builder's call: they describe entries, not
  series). Sort: recently updated (default) or title.
- **+1** is not offered on a series row (it belongs to a part).

Sketch (Series tab, table view):

```text
Books | Light novels | Series
▸ The Expanse              3/9   Reading    · updated 2 days ago
▾ The Lord of the Rings    1/3   Reading    · updated 3 days ago
    1  The Fellowship of the Ring   Read      ★ 9
    2  The Two Towers               Reading   140/352
    3  The Return of the King       Want to read
```

Sketch (Books tab, a part's row):

```text
  The Two Towers   [2 · The Lord of the Rings]   Reading  140/352
```

## Series hints (plugin data)

Decided with the owner (option 2 of three: keep separate, hints, automatic
grouping). A kind may declare which of its fields carry a source's series
name and position, so the core can **suggest**, never group by itself:

```go
// KindDef, optional
SeriesHint *SeriesHintDef `json:"series_hint,omitempty"`

type SeriesHintDef struct {
    TitleField    string `json:"title_field"`              // a text field, e.g. "series"
    PositionField string `json:"position_field,omitempty"` // a number field, e.g. "series_number"
}
```

- Validation: both keys must be fields of that kind, of type text and
  number; JSON round-trip test like the other optional members.
- "Make this a series" prefills the title from the entry's title field when
  it has one (else the display title).
- "Add from your list" lists entries whose hint title equals the series
  title (case-insensitive, trimmed) first.
- A part added while the series has positions free is placed by its
  position field when it has one (between the parts with lower and higher
  numbers); otherwise at the end. Reordering by hand always wins; the field
  is never written by the core.
- The books plugin declares it for the Book kind (`series`,
  `series_number`). Other plugins can add it later.

## Books: relabel the light-novel kind

Decided with the owner: the books plugin's `series` kind (light novels,
model 1 counters) is relabelled **"Light novel" / "Light novels"** (its
status labels and fields stay). Only the labels change: the kind key stays
`series`, so stored entries, URLs (`?kind=series`), settings keys, backups
and exports are untouched. README, `docs/usage.md` and `docs/handoff.md`
use the new label.

## The series page

Route `GET /{plugin}/series/{uid}` (`series` becomes a reserved source id
next to `manual`, `export`, `search`). It shows the title (rename inline),
the derived progress and status, and the parts in order with cover, title,
status, progress and rating, each linking to its overview. Actions:

- **Add a part**: (a) "From your list": a search field over this plugin's
  entries of the same kind that are in no series; (b) "Search
  <sources>": the plugin's normal search for that kind, where the add
  dialog attaches the new item to the series (`?series=<uid>` carried
  through search and add). A found item already in the list is attached
  without a second add; one already in another series is refused with a
  message.
- **Reorder**: up/down buttons per part (forms, no drag and drop; works
  without JavaScript).
- **Remove from series**: keeps the entry.
- **Delete series**: a confirmation with two choices: "Ungroup (keep the
  entries)" (default) and "Delete the series and remove its N entries from
  your list".

## The item overview

- An entry in no series gets **"Make this a series"**: a small form with
  the title, prefilled with the entry's display title; submitting creates
  the series with the entry as part 1 and opens the series page.
- An entry in a series shows "Part 2 of <Series>" (linked) with previous /
  next links, and "Remove from series".
- Matching a manual part to a source keeps the item id (`ConvertToSource`
  updates in place), so the membership survives.

## Backup, restore, export

- `backup.json` gets a top-level `series` array **after** `items`:
  `{"uid","plugin_id","kind","title","created_at","updated_at","parts":
  [{"plugin_id","kind","source","source_id"}, ...]}` in position order.
  Items are identified as everywhere in the backup (no numeric ids).
  Unknown top-level members are already skipped by older versions, so the
  format version stays 1 (like `appearance`).
- Restore (merge) handles `series` after all items: a series whose uid
  exists is skipped; otherwise it is created with the parts that exist now
  (restored or already present), in order; a part that is missing or
  already in another series is left out and counted in the restore report;
  a series left with no parts is not created.
- Per-plugin JSON export: each entry gets optional `series` (title) and
  `series_position`; `plugin.ExportEntry` gains the same two optional
  fields (omitted when empty; JSON round-trip test).

## What is left to the builder

- Exact template markup and wording (following the existing list row,
  card and overview styles and theme tokens), the expand control's icon.
- How `Counts`/`List` share the grouped query; indexes beyond the one
  above if a test library shows a need.
- The restore report's wording for left-out parts.
- Splitting the work into reviewable steps (library first, then web, then
  backup/export), each green.
