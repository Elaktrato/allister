# IMDb and Letterboxd imports, Google Books key (design, 2026-11-15)

Three features the owner asked for after the series release. A fourth, a
manga release calendar from MangaDex, was researched and **dropped by the
owner**: MangaDex only knows chapters a scanlation group has already
scheduled (usually none), and it would need a core change for a second
schedule source. The reasons go into `docs/handoff.md` so it is not retried
blindly.

## Intent

- Bring the owner's film and TV history from IMDb and Letterboxd into the
  movies and TV plugins with as little hand-matching as possible.
- Let the Google Books key be set in Settings like every other source key.

## Goals

1. **IMDb CSV import** (ratings export and watchlist export) for the movies
   and TV plugins, resolved by IMDb id.
2. **Letterboxd export import** (the ZIP from Settings > Data) for the
   movies plugin, resolved by **title + year search on TMDB** (decided with
   the owner: approach A, a core capability of the import runner).
3. **Google Books API key in the API keys card**, optional, with
   `GOOGLE_BOOKS_API_KEY` kept as a fallback.

## Non-goals

- Scraping letterboxd.com or imdb.com (both terms forbid automated
  extraction); the Letterboxd API (not open to personal projects).
- Importing Letterboxd lists, likes, comments or tags; IMDb check-ins or
  lists other than ratings and watchlist.
- Fuzzy matching. A title search only ever accepts an exact match.
- Write-back to IMDb or Letterboxd.

## Anti-goals

- A wrong match is worse than no match: an ambiguous or missing title must
  become an unmatched manual entry the owner can match by hand, never a
  guess.
- A keyless Google Books source must keep working without warnings ("needs
  key") because the key is optional.

## Constraints

- Plugins stay data only; the title-resolution logic is core
  (`internal/importer`), declared per importer as data.
- No new Go dependencies (no `golang.org/x/text`); normalisation uses the
  standard library.
- Uploads keep the existing 20 MiB cap; a ZIP is read only from that body,
  never extracted to disk, with per-file size caps and only known file
  names read (zip-slip and zip-bomb safe).
- The owner's export files are never committed: `.gitignore` gets
  `imdb_*.csv` and `letterboxd_*.zip`. The owner provides an IMDb export
  (`imdb_ratings.csv`, optionally `imdb_watchlist.csv`) in the repo root to
  confirm the exact columns; tests use synthetic fixtures only.
- README ≤ 150 lines; TDD; the usual checks.

## Approaches (title matching)

- **A. Core runner capability (chosen).** `ImporterInfo` declares that rows
  without an id resolve by a title search through `ResolveVia`. The runner
  searches, accepts only an exact title + year match, fetches the details,
  and otherwise saves an unmatched manual entry. Reuses the runner's pacing
  (the source's `RequestsPerMinute`), pause/resume, import modes and
  history.
- **B. The importer searches TMDB inside `Parse`.** No core change, but
  hundreds of unpaced requests in one call, no resume, and one rate limit
  fails the whole import. Rejected.

## Design

### Core: resolve by title (`internal/plugin`, `internal/importer`)

- `ImporterInfo.ResolveByTitle bool` (`json:"resolve_by_title,omitempty"`).
  Validation: requires `ResolveVia` (a declared source of the importer's
  kinds) — the source must answer `Search` for the entry's kind.
- A `ParsedEntry` without a value for `ExternalIDType` but with a title and
  `Fields["year"]` (number) is resolved by title when the importer has
  `ResolveByTitle`:
  1. `Search(kind, title)` on `ResolveVia` (paced like a lookup).
  2. A candidate **matches** when one of its titles, normalised, equals the
     entry's normalised title **and** its `year` field equals the entry's
     year. Normalisation: lower case, every rune that is not a letter or a
     digit dropped, "&" read as "and". (Diacritics are not folded: a
     difference there becomes unmatched, which is safe.)
  3. Exactly one matching candidate → `Details` for it → saved as a normal
     source item (the existing-entry rules skip/newer/always apply, and the
     resolved id is checked against existing items first, so a film already
     in the list is updated, not duplicated).
  4. Zero or several → an unmatched manual entry (ManualRows), with the
     importer's `UnmatchedNote` ("… TMDB had no unique match for
     "{title}" ({year}).") and the row's own id (see Letterboxd) kept.
  5. A row without a year is never searched: unmatched manual.
- `ErrRateLimited`/`ErrUnavailable` pause and retry like lookups;
  `ErrNotConfigured` (no TMDB key) makes every title row manual-unmatched,
  like keyless id imports today.

### IMDb CSV (movies and TV plugins)

- One importer per plugin, both named "IMDb CSV", accepting `.csv`: the
  owner uploads the same file to Movies and to TV; each keeps its own
  types and counts the rest as ignored.
- Columns are read by name, case-insensitive, with aliases for the two
  export layouts (ratings: `Const`, `Your Rating`, `Date Rated`, `Title`,
  `Title Type`, `Year`, `URL`, ...; list/watchlist: `Const`, `Created`,
  `Title`, `Title Type`, `Year`, ...). The exact current names are
  confirmed against the owner's export before the parser is finalised.
- `Title Type`, normalised (lower case, spaces removed): movies take
  `movie`, `tvmovie`, `short`, `video`, `tvspecial`; TV takes `tvseries`,
  `tvminiseries`. Everything else (`tvepisode`, `videogame`, `podcast...`)
  is ignored.
- Status: a row with `Your Rating` → completed (Seen / Watched); without
  (watchlist) → planned. Rating: 1–10 as is. Finished date: `Date Rated`
  (builder's call: the closest thing IMDb has to a watch date; documented).
- External id `imdb` = `Const` (`tt` + digits, validated). Movies:
  `ResolveVia: tmdb` (its lookup handles `imdb` via `/find`). TV:
  `ResolveVia: tvmaze`, `ResolveViaFallback: [tmdb]` (both look up `imdb`;
  TVmaze first because it is keyless and feeds the calendar).
- Duplicates (same `Const`) reported once; the import modes apply.

### Letterboxd export (movies plugin)

- Importer "Letterboxd export", accepting `.zip` (the file Letterboxd
  mails). `Parse` reads the body (≤ 20 MiB) into memory, opens it with
  `archive/zip`, and reads only `watched.csv`, `diary.csv`, `ratings.csv`,
  `watchlist.csv` and `reviews.csv` at the archive root (each ≤ 10 MiB
  uncompressed; anything else ignored). A body that is not a ZIP, or a ZIP
  without `watched.csv`/`diary.csv`/`watchlist.csv`, is `ErrBadInput`.
- Rows are merged per film by `Letterboxd URI` (fallback: name + year):
  - `watched.csv` → completed (Seen);
  - `diary.csv` → completed; finished date = the latest `Watched Date`;
    a `Rewatch` row adds a note "watched N times";
  - `ratings.csv` → rating = stars × 2 (0.5–5 → 1–10);
  - `reviews.csv` → comment = the latest review (MultiLine, capped);
  - `watchlist.csv` → planned, unless the film is also watched.
- Each entry carries `Fields["year"]`, the title, and external id
  `letterboxd` = the URI's path (sanitised), so an unmatched manual entry
  keeps it and a re-import finds it again. `ResolveVia: tmdb`,
  `ResolveByTitle: true`, `ManualRows: true`, `FallbackTitleLang: en`.
- Exact column names are taken from the export (owner's sample if
  provided, else the documented names) and read case-insensitively.

### Google Books key

- The Google Books `SourceInfo` gets `KeyLabel: "API key"` and a `KeyHelp`
  saying it is optional (without it Google limits requests), plus a new
  optional flag `KeyOptional bool` (`json:"key_optional,omitempty"`): a
  source with an optional key never shows "needs key" in labels, notices or
  the default-source choice.
- The book plugin resolves the key per request (like TMDB, IGDB, Steam):
  `Options.GoogleBooksKey` becomes a resolver func; `cmd/allister` wires
  `credential(ctx, "googlebooks", "key")` with `GOOGLE_BOOKS_API_KEY` as
  the fallback. An empty key still sends requests without `key=`.

### Docs

- `docs/usage.md`: the three imports (what is read, the title-match rule,
  the unmatched fallback, Seen/Want to watch mapping, the IMDb type
  routing), the Google Books key.
- `docs/plugins.md`: `ResolveByTitle`, `KeyOptional`.
- `docs/handoff.md`: the imports; MangaDex not built and why.
- README: the import sentence names IMDb and Letterboxd (≤ 150 lines).

## What is left to the builder

- Exact column alias lists (confirmed against the samples), note wording,
  the per-file caps' exact values, and how the runner's title resolution
  shares code with the id resolution.
