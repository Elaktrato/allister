# Handoff (2026-11-10)

State of the project when the work moved from Claude Code to another agent. `main` contains everything below; nothing is pushed (no remote).

## What exists

- **Core + plugins.** Generic core, anime as the first default plugin (AniList only) and manga as a second, opt-in plugin (`DefaultEnabled: false`), both togglable in Settings > Plugins. Plugin contract in `internal/plugin`, author guide in `docs/plugins.md`. Book and TV fixtures in `internal/plugin/plugintest` prove the contract end to end (no shipped book/TV plugin yet).
- **Anime features.** Search (AniList), add, manual entries, edit details for every item, match-to-source by URL or dialog, local covers, genres and tags, recommendations (async fragment), Yamtrack CSV import (~830 rows, paced at 30 requests/min, resumable, unmatched allowed, 0.0 = unrated), per-plugin export, universal backup/restore (merge).
- **Manga features.** The same flow through a manga-typed AniList source: search, add, manual entries, edit details, matching, covers, genres/tags, staff credits, recommendations, AniList-account and MyAnimeList-XML imports, JSON export and backup. Two count progress tracks (chapters and volumes, chapter primary); no calendar because AniList has no chapter schedule.
- **List UI.** Left sidebar navigation from the enabled plugins (Material Symbols icons, vendored and pinned), a **card view** (cover, title overlay, usable +1, colored status corner) that the list opens with by default, alongside the table view (sortable head, configurable columns); a view toggle in the header; search bar with a collapsible filter panel (filters generated from the manifest, any/all genres and tags with counts, URL-bookmarkable, live htmx updates), compact "Sort by" control, per-plugin **home settings** (default view and status), settings with their own tab column.
- **Per plugin settings** (`/settings/plugins/{id}`): title language (declared languages plus tags found in that plugin's stored titles; fallback order is plugin data via `KindDef.TitleFallback`) and visible table columns with order (`KindDef.Columns`, stored as `plugin.<id>.columns.<kind>`).
- **Appearance** (`/settings/appearance`, package `internal/appearance`): colour tokens as CSS custom properties from a generated, content-versioned `/appearance.css`; `tailwind.config.js` maps every colour class of the templates to a token through `internal/appearance/tailwind-map.json` (templates keep their slate/indigo classes; a test refuses unmapped ones). Presets plus an edited palette, system/bundled/uploaded fonts (table `fonts`, migration 00013, files in `DATA_DIR/fonts`), text size; included in the backup.
- **Imports.** Yamtrack CSV on Settings > Import; AniList account (by username, with an optional daily/weekly repeat) and MyAnimeList XML export in each of the anime and manga plugins' Import sections, each with a rule for existing entries (skip / update when the source is newer / always update). The MAL XML export has no timestamps, so "newer" never updates there.
- **Calendar.** `GET /calendar.ics` serves an iCalendar feed for local subscribers: timed events for the upcoming releases of watching entries, from stored schedules (the optional `ScheduleSource` plugin capability, implemented by the anime plugin via AniList air dates; refreshed by the `sync_schedule` job, at most daily through the feed). Manga declares no schedule: AniList has no chapter release dates.
- **Builds.** Everything pinned and checksummed, see `PINS.md`.

## Decisions worth knowing

- Migration 00007 dropped the first version's anime tables without copying (the only prior data was an empty preview). A volume from the first preview build starts empty after upgrading; the README warns about it. Migration 00011 moved the global `title_lang` setting to `plugin.anime.title_lang`.
- Progress combination switching (count/pages/percent) lives in the entry dialog, not in Edit details.
- Source "bad answer" errors map to a 502 page in the web (not retried), a failed row in imports.
- The universal backup contains everything including disabled plugins; there is no per-plugin selection screen (per-plugin export exists).
- The AniList client and the JSON exporter are shared packages (`internal/plugins/anilist`, `internal/plugins/jsonexport`); media-specific mapping stays in each plugin. Relation kinds are normalised (`source`, `adaptation`, `sequel`, `prequel`, `other`) and each plugin derives the related item's kind from the node's media type.
- Hiding the Progress column also hides the +1 button (documented, noted on the settings page).

## Known follow-ups (none blocking)

- Not built: other media types (books, TV, movies, games) as plugins, a manga release calendar (AniList has no chapter schedule; a MangaDex source could add one), MyAnimeList as a live source (deliberately skipped), dashboards, accounts.

## Running and previews

- The owner's containers: 18080 (old first-version preview, own volume), 18081 (project `allister-main`, an older build of `main`), 18082 (project `allister-ui`, rebuilt from the latest `main`). Do not touch them without asking; stop a preview with `docker compose -p <project> down -v` (removes its volume).
- `curl http://localhost:<port>/healthz` answers `ok`.

## Pushing to GitHub

The repo has no remote. The owner pushes themselves:

```
git remote add origin <repo URL>
git push -u origin main
```
