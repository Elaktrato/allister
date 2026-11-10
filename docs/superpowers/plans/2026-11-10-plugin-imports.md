# Plugin Imports and the Scheduled AniList Import — plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** AniList-by-username and MyAnimeList-XML importers on the anime plugin settings page, a user-chosen rule for existing entries, and an optional scheduled AniList import.

**Architecture:** Optional `AccountImporter` contract → AniList username request / MAL XML parse → `SubmitAccount`/`Submit` with a stored mode → import job applies skip/newer/always (migration 00015 `imports.mode`) → self-rescheduling `scheduled_import` job.

**Spec:** `docs/superpowers/specs/2026-11-10-plugin-imports-design.md`

## Global Constraints

- No media words in core code/templates; migration next number is **00015**; full suite, vet, fmt before each commit; TDD; branch + merge.
- Updates write only status/rating/primary progress of the list entry; never item data, covers or credits.
- The generic `/settings/import` page keeps working; the new section lives on `/settings/plugins/{id}`.

---

### Task 1: Contract, AniList account importer, MAL XML importer

**Files:** `internal/plugin/{contract.go,interfaces.go,data.go}`, `internal/plugins/anime/{importer.go,plugin.go,manifest.go,source.go}`, `internal/plugins/anime/anilist/{client.go,types.go}`, `.../anilist/testdata/account.json`, `internal/plugins/anime/anilist/testdata/account_mal.xml`; docs `docs/plugins.md`; tests.

**Interfaces:** `AccountImporter { ImportAccount(ctx, account string) (ParseResult, error) }`; `ImporterInfo.AccountLabel string`; `ParsedEntry.UpdatedAt *time.Time`; anime importers `anilist-user` (account, `ExternalIDType: "anilist"`, `ResolveVia: anilist`) and `mal-xml` (file, `ExternalIDType: "mal"`); source `Lookup` gains the `anilist` type (Details by id).

- [ ] RED: AniList account fixture maps statuses (CURRENT/REPEATING→active, PLANNING→planned, COMPLETED→completed, PAUSED→paused, DROPPED→dropped), score normalisation (0–100 → 0–10, 0 unrated), updatedAt, titles; unknown user error; MAL XML fixture maps ids/titles/progress/score/status codes (1/2/3/4/6); `Lookup("anilist", id)` returns the details.
- [ ] Implement; `docs/plugins.md` (the capability and both importers).
- [ ] Full suite, vet, fmt; commit `feat: account and XML importers for the anime plugin`.

### Task 2: Import modes in the runner

**Files:** create `internal/db/migrations/00015_import_mode.sql`; `internal/importer/{submit.go,runner.go,report.go}`, `internal/library/entries.go` (update helper); tests; `internal/db/sync_jobs_test.go`/`fonts_test.go` version pins.

**Interfaces:** `Submit(..., mode string)` and `SubmitAccount(ctx, pluginID, importerID, account, mode string)`; modes `skip|newer|always`; `imports.mode`; `Report.Updated` (+ `UpdatedRows` list capped like the others); `library.ApplyImport(ctx, itemID, library.Entry, newerThan *time.Time) (bool, error)` — applies status/rating/primary progress, bumping `updated_at`, only when `newerThan` is nil or later than the entry.

- [ ] RED: skip leaves entries untouched; newer applies only when the source row is newer (no timestamp → skip); always applies; updates write only the three fields; the report counts updated; Resume keeps the stored mode.
- [ ] Implement; version pins updated.
- [ ] Full suite, vet, fmt; commit `feat: import modes for existing entries`.

### Task 3: The scheduled import job

**Files:** create `internal/jobs/scheduled_import.go`; `internal/settings/settings.go` (typed accessors: ImportAccount/ImportMode/ImportSchedule + setters); `cmd/allister/main.go`; tests.

**Interfaces:** `KindScheduledImport = "scheduled_import"`; payload `{plugin_id, importer_id}`; handler deps `{Imports interface{ SubmitAccount(...) }, Settings *settings.Store, Queue EnqueueAt-queue}`; `jobs.Queue` gains `EnqueueAt(ctx, kind, payload, at time.Time)`; intervals daily 24h / weekly 168h; the handler re-enqueues itself while the schedule is enabled and never fails the job on `ErrActive`.

- [ ] RED: runs the stored account through SubmitAccount with the stored mode and re-enqueues at +interval; stops when off; steps aside (re-enqueues) when another import is active; skips when the account is missing.
- [ ] Implement; register in `cmd/allister`; the queue's `EnqueueAt`.
- [ ] Full suite, vet, fmt; commit `feat: scheduled AniList import`.

### Task 4: UI — the Import section and the Updated counter

**Files:** `internal/web/plugin_settings.go` (+ view data and POST), `server.go` (route), `templates/settings_plugin.html`, `templates/import.html` (Updated counter/list), `internal/web/import.go` (mode passthrough for the generic page), tests.

**Interfaces:** `POST /settings/plugins/{id}/import/{importer}` — account importers read `account`, file importers a multipart `file`, plus `mode` and (account importers) `schedule`; stores `plugin.<id>.import.<importer>.{mode,account,schedule}`; redirects to `/settings/import`. The section lists each importer with its form, the mode select (newer annotated for the XML importer as unavailable) and the schedule select for the AniList importer.

- [ ] RED: the section renders the right controls per importer; posting an account starts an import (fixture) with the chosen mode and stores account/schedule; invalid mode/schedule refused; the import page shows the updated count.
- [ ] Implement.
- [ ] Full suite, vet, fmt; commit `feat: import section on the plugin settings page`.

### Task 5: Docs and finish

- [ ] `docs/usage.md` (the import section, the rules, the schedule). Full suite, vet, fmt; merge `feat/imports`; rebuild the 18083 preview.
