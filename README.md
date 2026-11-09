# Allister

Allister is a self-hosted personal tracker for the things you watch, read and play. It starts with anime. Manga, movies, TV, games and books are planned.

It is a single Go binary with a SQLite database, server-rendered pages, htmx and Tailwind. Metadata comes from AniList.

## Features

- Search AniList and open an overview: titles in several languages, related entries, cast and recommendations.
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
2. In Allister, open Settings, then Import from Yamtrack, and upload the file.

Only anime rows with MyAnimeList ids are used. Each id is looked up on AniList at 30 requests per minute, so about 830 entries take about 30 minutes. Cast and cover downloads then continue in the background for another hour or two.

The page shows progress. You can cancel an import and resume it later. Entries AniList cannot match are kept as manual entries marked "unmatched". Importing the same file again never creates duplicates.

## Updating and stopping

After `git pull`, run `docker compose up -d --build`. `docker compose down` stops and removes the container but keeps the data volume. **`docker compose down -v` deletes the volume and all your data.**

## Data and backups

Compose prefixes the volume with the project name, usually `<project>_allister-data`. Find the exact name with `docker volume ls | grep allister-data` and use it below as `VOLUME`.

Cold backup (recommended). The archive contains `allister.db` (plus `-wal` and `-shm` if present) and `covers/`:

```
docker compose stop
docker run --rm -v VOLUME:/data -v "$PWD":/backup alpine tar czf /backup/allister-backup.tgz -C /data .
docker compose start
```

To restore, run `docker compose stop`, extract into the volume with `docker run --rm -v VOLUME:/data -v "$PWD":/backup alpine tar xzf /backup/allister-backup.tgz -C /data`, then `docker compose start`.

Live backup (optional, database only). Copying the database files of a running instance is not safe, so use SQLite's `.backup` from a helper container (the Allister image has no `sqlite3`):

```
docker run --rm --user root -v VOLUME:/data -v "$PWD":/backup keinos/sqlite3 sqlite3 /data/allister.db ".backup '/backup/allister.db'"
```

Copy `covers/` separately in that case.

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
- `internal/store`, `internal/db`: SQLite access and migrations
- `internal/provider`: provider interface and the AniList client
- `internal/jobs`: background queue and anime sync
- `internal/importer/yamtrack`: CSV import
- `internal/covers`, `internal/settings`, `internal/titles`, `internal/config`: small helpers

## Roadmap

- Match manual and unmatched entries to AniList (planned, not built yet).
- Other media types: manga, movies, TV, games, books.
