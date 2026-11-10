# Allister

Allister is a self-hosted personal tracker for the things you watch, read and play. A generic core stores your library and draws every page. Each media type is a plugin that you can switch on and off. Anime is the first and, for now, the only plugin. Manga, movies, TV, games and books are planned as further plugins.

It is a single Go binary with a SQLite database, server-rendered pages, htmx and Tailwind. Anime metadata comes from AniList.

## Features

- Search AniList and open an overview: titles in several languages, genres and tags (spoiler tags hidden until asked), related entries, cast, and recommendations that load after the page.
- Your list: status, rating, progress, dates and comment; 100 per page with status tabs, +1, and search and filters in the URL.
- Edit details of any saved entry (titles, fields, genres, tags, cover); AniList never overwrites your edits.
- Character and voice-actor pages, manual entries with cover upload, and "Match to AniList".
- Yamtrack CSV import, JSON export, and a universal backup with restore.
- Plugins you can switch off and on in Settings without losing data; title language setting; covers and photos stored locally.

## Quick start

```
docker compose up -d
```

Open http://localhost:8080. To use another port: `PORT=9000 docker compose up -d`.

All data lives in the named volume `allister-data`, mounted at `/data`. It holds the SQLite database (`allister.db`) and the `covers/` directory. Configuration: `PORT` (default `8080`, the HTTP port) and `DATA_DIR` (default `/data`, the directory for the database and `covers/`).

## Plugins

A plugin adds one media type: what its entries look like (fields, statuses, progress tracks, the blocks of the overview page), where the data comes from (a source such as AniList), and how lists get in and out (importers and exporters). Plugins are data only: they never ship HTML or JavaScript. The core draws every page, runs search, filters, imports, exports and backups, and stores everything in one database. Plugins are compiled into the binary. There is no marketplace or runtime installation yet.

The sidebar on the left (a top bar on phones) has one entry per enabled plugin, with the plugin's icon, and Settings at the bottom. Settings has its own column of sections: General (title language), Plugins, Import and Backup. Settings > Plugins lists the installed plugins with their version, kinds, sources, importers and exporters, plus a Disable or Enable button. Disabling a plugin hides its sidebar entry, pages, person pages, import choices and export links (they answer 404), and pauses its background jobs and imports. All its data stays, and enabling it brings everything back. The home page opens the first enabled plugin's list, or Settings when none is enabled.

The **anime plugin** is on by default. It provides the anime kind (type, episodes, year, studio, synopsis, source, start and end dates, genres and tags), the AniList source (30 requests per minute), the Yamtrack CSV importer and the JSON exporter. Its pages live under `/anime`. To write a plugin, see [docs/plugins.md](docs/plugins.md).

## Title language

Settings > General has a title language: Japanese (default), romaji or English. Further languages (for example German, French, Spanish) appear in the dropdown once a saved entry has a title in them. If the chosen language is missing for an entry, Allister falls back to Japanese, romaji, English, then any title.

## Importing from Yamtrack

1. In Yamtrack, export the general CSV.
2. In Allister, open Settings > Import and upload the file (up to 20 MiB).

Only anime rows with MyAnimeList ids are used. Each id is looked up on AniList at 30 requests per minute, so about 830 entries take about 30 minutes. Cast and cover downloads then continue in the background for another hour or two.

The page shows progress. You can cancel an import and resume it later. Entries AniList cannot match are kept as manual entries marked "unmatched". Importing the same file again skips rows already saved, matched by MyAnimeList id. The one exception is rare: if you matched an unmatched entry to an AniList page whose MyAnimeList id differs from the one in the export, a re-import can bring that row back as a new unmatched entry.

An import made by an older version of Allister cannot be continued after the upgrade: one that was still running is marked failed, and the unfinished rows of one that had failed or was cancelled are marked failed as well, so it can no longer be resumed (each says "created by an older version; upload the file again"). Upload the file again: rows already in your library are skipped (after an upgrade from the first preview build the library starts empty, see Updating and stopping). If you disable the anime plugin while an import is running, the import pauses. The import page says why, and the import continues when you enable the plugin again (or you can cancel it).

## Searching your list

The filter bar above the list searches only your own entries and updates as you type: title text in any language, type, year range, studio, genres and tags (any or all), progress (not started, in progress, finished), score, unmatched and partially synced entries, plus sorting. Everything is in the URL (for example `/anime?q=bebop&f.year.from=1990&sort=title`), so a filtered list can be bookmarked; paging and the status tabs keep it, and "Clear filters" removes it. Every parameter is listed in [docs/usage.md](docs/usage.md#searching-your-list).

## Editing details

Every saved entry (from AniList, manual, imported or unmatched) has an "Edit details" link on its overview. It edits the titles per language, the type and the other fields, genres, tags and the cover (upload a new one or remove it). After saving you return to the overview. Edits are local: nothing from AniList overwrites them later, and the only way to replace the data is "Match to AniList" on a manual entry. Status, rating, progress, dates and comment stay in the list entry's Edit dialog.

## Matching manual entries to AniList

Open an unmatched entry (or any manual entry) and press "Match to AniList". Paste its AniList URL (or just the number), or search by title and pick a result. The entry becomes a normal AniList entry in place: your status, rating, progress, dates and comment stay exactly as they are, and cast and cover arrive in the background. If that AniList anime is already in your library, Allister refuses and links to the existing entry. If AniList is not answering, nothing is changed and you can try again.

## URLs

Anime pages live under `/anime` (list, `/anime/search`, overviews at `/anime/anilist/{id}`, manual entries at `/anime/manual/{id}`, each with `/edit` for Edit details). List entries are edited at `/list/{id}/edit`, people at `/people/{id}`, and Settings under `/settings` (sections `/settings/plugins`, `/settings/import`, `/settings/backup`). A bookmark like `/anime?status=watching` from the first version now opens the All tab with a notice: statuses are the core's classes (`status=active`). The full table is in [docs/usage.md](docs/usage.md#urls).

## Updating and stopping

**Upgrading from the first (pre-plugin) preview build: your library starts empty after this upgrade, because the old anime tables are dropped, not copied. If it holds data you want, make a cold backup of the volume first (see below).** Then, after `git pull`, run `docker compose up -d --build`. `docker compose down` stops and removes the container but keeps the data volume; compose gives the app 30 seconds (`stop_grace_period`) to finish open requests and hand the running background job back to the queue. **`docker compose down -v` deletes the volume and all your data.**

Every image (the build and runtime base images and the helper images in the commands below) is pinned by digest, and the Tailwind CLI, htmx and the vendored Material Symbols icon font are checked against recorded sha256 sums (see `PINS.md`; the font is Apache 2.0, its licence is in `LICENSES/`). A rebuild therefore never picks up a base-image security update by itself: base-image security updates now need running `scripts/update-pins.sh` (it shows the new pins as a diff; `scripts/update-pins.sh --write` applies them), then `docker compose up -d --build`.

## Data and backups

There are two ways out of the app:

- **Per-plugin export**: the "Export JSON" link on the list page (and the plugin's row in Settings > Plugins) downloads all entries of the anime list (status, rating, progress, dates, comment, titles and external ids) as `anime-json-<date>.json`, built in memory (fine for a few thousand entries). It is for using your list elsewhere and cannot be restored.
- **Universal backup** (Settings > Backup): downloads `allister-backup-YYYY-MM-DD.zip` with every item of every installed plugin (also disabled ones), including your edits, people and credits, relations, external ids, genres and tags, your list entries with progress, and every local item cover. A cover's remote URL is not kept once it is downloaded, so the file itself is saved. Settings, plugin choices, jobs, import history and person photos are not included. Photos are fetched again by the background sync after a restore.

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

- Go 1.23.12 is pinned and no longer supported upstream (no more security fixes); the upgrade is a separate follow-up.
- Only the anime plugin exists; plugins are compiled in (no marketplace or runtime installation).
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

`scripts/go.sh` runs the same pinned golang image as the Dockerfile. The exact Go version (1.23.12, out of upstream support; the upgrade is a separate follow-up) is also the `toolchain` line of `go.mod`. `PINS.md` lists every pin and how to update it (`scripts/update-pins.sh`, then `--write`; a new version means editing the tag first). Tests in `internal/guard` and `internal/web` fail when an image is not pinned by digest or a recorded checksum does not match.

The plugin contract is documented in [docs/plugins.md](docs/plugins.md), the code layout in [docs/usage.md](docs/usage.md#code-layout).

## Roadmap

- Other media types as plugins: manga, movies, TV, games, books.
- Upgrade to a supported Go version.
