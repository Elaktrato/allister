# Books plugin (design, 2026-11-10)

## Intent

A third media type as a plugin for print books and ebooks, using the anime and
manga plugins as the baseline. It adds three sources (Open Library, Google
Books and AniList for Japanese light novels), a Goodreads CSV import, an
AniList-account import for light novels, local covers and people, per-plugin
export and the generic backup. The plugin is opt-in (`DefaultEnabled: false`)
and lives under `/book`.

The series model is deliberately the flat one chosen with the owner:

- **Book** is one individual print/ebook (standalone or a volume) with
  `pages` or `percent` progress.
- **Series** is one light-novel series with `volume` and cumulative `chapter`
  progress in the same entry, for example `Sword Art Online - Vol 5 / 12,
  Ch 42 / 120`. There are no per-volume child items; true series/volume
  hierarchy (model 3) is a later core project.
- General book series such as Dune use the book kind with optional `series`
  and `series_number` fields, not a series entry.

No calendar: books have no release schedule source here.

## Approaches considered

- **A. Two kinds, three sources, flat series counters (chosen).** `book`
  (Open Library + Google Books) and `series` (AniList), with source-kind
  declarations so the UI never offers a source for a kind it cannot serve.
  Each kind gets its own progress defaults and primary track: pages for books,
  chapter for series. The series counters are the owner-approved model 1.
- **B. One kind with a `type` field (rejected).** Simpler manifest, but the
  add dialog always starts at one default combination: a light novel would be
  created with pages and the user would have to switch it to volume/chapter;
  the one primary track cannot be useful for both pages and chapters; and every
  result, book or series, would share one list with many empty fields.
- **C. True series -> volume hierarchy (deferred).** A series row with
  expandable volume rows and roll-up. Needs parent/child items, aggregate
  progress, a new list UI, backup/import changes and matching; also fuzzy
  because AniList has one entry per light-novel series while Google Books and
  Open Library have per-volume records. This is model 3, a separate core
  project.

## Goals

- Search and add books from Open Library and Google Books, and light-novel
  series from AniList.
- Track a book by pages or percent; track a light-novel series by volume and
  cumulative chapter.
- Import a Goodreads CSV into the book kind, resolving ISBNs through Open
  Library and Google Books; keep rows without an ISBN as manual entries.
- Import the novels of an AniList account into the series kind, with status,
  score, chapter progress, volume progress and optional daily/weekly repeat.
- Match manual entries to any declared source; edit, cover, genres, tags,
  people and JSON export work through the generic core.
- Enable in Settings > Plugins; no calendar events.

## Non-goals and anti-goals

- No model-3 hierarchy, no per-volume child items, no aggregate progress
  between a series and its volumes.
- No chapter release calendar; no recommendations or related section for
  books (no suitable data in these sources).
- No StoryGraph, Calibre, LibraryThing or Goodreads account import (Goodreads
  has no public API).
- No AniList account import of manga into books; only `format: NOVEL` entries.
- No new core schema or migration. No plugin HTML/JS; no hard-coded colours.
- No automatic series grouping across Google Books/Open Library; the series
  field is manual and source data fills it only where available.

## Constraints

- The plugin contract is data-only. Sources return `plugin.ItemSummary`,
  `ItemData`, `Credit`, `Airing` etc.; the core owns pages.
- The core is flat: items, entries, progress tracks, terms, credits and
  stored relation links. There is no parent/child or roll-up.
- AniList light novels are one entry per series and often have null
  `volumes`/`chapters`; the user can edit the totals.
- Google Books without an API key is quota-limited (a live check returned
  `429`); the key is optional but recommended for a large import.
- Open Library page counts and publishers are edition-specific; a work's
  edition must be chosen.
- Text and URLs are sanitised with `plugin.SingleLine`, `plugin.MultiLine`,
  `plugin.HTTPSURL`; lists are capped. The core guard must stay free of
  media-specific code; the plugin package is exempt.
- The README stays at or below 150 lines; long reference material goes to
  `docs/usage.md` and `docs/plugins.md`.

## Plugin and kinds

Plugin id `book`, name `Books`, version `1.0.0`, icon `auto_stories`,
`DefaultEnabled: false`, contract version 1.

### Book kind

Key `book`, labels Book/Books.

Fields:

| Key | Type | Notes |
|---|---|---|
| `author` | text | first author; shown, editable, filterable |
| `pages` | number | 1 to 100000; the pages total; shown, editable |
| `year` | number | 1000 to 2100; shown, editable, filterable, sortable |
| `publisher` | text | shown, editable, filterable |
| `isbn` | text | normalised ISBN-13; shown, editable |
| `series` | text | optional series name; shown, editable, filterable |
| `series_number` | number | 1 to 10000; editable, sortable; no default column |
| `synopsis` | text | editable; the Summary section |
| `published_on` | date | shown, editable |

Genres and tags enabled (labels Genres/Tags), both editable and filterable.

Statuses: Want to read, Reading, Paused, Read, Dropped.

Progress:

- `pages` (type Pages, label Page, total field `pages`)
- `percent` (type Percent, label Percent)
- Defaults `[pages]`; combinations `[[pages], [percent]]`; primary `pages`.

Sections: Info ("Details"), Text ("Summary", field `synopsis`), Titles,
People ("Authors", roles `author`, `illustrator`), Genres, Tags, Links.

Titles: `en`, `de`, `ja`, `ja-latn`; fallback `en`, `de`, `ja-latn`.

Columns: Author, Year (from md), Progress, Score, Status, Updated (from lg)
by default; optional Series (from md), Publisher, ISBN, Published and Series
number (from lg).

### Series kind

Key `series`, labels Series/Series.

Fields:

| Key | Type | Notes |
|---|---|---|
| `author` | text | first story staff; shown, editable, filterable |
| `volumes` | number | 1 to 10000; the volume total; shown, editable |
| `chapters` | number | 1 to 100000; the cumulative chapter total; shown, editable |
| `year` | number | 1000 to 2100; shown, editable, filterable, sortable |
| `synopsis` | text | editable; the Summary section |
| `published_on` | date | shown, editable |

Genres and tags enabled (labels Genres/Tags).

Statuses: Plan to read, Reading, On hold, Completed, Dropped.

Progress:

- `volume` (type Count, label Volume, total field `volumes`)
- `chapter` (type Count, label Chapter, total field `chapters`)
- Defaults `[volume, chapter]`; combinations
  `[[volume, chapter], [chapter], [volume]]`; primary `chapter`.

The primary chapter means +1 advances the chapter; volume is updated in the
entry dialog. Chapters are cumulative across the series, as approved.

Sections: the same as the book kind (Info, Summary, Titles, Authors, Genres,
Tags, Links).

Titles and columns: same title languages/fallback as the book kind;
Author, Year (from md), Progress, Score, Status, Updated (from lg) by default;
optional Volumes and Chapters (from md) and Published (from lg).

## Sources

All sources are read-only, need no account and use the core's pacing.

### Open Library (`openlibrary`)

- Name "Open Library", 30 requests/minute, `Kinds: [book]`, external id type
  `isbn`, lookup supported, link "View on Open Library".
- URL patterns: `openlibrary.org/works/{id}`. The item id is the work id
  (`OL...W`).
- A descriptive `User-Agent` is sent; no key.
- Search: `GET /search.json?q=...&limit=20&fields=...`; map key, title,
  first author, first publish year, cover id.
- Details: fetch the work (`/works/{id}.json`), choose an edition from
  `/works/{id}/editions.json?limit=50`, and fetch the author records.
  Edition choice: prefer one with pages and a cover, then one with pages,
  then the first; prefer English where several fit.
- Lookup by ISBN: `GET /isbn/{isbn}.json` (follow the redirect), then its work
  and authors. The exact edition supplies pages, publisher, date, ISBN and
  cover; the work supplies the title and subjects.
- Fields: work title as `en`, edition title under the edition's language when
  it differs and the MARC language is known; `author`, `pages` (edition),
  `year`/`published_on` (work first-publish date else edition date),
  `publisher`, `isbn`, `synopsis` (HTML stripped), and `series` from the
  edition's `series` string when present (`series_number` stays manual).
  `published_on` is set only when the date is complete.
- Genres: a small exact-match allow-list from the work subjects; everything
  else (filtered subjects) becomes tags. Noise such as `nyt:`, `award:`,
  "New York Times reviewed", "Large type books", "Protected DAISY" and
  colon/equals subjects is dropped; tags are deduplicated and capped.
- People: work authors, with Open Library author ids, photos and profile URLs.
- Cover: `https://covers.openlibrary.org/b/id/{cover}-L.jpg`.
- External URL: `https://openlibrary.org/works/{id}`. External IDs: `isbn`.
- No recommendations. Errors map 404 to `ErrNotFound`, 429 to
  `ErrRateLimited`, 5xx/network to `ErrUnavailable`, malformed JSON to
  `ErrBadResponse`; context errors pass through.

### Google Books (`googlebooks`)

- Name "Google Books", 30 requests/minute, `Kinds: [book]`, external id type
  `isbn`, lookup supported, link "View on Google Books". No URL patterns:
  Google Books URLs carry the id in a query string, which the core's URL
  matcher does not support; paste the volume id or use search instead.
- Base URL `https://www.googleapis.com/books/v1`. If
  `GOOGLE_BOOKS_API_KEY` is set, add `key=`; never log the key (redact the
  request URL in errors). Without a key the public quota can answer 429.
- Search: `/volumes?q=...&maxResults=20&printType=books`; map items.
- Details: `/volumes/{id}`.
- Lookup by ISBN: `/volumes?q=isbn:{isbn}`; use the first item whose
  `industryIdentifiers` match. One request.
- Fields: `title` plus subtitle when present; first author; `pages`
  (`pageCount`); `year`/`published_on` parsed from `publishedDate` (the date
  only when complete); `publisher`; ISBN-13 (fall back to ISBN-10 converted);
  `synopsis` (HTML stripped); `language` from `volumeInfo.language` for the
  title language.
- Genres: `categories`, split on `/`, take the last meaningful segment
  (skipping "General"), dedupe, cap. No tags.
- People: the author names as credits. The library requires a person id, so
  Google Books gets a stable synthetic one derived from the normalised name
  (`gb-` plus 16 hex digits of its SHA-256); no image or URL. The same name
  deduplicates to one person within the plugin.
- Cover: `imageLinks.thumbnail` (or `smallThumbnail`/`large` when present),
  https.
- External URL: `infoLink`/`canonicalVolumeLink`; external IDs: `isbn`.
- No recommendations. Error mapping: 404 and an empty result are
  `ErrNotFound`; 429 and quota 403 are `ErrRateLimited`; 5xx/network
  `ErrUnavailable`; malformed JSON `ErrBadResponse`.

### AniList (`anilist`)

- Name "AniList", 30 requests/minute, `Kinds: [series]`, external id types
  `anilist` and `mal`, lookup supported, link "View on AniList".
- Uses the shared `internal/plugins/anilist` client with media type `MANGA`.
  URL patterns: `anilist.co/manga/{id}`.
- Search uses the new format-filtered query with `format: NOVEL`; results are
  also checked for `Type == "NOVEL"`.
- Details verifies `Format == "NOVEL"` and otherwise returns `ErrNotFound`.
- Fields: first story staff as `author`; `volumes` and `chapters` from the
  media (only when positive); `year`/`published_on` from `startDate` (the
  date only when complete); `synopsis` (`anilist.PlainText`).
- Titles: the shared Japanese, romaji and English titles. Genres and tags map
  like the manga plugin (rank/spoiler, caps).
- People: staff with roles containing "story" -> `author`; roles containing
  "art"/"illustration" -> `illustrator`; the source role stays in `Detail`.
- Cover: the AniList large cover. External URL: the AniList site URL.
  External IDs: `anilist`, `mal`.
- Lookup: `anilist` id via Details, `mal` via `MediaByMALID(..., MANGA, ...)`;
  both verify the novel format.
- No recommendations, relations or schedule.

## Core changes

These are generic capabilities, useful beyond this plugin; no migration.

1. **`plugin.SourceInfo.Kinds []string`.** Optional source-kind declaration
   (empty means every kind). Validation checks the kinds exist and are unique.
   The search page and match dialog filter sources by the selected kind; the
   default source is the first that serves it. The plugin settings page may
   list them. JSON `kinds,omitempty`.
2. **`plugin.ImporterInfo.ResolveViaFallback []string`.** Optional ordered
   fallback sources for an importer. Validation requires each to exist, support
   lookup and declare the importer's external id type. The runner tries the
   primary source, then fallbacks, only on `ErrNotFound`; a rate-limited or
   unavailable source is retried/paused, not skipped. The import note names
   the sources. JSON `resolve_via_fallback,omitempty`.
3. **`plugin.ImporterInfo.ManualRows bool` and
   `plugin.ParsedEntry.Fields map[string]FieldValue`.** With `ManualRows`, a
   row without the declared external id is kept and saved as a manual entry
   (a title is required) instead of being skipped. `Fields` supplies the
   manual item's fields (author, pages, year, publisher, ISBN). `Submit`
   stores such rows; the runner saves them with the `imported` outcome, not
   `unmatched`, because they carry no source id. `saveUnmatched` copies all
   external IDs, the fields and the title. JSON `manual_rows,omitempty` and
   `fields`.
4. **Import identity and field fallback.** The runner deduplicates a row by
   every external id it carries, not only the importer's declared id type
   (the Goodreads `Book Id` joins the ISBN so a re-import does not create a
   second no-ISBN manual entry). A source match merges the row's external ids
   into the new item and fills any field the source left empty (`pages`,
   `author`, `year`, `publisher`, `isbn`); source values always win.
5. **Import updates respect the entry's progress combination.**
   `ApplyImport` writes every progress key present in the row that is active
   on the existing entry, not only the primary track, and skips inactive keys
   instead of failing validation. This lets an AniList row update both volume
   and chapter, and keeps a Goodreads `pages` row from breaking an entry the
   user tracks by percent. The runner notes skipped keys.
6. **AniList account fields.** Add `format` and `progressVolumes` to the
   `MediaList` query and `AccountEntry` (`Format string`,
   `ProgressVolumes int`). The manga importer ignores them; the books
   importer uses them.
7. **AniList format search.** Add `Client.SearchFormat(ctx, typ, format, q)`
   using a `format:`-filtered search query; keep the existing `Search` query
   unchanged so anime/manga tests and behaviour do not move.
8. **Config key.** `config.Config.GoogleBooksKey` from
   `GOOGLE_BOOKS_API_KEY`; pass it to `book.New`. Add the variable to the
   compose environment. It is optional; absent means the keyless quota.

## Imports

Both importers use the generic import runner, rules (skip / newer / always)
and history; the AniList account importer gets the optional off/daily/weekly
repeat.

### Goodreads CSV (`goodreads-csv`)

- File importer, `.csv`, external id type `isbn` (label ISBN), `ResolveVia`
  `openlibrary`, fallback `googlebooks`, `ManualRows: true`, fallback title
  language `en`.
- Parse the standard My Books export by header name, tolerating a BOM and
  quoted fields. Only Title is required; ISBN is optional, so a book without
  one becomes a manual entry instead of being skipped. A file without the
  expected header is `ErrBadInput`.
- ISBN: prefer ISBN13; strip Excel `="..."`, spaces and hyphens; validate;
  convert ISBN-10 to ISBN-13; invalid becomes absent with a note. The
  Goodreads `Book Id` is stored as an additional `goodreads` external id, so
  a second import of the same file recognises no-ISBN rows too.
- Status: `read` -> completed, `currently-reading` -> active, `to-read` ->
  planned; unknown exclusive shelves are skipped with a reason.
- Rating: `My Rating` 0 to 5; 0 is unrated, otherwise multiply by 2 for the
  core's 0 to 10 scale.
- Pages: `Number of Pages` fills the `pages` field; a completed book gets
  pages progress equal to that number (clamped by the core if the source
  edition differs). Currently-reading and to-read start at 0.
- Year: `Original Publication Year`, else `Year Published`; publisher and
  author fill their fields. `Date Read` (YYYY/MM/DD) becomes `finished_on`;
  `My Review` becomes the comment.
- Fields are set on every row so a no-ISBN row keeps its metadata. A source
  match fills only the fields the source left empty (pages, author, year,
  publisher, ISBN); source values win, and the row's other external ids are
  added to the item.
- A no-ISBN row is saved as a normal imported manual entry (not an
  "unmatched" one, because it has no source id); the import report says
  "saved as a manual entry (no ISBN)".
- No cover in the CSV; the source lookup supplies it. No timestamps, so
  "newer" never updates a Goodreads entry (the UI already documents this for
  formats without modification times).
- On an existing entry, only active progress keys are updated; a `pages` row
  against a percent entry leaves the progress alone instead of failing.

### AniList account (`anilist-account`)

- Account importer, label "AniList username", external id type `anilist`
  (label AniList), `ResolveVia` `anilist`, fallback title language `ja-latn`.
- Fetch `MediaListCollection` type MANGA, then keep only
  `media.format == "NOVEL"`; other manga entries count as ignored.
- Rows use kind `series`, the AniList id and title, status class, score,
  `progress` as chapter, `progressVolumes` as volume, the cover and
  `updatedAt`. `Parse` refuses a file, like the manga account importer. On an
  existing entry, both tracks update when they are active; an inactive track
  is skipped with a note rather than failing the row.

## Export and backup

The plugin declares the shared JSON exporter with format
`allister.book.export`. Entry fields, progress keys, titles, external IDs and
both kinds export as data. The universal backup needs no plugin code.

## UI

- Sidebar entry "Books" with `auto_stories`; `/book` opens the first kind
  (Books) with the generic kind switch for Series.
- Search has the source selector (filtered by kind) and kind selector;
  AniList is only offered for Series, Open Library/Google Books only for Books.
- Match dialogs filter sources by the item's kind.
- Plugin settings: title language (the four declared languages), home view and
  status, columns per kind, and an Import section with the Goodreads CSV and
  AniList account importers. The AniList importer shows the repeat schedule.
- No calendar events and no schedule capability.

## Text, caps and error handling

- All mapping sanitises text and URLs; lists are capped like the anime/manga
  plugins (titles 300 runes, text 20000, terms 60, names 300, ids 100, genres
  50, tags 100, credits 500).
- A source's answer with unusable data is `ErrBadResponse` (502 page, failed
  import row); rate limits/unavailability are retryable; not found is 404 or
  an unmatched import row.
- User-facing source names come from the manifest; error messages never
  include the Google key.

## Tests

- Contract: `SourceInfo.Kinds`, `ResolveViaFallback`, `ManualRows`, and
  `ParsedEntry.Fields` validation and JSON round trip.
- Web: source filtering by kind in search and match; default source for a
  kind; existing single-kind plugins unchanged.
- Importer runner: fallback only on not-found; rate limit/unavailability do
  not fall back; no-ISBN rows become imported manual entries with fields;
  title required; dedupe by every external id; matched rows merge missing
  fields; existing entries update only active progress keys.
- AniList: format search query; account `format`/`progressVolumes`.
- Books plugin: manifest shape; source mapping for Open Library, Google Books
  (including synthetic author ids) and AniList fixtures; ISBN lookup and ISBN
  conversion; title languages; genres/tags caps; Goodreads CSV
  statuses/rating/pages/dates/no-ISBN/re-import; AniList account filters
  novels and maps both progress tracks; error mapping; export round trip.
- Guard: core stays free of media-specific code; plugin registration lines
  are the only core references.
- Docs: README line cap and link checks.

## Reviewer calls

The builder check raised five gaps; settled as follows:

1. **Google Books author ids.** The library requires a person id, so Google
   Books credits get a stable id derived from the normalised author name; no
   image or URL. The same name deduplicates to one person.
2. **Re-import of no-ISBN Goodreads rows.** The Goodreads `Book Id` is kept as
   an additional external id, and the runner deduplicates on every id a row
   carries; a second import updates/skips instead of duplicating.
3. **Existing progress combinations.** `ApplyImport` updates every row track
   that is active on the entry and skips inactive ones, rather than failing or
   switching the user's combination.
4. **Open Library editions without pages.** A matched row's fields fill only
   keys the source left empty, so the Goodreads page count still gives a
   completed book a bounded total.
5. **No-ISBN rows.** They are normal imported manual entries with the
   `imported` outcome, not "unmatched", because they carry no source id.

## Rollout

1. Core changes with tests (contract, web, importer, AniList, config).
2. Book plugin package and fixtures; register it in `cmd/allister` with the
   config key; full suite, vet, fmt.
3. Docs (README, usage, plugins, handoff).
4. Rebuild the preview on the assistant's port, enable Books, verify search
   on each source, add a book and a series, both imports, edit, export and a
   backup restore; confirm the calendar has no book events.
5. Report the result; do not push (the owner pushes).

## Follow-ups (out of scope)

- Model 3: series -> volume child items with aggregate progress.
- Per-volume entries under a series and source-driven series grouping.
- StoryGraph/Calibre/LibraryThing imports.
- Google Books key in the UI instead of an environment variable.
- Book recommendations or "more by this author".
- A Google Books URL pattern with query-string ids.
