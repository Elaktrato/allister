# Series with their own data, and spin-offs (design, 2026-11-15)

Follow-up to the series groups (`2026-11-15-series-groups-design.md`).
Decided with the owner: approach **A** (extend the core series groups; a
series stays a group, not a list entry).

## Intent

Today a series is only a named, ordered list of parts; everything it shows
(cover, progress, status) is borrowed from them. The owner wants a series to
carry **its own data, like a normal entry**: a cover image, a description,
**when it started and when it ended** (the series' own run), the number of
volumes and the total number of chapters, "and so on" — and **spin-offs**,
both as single extra parts and as whole series of their own.

The owner's rules, in their words:

- "It'll be an easy import for light novels" — a series can take its data
  from the matching Light novel entry (AniList).
- "for the rest … if it is marked as finished, it should be the release date
  of the first item and end with the release date of the [last] item."
- Spin-offs "could be both": flagged parts inside a series, and whole spin-off
  series linked to the main one.

## Goals

1. **Base data on every series** (all plugins): cover image (upload or URL,
   like manual entries), description (multi-line), started on, ended on
   (calendar days), a **finished** marker (the series has ended), and the
   **expected number of parts** (so progress can read "5/12" while only five
   parts are attached).
2. **Plugin-declared series fields**: a plugin declares, per kind, extra
   fields its series carry (books: Volumes, Chapters, Author). Edited and
   shown like entry fields.
3. **Linked entry** (optional): a series can be linked to one entry of a kind
   the plugin declares as a series source (books: Light novels). Its cover,
   description, start/end dates and same-named fields show wherever the
   series' own values are blank. The link is live (AniList updates of that
   entry show through); the owner's own values always win.
4. **Derived from the parts** when neither own nor linked values exist:
   - started on = the earliest release date of the main parts;
   - ended on = the latest release date of the main parts, **only when the
     series is marked finished**;
   - expected parts = the number of main parts.
   The plugin declares which field of a part is its release date.
5. **Spin-offs**:
   - a **spin-off part**: any part can be flagged as a spin-off; spin-off
     parts are listed in their own block on the series page and do not count
     towards the series' progress, derived status or derived dates;
   - a **spin-off series**: a series can name a parent series of the same
     plugin; the parent's page lists its spin-off series (with their
     progress), the spin-off's page says "Spin-off of <parent>" with a link.
     One level only (a spin-off series has no spin-offs of its own).
6. Everything survives backup/restore; the home page, the Series tab and the
   series page use the new data (own cover, expected parts).

## Non-goals

- Making a series a list entry (status, rating, dates of the owner's own
  reading) — that was approach B, rejected.
- Searching sources for series metadata directly (the light-novel route goes
  through a linked entry the owner already has).
- Copying linked data into the series (the link is read live).
- Nested spin-offs, cross-plugin spin-offs.

## Anti-goals

- Existing series must keep working unchanged: no field set means exactly
  today's behaviour (first part's cover, progress over attached parts).
- A spin-off part never changes the main progress or status.
- Owner-entered values are never overwritten by linked or derived ones.

## Constraints

- Plugins declare data only; the core resolves own → linked → derived.
- Migration **00019** (00008 stays unused); never edit 00018.
- Contract additions stay optional and JSON round-trip tested;
  `ContractVersion` stays 1.
- Text hygiene as everywhere (description `MultiLine`, capped like entry
  synopses; fields validated like entry fields).
- Covers go through the existing cover store (local files, content hash
  names), like manual entries.
- Backup `format_version` stays 1; an older Allister must still restore a new
  archive's items (new data inside the `series` member is ignored by it).
- No media words in core; theme colours only; no inline script/style;
  README ≤ 150 lines.

## Approaches

- **A. Extend the series groups (chosen).** Base fields as columns of
  `series`, plugin-declared fields in a `series_fields` table shaped like
  `item_fields`, an optional linked item, a part flag and a parent link for
  spin-offs. Reuses the entry field editing and the cover store.
- **B. Every series a real entry of a per-plugin "series" kind.** Gets
  editing, covers and source matching for free, but every plugin needs a new
  kind, series show up as list entries with their own status and rating, and
  existing series need migrating. Rejected.
- **C. Fixed base fields only.** No plugin numbers, no linked entry: volumes
  and chapters would be free text and the light-novel import is lost.
  Rejected.

## Design

### Data (migration 00019)

- `series` gains: `cover_path TEXT` (cover store file, NULL = none),
  `description TEXT`, `started_on TEXT`, `ended_on TEXT` (calendar days),
  `finished INTEGER NOT NULL DEFAULT 0`, `parts_total INTEGER` (NULL = not
  set), `linked_item_id INTEGER REFERENCES items(id) ON DELETE SET NULL`,
  `parent_id INTEGER REFERENCES series(id) ON DELETE SET NULL`.
- `series_parts` gains `spin_off INTEGER NOT NULL DEFAULT 0`.
- New `series_fields(series_id REFERENCES series(id) ON DELETE CASCADE, key,
  vtype, text, num, date, seq, PRIMARY KEY (series_id, key, seq))`, shaped
  like `item_fields`.
- Rules checked in `internal/library`: the linked item belongs to the same
  plugin and to the declared link kind; the parent series belongs to the same
  plugin, is not the series itself and has no parent of its own; a series
  that has spin-offs cannot become a spin-off.

### Contract (`internal/plugin`)

- `SeriesHintDef` gains `ReleaseField string` (`json:"release_field,omitempty"`):
  a date field of the parts' kind used for derived dates.
- `KindDef.SeriesFields []FieldDef` (`json:"series_fields,omitempty"`): extra
  fields of this kind's series. Validated like `Fields`; keys must not
  collide with the base keys (`cover`, `description`, `started_on`,
  `ended_on`, `finished`, `parts_total`).
- `KindDef.SeriesLink *SeriesLinkDef` (`json:"series_link,omitempty"`):
  `{Kind string; DescriptionField, StartField, EndField, PartsField string}`
  — the kind whose entries can be linked and which of its fields fill the
  base description, dates and expected number of parts (builder's call:
  books map `PartsField: "volumes"`, so a series linked to a Light novel
  with 12 volumes reads "5/12" at once). A series field with the same key as a field of the
  link kind is filled from it too. Validated: the kind exists in the
  manifest; the named fields exist with the right types (text, date, date,
  number).
- `SeriesHintDef.TitleField` becomes **optional** (builder's call: most
  kinds have no series-name field but do have a release date); when empty,
  the title prefill and the "same series first" ordering in "Add from your
  list" are simply skipped. Validation: a hint must name at least one field.

### Plugins

- **Books**: the Book kind declares `SeriesHint.ReleaseField:
  "published_on"`, `SeriesFields` Volumes (number), Chapters (number), Author
  (text), and `SeriesLink{Kind: "series" (Light novels), DescriptionField:
  "synopsis", StartField: "published_on", EndField: "ended_on"}`. The Light
  novels kind gains a field **`ended_on` ("Ended", date)**, filled from
  AniList's `endDate` by the AniList novel mapping (the client already
  fetches it).
- **Anime, manga, TV**: `ReleaseField: "started_on"`. **Movies, games**:
  `ReleaseField: "released_on"`. No series fields or link for now (the base
  fields cover them).

### Resolution (core, on read)

For each base field and series field, in order: the series' own value → the
linked entry's mapped value → the derived value (dates, expected parts) →
nothing. The cover: own cover → linked entry's cover → first main part's
cover. Progress: completed main parts / (expected parts if set, else main
parts). Derived status (the six rules of the series groups design) uses main
parts only, with one change (builder's call): rule 2 (completed) also
requires the expected number of parts to be reached when it is set — "5/12,
all five read" is **active** (waiting for more), not completed.
`DeriveSeriesStatus` takes the expected number (0 = not set) and the SQL
behind the home page's `ActiveSeries`, `SeriesCounts` and `ListSeries`
follow it; the existing equivalence test is extended to cover it. "Finished" is the owner's marker; when the series is linked and
the marker is not set, an ended date on the linked entry counts as finished.

### Pages

- **Series page**: a header like an item overview — cover, title,
  description, an info block (started–ended, finished, expected parts,
  plugin fields; each value marks where it comes from only by being shown,
  no badges), progress over main parts. Below: the parts, then a
  **Spin-offs** block with the spin-off parts and the spin-off series (each
  with progress, linking to its page). A spin-off series shows
  "Spin-off of <parent>" under its title.
- **Edit details** (a dialog like the entry details edit, reusing its field
  rendering): title, cover (upload or URL), description, started/ended,
  finished, expected parts, plugin series fields, **Linked entry** (pick an
  entry of the link kind of this plugin — same picker pattern as "Add from
  your list" — or clear it), **Spin-off of** (pick another series of the
  plugin, or none).
- **Parts list**: each part gets a "Mark as spin-off" / "Mark as main part"
  toggle (a form, like move/remove).
- **Numbering** (builder's call): main parts and spin-off parts keep
  **separate** dense sequences (1..n and 1..m). "Part N of <series>", the
  "N · <series>" badge, previous/next and placement by the position field
  use the main sequence; a spin-off part reads "Spin-off N of <series>" and
  its badge "Spin-off · <series>". Up/down moves stay inside a part's own
  block; flipping the flag moves the part to the end of the other block and
  renumbers both. Backup stores each block in order with the flag.
- **Series tab and home page**: use the resolved cover and expected parts;
  a spin-off series shows a small "Spin-off of <parent>" line.

### Backup and export

- The `series` member's objects gain: `cover` (archived like item covers — the backup's cover collection must
  include series covers, which today it does not),
  `description`, `started_on`, `ended_on`, `finished`, `parts_total`,
  `fields`, `linked` (item identity `plugin_id, kind, source, source_id`),
  `parent` (uid), and each part reference gains `spin_off`.
- Restore: unknown members inside a series object are ignored by older
  versions (checked: no decoder uses `DisallowUnknownFields`). A parent is resolved after all
  series are restored (two passes); a missing linked item or parent leaves
  the link empty with a note.
- Per-plugin JSON export: unchanged (entries already carry their series and
  position).

## Left to the builder

Exact markup and wording within the existing styles; how the dialog's field
rendering is shared with entry details; the pickers' search details; the
order of fields in the info block (follow the kind's `SeriesFields` order,
base fields first).
