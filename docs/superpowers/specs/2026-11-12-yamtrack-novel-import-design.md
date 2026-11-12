# Yamtrack novel import for the books Series kind (design, 2026-11-12)

## Intent

The Yamtrack export in the repository root has 539 `manga` rows. Eleven of
them are AniList **NOVELs**; the rest are manga (482), one-shots (43) or no
longer on AniList (2). The owner wants those eleven imported into the books
plugin's **Series** kind (the light-novel series feature), not into the manga
plugin and not as manual Series rows.

The CSV does not carry the novel format. The importer therefore batch-resolves
the MAL ids through AniList, keeps only `NOVEL`, and submits those rows through
the existing books AniList source. Result: 11 imported; every other row of the
full export is ignored (2,827 for this export, 2,838 rows minus 11; 528 of them
are the non-novel manga rows, the rest are other media rows). No manual noise.

## Goals

- A books-plugin importer, `yamtrack-novel`, named "Yamtrack Novel CSV".
- It reads `media_type=manga` + `source=mal` rows with numeric ids from a
  Yamtrack general CSV.
- One batch AniList format lookup over the distinct MAL ids (50 per request;
  11 requests for the 538 ids) keeps only `NOVEL`.
- Each kept row becomes a `KindSeries` entry with `ExternalIDs{"mal": id}`,
  chapter progress, status, rating, dates and notes; `ResolveVia` is the books
  AniList Series source, which already rejects non-novels and adds volumes,
  chapters, author, cover and AniList ids.
- Non-novels, non-MAL rows and ids AniList does not know are counted as
  `Ignored`, not turned into manual Series rows.
- The 11 novels resolve at the importer's 30 requests/minute pace (about 22
  seconds) and the import is resumable like any other.
- Tests, docs and a preview upload of the real CSV.

## Non-goals

- The 92 `book` rows: they use Hardcover (57), Open Library (31) and manual (4)
  ids; the books sources only resolve ISBNs today, and there is no Hardcover
  source. That is a separate feature.
- Volume progress: the Yamtrack CSV has one `progress` column; it is chapters.
  AniList supplies the volume/chapter totals, not a volume-read value.
- A single global import for all media types. The importer contract stays
  per plugin.
- A scheduled or automatic import; the user uploads the CSV.
- Adding a Hardcover source or a title-search fallback.

## Approach

Chosen: **batch format pre-filter inside the novel importer**. One batch
AniList query set (11 light requests for the current export) identifies the 11
novels; only those enter the import queue. The alternatives were:
- submit all 538 rows and let the Series source reject non-novels (527 skipped
  rows, about 18 minutes, noisy report) — rejected;
- use the AniList account importer (already exists, but it reads the current
  AniList account, not the owner's Yamtrack history) — not what was asked;
- infer novels from titles — unreliable; rejected.

## Shared client: batch formats

`internal/plugins/anilist` gains:

```go
// MediaFormatsByMALIDs returns the AniList format of each known MAL id,
// keyed by MAL id. Unknown ids are absent.
func (c *Client) MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
```

- Empty input returns an empty map without a request.
- Distinct ids are split into chunks of 50; one GraphQL request per chunk:
  ```graphql
  query ($ids: [Int], $type: MediaType) {
    Page(page: 1, perPage: 50) {
      media(idMal_in: $ids, type: $type) {
        idMal
        format
      }
    }
  }
  ```
- `idMal` null entries are skipped; the map has the found formats only.
- HTTP/status/GraphQL errors use the client's existing mapping
  (`ErrRateLimited`, `ErrUnavailable`, `ErrBadResponse`, context).
- The method is sequential; a caller with many chunks may take a few seconds.
  The current 538 ids are 11 requests at upload time.

## Importer

New `internal/plugins/book/yamtrack.go`:

```go
type yamtrackNovelImporter struct{ c novelClient }
func (yamtrackNovelImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error)
```

- `yamtrack.ParseRaw` (shared parser); a bad CSV is `plugin.ErrBadInput`.
- Filter rows: `media_type` equals `manga` (case-insensitive), `source`
  equals `mal`, `media_id` is a positive integer. Every other row increments
  `Ignored` (all other media types, the one `mangaupdates` row, and later the
  non-novels).
- Deduplicate MAL ids and rows first (first row wins; a duplicate is skipped
  with a reason).
- Batch-resolve the distinct MAL ids with `MediaFormatsByMALIDs(ctx,
  anilist.TypeManga, ids)`. A client error is mapped with the book plugin's
  `mapAniListErr`.
- Keep rows whose format is exactly `NOVEL`. Every other manga/MAL row
  increments `Ignored`. An id absent from the batch answer also increments
  `Ignored`.
- Unknown/blank status: skipped with a reason; no title: use
  `MyAnimeList #<id>` (the title is normally present).
- Map each kept row to:
  ```go
  plugin.ParsedEntry{
      Kind:        KindSeries,
      Title:       plugin.SingleLine(row.Title, maxTitleRunes),
      ExternalIDs: map[string]string{"mal": strconv.Itoa(id)},
      Status:      row.Status,
      Rating:      row.Rating,
      Progress:    map[string]float64{"chapter": float64(max(row.Progress, 0))},
      StartedOn:   row.StartedOn,
      FinishedOn:  row.FinishedOn,
      Comment:     plugin.MultiLine(row.Comment, maxCommentRunes),
      CoverURL:    plugin.HTTPSURL(row.ImageURL),
  }
  ```
  and copy the parser's notes (sanitised, short). A comment at the parser's
  2000-rune cap gets the same truncation note as the anime importer.
- No manual fallback for non-novels: `ImporterInfo.ManualRows` is false. Kept
  novels that the Series source cannot resolve are handled by the runner's
  normal unmatched path (manual Series with the `UnmatchedNote`), because they
  are known novels; a missing `NOVEL` format is ignored before submission.

Manifest entry:

```go
{
    ID:                ImporterYamtrack, // "yamtrack-novel"
    Name:              "Yamtrack Novel CSV",
    FileHint:          "Upload the Yamtrack general CSV. Only manga rows that AniList reports as novels are imported into Series.",
    Accept:            []string{".csv"},
    ExternalIDType:    "mal",
    ExternalIDLabel:   "MyAnimeList",
    ResolveVia:        SourceAniList,
    ManualRows:        false,
    FallbackTitleLang: "ja-latn",
    UnmatchedNote:     "Imported from Yamtrack; AniList had no match for MyAnimeList #{id}.",
}
```

`plugin.New` wires `ImporterYamtrack: yamtrackNovelImporter{c: ac}`; the
`novelClient` interface gains `MediaFormatsByMALIDs`. The `anilistSource`
remains unchanged; its `Lookup("mal")` already rejects non-novels.

## Tests

- Client: exact batch query and chunking (51 ids -> two requests), map,
  missing ids, empty input no request, 429/5xx/GraphQL error mapping.
- Importer: a synthetic CSV with two novels, manga/one-shot rows, a
  `mangaupdates` row, a missing id, a book row, an unknown status and a
  duplicate; assert entries/ignored/skipped, chapter progress, status, rating,
  dates, comment and MAL external id; bad CSV -> `ErrBadInput`; batch client
  `ErrRateLimited`/`ErrUnavailable` mapped.
- Manifest: the importer info exact; `plugin.New` provides every declared
  importer.
- The real export check (local-only, never committed): 539 manga rows -> 11
  entries, 2,827 ignored across the full export (528 of them the non-novel
  manga rows); entry titles/statuses/progress as listed above.
- Full suite/vet/fmt; preview upload on 18083: 11 imported, 2,827 ignored, the
  Series list shows chapters/volumes/author/cover; no manual non-novel rows.

## Constraints and decisions

- No new Go dependencies; no new source; no migration.
- The importer's `Parse` makes the batch AniList requests; the web upload may
  take a few seconds. The 11 detail lookups happen later at the runner's
  30/minute pace.
- No manual rows for non-novels; the report's `Ignored` count is the signal.
- No volume progress; chapters only.
- The root CSV is the owner's data; it stays untracked and out of git.
- README stays <= 150 lines; docs go in `docs/usage.md` and `docs/handoff.md`.
