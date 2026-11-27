# Changelog

All notable changes to Allister. Versions follow [semantic versioning](https://semver.org) (`vMAJOR.MINOR.PATCH`, release candidates `-rc.N`); while the major version is 0, a minor release may break compatibility.

How a release is cut: move the entries under "Unreleased" to a new `## vX.Y.Z - YYYY-MM-DD` section on `main`, run `scripts/release-check.sh vX.Y.Z`, then push an annotated tag (`git tag -a vX.Y.Z -m "Allister vX.Y.Z"`, `git push origin vX.Y.Z`). The Release workflow publishes the image and the GitHub release; its notes list every commit since the previous tag, grouped by Conventional Commit prefix. Details: [docs/ci.md](docs/ci.md#releases).

## Unreleased

## v0.7.0 - 2026-11-27

### Added

- **Release status filter.** Anime, manga, light novels (AniList) and games (IGDB) store the title's own release status (finished, releasing, upcoming, hiatus, cancelled for AniList; released, early access, upcoming, cancelled, delisted, other for IGDB) and filter by it (`f.release_status`).
- **More AniList data for anime, manga and light novels.** Season (anime), country of origin and adult flag as filters; the community score and popularity shown on the item page and sortable (the community score never mixes with your own score); alternative titles, which the list search finds but which never become the displayed title; streaming links in the Links section; and all animation studios of an anime.
- **Banner and trailer.** The item page shows AniList's banner as a wide image and the trailer as a thumbnail with a play button that opens the video site (YouTube or Dailymotion) in a new tab. Both images are downloaded into the local image store like covers: nothing is loaded from a third-party host.
- **Hide adult items.** A per-plugin setting (Settings > Plugins > the plugin, `plugin.<id>.hide_adult`, off by default) leaves adult items out of the lists, the home page, the random pick, the calendar, Related and Recommendations and the search results. Their pages still open, and statistics still count them.
- **Update from source for a whole plugin.** The plugin's settings page asks the source about every sourced item in the background and fills only empty values and missing list entries; nothing you set is changed. Statistics you cannot edit (community score and popularity) are refreshed to the source's current value. It runs at the source's request rate without holding up other background work, shows its progress and result, stops at once when the source's credential is missing, and can be run again at any time. The single item's comparison page lists the new data as well.
- **Plugin authors:** `KindDef.alt_titles`, `banner`, `trailer` and `adult_field`, the reserved title language `alt`, and `ItemData.banner_url`, `trailer` and `links` (migration 00023); see [docs/plugins.md](docs/plugins.md).

### Changed

- **Studio is a list of animation studios.** An anime's studio field is now a list of all its animation studios (producers and licensors are left out) and its filter matches a whole studio name, like genres, instead of "contains". A stored single studio becomes a one-element list.
- **Backups are format 5** (banners, trailer thumbnails, trailers and links). Allister v0.6.0 and older refuse such a backup instead of silently dropping that data; formats 1 to 5 still restore, and a text value an older backup holds in a field that is now a list restores as a one-element list. (Breaking for downgrades.)

## v0.6.0 - 2026-11-25

### Added

- **Lists load more while scrolling.** The list pages (table and cards, and the Series tab) no longer stop after 100 entries with Previous and Next: the next 100 load when you scroll to the end, the address bar keeps only the filters, and bulk edit's page select-all becomes "Select all loaded". Settings > General > Lists switches back to **Split into pages** (setting `list_paging`, carried by the backup and restored only into an installation without its own). Without JavaScript a "Load more" link opens the next page.
- **Series with their own data and spin-offs.** A series has its own cover (upload or https URL, downloaded into the cover store), description, start and end day, a finished marker, the expected number of parts and plugin-declared fields (books: Volumes, Chapters, Author), plus an optional linked entry and a parent series for spin-off series (one level). Parts can be marked spin-off: they are numbered separately and not counted towards the series' progress. With expected parts set, 5 of 12 stays in progress. Removing the last part keeps a series that holds its own data. Backups carry all of it (series covers are archived) and are now format 4: older Allister versions (v0.5.0 and earlier) refuse them instead of dropping series data, formats 1 to 3 still restore (migration 00021).
- **Idle games pause on import.** The Steam account import reads each game's last play: a game not played for longer than a cutoff (Games settings, "Pause entries without activity for … days", default 30, 0 = off) is imported as, or moved to, Paused, and goes back to Playing once you play it again. While the rule is on (cutoff above 0), Played and Dropped games keep their status. "Update when the source is newer" now compares against the last play, so in that mode a re-import with nothing new leaves your edits alone (a game you set back to Playing yourself is paused again only once your change is older than the cutoff too); "Always update" still overwrites the status of the other games. Plugin authors: `ParsedEntry.LastActive` and `ImporterInfo.IdlePause` opt an account importer into this rule.
- **Ratings on cards.** List cards and the home page's cards show the entry's score ("★ 8") when it is rated; 0 is a rating and shows, an unrated entry shows nothing.
- **Clearer progress on cards.** When an entry follows several tracks, a card now separates them with " · " ("Season 2 · Episode 3 / 10"), and a secondary track you have not set yet reads "–" instead of 0 on cards and in the table. With manga now tracking volumes, manga cards read "Chapter 45 / 120 · Volume 5 / 12".

### Changed

- **Manga tracks Chapter + Volume.** Every manga follows chapters and volumes: +1 still adds a chapter, and a volume typed in the Edit dialog is saved (before, it was dropped while only chapters were active). Existing manga that tracked chapters only switch once at the first start (migration 00022 records it); a volume stored before shows again, and nothing is logged as activity. Backups record the switch (`progress_upgrades`), so restoring an older backup, also into a fresh install, gives its manga Chapter + Volume too. Chapter only and Volume only stay available in the Edit dialog.
- **Plugin authors: progress upgrades.** A kind can declare `progress.upgrades` (`{id, from, to}`) to move existing items from one allowed combination to a larger one once per installation; see [docs/plugins.md](docs/plugins.md).

### Fixed

- **Bulk edit of many ticked entries** no longer ends on a bare "request body too large" page: the bulk form takes far more entries, and beyond that answers with the form and a hint to use "Select all N matching".
- Cover downloads refuse a URL with userinfo (`user@host`), whitespace or a line break before making any request.
- **Plugin authors: `group_key` now needs a `primary_key`.** A manifest that sets `group_key` without a primary progress track is refused at load (it had no defined meaning). No bundled plugin is affected.
- **Calendar plugin select keeps keyboard focus** after you change it, and the grid no longer nests a second copy of itself in the page.
- **No empty `plugin=` in the calendar address** after choosing "All plugins".
- **Mark watched is no longer offered when it cannot change anything**, for example when your progress is already at the total and a stored release is numbered above it.
- **Clearer entry dialog for a still airing item** whose shown run is an earlier one: it says there is nothing to answer instead of asking to correct the current run with no button for it.
- **Pick filter links:** Clear filters also clears the length filter, and the view, sort, status and page links of a list keep it.

## v0.5.0 - 2026-11-17

### Added

- **Up next on the home page.** The home page's active cards (formerly In progress) name the next unit to watch, read or play (for example the next episode or page) and show "N aired" when stored schedules know of releases beyond your progress. The plugin lists' cards are unchanged.
- **Mark watched on the calendar.** Releases that have aired and lie beyond your progress get a check button in the month and week view that sets your progress to that release (season and episode where the item has both). It never lowers progress within a season (a new season restarts the unit count, and a season you never set counts as the first), a double click changes nothing, and it counts as activity in the statistics.
- **Calendar week view and plugin filter.** The calendar has a Month/Week toggle (the week starts on Monday, in your time zone) and a plugin select that narrows either view to one plugin.
- **No repeat run while airing.** An item with upcoming releases cannot be started again (button and "<Action> instead?" are hidden with a note; the request answers 422), so airings always belong to the first run. It can be started again once it has finished airing.
- **Pick one.** A button on each list draws one entry at random from the matching planned entries (or the status you filtered): title, cover, length and genres, with Roll again and Start. Games, movies, TV and anime also filter by length (for example under 90 minutes, or under 13 episodes); entries without that data are left out. The same filters as the list apply.
- **Update from source shows a comparison.** On a saved item, Update from source asks the source again and lists only what differs (each field, and titles, genres, tags, cover, relations and credits as a whole), with rows you have left empty ticked; it writes only the rows you tick. A blank source value never clears yours.
- **Bulk edit in every list.** Tick rows or cards (or the whole page, or every entry the current filter matches) and set their status, add them to a custom list or a series, add or remove own tags, or remove them from your library in one step. A confirmation names the count first and asks again if it changed; afterwards the list says how many changed and how many were skipped. Completed fills an empty finish date and the known totals.
- **Duplicates and merge.** Each plugin's list links to its possible duplicates (items sharing an id such as an ISBN, or the same title and year) when there are any; hide a pair with "Not a duplicate". "Compare & merge" there, or "Merge with…" on an item page, shows both items side by side: tick which details to take from the other item, then merge. Runs, notes, own tags, custom lists and the series place move to the item you keep, the other is removed, and adding or importing it again finds the kept item. Backups keep the merged identities and the hidden pairs. Backups are now format 3: older Allister versions refuse them, older backups (formats 1 and 2) still restore.

### Changed

- **Round checkboxes.** Every checkbox (bulk selection on cards and rows, filters, settings) is now a round ring in the theme's colours whose checkmark sticks out past the ring when ticked; it follows the accent colour, keeps a visible focus ring, honours reduced motion and forced colours.
- **Update from source no longer fills empty fields silently**: it always shows the comparison first and changes only what you tick.
- **Bulk changes do not count on the activity heatmap.** They appear in each run's timeline but, like imports and restores, are not counted as your activity in the statistics heatmap.

## v0.4.0 - 2026-11-16

### Added

- **Home page.** `/` shows every enabled plugin at once: In progress (cards with +1), Coming up (the next 7 days of releases), Series in progress and Recently updated; each section hides when empty.
- **Time zone setting** (Settings > General). The home page's Coming up and the calendar page group their days and show their times in your zone; empty uses `TZ`, else UTC. The calendar feed stays UTC.
- **Runs and Start again.** Every entry keeps each viewing, reading or playthrough as a run with its own status, rating, review, progress and dates. "Start again" (Rewatch, Reread, Replay, in each plugin's own words) begins a new run, and moving a completed entry back in the Edit dialog first asks whether to start a new run (Rewatch, ...) or correct the current one. A repeat shows a marker while active and "x2" once finished twice; a repeat you drop leaves the entry showing its last completed run. Earlier runs can only be completed or dropped, and edited or deleted from the run history. Statistics count every run.
- **Run timeline.** Each run in an entry's history has a collapsed, read-only timeline of its logged changes (progress steps and jumps, status and rating changes) with times in your time zone; imported and restored changes are marked. Plugins can declare an optional plural track label (`label_plural`).
- **Notes, reviews and unit notes.** The Edit dialog holds the entry's notes and the review of the current run (10,000 characters each; past runs' reviews are edited in the run history). Add a short note to a single unit (an episode, a page, ...) from a run's timeline or from the "Note on ..." link after a +1; notes show in the timeline at their unit. Notes and reviews take simple formatting: bold, italic, lists, https links and spoilers that stay hidden until clicked.
- **Own tags.** Label entries of any plugin with your own tags ("comfort", "with-partner") in the Edit dialog, with suggestions from your existing tags. Filter every list by them (any or all, `ut` / `ut_all=1`), show them as a table column, see them on the item page, and rename, merge or delete them under Settings > Tags. Names ignore case, also beyond ASCII.
- **Custom lists.** Collect entries of any plugin in your own named lists ("Kingdom Hearts" across games, manga and novels; "100% completed"), each with its own page of cards, a progress line ("6 of 14 done"), sorting by title, date added or status, or a manual order you arrange. Add entries from an item page (tick the lists), by searching your library on the list page, or straight from a plugin's search (the new entry lands as planned). The sidebar lists them under the plugins. Deleting a list keeps its entries; removing an entry takes it out of every list.

### Changed

- An entry's comment is now called notes. Notes longer than 10,000 characters are refused with a message instead of being cut silently. Existing entries become run 1 of their history (migration 00019).
- **Backups are format 2.** A backup now carries every run of an entry (with its progress, review, unit notes and change log), the entry's notes, your own tags and your custom lists. Older backups (format 1) still restore: each entry becomes its first run. Older Allister versions cannot read format 2 backups. Restoring a backup merges custom lists by name and adds nothing when repeated.
- **JSON export version 2.** The per-plugin JSON export's `comment` is now `notes`, and each entry gains `runs`, `user_tags` and `lists`; the top-level status, rating, progress and dates stay those of the run the list shows.

### Fixed

- A Google Books 403 with a key but no known reason is reported instead of retried.
- The import time estimate counts rows resolved by title as two requests.

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
