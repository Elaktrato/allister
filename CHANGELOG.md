# Changelog

All notable changes to Allister. Versions follow [semantic versioning](https://semver.org) (`vMAJOR.MINOR.PATCH`, release candidates `-rc.N`); while the major version is 0, a minor release may break compatibility.

How a release is cut: move the entries under "Unreleased" to a new `## vX.Y.Z - YYYY-MM-DD` section on `main`, run `scripts/release-check.sh vX.Y.Z`, then push an annotated tag (`git tag -a vX.Y.Z -m "Allister vX.Y.Z"`, `git push origin vX.Y.Z`). The Release workflow publishes the image and the GitHub release; its notes list every commit since the previous tag, grouped by Conventional Commit prefix. Details: [docs/ci.md](docs/ci.md#releases).

## Unreleased

### Added

- **Run timeline.** Each run in an entry's history has a collapsed, read-only timeline of its logged changes (progress steps and jumps, status and rating changes) with times in your time zone; imported and restored changes are marked. Plugins can declare an optional plural track label (`label_plural`).

## v0.3.0 - 2026-11-15

### Added

- **Series groups in every list.** Make any entry a series, add parts from your list or straight from a search (or as a manual entry), reorder them, take parts out, rename or delete the series (keeping or removing its entries). A **Series** tab shows each series with progress over its parts ("3/7"), a status derived from them, and expand/collapse; the other tabs mark parts with "2 · <series>". Books suggest the series name and place new parts by the series number. Series are kept in backups and the JSON export.
- **IMDb CSV import** in Movies and TV: the ratings and watchlist exports (English or German title types). Rated titles become Seen/Watched with their rating and date, watchlist titles Want to watch; episodes and games are ignored. Matched by IMDb id through TMDB (movies) and TVmaze, then TMDB (TV).
- **Letterboxd export import** in Movies: upload the ZIP from Letterboxd. Watched films, diary dates and rewatches, ratings, reviews and the watchlist are merged per film and matched on TMDB by exact title and year; anything ambiguous stays a manual entry you can match.
- **Google Books API key** in Settings > Plugins > API keys (optional; `GOOGLE_BOOKS_API_KEY` still works as a fallback).
- **Magazine** field for manga, filled from the official MyAnimeList API when a MyAnimeList Client ID is set (data courtesy of MyAnimeList).
- Imports can match rows without an id by an exact title and year search (`resolve_by_title` in the plugin contract); sources can declare an optional key (`key_optional`).

### Changed

- The books plugin's light-novel kind is labelled **Light novels** (only the label; entries, links and backups are unchanged).

### Fixed

- A refused Google Books key is reported as a key problem instead of an unusable answer or endless retries.
- A failed attach after adding into a series lands on the new entry with a notice instead of an error page.
- Restore notes name the series parts that were left out and why.

## v0.2.0 - 2026-11-15

### Added

- Yamtrack Manga CSV import in the manga plugin: the general Yamtrack export's manga rows resolve through AniList by MyAnimeList id; light novels stay in Books > Series.
- `GET /version` (JSON: version, commit, build date, Go version) and the version in Settings > General; the Docker build takes `VERSION`, `COMMIT` and `DATE` build args.
- GitHub Actions: lint (golangci-lint, gofmt, go mod tidy, go vet), govulncheck, tests with coverage, hadolint, actionlint, shellcheck, gitleaks, an image smoke test and Trivy scan, CodeQL, dependency review, OpenSSF Scorecard, mutation testing on `main` and `dev`, Dependabot, and a release workflow (multi-architecture image on GHCR with provenance, SBOM and cosign signature; binaries, SBOM and signed checksums on the GitHub release).
- `scripts/ci.sh` runs every check locally with the same pinned tools; `scripts/release-check.sh` checks a release before tagging.

### Changed

- Go 1.27.4 (GO-2026-6859, an HTTP/1 server crash in `net/http`); goose 3.29.0 and modernc SQLite 1.60.1.
- Opening an item that is not in your list by an alias (an IGDB slug such as `/game/igdb/celeste`) redirects to the source's canonical id, and adding it saves that id, so the same game is never added twice.
- Steam store refusals (401/403) are reported as the store being unavailable, not as a refused key; a bogus Steam appid is "not found" even without a key.
- A source credential that cannot be read answers with an error page instead of treating the source as unconfigured; all fields of a credential are saved together.
- The Docker build compiles on the build machine's platform and cross-compiles for the target, so linux/arm64 images build without emulation.
- "version" is a reserved plugin id (the `/version` route).

### Fixed

- Yamtrack imports (games, novels, manga): a row with an unknown status no longer hides a later valid row of the same title; game rows from `igdb` and `IGDB` are one game; long playtimes (over about 1,667 hours) are accepted.
- A match whose source answers with an unusable id is refused instead of saving an item whose page cannot be opened.
- Removing one source's keys no longer removes the keys of another source whose id starts with the same name.
- An over-long IGDB slug is refused instead of being cut to another game's slug.
- Steam games without an appid are reported as skipped rows.
- Request paths and request values are logged escaped, so an encoded newline cannot forge log lines.
- Game fields and recommendations: white-space-only text is dropped, recommendation links are https only.

## v0.1.0 - 2026-11-12

The first release, tagged by hand: the generic core with six media plugins (anime, manga, books, TV, movies, games), statistics, the calendar, appearance settings, the imports (Yamtrack, Yamtrack novel, Goodreads, AniList account, MyAnimeList XML, Steam account), JSON export and the universal backup with restore. See the GitHub release for the details.
