# Using Allister: reference

Details behind the short sections of the [README](../README.md).

## Searching your list

The filter bar above the list searches only your own entries and updates as you type. Everything is in the URL, so a filtered list can be bookmarked. Paging and the status tabs keep the filters, and "Clear filters" removes them. Parameters:

| Parameter | Meaning |
|---|---|
| `q` | Text matched against every stored title in any language (case-insensitive substring). Genres and tags are not searched. |
| `status` | Status tab: `planned`, `active`, `paused`, `completed`, `dropped` (empty: all). |
| `f.type` | Anime type (`TV`, `MOVIE`, `OVA`, ...). Repeat it to match any of several. |
| `f.year.from`, `f.year.to` | Year range (canonical form `f.year=1990..2000`). |
| `f.studio` | Studio contains the text. |
| `genre`, `tag` | One or more genres or tags (repeat the parameter). `genre_mode=all` or `tag_mode=all` requires all of them; the default is any. The filter bar shows counts from your library. |
| `progress` | `not_started`, `in_progress` or `finished`. |
| `rmin`, `rmax` | Score range 0 to 10. `unrated=1` and `zero=1` add unrated entries and entries scored 0 (rows matching any checked score choice are shown). |
| `unmatched=1`, `partial=1` | Only unmatched entries, or only entries whose cast or images are still syncing. |
| `sort`, `dir` | `updated` (recently updated, the default), `title`, `rating`, `year` or `progress`, and `asc` or `desc` (title defaults to ascending, the others to descending). |

Unknown or invalid parameters are ignored, and a notice names them.

## URLs

| URL | Page |
|---|---|
| `/anime` | Your anime list (with the search and filter parameters above) |
| `/anime/search?q=...` | Search AniList |
| `/anime/anilist/{id}` | Overview of an AniList anime; `/anime/anilist/{id}/edit` edits its details |
| `/anime/manual/new`, `/anime/manual/{id}` | Create and open manual entries; `/anime/manual/{id}/edit` edits details, `/anime/manual/{id}/match` matches to AniList |
| `/anime/export/json` | The JSON export |
| `/list/{id}/edit` | The list entry's Edit dialog (status, rating, progress, dates, comment) |
| `/people/{id}` | Person page |
| `/settings`, `/settings/import`, `/settings/backup` | Settings, import, backup and restore |
| `/healthz` | Answers `ok`, for monitoring |

Changes from the first version: list statuses are the core's status classes, so a bookmark like `/anime?status=watching` now opens the All tab with a notice (use `status=active`). The list's search and filter parameters are new. Edit details (`/anime/{source}/{id}/edit`) and the recommendations fragment (`/anime/{source}/{id}/recommendations`, loaded by the overview) are new routes.

## Known limitations

- The Go toolchain is pinned to Go 1.23.12, which is no longer supported upstream (no more security fixes). Moving to a supported Go version is planned as a separate follow-up.
- Only the anime plugin exists. Plugins are compiled in, and there is no marketplace or runtime installation.
- When the chosen title language is missing, the fallback order (Japanese, romaji, English, then any title) is fixed for every plugin.
- "Match to AniList" accepts AniList URLs and numeric ids only.
- A live overview of an anime that is not saved makes two AniList requests (details, then recommendations). Both are cached for 10 minutes.
- The JSON export builds the whole list in memory (fine for a few thousand entries).
- A restored AVIF cover is recognised by the file's main brand only. A rare AVIF file that declares another main brand is restored without its cover.
- Files of removed or re-matched entries are not deleted from `covers/` (see Disk use in the README).
- When AniList answers with unusable data, the search page says "unavailable" instead of naming the cause.
- Only the amd64 image build has been tried. The Debian packages of the CSS build stage are not pinned, and the distroless runtime image is pinned by digest under its `latest` tag.

## Code layout

- `cmd/allister`: wiring and startup
- `internal/web`: HTTP handlers, templates, static assets
- `internal/plugin`: the plugin contract (manifest, source, importer, exporter) and the registry
- `internal/library`: the generic library (items, list entries, people) over SQLite
- `internal/db`: SQLite access and migrations
- `internal/jobs`: background queue and item sync
- `internal/backup`: the universal backup archive (download and merge restore)
- `internal/importer`: the generic import runner (every plugin's importers)
- `internal/plugins/anime`: the anime plugin; `anilist`: its AniList client, `yamtrack`: its Yamtrack CSV parser
- `internal/plugin/plugintest`: test-only book and TV fixture plugins that prove the contract
- `internal/guard`: tests that keep the core free of media-specific code and plugin imports
- `internal/covers`, `internal/settings`, `internal/config`, `internal/ids`: small helpers
