# Yamtrack Novel Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `yamtrack-novel` importer to the books plugin that imports only the AniList NOVEL rows from the Yamtrack `manga` rows into the Series kind, using one batch AniList format lookup.

**Architecture:** A new shared AniList client method resolves MAL ids to formats in chunks. A new books importer parses the Yamtrack CSV, filters `media_type=manga` + `source=mal`, keeps only `NOVEL`, and submits `KindSeries` entries through the existing books AniList source. No new source, no migration, no new dependencies.

**Tech Stack:** Go, existing plugin/importer contract, AniList GraphQL, `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-12-yamtrack-novel-import-design.md`

## Global Constraints

- No new Go dependencies; no new source; no migration.
- The importer's `Parse` makes the batch AniList requests at upload; the 11 detail lookups happen at the runner's 30/minute pace.
- Non-novels, non-MAL rows, unknown ids and non-numeric ids are counted as `Ignored`; they must not become manual Series rows.
- `ManualRows: false`; the importer submits only rows with a numeric MAL id.
- The root CSV is the owner's data: never commit it; the preview upload is the real-data check.
- No volume progress: the CSV has one `progress` column, mapped to `chapter`.
- README stays <= 150 lines.
- TDD: write the failing tests, show the real RED, implement, show GREEN.
- Full `scripts/go.sh test -count=1 ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...` before each commit; output must be pristine.
- Commit style: `feat: ...`, `docs: ...`.

## Review Focus

These are the failure modes most likely to bite the person using this importer; each has a test in the owning task.

1. **Batch lookup boundaries.** Empty input must make no request; 51 ids must make two requests (50+1); a null `idMal` must be skipped; 429/5xx/GraphQL errors must map to the existing sentinels.
2. **Only novels.** A `manga` row with format `MANGA`/`ONE_SHOT`, a `mangaupdates` row, a missing id and a `book` row must be `Ignored`, never a manual Series entry.
3. **Row mapping.** A novel maps its chapter progress, status, 0-vs-unrated score, dates, comment and `mal` external id; the import runner resolves the Series details from AniList.
4. **Wiring.** The manifest declares `yamtrack-novel` exactly; `plugin.New` provides it; the `novelClient` interface and every test fake include `MediaFormatsByMALIDs`.
5. **Errors and duplicates.** A bad CSV is `plugin.ErrBadInput`; a batch `ErrRateLimited`/`ErrUnavailable` is returned mapped; a duplicate MAL id is skipped with a reason.

---

## File Structure

- `internal/plugins/anilist/client.go` — `MediaFormatsByMALIDs` and its GraphQL query.
- `internal/plugins/anilist/client_test.go` — exact query, chunking, mapping, empty, errors.
- `internal/plugins/book/yamtrack.go` — the `yamtrackNovelImporter`.
- `internal/plugins/book/yamtrack_test.go` — filtering/mapping/error tests with a fake `novelClient`.
- `internal/plugins/book/anilist.go` — add `MediaFormatsByMALIDs` to `novelClient`.
- `internal/plugins/book/manifest.go`, `manifest_test.go` — `ImporterYamtrack = "yamtrack-novel"` and its `ImporterInfo`.
- `internal/plugins/book/plugin.go`, `plugin_test.go` — wire and provide the importer.
- `internal/plugins/book/anilist_test.go`, `account_test.go` — the fake `novelClient` gains the batch method.
- `README.md`, `docs/usage.md`, `docs/handoff.md` — docs.
- No migration, no new source.

---

### Task 1: AniList batch format lookup

**Files:**
- Modify: `internal/plugins/anilist/client.go`
- Modify: `internal/plugins/anilist/client_test.go`

**Interfaces:**
- Consumes: the existing `Client.do`, `ErrRateLimited`/`ErrUnavailable`/`ErrBadResponse`.
- Produces:

```go
// MediaFormatsByMALIDs returns the AniList format of each known MAL id,
// keyed by MAL id. Unknown ids are absent.
func (c *Client) MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
```

- [ ] **Step 1: Write the failing client tests**

Add to `client_test.go`:
- `TestMediaFormatsByMALIDs`: a fake GraphQL server returns `{"data":{"Page":{"media":[{"idMal":1,"format":"NOVEL"},{"idMal":2,"format":"MANGA"},{"idMal":null,"format":"MANGA"}]}}}`; call with `[]int{1,2,3}`; expect `map[1]NOVEL, map[2]MANGA`, no key 3. Assert the request query contains `media(idMal_in: $ids, type: $type)` and `idMal format`, and the variables carry the integer ids.
- `TestMediaFormatsByMALIDsChunks`: 51 ids; the fake counts requests; expect 2 requests with 50 and 1 ids.
- `TestMediaFormatsByMALIDsEmpty`: `nil` ids; expect an empty map and zero requests.
- `TestMediaFormatsByMALIDsErrors`: 429 -> `ErrRateLimited`; 500 -> `ErrUnavailable`; a GraphQL error object -> `ErrBadResponse`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugins/anilist/`
Expected: FAIL — `MediaFormatsByMALIDs` undefined.

- [ ] **Step 3: Implement the method**

Add a dedicated `malFormatsQuery` and decode:

```go
var out struct {
    Page struct {
        Media []struct {
            IDMal  *int   `json:"idMal"`
            Format string `json:"format"`
        } `json:"media"`
    } `json:"Page"`
}
```

Deduplicate `malIDs`, skip non-positive ids, chunk into 50, call `c.do` with `map[string]any{"ids": chunk, "type": typ}`, and fill the map. Empty input returns an empty map without a request.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/plugins/anilist/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: look up AniList formats in batches`

---

### Task 2: Books Yamtrack novel importer

**Files:**
- Create: `internal/plugins/book/yamtrack.go`, `internal/plugins/book/yamtrack_test.go`
- Modify: `internal/plugins/book/anilist.go` (the `novelClient` interface)
- Modify: `internal/plugins/book/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/book/plugin.go`, `plugin_test.go`
- Modify: `internal/plugins/book/anilist_test.go`, `account_test.go` (test fakes)

**Interfaces:**
- Consumes: Task 1's `MediaFormatsByMALIDs`; `yamtrack.ParseRaw`; the books `mapAniListErr`; `KindSeries`.
- Produces:

```go
const ImporterYamtrack = "yamtrack-novel"
type yamtrackNovelImporter struct{ c novelClient }
func (yamtrackNovelImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error)
```

Manifest entry exact:

```go
{
    ID:                ImporterYamtrack,
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

- [ ] **Step 1: Write the failing importer tests**

In `yamtrack_test.go`, use a fake `novelClient` whose `MediaFormatsByMALIDs` returns a map (NOVEL for 1 and 4, MANGA for 2, ONE_SHOT for 3). A synthetic CSV has:
- novel MAL 1, `Completed`, progress 19, score 8, dates, comment -> entry;
- manga MAL 2 -> ignored;
- one-shot MAL 3 -> ignored;
- `mangaupdates` row -> ignored;
- missing MAL 999 -> ignored;
- `book` row -> ignored;
- novel MAL 4, `Completed`, progress 0, score 0.0 -> entry with a nil rating (0 is unrated in the Yamtrack parser);
- novel MAL 5 with unknown status -> skipped;
- duplicate MAL 1 -> skipped.

Assert: two entries, ignored count 5, the skipped reasons, and each kept entry's `KindSeries`, `ExternalIDs["mal"]`, `Progress["chapter"]`, status, rating (nil for 0.0, 8 for score 8), dates and comment.

Add `TestYamtrackNovelImporterBadCSV` -> `plugin.ErrBadInput`; `TestYamtrackNovelImporterBatchError` -> mapped `ErrRateLimited`/`ErrUnavailable`; `TestYamtrackNovelImporterNoRows` -> no client call.

Add manifest/plugin tests: the importer is declared exactly and `plugin.New` provides every declared importer. Update the fake `novelClient` in `anilist_test.go`/`account_test.go` for the new method.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugins/book/`
Expected: FAIL — no importer / no manifest entry / fake missing method.

- [ ] **Step 3: Implement the importer and wiring**

`Parse`:
- call `yamtrack.ParseRaw`; `ErrBadFormat` -> `plugin.ErrBadInput`;
- iterate rows: non-manga, non-`mal`, non-numeric ids, `book` rows -> `res.Ignored++`;
- dedupe MAL ids/rows; duplicates -> skipped with "duplicate media_id";
- call `c.MediaFormatsByMALIDs(ctx, anilist.TypeManga, ids)`; map errors with `mapAniListErr`;
- keep only `format == "NOVEL"`; other formats/missing ids -> `res.Ignored++`;
- unknown/blank status -> skipped; title fallback `MyAnimeList #<id>`;
- map `ParsedEntry{Kind: KindSeries, Title: SingleLine, ExternalIDs: {"mal": id}, Status, Rating, Progress: {"chapter": row.Progress}, StartedOn, FinishedOn, Comment: MultiLine(..., 2000), CoverURL: HTTPSURL}` and copy sanitised notes; add the comment-truncation note at the 2000-rune cap.

Add `ImporterYamtrack` to the manifest and `yamtrackNovelImporter{c: ac}` to `plugin.New`; add `MediaFormatsByMALIDs` to `novelClient`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/plugins/book/ ./internal/plugins/anilist/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: import Yamtrack novels as book series`

---

### Task 3: Documentation

**Files:**
- Modify: `README.md`, `docs/usage.md`, `docs/handoff.md`

- [ ] **Step 1: Update the docs**

README: add "Yamtrack novel" to the import sentence; keep <= 150 lines (trim an older phrase if needed). `docs/usage.md`: in the books import paragraph, describe the **Yamtrack Novel CSV** (upload the general CSV; only `manga`/`mal` rows AniList reports as `NOVEL` become Series; chapters/status/score/dates/notes; other rows ignored; no volume progress; the real export yields 11 novels from 539 manga rows). `docs/handoff.md`: mention the new books importer and the 11/539 result.

- [ ] **Step 2: Verify**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./... && wc -l README.md`
Expected: all packages pass; README <= 150.

- [ ] **Step 3: Commit**

Commit: `docs: yamtrack novel import`

---

### Task 4: Preview and final verification

**Files:** none (verification only; fix any issue found).

- [ ] **Step 1: Build the preview**

`PORT=18083 docker compose -p allister-preview up -d --build`; enable the Books plugin if needed.

- [ ] **Step 2: Upload the real export**

Upload `yamtrack_2026-11-09 15_33_37.419384+01_00.csv` to `POST /settings/plugins/book/import/yamtrack-novel` with `mode=skip`. Verify:
- the upload succeeds (batch AniList lookup);
- the report shows **11 imported; all other rows ignored (2,827 for this export: 528 non-novel manga and 2,299 other media rows)** (or 10 imported if an existing Series entry is skipped);
- `/book/series` shows the novels with chapter progress, statuses, chapters/volumes, author and cover from AniList;
- no non-novel manga rows became manual Series entries.
- The CSV stays untracked; do not commit it.

- [ ] **Step 3: Final suite**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...`
Expected: 25 packages pass; vet/fmt silent; `git status` clean except the ledger.

- [ ] **Step 4: Record the verification**

Append the commands, observations and any accepted limitations to the SDD progress ledger. The controller then runs the whole-branch review and fix wave before merge.
