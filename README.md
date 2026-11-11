# Allister

> I'm vibe-coding this mainly for myself and to keep track of my stuff without having to go to x amount of sites. If there's a new thing I can and want to list, I want to be able to add and enable/disable it on the go. Hence the plug-in idea for every type of media. But in case anyone else has similar ideas and tastes to what I built here, please enjoy. All + List + Alistor... I swear I can be creative when I want to be!

Allister is a self-hosted personal tracker for the things you watch, read and play. A generic core stores your library and draws every page. Each media type is a plugin that you can switch on and off. Anime, manga, books, TV and movies are the first plugins; games are planned.

It is a single Go binary with a SQLite database, server-rendered pages, htmx and Tailwind. Anime metadata comes from AniList; TV uses TVmaze and TMDB, movies TMDB.

## Features

- Search AniList and open an overview: titles in several languages, genres and tags (spoiler tags hidden until asked), related entries, cast, and recommendations that load after the page.
- Your list: status, rating, progress, dates and comment; 100 per page with status tabs, +1, and search and filters in the URL.
- Edit details of any saved entry (titles, fields, genres, tags, cover); AniList never overwrites your edits.
- Character and voice-actor pages, manual entries with cover upload, and "Match to AniList".
- Yamtrack, Goodreads and TV imports, AniList light-novel import, JSON export, and a universal backup with restore.
- Plugins you can switch off and on in Settings without losing data; per-plugin title language and list columns; covers and photos stored locally.
- Appearance: colour presets (light, dark, high contrast or following your system) or every colour by hand, system, bundled or your own fonts, and a text size.

## Quick start

```
docker compose up -d
```

Open http://localhost:8080. To use another port: `PORT=9000 docker compose up -d`.

All data lives in the named volume `allister-data`, mounted at `/data`. It holds the SQLite database (`allister.db`) and the `covers/` and `fonts/` (uploaded fonts) directories. Configuration: `PORT` (default `8080`, the HTTP port), `DATA_DIR` (default `/data`, the database, `covers/` and `fonts/`) and the optional `GOOGLE_BOOKS_API_KEY` for books and `TMDB_API_KEY` for TV and movies. A TMDB key saved in Settings > Plugins wins over `TMDB_API_KEY`.

## Plugins

A plugin adds one media type: what its entries look like (fields, statuses, progress tracks, the blocks of the overview page), where the data comes from (a source such as AniList), and how lists get in and out (importers and exporters). Plugins are data only: they never ship HTML or JavaScript. The core draws every page, runs search, filters, imports, exports and backups, and stores everything in one database. Plugins are compiled into the binary. There is no marketplace or runtime installation yet.

The sidebar on the left (a top bar on phones) has one entry per enabled plugin, with the plugin's icon, and Settings at the bottom. Settings has its own column of sections: General, Appearance, Plugins, Import and Backup. Settings > Appearance (`/settings/appearance`) sets the colours (a preset, or every colour in Advanced with contrast warnings), the app font (system stacks, the bundled Inter, Source Serif 4 and JetBrains Mono, or up to 8 fonts you upload: woff2, woff, ttf or otf, 2 MiB each) and the text size, for the whole app; details in [docs/usage.md](docs/usage.md#appearance). Settings > Plugins has an **API keys** card for sources that need one (today TMDB, shared by TV and movies): save a key, save a new one to replace it, or remove it; a stored key is never shown again. It also lists the installed plugins with their version, kinds, sources, importers and exporters, a Settings link (the plugin's title language and list columns) and a Disable or Enable button. Disabling a plugin hides its sidebar entry, pages, person pages, import choices and export links (they answer 404), and pauses its background jobs and imports. All its data stays, and enabling it brings everything back. The home page opens the first enabled plugin's list, or Settings > Plugins when none is enabled.

The **anime plugin** is on by default. It provides the anime kind (type, episodes, year, studio, synopsis, source, start and end dates, genres and tags), the AniList source (30 requests per minute), the Yamtrack CSV importer and the JSON exporter. Its pages live under `/anime`. The **manga plugin** is off until you enable it in Settings > Plugins: it provides the manga kind (type, chapters, volumes, year, author, synopsis, dates, genres and tags), the same AniList source, AniList-account and MyAnimeList-XML imports, and its JSON export; its pages live under `/manga`, and it has no calendar because AniList has no chapter release dates. The **books plugin** is off until you enable it: it provides the book kind (author, pages, year, publisher, ISBN, series, synopsis, dates, genres and tags) and the light-novel series kind (volume and chapter progress), with Open Library, Google Books and AniList sources, Goodreads CSV and AniList-account imports and JSON export; its pages live under `/book`. The **TV plugin** is off until you enable it: one Show kind with season, episode and playback-position progress, TVmaze plus TMDB (which needs a key), Yamtrack TV CSV import, JSON export and calendar events from TVmaze; its pages live under `/tv`. The **movies plugin** is off until you enable it: one Movie kind (Want to watch, Watching, Paused, Seen and Dropped, with an optional playback position, director, studio, country, language, collection and runtime), the TMDB source, the Yamtrack movie CSV import and JSON export, and no calendar; its pages live under `/movie`. To write a plugin, see [docs/plugins.md](docs/plugins.md).

## Title language and columns

Each plugin has its own settings page (Settings > Plugins > Settings, `/settings/plugins/anime` for anime). **Title language**: the plugin's languages (anime: Japanese, the default, romaji or English), plus any other language a saved entry of that plugin has a title in; if an entry lacks it, the plugin's fallback order is used (anime: Japanese, romaji, English), then any title. It applies wherever that plugin's titles appear; list search still matches every language. **Table columns**: tick the list columns to show, order them with the arrows, or "Reset to defaults" (anime: Type, Year, Progress, Score, Status, Updated; Episodes, Studio, Source, Started and Ended can be added). The title is always shown. After an upgrade your earlier title language is kept for anime. Details in [docs/usage.md](docs/usage.md#plugin-settings).

## Importing from Yamtrack

1. In Yamtrack, export the general CSV.
2. In Allister, open Settings > Import and upload the file (up to 20 MiB).

The anime import uses only anime rows with MyAnimeList ids. Each id is looked up on AniList at 30 requests per minute, so about 830 entries take about 30 minutes. Cast and cover downloads then continue in the background for another hour or two.

The page shows progress. You can cancel an import and resume it later. Entries AniList cannot match are kept as manual entries marked "unmatched". Importing the same file again skips rows already saved, matched by MyAnimeList id. The one exception is rare: if you matched an unmatched entry to an AniList page whose MyAnimeList id differs from the one in the export, a re-import can bring that row back as a new unmatched entry.

An import made by an older version of Allister cannot be continued after the upgrade: one that was still running is marked failed, and the unfinished rows of one that had failed or was cancelled are marked failed as well, so it can no longer be resumed (each says "created by an older version; upload the file again"). Upload the file again: rows already in your library are skipped (after an upgrade from the first preview build the library starts empty, see Updating and stopping). If you disable the anime plugin while an import is running, the import pauses. The import page says why, and the import continues when you enable the plugin again (or you can cancel it).

## Searching your list

Above the list is a search bar for title text in any language. The Filters button next to it opens a panel with everything else: type, year range, studio, genres and tags (any or all), progress (not started, in progress, finished), score, unmatched and partially synced entries; its badge counts the filters that are set, and the panel opens by itself when you load a list with such filters (a bookmark, for example). Search and filters cover only your own entries and update the list as you type. Click a column heading (Title, Year, Progress, Score, Updated) to sort by it; click it again to reverse the order. On smaller screens the Year column (below 768 px wide) and the Updated column (below 1024 px) are hidden, so the Filters panel there also has a Sort by control (every sort key and the direction, also of columns you switched off). Everything is in the URL (for example `/anime?q=bebop&f.year.from=1990&sort=title`), so a filtered list can be bookmarked; paging and the status tabs keep it, and "Clear filters" removes it. Every parameter is listed in [docs/usage.md](docs/usage.md#searching-your-list).

## Editing details

Every saved entry (from AniList, manual, imported or unmatched) has an "Edit details" link on its overview. It edits the titles per language, the type and the other fields, genres, tags and the cover (upload a new one or remove it). After saving you return to the overview. Edits are local: nothing from AniList overwrites them later, and the only way to replace the data is "Match to AniList" on a manual entry. Status, rating, progress, dates and comment stay in the list entry's Edit dialog.

## Matching manual entries to AniList

Open an unmatched entry (or any manual entry) and press "Match to AniList". Paste its AniList URL (or just the number), or search by title and pick a result. The entry becomes a normal AniList entry in place: your status, rating, progress, dates and comment stay exactly as they are, and cast and cover arrive in the background. If that AniList item is already in your library, Allister refuses and links to the existing entry. If AniList is not answering, nothing is changed and you can try again.

## URLs

Anime pages live under `/anime` (list, `/anime/search`, overviews at `/anime/anilist/{id}`, manual entries at `/anime/manual/{id}`, each with `/edit` for Edit details); manga pages use `/manga` the same way, books pages use `/book`, TV pages use `/tv`, and movie pages use `/movie`. List entries are edited at `/list/{id}/edit`, people at `/people/{id}`, and Settings under `/settings` (sections `/settings/plugins`, each plugin's `/settings/plugins/{id}`, `/settings/import`, `/settings/backup`). A bookmark like `/anime?status=watching` from the first version now opens the All tab with a notice: statuses are the core's classes (`status=active`). The full table is in [docs/usage.md](docs/usage.md#urls).

## Updating and stopping

**Upgrading from the first (pre-plugin) preview build: your library starts empty after this upgrade, because the old anime tables are dropped, not copied. If it holds data you want, make a cold backup of the volume first (see below).** Then, after `git pull`, run `docker compose up -d --build`. `docker compose down` stops and removes the container but keeps the data volume; compose gives the app 30 seconds (`stop_grace_period`) to finish open requests and hand the running background job back to the queue. **`docker compose down -v` deletes the volume and all your data.**

Every image (the build and runtime base images and the helper images in the commands below) is pinned by digest, and the Tailwind CLI, htmx, the vendored Material Symbols icon font and the bundled app fonts are checked against recorded sha256 sums (see `PINS.md`; the icon font is Apache 2.0, the app fonts OFL 1.1, their licences are in `LICENSES/` and in the image under `/licenses`). A rebuild therefore never picks up a base-image security update by itself: base-image security updates now need running `scripts/update-pins.sh` (it shows the new pins as a diff; `scripts/update-pins.sh --write` applies them), then `docker compose up -d --build`.

## Data and backups

There are two ways out of the app:

- **Per-plugin export**: the "Export JSON" link on the list page (and the plugin's row in Settings > Plugins) downloads all entries of the anime list (status, rating, progress, dates, comment, titles and external ids) as `anime-json-<date>.json`, built in memory (fine for a few thousand entries). It is for using your list elsewhere and cannot be restored.
- **Universal backup** (Settings > Backup): downloads `allister-backup-YYYY-MM-DD.zip` with every item of every installed plugin (also disabled ones), including your edits, people and credits, relations, external ids, genres and tags, your list entries with progress, and every local item cover. A cover's remote URL is not kept once it is downloaded, so the file itself is saved. Your appearance settings and uploaded fonts are included (a restore adds the fonts and applies the settings only when the installation has none of its own). Other settings, plugin choices, jobs, import history and person photos are not included. Photos are fetched again by the background sync after a restore.

To restore, upload the zip on the same page (up to 512 MiB). Items you do not have yet are added, items you already have (same plugin, source and id) are skipped, and nothing is overwritten or deleted, so restoring the same file twice is harmless. A backup from a newer Allister is refused. Items of a plugin that is not installed are counted and skipped. A cover larger than 10 MiB in the archive is restored without its cover. For a full copy of the data volume, use the commands below.

Compose prefixes the volume with the project name. Replace `<volume>` in every command below with the exact name shown by `docker volume ls | grep allister-data` (a wrong name silently creates an empty volume and the backup is empty).

Cold backup (recommended):

```
docker compose stop
docker run --rm -v <volume>:/data -v "$PWD":/backup alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf tar czf /backup/allister-backup.tgz -C /data .
tar tzf allister-backup.tgz
docker compose start
```

The `tar tzf` check must list `allister.db` and `covers/` (plus `-wal` and `-shm` if present). If it does not, the volume name was wrong.

Restore: extracting overwrites `allister.db` and `covers/` in the volume. Stop the container, remove the old database files so a stale WAL is not replayed against the restored database, extract, then start:

```
docker compose stop
docker run --rm -v <volume>:/data alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf rm -f /data/allister.db /data/allister.db-wal /data/allister.db-shm
docker run --rm -v <volume>:/data -v "$PWD":/backup alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf tar xzf /backup/allister-backup.tgz -C /data
docker compose start
```

Live backup (optional). Copying the database files of a running instance is not safe, so use SQLite's `.backup` from a helper container (the Allister image has no `sqlite3`). `--user root` is needed because the data files belong to root (the app runs as root in its image) while the sqlite image runs as another user; the backup file in `$PWD` is therefore owned by root. Do not mount the volume read-only for the sqlite helper (SQLite needs the `-shm` file). The covers command below only reads, so `:ro` is fine there.

```
docker run --rm --user root -v <volume>:/data -v "$PWD":/backup keinos/sqlite3:3.54.1@sha256:b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17 sqlite3 /data/allister.db ".backup '/backup/allister-$(date +%F).db'"
docker run --rm -v <volume>:/data:ro -v "$PWD":/backup alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf tar czf /backup/allister-covers.tgz -C /data covers
```

This saves the database and the covers separately. The database file is named with today's date; if a file of the same name already exists, it is overwritten.

## Disk use

Covers and person images are stored locally in `covers/` inside the data volume. Measured on AniList: a cover is about 40 KB (the larger size), a person photo about 21 KB (the `medium` size Allister requests; `large` would be about 87 KB), and an anime credits up to about 50 people (25 characters with one voice actor each). That is roughly 1 MB of person photos plus the cover per anime; voice actors and characters shared between anime are downloaded once. A single downloaded image is limited to 2 MiB. Files of removed or re-matched entries are not deleted automatically, and the app does not prune them; if you ever need the space, you can clean up `covers/` by hand.

## Known limitations

- Only the anime, manga, books, TV and movies plugins exist; plugins are compiled in (no marketplace or runtime installation).
- Files of removed or re-matched entries are not deleted from `covers/`.
- More in [docs/usage.md](docs/usage.md#known-limitations).

## Security

Allister has no accounts or login, by design. Run it on a private network or behind a reverse proxy that does authentication. Do not expose it to the internet.

## Rate limits

AniList limits API requests, currently to about 30 per minute. Allister paces itself: background jobs run one at a time with a pause between them, and imports are throttled. Browsing may show a temporary error if AniList is rate limiting you.

## Development

Go is run in Docker, so you do not need it installed:

```
scripts/go.sh test ./...
scripts/go.sh vet ./...
```

The Tailwind CSS is built inside the Docker image (`docker compose up --build`); `app.css` is not committed.

`scripts/go.sh` runs the same pinned golang image as the Dockerfile. The exact Go version (1.27.3) is also the `toolchain` line of `go.mod`. `PINS.md` lists every pin and how to update it (`scripts/update-pins.sh`, then `--write`; a new version means editing the tag first). Tests in `internal/guard` and `internal/web` fail when an image is not pinned by digest or a recorded checksum does not match.

The plugin contract is documented in [docs/plugins.md](docs/plugins.md), the code layout in [docs/usage.md](docs/usage.md#code-layout).

## Roadmap

- Other media types as plugins: games.
- Upgrade to a supported Go version.
