# Yamtrack manga import (design, 2026-11-12)

## Intent

The manga plugin is the last one without a Yamtrack CSV importer. The owner's
general export has 539 `manga` rows. Eleven are AniList NOVELs and now live in
the books **Series** kind through `yamtrack-novel`; the rest belong in the
manga plugin:

- 482 manga, 43 one-shots -> resolved through AniList by MAL id;
- 2 MAL ids AniList no longer knows -> kept as manual unmatched manga;
- 1 `mangaupdates` row without a MAL id -> kept as a manual manga entry.

The full 539-row partition is therefore **528 manga entries + 11 Series
entries**, with no duplicates.

## Goals

- A manga-plugin importer, `yamtrack-manga`, named "Yamtrack Manga CSV".
- Reads `media_type=manga` rows from the Yamtrack general CSV.
- Batch-resolves the MAL ids through the shared `MediaFormatsByMALIDs` call
  (11 requests for the 538 ids) and **skips `NOVEL`**; the 11 novels stay only
  in books Series.
- Keeps `MANGA`, `ONE_SHOT` and unknown/missing MAL ids; the missing ids are
  submitted with their MAL external id and become manual unmatched manga via
  the runner's normal path.
- A non-MAL manga row with a title (the one `mangaupdates` row) becomes a
  manual manga entry; `ManualRows: true`.
- Maps chapter progress, status, score, dates, comment, cover and notes; the
  AniList source adds chapters/volumes totals, author, cover and an AniList id.
- No new source, migration or dependency; no volume progress (the CSV has one
  `progress` column).

## Non-goals

- Importing the 11 novels into the manga plugin (they are in Series).
- The 92 `book` rows (Hardcover/Open Library ids; no Hardcover source).
- Volume progress, a global all-media import, scheduled imports, or a new
  source.
- Replacing the existing AniList-account and MyAnimeList-XML manga imports.

## Approach

Mirror `yamtrack-novel` with the filter inverted:

1. `yamtrack.ParseRaw`; a bad CSV is `plugin.ErrBadInput`.
2. Iterate rows: non-manga -> `Ignored`.
3. Manga rows:
   - `source=mal` with a positive numeric id -> candidate (dedupe by id).
   - any other source with a non-empty title -> manual entry.
   - no title/no id -> skipped with a reason.
4. Batch `MediaFormatsByMALIDs(ctx, anilist.TypeManga, ids)`; map AniList
   errors with the manga `mapErr`.
5. For each candidate:
   - `format == "NOVEL"` -> `Ignored` (books Series owns it).
   - otherwise (including an id absent from the format map) -> a `KindManga`
     entry with `ExternalIDs{"mal": id}`, chapter progress, status, rating
     (0.0 unrated), dates, comment, cover, notes.
6. Manual non-MAL rows become `KindManga` entries with no external id; the
   runner's `ManualRows:true` saves them.
7. The runner resolves each MAL entry through the manga AniList source at 30
   requests/minute (~17.5 minutes for 525 lookups), resumable like every
   other import.

Manifest entry:

```go
{
    ID:                ImporterYamtrack, // "yamtrack-manga"
    Name:              "Yamtrack Manga CSV",
    FileHint:          "Upload the Yamtrack general CSV. Manga rows are matched on AniList by MyAnimeList id; novels are skipped (they belong in Books > Series).",
    Accept:            []string{".csv"},
    ExternalIDType:    "mal",
    ExternalIDLabel:   "MyAnimeList",
    ResolveVia:        SourceID,
    ManualRows:        true,
    FallbackTitleLang: "ja-latn",
    UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
}
```

The batch client is the shared `anilist.Client`; the importer uses a narrow
interface:

```go
type malFormatClient interface {
    MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
}
```

The manga plugin passes its source client (the concrete `*anilist.Client` in
production); a provider without the method makes the importer answer
`plugin.ErrUnavailable` rather than silently importing everything.

## Expected real-data result

- 2,838 rows total; 2,310 ignored (2,299 non-manga + 11 novels).
- 528 processed: 525 resolved manga/one-shots imported, 1 `mangaupdates`
  manual imported, 2 missing MAL ids unmatched/manual. No skipped rows.
- The manga list ends with 528 entries; books Series keeps the 11 novels.

## Tests

- Importer with a fake format client: novel skipped; manga and one-shot
  imported; missing MAL id submitted; `mangaupdates` manual; `book` row
  ignored; unknown status/duplicate skipped; 0.0 rating nil; chapter progress,
  status, dates, comment, cover and MAL id mapped.
- Bad CSV -> `ErrBadInput`; batch `ErrRateLimited`/`ErrUnavailable` mapped;
  empty candidate set -> no batch call.
- Manifest exact; `plugin.New` provides the importer; the fake format client
  satisfies `malFormatClient`.
- Real preview upload: 528 processed, 526 imported, 2 unmatched, 2,310
  ignored; the 11 novels remain only in Series; the manga list shows the
  expected titles/progress.
- Full suite/vet/fmt; README <= 150.

## Constraints and decisions

- No new Go dependencies, source or migration.
- The root CSV is owner data; never commit it.
- `ManualRows:true` solely for the non-MAL manga row; the 2 missing MAL ids go
  through the runner's unmatched path.
- The 11 novels are skipped, not duplicated in manga.
- A mangaupdates row cannot be resolved automatically; manual is the honest
  outcome.
- After merge, release **v0.2.0**; existing volumes upgrade in place (no new
  migration).
