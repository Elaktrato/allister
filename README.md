# Allister

Allister is a self-hosted personal tracker for the things you watch, read and play. It starts with anime. Manga, movies, TV, games and books are planned.

It is a single Go binary with a SQLite database, server-rendered pages, htmx and Tailwind. Metadata comes from AniList.

## Features

- Search AniList and open an overview: titles in several languages, related entries, cast and recommendations (loaded after the page, so a slow AniList never delays it).
- Add to your list with status, rating, progress, start and finish dates and a comment.
- Paginated list with status tabs and a +1 episode button.
- Character and voice-actor pages that link to the anime in your own list.
- Manual entries for anime AniList does not have, including cover upload.
- Import from a Yamtrack CSV export, with a progress page.
- Title language setting.
- Covers and person images are stored locally.

## Quick start

```
docker compose up -d
```

Open http://localhost:8080. To use another port: `PORT=9000 docker compose up -d`.

All data lives in the named volume `allister-data`, mounted at `/data`. It holds the SQLite database (`allister.db`) and the `covers/` directory.

## Configuration

| Variable   | Default | Meaning                                  |
|------------|---------|------------------------------------------|
| `PORT`     | `8080`  | HTTP port the server listens on          |
| `DATA_DIR` | `/data` | Directory for the database and `covers/` |

## Title language

Settings has a title language: Japanese (default), romaji or English. Further languages (for example German, French, Spanish) appear in the dropdown once a saved entry has a title in them. If the chosen language is missing for an entry, Allister falls back to Japanese, romaji, English, then any title.

## Importing from Yamtrack

1. In Yamtrack, export the general CSV.
2. In Allister, open Settings, then Import from Yamtrack CSV, and upload the file (up to 20 MiB).

Only anime rows with MyAnimeList ids are used. Each id is looked up on AniList at 30 requests per minute, so about 830 entries take about 30 minutes. Cast and cover downloads then continue in the background for another hour or two.

The page shows progress. You can cancel an import and resume it later. Entries AniList cannot match are kept as manual entries marked "unmatched". Importing the same file again skips rows already saved, matched by MyAnimeList id. The one exception is rare: if you matched an unmatched entry to an AniList page whose MyAnimeList id differs from the one in the export, a re-import can bring that row back as a new unmatched entry.

An import made by an older version of Allister cannot be continued after the upgrade: one that was still running is marked failed, and the unfinished rows of one that had failed or was cancelled are marked failed as well, so it can no longer be resumed (each says "created by an older version; upload the file again"). Upload the file again: already saved rows are skipped.

## Exporting

The list page (and the plugin's row in Settings) has an "Export JSON" link: it downloads all entries of the anime list (status, rating, progress, dates, comment, titles and external ids) as `anime-json-<date>.json`. The whole list is built in memory before it is written, which is fine for a few thousand entries.

## Matching manual entries to AniList

Open an unmatched entry (or any manual entry) and press "Match to AniList". Paste its AniList URL (or just the number), or search by title and pick a result. The entry becomes a normal AniList entry in place: your status, rating, progress, dates and comment stay exactly as they are, and cast and cover arrive in the background. If that AniList anime is already in your library, Allister refuses and links to the existing entry. If AniList is not answering, nothing is changed and you can try again.

## Updating and stopping

After `git pull`, run `docker compose up -d --build`. `docker compose down` stops and removes the container but keeps the data volume. **`docker compose down -v` deletes the volume and all your data.**

## Data and backups

Backup from the app: Settings > Backup downloads `allister-backup-YYYY-MM-DD.zip` with every item of every plugin (also disabled ones), your list entries with progress, and every local item cover (a cover's remote URL is not kept once it is downloaded, so the file itself is saved). Settings, plugin choices, jobs, import history and person photos are not included; photos are fetched again by the background sync. To restore, upload the zip on the same page: items you do not have yet are added, items you already have are skipped, nothing is overwritten or deleted, and a backup from a newer Allister is refused. For a full copy of the data volume, use the commands below.

Compose prefixes the volume with the project name. Replace `<volume>` in every command below with the exact name shown by `docker volume ls | grep allister-data` (a wrong name silently creates an empty volume and the backup is empty).

Cold backup (recommended):

```
docker compose stop
docker run --rm -v <volume>:/data -v "$PWD":/backup alpine tar czf /backup/allister-backup.tgz -C /data .
tar tzf allister-backup.tgz
docker compose start
```

The `tar tzf` check must list `allister.db` and `covers/` (plus `-wal` and `-shm` if present). If it does not, the volume name was wrong.

Restore: extracting overwrites `allister.db` and `covers/` in the volume. Stop the container, remove the old database files so a stale WAL is not replayed against the restored database, extract, then start:

```
docker compose stop
docker run --rm -v <volume>:/data alpine rm -f /data/allister.db /data/allister.db-wal /data/allister.db-shm
docker run --rm -v <volume>:/data -v "$PWD":/backup alpine tar xzf /backup/allister-backup.tgz -C /data
docker compose start
```

Live backup (optional). Copying the database files of a running instance is not safe, so use SQLite's `.backup` from a helper container (the Allister image has no `sqlite3`). `--user root` is needed because the data files belong to root (the app runs as root in its image) while the sqlite image runs as another user; the backup file in `$PWD` is therefore owned by root. Do not mount the volume read-only for the sqlite helper (SQLite needs the `-shm` file). The covers command below only reads, so `:ro` is fine there.

```
docker run --rm --user root -v <volume>:/data -v "$PWD":/backup keinos/sqlite3:3.54.1 sqlite3 /data/allister.db ".backup '/backup/allister-$(date +%F).db'"
docker run --rm -v <volume>:/data:ro -v "$PWD":/backup alpine tar czf /backup/allister-covers.tgz -C /data covers
```

This saves the database and the covers separately. The database file is named with today's date; if a file of the same name already exists, it is overwritten.

## Disk use

Covers and person images are stored locally in `covers/` inside the data volume. Measured on AniList: a cover is about 40 KB (the larger size), a person photo about 21 KB (the `medium` size Allister requests; `large` would be about 87 KB), and an anime credits up to about 50 people (25 characters with one voice actor each). That is roughly 1 MB of person photos plus the cover per anime; voice actors and characters shared between anime are downloaded once. A single downloaded image is limited to 2 MiB. Files of removed or re-matched entries are not deleted automatically, and the app does not prune them; if you ever need the space, you can clean up `covers/` by hand.

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

Layout:

- `cmd/allister`: wiring and startup
- `internal/web`: HTTP handlers, templates, static assets
- `internal/plugin`: the plugin contract (manifest, source, importer, exporter) and the registry
- `internal/library`: the generic library (items, list entries, people) over SQLite
- `internal/db`: SQLite access and migrations
- `internal/jobs`: background queue and item sync
- `internal/backup`: the universal backup archive (download and merge restore)
- `internal/importer`: the generic import runner (every plugin's importers)
- `internal/plugins/anime`: the anime plugin; `anilist`: its AniList client, `yamtrack`: its Yamtrack CSV parser
- `internal/guard`: tests that keep the core free of media-specific code and plugin imports
- `internal/covers`, `internal/settings`, `internal/config`, `internal/ids`: small helpers

## Roadmap

- Other media types: manga, movies, TV, games, books.
