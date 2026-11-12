# Yamtrack Manga Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `yamtrack-manga` importer to the manga plugin that imports the 527 non-novel manga rows (482 manga, 43 one-shots, 2 missing MAL ids) and the one `mangaupdates` row, while the 11 novels stay only in books Series.

**Architecture:** Reuse the shared `MediaFormatsByMALIDs` lookup to skip AniList `NOVEL` rows. The importer submits `KindManga` entries with MAL external ids (or a manual entry for the non-MAL row); the existing manga AniList source resolves the details. No new source, migration or dependency.

**Tech Stack:** Go, existing plugin/importer contract, AniList GraphQL, `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-12-yamtrack-manga-import-design.md`

## Global Constraints

- No new Go dependencies; no new source; no migration.
- The importer's `Parse` makes one batch format call (11 requests for 538 MAL ids); the 525 detail lookups happen at the runner's 30/minute pace and are resumable.
- The 11 AniList `NOVEL` rows are skipped, never duplicated in the manga plugin.
- The 2 MAL ids AniList no longer knows are submitted and become manual unmatched manga; the 1 non-MAL `mangaupdates` row becomes a manual manga entry (`ManualRows:true`).
- Chapter progress only; the CSV has one `progress` column.
- The root CSV is owner data: never commit it.
- README <= 150 lines.
- TDD: write the failing tests, show the real RED, implement, show GREEN.
- Full `scripts/go.sh test -count=1 ./...`, `scripts/go.sh vet ./...`, `scripts/go.sh fmt ./...` before each commit; output must be pristine.
- Commit style: `feat: ...`, `docs: ...`.

## Review Focus

1. **Novel exclusion.** The 11 `NOVEL` rows must be `Ignored`; the other formats (including unknown/missing MAL ids) must be submitted.
2. **Manual row.** The non-MAL `mangaupdates` row must become a manual manga entry, not be dropped; `ManualRows:true` is required.
3. **Row mapping.** Chapter progress, status, 0-vs-unrated score, dates, comment, cover and MAL external id; no volumes.
4. **Wiring.** Manifest `yamtrack-manga` exact; `plugin.New` provides it; the narrow `malFormatClient` interface and fake are correct.
5. **Errors.** Bad CSV -> `ErrBadInput`; batch rate-limit/unavailable mapped; empty candidate set no request; duplicate/unknown-status skipped.

---

## File Structure

- `internal/plugins/manga/yamtrack.go` — the importer and `malFormatClient` interface.
- `internal/plugins/manga/yamtrack_test.go` — filter/mapping/error tests.
- `internal/plugins/manga/manifest.go`, `manifest_test.go` — `ImporterYamtrack = "yamtrack-manga"` and its info.
- `internal/plugins/manga/plugin.go`, `plugin_test.go` — wire and provide the importer.
- `README.md`, `docs/usage.md`, `docs/handoff.md` — docs.
- No migration, no source, no client change (the batch method already exists).

---

### Task 1: Manga Yamtrack importer

**Files:**
- Create: `internal/plugins/manga/yamtrack.go`, `internal/plugins/manga/yamtrack_test.go`
- Modify: `internal/plugins/manga/manifest.go`, `manifest_test.go`
- Modify: `internal/plugins/manga/plugin.go`, `plugin_test.go`

**Interfaces:**
- Consumes: `anilist.Client.MediaFormatsByMALIDs(ctx, typ string, malIDs []int) (map[int]string, error)`; `yamtrack.ParseRaw`; the manga `mapErr`; `KindManga`.
- Produces:

```go
const ImporterYamtrack = "yamtrack-manga"

type malFormatClient interface {
    MediaFormatsByMALIDs(ctx context.Context, typ string, malIDs []int) (map[int]string, error)
}
type yamtrackMangaImporter struct{ c anilist.Provider }
func (yamtrackMangaImporter) Parse(ctx context.Context, r io.Reader) (plugin.ParseResult, error)
```

Manifest entry exact:

```go
{
    ID:                ImporterYamtrack,
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

- [ ] **Step 1: Write the failing importer tests**

Use a fake format client whose `MediaFormatsByMALIDs` returns `NOVEL` for 1, `MANGA` for 2 and 4, `ONE_SHOT` for 3, and no entry for 999. A synthetic CSV:
- novel MAL 1 -> ignored;
- manga MAL 2, progress 12, score 8, dates, comment -> entry;
- one-shot MAL 3, progress 1 -> entry;
- missing MAL 999 -> entry with `ExternalIDs{"mal":"999"}` (the runner will mark it unmatched);
- `mangaupdates` row, title, status -> manual entry with no external id;
- `book` row -> ignored;
- unknown-status manga MAL 4 -> skipped;
- duplicate MAL 2 -> skipped.

Assert two resolved entries + one missing entry + one manual entry, ignored count 2, skipped reasons, chapter progress, status, rating (0.0 nil), dates, comment, cover, and the manual entry's empty external ids. Add `TestYamtrackMangaImporterBadCSV` -> `ErrBadInput`, `TestYamtrackMangaImporterBatchError` -> mapped `ErrRateLimited`/`ErrUnavailable`, and `TestYamtrackMangaImporterNoCandidates` -> no batch call.

Add manifest/plugin tests: the importer is declared exactly and `plugin.New` provides every declared importer.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/go.sh test ./internal/plugins/manga/`
Expected: FAIL — no importer / no manifest entry.

- [ ] **Step 3: Implement the importer and wiring**

`Parse`:
- `yamtrack.ParseRaw`; `ErrBadFormat` -> `plugin.ErrBadInput`.
- Iterate rows: non-manga -> `Ignored`; manga with `source=mal` + positive numeric id -> candidate (dedupe by id); manga with another source + title -> manual candidate; no title/id -> skipped.
- Batch `MediaFormatsByMALIDs(ctx, anilist.TypeManga, ids)` through a type assertion on `c`; if unsupported -> `plugin.ErrUnavailable`; map errors with `mapErr`.
- For each MAL candidate: `NOVEL` -> `Ignored`; otherwise a `KindManga` entry with `ExternalIDs{"mal": id}`, `Progress{"chapter": row.Progress}`, status, rating, dates, sanitised comment (2000), cover, notes; title fallback `MyAnimeList #<id>`.
- Manual candidates: `KindManga` entry with no external id, same mapping, title required.
- Unknown/blank status -> skipped.

Add `ImporterYamtrack` to the manifest and `yamtrackMangaImporter{c: p.source.c}` to `plugin.Importer`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/go.sh test ./internal/plugins/manga/ ./internal/plugins/anilist/`
Expected: PASS.

- [ ] **Step 5: Full verification and commit**

Run the full suite, vet and fmt. Commit: `feat: import Yamtrack manga`

---

### Task 2: Documentation

**Files:**
- Modify: `README.md`, `docs/usage.md`, `docs/handoff.md`

- [ ] **Step 1: Update the docs**

README: add "Yamtrack manga" to the import sentence; keep <=150 lines. `docs/usage.md`: manga import paragraph — upload the general CSV; 538 MAL rows resolve through AniList; the 11 NOVELs are skipped to Books > Series; 482 manga + 43 one-shots + 2 missing MAL ids become 528 manga entries (2 unmatched manual); the 1 mangaupdates row becomes manual; 2,310 rows ignored overall (2,299 other media + 11 novels); no volume progress. `docs/handoff.md`: manga import bullet.

- [ ] **Step 2: Verify**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./... && wc -l README.md`
Expected: all packages pass; README <=150.

- [ ] **Step 3: Commit**

Commit: `docs: yamtrack manga import`

---

### Task 3: Preview and final verification

**Files:** none (verification only; fix any issue found).

- [ ] **Step 1: Build the preview**

`PORT=18083 docker compose -p allister-preview up -d --build`; enable Manga and Books if needed.

- [ ] **Step 2: Upload the real export**

Upload the root CSV to `POST /settings/plugins/manga/import/yamtrack-manga` with `mode=skip`. Verify:
- 528 processed, 526 imported (525 resolved + 1 manual), 2 unmatched, 2,310 ignored; adjust for an existing manga entry (skip one, import 525/526).
- `/manga` has 528 entries; no NOVEL titles; the 11 novels remain only in `/book?kind=series`.
- The 2 missing MAL ids are manual with the unmatched note; the `mangaupdates` row is manual.
- The root CSV stays untracked.

- [ ] **Step 3: Final suite**

Run: `scripts/go.sh test -count=1 ./... && scripts/go.sh vet ./... && scripts/go.sh fmt ./...`
Expected: 25 packages pass; vet/fmt silent; `git status` clean except the ledger.

- [ ] **Step 4: Final review and release**

Run the whole-branch review. After merge:
- Tag `v0.2.0` at the merged `main` and create a GitHub release with the manga importer and the 528/11 partition.
- Keep the `v0.1.0` release unchanged.
