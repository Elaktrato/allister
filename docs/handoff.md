# Handoff (2026-11-10)

State of the project when the work moved from Claude Code to another agent. `main` contains everything below; nothing is pushed (no remote).

## What exists

- **Core + plugins.** Generic core, anime as the first default plugin (AniList only), togglable in Settings > Plugins. Plugin contract in `internal/plugin`, author guide in `docs/plugins.md`. Book and TV fixtures in `internal/plugin/plugintest` prove the contract end to end (no shipped book/TV plugin yet).
- **Anime features.** Search (AniList), add, manual entries, edit details for every item, match-to-source by URL or dialog, local covers, genres and tags, recommendations (async fragment), Yamtrack CSV import (~830 rows, paced at 30 requests/min, resumable, unmatched allowed, 0.0 = unrated), per-plugin export, universal backup/restore (merge).
- **List UI.** Left sidebar navigation from the enabled plugins (Material Symbols icons, vendored and pinned), sortable table head, search bar with a collapsible filter panel (filters generated from the manifest, any/all genres and tags with counts, URL-bookmarkable, live htmx updates), compact "Sort by" control on small screens, settings with their own tab column.
- **Per plugin settings** (`/settings/plugins/{id}`): title language (declared languages plus tags found in that plugin's stored titles; fallback order is plugin data via `KindDef.TitleFallback`) and visible table columns with order (`KindDef.Columns`, stored as `plugin.<id>.columns.<kind>`).
- **Builds.** Everything pinned and checksummed, see `PINS.md`.

## Decisions worth knowing

- Migration 00007 dropped the first version's anime tables without copying (the only prior data was an empty preview). A volume from the first preview build starts empty after upgrading; the README warns about it. Migration 00011 moved the global `title_lang` setting to `plugin.anime.title_lang`.
- Progress combination switching (count/pages/percent) lives in the entry dialog, not in Edit details.
- Source "bad answer" errors map to a 502 page in the web (not retried), a failed row in imports.
- The universal backup contains everything including disabled plugins; there is no per-plugin selection screen (per-plugin export exists).
- Hiding the Progress column also hides the +1 button (documented, noted on the settings page).

## Known follow-ups (none blocking)

- Docs under `docs/superpowers/specs` and `plans` are historical; they still describe the global title-language setting in places.
- Not built: other media types (books, manga, TV, movies, games) as plugins, MyAnimeList source (deliberately skipped), dashboards, accounts.

## Running and previews

- The owner's containers: 18080 (old first-version preview, own volume), 18081 (project `allister-main`, an older build of `main`), 18082 (project `allister-ui`, rebuilt from the latest `main`). Do not touch them without asking; stop a preview with `docker compose -p <project> down -v` (removes its volume).
- `curl http://localhost:<port>/healthz` answers `ok`.

## Pushing to GitHub

The repo has no remote. The owner pushes themselves:

```
git remote add origin <repo URL>
git push -u origin main
```
