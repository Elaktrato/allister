# Series with their own data, and spin-offs — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every series its own cover, description, run dates, finished marker, expected parts and plugin-declared fields, fill blanks from a linked entry or from its parts, and support spin-off parts and spin-off series.

**Architecture:** Extend the core series groups (migration 00019 columns + a `series_fields` table), declare series data per kind in the plugin contract (`SeriesFields`, `SeriesLink`, `SeriesHint.ReleaseField`), resolve each value on read (own → linked entry → derived from main parts), and extend the series page, Series tab, home page and backup.

**Tech Stack:** Go 1.27, SQLite (modernc, goose), html/template + htmx, existing cover store; `scripts/go.sh`.

**Spec:** `docs/superpowers/specs/2026-11-15-series-data-design.md`

## Global Constraints

- Go only via `scripts/go.sh` (test -count=1 ./..., vet, fmt) + `scripts/ci.sh lint` (0 issues) before each commit; TDD with a behavioural RED shown.
- Migration **00019**; never edit 00018; 00008 stays unused.
- Contract additions optional (`omitempty`), JSON round-trip tested; `ContractVersion` stays 1.
- Base series field keys reserved: `cover`, `description`, `started_on`, `ended_on`, `finished`, `parts_total`.
- Resolution order per value: own → linked entry → derived → nothing. Owner values never overwritten.
- Derived dates from **main** parts only: start = earliest release date; end = latest release date **only when finished**. Expected parts derived = number of main parts.
- Completed requires every main part completed **and**, when expected parts is set, completed main parts ≥ expected parts ("5/12, all read" → active).
- Main parts and spin-off parts keep **separate** dense positions (1..n, 1..m); moves stay in the block; flipping moves to the end of the other block.
- Spin-off series: same plugin, one level only (a parent has no parent; a series with spin-offs cannot become one).
- Description `plugin.MultiLine`, cap 20000 runes; covers through the existing cover store.
- Backup `format_version` stays 1; series covers included in the archive.
- No media words in core; theme colours only; no inline script/style; README ≤ 150 lines.
- Branch `feat/series-data`; commits `feat:`/`fix:`/`test:`/`docs:` + trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Deleting the linked entry or the parent series** → the series keeps its own data, the link/parent becomes empty, nothing errors. *(Task 2: `TestSeriesLinkAndParentClearedOnDelete`.)*
2. **A part flagged spin-off while it is the only main part** → main progress shows 0 main parts (or the expected number), the series is not deleted, positions stay dense in both blocks. *(Task 2: `TestSpinOffFlipKeepsBlocksDense`.)*
3. **Linked entry with no volumes or dates** → the series falls back to derived values, not to zero or empty dates. *(Task 3: `TestResolveFallsBackWhenLinkedBlank`.)*
4. **Hostile edit input** (a cover URL that is not https, a date like `2026-02-30`, a non-number in a number field, a 20001-rune description) → 422 with the field's message, nothing stored. *(Task 4: `TestSeriesEditRefusesInvalid`.)*
5. **Restoring a backup whose parent series or linked entry is missing** → the series restores with the link empty and a note. *(Task 6: `TestRestoreSeriesMissingLinkAndParent`.)*

---

### Task 1: Contract and plugin declarations

**Files:** `internal/plugin/contract.go`, `validate.go`, tests; `internal/plugins/book/manifest.go` (+ AniList novel mapping for `ended_on`), `internal/plugins/{anime,manga,tv,movie,game}/manifest.go`; `docs/plugins.md`.

**Interfaces — Produces:**
- `SeriesHintDef.ReleaseField string` (`json:"release_field,omitempty"`); `TitleField` optional (a hint must name at least one field; `ReleaseField` must be a date field of the kind).
- `KindDef.SeriesFields []FieldDef` (`json:"series_fields,omitempty"`), validated like `Fields`, keys not in the reserved base keys.
- `type SeriesLinkDef struct { Kind, DescriptionField, StartField, EndField, PartsField string }` (json `kind`, `description_field`, `start_field`, `end_field`, `parts_field`, the four field names `omitempty`); `KindDef.SeriesLink *SeriesLinkDef` (`json:"series_link,omitempty"`). Validation: `Kind` names a kind of the manifest; the named fields exist on that kind with types text / date / date / number.

- [ ] **Step 1: Failing tests** — JSON tests for the three additions (set → exact JSON, unset → omitted, round trip, in the full-manifest round trip); validation tables (empty hint refused; ReleaseField not a date refused; SeriesFields key `description` refused; SeriesLink unknown kind / wrong field type refused; valid ones accepted); book manifest: Book kind declares `SeriesHint{TitleField:"series", PositionField:"series_number", ReleaseField:"published_on"}`, `SeriesFields` Volumes (number), Chapters (number), Author (text) in that order, `SeriesLink{Kind:"series", DescriptionField:"synopsis", StartField:"published_on", EndField:"ended_on", PartsField:"volumes"}`; Light novels kind has `{Key:"ended_on", Label:"Ended", Type:FieldDate, Show:true, Editable:true}`; the AniList novel mapping fills `ended_on` from `endDate` (test with a fixture); anime/manga/tv `ReleaseField:"started_on"`, movie/game `ReleaseField:"released_on"` (TitleField empty).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**; with `TitleField` empty, the web's "Make this a series" prefill and the "same series first" candidate ordering skip the hint (test both); document in `docs/plugins.md`.
- [ ] **Step 4: Run** full suite, vet, fmt, lint → PASS.
- [ ] **Step 5: Commit** `feat: series fields, series link and release field in the contract`

### Task 2: Migration 00019 and the series store

**Files:** `internal/db/migrations/00019_series_data.sql`; `internal/library/series.go` (+ tests); latest-version constants in `internal/db` tests.

**Interfaces — Produces (internal/library):**
- `Series` gains `CoverPath, Description string; StartedOn, EndedOn *time.Time; Finished bool; PartsTotal int /* 0 = unset */; LinkedItemID, ParentID int64 /* 0 = none */; Fields map[string]plugin.FieldValue`.
- `type SeriesDetails struct { Title, CoverPath, Description string; StartedOn, EndedOn *time.Time; Finished bool; PartsTotal int; LinkedItemID, ParentID int64; Fields map[string]plugin.FieldValue }`
- `func (l *Library) UpdateSeriesDetails(ctx context.Context, id int64, kind plugin.KindDef, d SeriesDetails) error` — validates fields against `kind.SeriesFields` (like entry details), the link kind against `kind.SeriesLink.Kind` and the same plugin, the parent rules; `ErrInvalid` with a message naming the field.
- `func (l *Library) SetPartSpinOff(ctx context.Context, seriesID, itemID int64, spinOff bool) error` — moves the part to the end of the other block, renumbers both.
- `SeriesPart{ItemID int64; Position int; SpinOff bool}` returned by a `SeriesPartsOrder(ctx, seriesID) ([]SeriesPart, error)`; `SeriesParts` returns main parts then spin-offs, each in position order; `MovePart` stays inside the part's block; `AddPart` adds to the main block; `SeriesOfItem` returns the position within the part's block and a `spinOff bool` (new return value — update callers).
- `func (l *Library) SpinOffsOf(ctx context.Context, parentID int64) ([]Series, error)`.

- [ ] **Step 1: Failing tests** — `TestUpdateSeriesDetails` (round trip of every base field and a number/text series field; empty values clear); `TestUpdateSeriesDetailsRefuses` (link of another plugin/kind, parent = self, parent that has a parent, series with spin-offs as child, unknown field key, wrong type); `TestSpinOffFlipKeepsBlocksDense` (Review Focus 2); `TestMovePartStaysInBlock`; `TestSeriesLinkAndParentClearedOnDelete` (Review Focus 1); `TestSpinOffsOf`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** (migration as the spec's Data section). **Step 4: Run** all → PASS. **Step 5: Commit** `feat: series details, spin-off parts and parent series (migration 00019)`

### Task 3: Resolution and status

**Files:** `internal/library/series.go`, `series_resolve.go` (new, pure resolution), tests.

**Interfaces — Produces:**
- `type ResolvedSeries struct { Series Series; Cover, Description string; StartedOn, EndedOn *time.Time; Finished bool; PartsTotal int; Fields map[string]plugin.FieldValue; MainParts, SpinOffParts, Completed int; Status Status }`
- `func ResolveSeries(s Series, kind plugin.KindDef, linked *Item, linkedEntry *Entry, main []Row) ResolvedSeries` — pure; order own → linked (via `SeriesLink` mapping; same-key series fields) → derived (`ReleaseField` of the main parts; end only when finished; finished also true when not set but the linked entry has an end date) → nothing. Cover: own → linked cover → first main part's cover.
- `func DeriveSeriesStatus(parts []Status, expected int) Status` — the six rules on main parts; rule 2 requires completed ≥ expected when expected > 0.
- `ListSeries`, `SeriesCounts`, `ActiveSeries` and their `SeriesRow` use the resolved cover, expected parts and the new status rule; `SeriesRow` gains `PartsTotal int` and `ParentID int64`.

- [ ] **Step 1: Failing tests** — `TestResolveOrder` (own beats linked beats derived for each base field and a series field); `TestResolveDerivedDates` (finished → first/last release; not finished → no end); `TestResolveFallsBackWhenLinkedBlank` (Review Focus 3); `TestResolveSpinOffsExcluded`; `TestDeriveSeriesStatusExpected` (5 of 12 completed → active; 12 of 12 → completed; expected 0 → old rules); extend `TestActiveSeriesMatchesDerivedStatus` to expected parts.
- [ ] **Step 2–5:** RED, implement, GREEN, commit `feat: resolve series data from own, linked and derived values`

### Task 4: Series page and edit details

**Files:** `internal/web/series.go`, `series_edit.go` (new), templates `series.html`, `series_edit.html` (new); reuse the details field rendering (`internal/web/details.go`, `itemform.go`) and the cover store; tests.

**Interfaces:** Consumes Tasks 1–3. Produces routes `GET /series/{uid}/edit` (dialog), `POST /series/{uid}/edit`, `POST /series/{uid}/parts/{item}/spinoff` (`spin_off=1|0`), and pickers `GET /series/{uid}/link-candidates?q=`, `GET /series/{uid}/parent-candidates?q=`.

- [ ] **Step 1: Failing tests** — the page header shows the resolved cover, description, dates, expected parts and series fields in `SeriesFields` order, "Spin-off of <parent>" for a spin-off series, a Spin-offs block (spin-off parts and spin-off series with progress); edit round trip (upload cover, URL cover, all fields, link, parent); `TestSeriesEditRefusesInvalid` (Review Focus 4: non-https URL, `2026-02-30`, `abc` in a number field, a 20001-rune description → 422 with the field named, nothing stored); spin-off toggle renumbers and redirects; the item overview of a spin-off part reads "Spin-off N of <series>" and its list badge "Spin-off · <series>"; pickers list only valid candidates.
- [ ] **Step 2–5:** RED, implement, GREEN, commit `feat: series details page, edit dialog and spin-offs`

### Task 5: Series tab and home page

**Files:** `internal/web/list.go` (`buildSeriesTab`), `home.go`, templates `series_rows.html`, `home.html`, tests.

- [ ] **Step 1: Failing tests** — Series tab rows and home "Series in progress" use the resolved cover and "completed/expected"; a spin-off series shows "Spin-off of <parent>"; "5/12 all read" appears under the Active status tab, not Completed.
- [ ] **Step 2–5:** RED, implement, GREEN, commit `feat: Series tab and home page show series data`

### Task 6: Backup and restore

**Files:** `internal/library/backup.go`, `internal/backup/backup.go` (+ cover collection), tests.

- [ ] **Step 1: Failing tests** — round trip of every new series value incl. the cover file, the linked entry (by identity), the parent (by uid, resolved in a second pass) and spin-off flags in block order; `TestRestoreSeriesMissingLinkAndParent` (Review Focus 5); an archive from before this change still restores; a new archive restores in a reader that ignores the new members (decode into the old struct shape).
- [ ] **Step 2–5:** RED, implement, GREEN, commit `feat: series data in backup and restore`

### Task 7: Docs, preview, PR

- [ ] **Step 1:** `docs/usage.md` (series page, edit, spin-offs, linked entry, derived dates), `docs/handoff.md`, README ≤ 150.
- [ ] **Step 2:** Preview on port 18093 (own compose project): link a series to a Light novel entry (manual entry with volumes, dates), check the header, "5/12", a spin-off part and a spin-off series; tear down.
- [ ] **Step 3:** `scripts/ci.sh all` green; PR (label `feat`), CI green, rebase-merge.
