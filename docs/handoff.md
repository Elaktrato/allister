# Handoff (2026-11-11)

State of the project when the work moved from Claude Code to another agent. After the local merge of `feat/movies-plugin`, `main` contains everything below, including the Movies plugin, source API keys and the shared TMDB client; the owner pushes to `origin`.

## What exists

- **Core + plugins.** Generic core, anime as the first default plugin (AniList only), manga as an opt-in plugin (`DefaultEnabled: false`), books as another opt-in plugin with two kinds and three sources, and TV and movies as opt-in plugins, all togglable in Settings > Plugins. Plugin contract in `internal/plugin`, author guide in `docs/plugins.md`. Book and TV fixtures in `internal/plugin/plugintest` still prove the contract end to end.
- **Anime features.** Search (AniList), add, manual entries, edit details for every item, match-to-source by URL or dialog, local covers, genres and tags, recommendations (async fragment), Yamtrack CSV import (~830 rows, paced at 30 requests/min, resumable, unmatched allowed, 0.0 = unrated), per-plugin export, universal backup/restore (merge).
- **Manga features.** The same flow through a manga-typed AniList source: search, add, manual entries, edit details, matching, covers, genres/tags, staff credits, recommendations, AniList-account and MyAnimeList-XML imports, JSON export and backup. Two count progress tracks (chapters and volumes, chapter primary); no calendar because AniList has no chapter schedule.
- **Books features.** Two kinds: **Book** (author, pages, year, publisher, ISBN, series, synopsis, dates, genres/tags; pages or percent) and **Series** (light novels; volume and cumulative chapter, with both active by default and chapter-only or volume-only combinations). Sources: Open Library and Google Books for books, AniList for novels (`format: NOVEL`). Imports: Goodreads CSV (ISBN through Open Library then Google Books, no-ISBN rows manual) and AniList account (novels only, with optional repeat). JSON export, backup, no calendar. The source-kind declarations keep AniList to series and the book sources to books.
- **TV features.** One **Show** kind: season + episode + position (position cleared by +1), TVmaze primary with cast, genres and episode schedules, and TMDB (always declared, needs a key; no schedule). Yamtrack TV CSV import groups `tv`/`season`/`episode` rows into one show; episode-row presence/season progress is the watched data, blank episode statuses are accepted, and no-id shows stay manual. JSON export, backup, calendar events from TVmaze with season-aware labels.
- **Movies features.** One **Movie** kind: Want to watch / Watching / Paused / Seen / Dropped, an optional playback position (duration, no +1), year, runtime, director, studio, country, language, collection, release status, synopsis and release date, genres and tags. TMDB source: search, details, cast and director/writer credits, recommendations, TMDB and IMDb ids, cover. Yamtrack movie CSV import uses only movie rows, reports duplicates once, resolves numeric TMDB ids and keeps everything else manual with a note. JSON export, backup, no calendar (TMDB has release dates but no air times). Opt-in.
- **Source API keys.** Settings > Plugins has an API keys card for every distinct keyed source of the installed manifests (enabled or not): save, replace or remove; a stored key is never echoed. The key is stored as `source.<source-id>.key`, read per request, and passed to the plugin by the host. `TMDB_API_KEY` remains the fallback and the stored key wins; TV and Movies both declare `tmdb`, so they share one key. `plugin.ErrNotConfigured` gives actionable 503 messages, keyless imports keep manual rows and syncs keep stored credits.
- **Shared TMDB client.** `internal/plugins/tmdb` serves TV and Movies, resolving the key per request (v3 API key or v4 read token). Both manifests always declare TMDB; the key gates the calls, so there is no conditional manifest and no rebuild on save.
- **List UI.** Left sidebar navigation from the enabled plugins (Material Symbols icons, vendored and pinned), a **card view** (cover, title overlay, usable +1, colored status corner) that the list opens with by default, alongside the table view (sortable head, configurable columns); a view toggle in the header; search bar with a collapsible filter panel (filters generated from the manifest, any/all genres and tags with counts, URL-bookmarkable, live htmx updates), compact "Sort by" control, per-plugin **home settings** (default view and status), settings with their own tab column.
- **Per plugin settings** (`/settings/plugins/{id}`): title language (declared languages plus tags found in that plugin's stored titles; fallback order is plugin data via `KindDef.TitleFallback`) and visible table columns with order (`KindDef.Columns`, stored as `plugin.<id>.columns.<kind>`).
- **Appearance** (`/settings/appearance`, package `internal/appearance`): colour tokens as CSS custom properties from a generated, content-versioned `/appearance.css`; `tailwind.config.js` maps every colour class of the templates to a token through `internal/appearance/tailwind-map.json` (templates keep their slate/indigo classes; a test refuses unmapped ones). Presets plus an edited palette, system/bundled/uploaded fonts (table `fonts`, migration 00013, files in `DATA_DIR/fonts`), text size; included in the backup.
- **Imports.** Yamtrack CSV (the shared parser handles anime, TV and movies) and the generic queue/history on Settings > Import; AniList account (by username, with an optional daily/weekly repeat) and MyAnimeList XML export in each of the anime and manga plugins' Import sections; Goodreads CSV and AniList account in the books plugin; Yamtrack TV CSV in the TV plugin and Yamtrack movie CSV in the movies plugin. Each has a rule for existing entries (skip / update when the source is newer / always update). The MAL XML, Goodreads and Yamtrack TV exports have no timestamps, so "newer" never updates there. Import resolution can fall back to a second source on `ErrNotFound`; a source that needs its credential configured (`ErrNotConfigured`) is handled the same way, so the row falls back or stays manual. Rows without an external id can become manual entries.
- **Calendar.** `GET /calendar.ics` serves an iCalendar feed for local subscribers: timed events for the upcoming releases of watching entries, from stored schedules (the optional `ScheduleSource` plugin capability, implemented by the anime plugin via AniList air dates; refreshed by the `sync_schedule` job, at most daily through the feed). Manga has no chapter schedule (AniList has none) and books have no release-schedule source. TV uses TVmaze episode air times; the calendar carries a season and labels a release `Season 2, Episode 5`. TMDB shows have no events because TMDB has no air time.
- **Deferred TV work.** MyDramaList is issue #2; Trakt and TV Time imports are issue #3. Model 3 (book series hierarchy) remains issue #1.
- **Builds.** Everything pinned and checksummed, see `PINS.md`.

## Decisions worth knowing

- Migration 00007 dropped the first version's anime tables without copying (the only prior data was an empty preview). A volume from the first preview build starts empty after upgrading; the README warns about it. Migration 00011 moved the global `title_lang` setting to `plugin.anime.title_lang`. The movies work added no migration: source keys use the existing settings table (`source.<source-id>.key`).
- Source credentials are plugin data only in the sense that a source declares `KeyLabel`/`KeyHelp`; the key itself is a core setting with no manifest involvement. It is read per request, so saving in the UI takes effect without a restart, and the UI never renders it. It is not in the backup or a JSON export.
- Progress combination switching (count/pages/percent) lives in the entry dialog, not in Edit details.
- Source "bad answer" errors map to a 502 page in the web (not retried), a failed row in imports.
- The universal backup contains everything including disabled plugins; there is no per-plugin selection screen (per-plugin export exists).
- The AniList client and the JSON exporter are shared packages (`internal/plugins/anilist`, `internal/plugins/jsonexport`); media-specific mapping stays in each plugin. Relation kinds are normalised (`source`, `adaptation`, `sequel`, `prequel`, `other`) and each plugin derives the related item's kind from the node's media type.
- The core now supports source-kind declarations, importer fallback sources, manual import rows with fields, all-id import dedupe and active-progress-only updates. `GOOGLE_BOOKS_API_KEY` is optional; the Google Books source is quota-limited without it.
- Model 3 (series -> volume child items with aggregate progress) is a later core project, documented in GitHub issue #1.
- Hiding the Progress column also hides the +1 button (documented, noted on the settings page).

## Known follow-ups (none blocking)

- Not built: model 3 (series -> volume child items; issue #1), other media types (games) as plugins, a manga release calendar (AniList has no chapter schedule; a MangaDex source could add one), a Goodreads/StoryGraph/Calibre import beyond the Goodreads CSV, a Google Books key in the API keys card (it is an environment variable), MyAnimeList as a live source (deliberately skipped), other movie sources and imports (Letterboxd, IMDb, Trakt), dashboards, accounts.

## Running and previews

- The owner's containers: 18080 (old first-version preview, own volume), 18081 (project `allister-main`, an older build of `main`), 18082 (project `allister-ui`, rebuilt from the latest `main`). Do not touch them without asking; stop a preview with `docker compose -p <project> down -v` (removes its volume).
- `curl http://localhost:<port>/healthz` answers `ok`.

## Pushing to GitHub

The repo has no remote. The owner pushes themselves:

```
git remote add origin <repo URL>
git push -u origin main
```
