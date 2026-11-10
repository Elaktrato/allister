# Allister: generic core and media plugins (iteration 2)

Date: 2026-11-09
Status: draft for approval
Builds on: `2026-11-09-allister-anime-design.md` (everything in it stays true unless this document says otherwise). The anime feature set that exists today (search, overview, add to list, paginated list, people pages, manual entries with cover upload, Yamtrack import, unmatched entries and "Match to AniList", title language, local covers, background sync) is the behaviour baseline.

## Intent and why

The owner wants Allister to be a completely generic, "dumb" collector of data about things they consume. Anime is only the first media type; manga, movies, TV shows, games and books follow. Each media type must be a plugin that can be switched on and off, and modified, without touching the core code. Later, plugins should be installable from a marketplace and become part of the running app without a rebuild. The marketplace itself is not part of this iteration, but the plugin contract must not make it impossible.

This iteration also delivers the follow-ups the owner approved after the first working version (listed under "Also in this iteration").

## Goals

- A **generic core** that knows nothing about any media type. It stores items, people, relations, list entries, progress, images, jobs and settings, draws every page, runs search and filters, runs imports and exports, and loads plugins.
- A **plugin contract made only of data**: a plugin declares what it wants shown and how data gets in and out. A plugin never ships HTML or JavaScript. The core draws every page from a fixed set of building blocks.
- **Anime as the first plugin**, built on the same contract that later plugins will use, with feature parity with today's app. It is on by default and can be turned off in Settings.
- **Plugins can be enabled and disabled** at runtime from Settings. Disabling hides the media type (navigation, search, routes) and keeps all its data; enabling brings it back.
- The contract is **serialisable** (plain, versioned data in and out; no Go types, database handles or HTTP handlers cross the boundary), so a runtime loader (sandboxed module or separate process) can be added later behind the same contract without changing plugins.
- **Progress is flexible**: progress varies per media type and per item (see below).
- The follow-ups under "Also in this iteration".

## Non-goals (this iteration)

- The marketplace, signing, runtime installation of plugins, and any sandbox for untrusted code. Only the design must not block them.
- Plugins shipping HTML, CSS or JavaScript. Plugins running their own HTTP routes or database access.
- Other media-type plugins (manga, movies, TV, games, books). Only anime is implemented, though the contract must be demonstrably able to express them (see "Proving the contract").
- Accounts, multi-user, sharing (unchanged non-goals).

## Anti-goals

- Any media-specific code (anime words, AniList, episodes) remaining in the core.
- A plugin being able to run arbitrary code in the page, or read data of another plugin.
- Losing data when a plugin is disabled.
- Regressing any behaviour of the current anime app (the existing tests express the baseline).

## Decisions made by the owner

- Plugins are **data-only**: they declare what they want to display, their **source(s)**, and their **importer/exporter**. No plugin HTML or JS.
- The core is generic and "dumb"; anime and the other media types are default plugins that can be turned off.
- Plugins should in future be loadable from a marketplace, instantly, without a rebuild (future, not now).
- **Progress has several forms**: a book may be page-based or percentage-based depending on the book; movies and TV shows may use a timestamp; a TV show may use an episode count plus a timestamp on top.
- Everything in "Also in this iteration" is approved as described there.
- Locally merge the branch into `main` when everything is done; the owner pushes to GitHub themself and wants the exact command at the end.

## The plugin contract (what the core needs from a plugin)

All of it is declarative or request/response data with a versioned shape.

1. **Manifest**: id (stable slug, e.g. `anime`), display name, version, contract version, default-enabled flag, the media kinds it defines (a plugin may define one or more; anime defines one), icon/label strings for navigation (text only).
2. **Schema** of an item: typed fields (text, number, date, enum, list of text, URL) each with key, label, whether it shows in the info panel, the edit form, the filter bar and sort options, validation bounds. Title languages are first-class (every item has titles per language; the plugin names the languages it knows, the user picks the display language in Settings as today). Every kind may also enable **genres** and **tags** as first-class item metadata (see "Genres and tags" below); the plugin decides whether each is enabled, its label, and whether it is editable and filterable.
3. **Statuses**: the core has five status classes (planned, active, paused, completed, dropped); the plugin supplies the labels per class ("Watching", "Reading", "Playing").
4. **Progress types**: the core owns a **closed enum of progress types** it knows how to store, validate, render and filter: `count` (episodes, chapters, volumes), `pages`, `percent`, and `duration` (a timestamp). A plugin cannot invent a type; adding a type is a core change and a contract version bump. A plugin declares, per media kind, the **eligible progress types** it allows, which of them is the **default**, and for each a display label ("Episode", "Chapter", "Page", "Position") and, for bounded types, which item field supplies the total (episode count, page count, runtime). The same type may be declared more than once with different labels when a media kind needs it (for example a TV kind with a `count` labelled Season, a `count` labelled Episode and a `duration` for the position inside the episode). An item uses the plugin's default unless the item or the user selects another eligible combination (a book can be page-based or percentage-based; a TV show uses an episode count plus a duration; a movie uses a duration). A list entry stores a value per active progress type. The core renders progress, the "+1" quick action for count types, validation against totals, and filters like "not started / in progress / finished" generically. The entry dialog (Edit on a list row or the overview) only offers the combinations the plugin lists as eligible. (Amended after implementation: the combination is switched in the entry dialog, not in Edit details, which edits item data only.)
5. **Page sections** to show on an item's overview, chosen from the core's fixed building blocks: info list, text block, titles list, related items, people with roles (characters and voice actors, authors, directors, cast), genre chips, tag chips, recommendation cards, external links. A plugin chooses which blocks appear and in which order and with which labels.
6. **Sources**: how to find and fetch data. A source implements `search(query) -> item summaries` and `details(id) -> item (fields, titles, people with roles and images, relations, recommendations)`, plus `lookup by external id` when it supports it (used by importers and matching). The contract states the rate limit the core must respect for each source, the allowed image size to download, and the identity of the source (for "View on ..." links). Anime's source is AniList.
7. **Importers and exporters**: a named format, the file type, and a mapping from file contents to entries (status, rating, progress per track, dates, comment, external identifiers) and back. The core runs them as resumable, rate-limited jobs with the progress page. Anime ships the Yamtrack CSV importer (with unmatched rows kept as manual entries and later matched by hand, as today) and a JSON exporter. The core also provides a universal JSON backup export/import of everything.
8. **Manual entries and matching** are core features driven by the schema (create/edit form from the schema; "match to a source" via the plugin's source).

The core owns: storage, the job queue, covers and image downloads, the page shell and navigation, settings, search and filtering, pagination, edit forms, the import/export runner, plugin enable/disable, and cross-origin protection.

## Data model direction

Plugin data is stored in generic, plugin-namespaced tables (items, item fields, titles, people, item-people roles, relations, list entries, progress values), not in per-plugin tables, so a plugin never needs its own migrations. Filterable and sortable fields must be efficiently queryable (the builder chooses the representation). Disabling a plugin never deletes rows. The owner currently has no real data (the only preview volume is empty), so the builder may replace the current anime-specific schema with a new baseline instead of writing a migration from it; the application must still start cleanly on an empty volume and on a volume created by this iteration.

## Proving the contract

Because only anime is built, the builder must prove the contract can express other media types without building them: a documented, tested **example fixture** (in tests, not shipped as a plugin) for at least a book-like plugin (page or percent progress per item, authors as people) and a TV-like plugin (episode count plus timestamp track) that loads through the same registry and renders through the same core pages. These fixtures are test-only.

## Also in this iteration (approved by the owner)

1. **Edit details** for every saved item (manual, unmatched, imported or sourced): titles per language, type, and every field the schema marks editable, plus cover upload/replace. Edits are local and are never overwritten by a source refresh (there is none), except by the explicit "Match to source" action on a manual item. After saving an edit that was started from an item's overview the user returns to that overview.
2. **Recommendations load afterwards**: the item overview renders immediately and the recommendations row fills in through a separate request with a short timeout and a cache; a slow or failing source only costs that row.
3. **Image sizes**: person photos use the source's smaller `medium` size (about 21 KB each instead of 87 KB), covers keep the larger size (about 40 KB), the download limit becomes 2 MiB per file, and the README's disk-use claim (currently "a few hundred MB", which was a guess and is wrong) is corrected with the measured numbers: roughly 1 MB of person photos per anime at the smaller size.
4. **Search within the user's own list** (generic, driven by the schema): free text matched against all stored titles in any language (case-insensitive substring), filters on every schema field marked filterable (for anime at least: type, year from/to, studio), plus status (the existing tabs), score range (with "unrated" and an explicit 0 as separate cases), "unmatched" and "partially synced", and sort by recently updated (default), title, score, year, progress. The query lives in the URL (bookmarkable), works with the 100-per-page paging, updates live as you type, and has a "clear filters" link.
5. **Pin and checksum everything**: Docker base images by digest; the Tailwind binary verified by sha256 per architecture; the vendored htmx file recorded with a checksum and a test that verifies it; the Go toolchain version pinned; helper images in the README pinned; a small script to update the pins deliberately (the owner accepts that base-image security updates then become a manual step).
6. **Already approved and unchanged**: the background sync approach as it is, the Yamtrack import behaviour, CSRF protection, the README.
7. **Genres and tags** (requested by the owner while the plan was being written): items can carry **genres** (a list of names) and **tags** (names, each optionally with a relevance rank 0 to 100 and a spoiler flag). Both come from the source (AniList provides genres, and tags with rank and spoiler flag), can be edited in Edit details and set when creating a manual item, are shown as chips on the item overview (spoiler tags hidden behind an explicit "show spoiler tags" control), and are stored as dedicated, indexed rows so the list can filter by them. List filters: choose one or more genres and/or tags with item counts from the user's own library, and choose whether an item must have ALL of them or ANY of them; the choices live in the URL like every other filter. Free-text search does not match genres or tags. Genres and tags are included in the universal backup. Anime enables both. Terms are trimmed, de-duplicated case-insensitively, at most 60 characters each and at most 50 genres and 100 tags per item. User-assigned personal labels on list entries are NOT part of this iteration (a possible follow-up).

## Constraints

- Go 1.23, pure Go (no CGO), htmx + Tailwind, SQLite, Docker with one volume, no login (unchanged).
- Everything the current test-suite asserts about anime behaviour must keep holding (tests may be moved and adapted to the new structure, not weakened).
- AniList request pacing stays at 30 requests per minute; the contract must let a source declare its own limit.
- No personal data in the repository (the owner's real Yamtrack export stays git-ignored).

## My calls (to be checked by the owner)

- **Compiled-in first**: built-in plugins are Go packages registered with the core and talk to it only through the data contract; the runtime loader is future work.
- Plugin data lives in generic namespaced tables; no per-plugin migrations.
- The five core status classes stay fixed; plugins only relabel them.
- The universal JSON backup export/import is added in the core because the owner asked for importer/exporter to be part of the plugin story and a generic round-trip is the safest first exporter.
- Order of work: contract and core first, anime moved onto it with parity, then the approved follow-ups on top, then pinning, then the final review and the local merge.

## Further calls (from the builder check; mine, not the owner's)

- **URLs**: a plugin's pages live under `/{plugin-id}/...`, so anime keeps today's URLs (`/anime`, `/anime/search`, `/anime/{source}/{id}`, `/anime/manual/new`, `/anime/{source}/{id}/match`). Generic core routes stay as they are (`/list/{id}/edit|increment|remove`, `/people/{id}`, `/settings`, `/settings/import`, `/covers/`). Tests may change where a route moved, but they must assert the same behaviour.
- **Generic fields**: "efficient" means interactive speed (well under a second) for a few thousand items. A field's type cannot change between contract versions in this iteration. Typed value storage (not free-form text) is required so range filters and sorts work.
- **People and roles**: the contract's "people with roles" supports an optional counterpart on a credit, so "character, voiced by X" is one credit with a counterpart person; the anime overview and the person pages keep today's behaviour (an actor's page shows the character played). Credits without a counterpart (author, director) are the simple case. A credit may carry a short free-text qualifier such as main/supporting; single line, at most 100 characters.
- **Importers and exporters**: an importer is a request/response step (plain data in and out): it receives the uploaded file and returns normalised rows (status, rating, progress per track, dates, comment, external identifiers, title). It also declares its resolution policy as data: which external identifier type to look up, through which source, and what to do when nothing matches ("keep as manual entry with this title language"). The core owns the lookup through the source, the fallback to a manual entry, de-duplication by external identifier, pacing and resumability. An exporter is the reverse (entries in, file out).
- **External identifiers and "unmatched"**: the core has a generic external-identifiers table (type, value per item). "Unmatched" means a manual item that carries an external identifier from an importer. `sync_state` / "partially synced" is a core concept tied to the job queue.
- **Separable source calls**: the source contract keeps separate operations (`search`, `details`, `people`, `recommendations`, `lookup by external id`) so jobs, pacing, the partial-sync state and the late-loading recommendations row behave as today (about two requests per added anime).
- **Progress rules**: the plugin lists the eligible progress types per media kind and the default (owner's proposal); an item selects its active types through a per-item field that starts at the plugin default and can be changed in the entry dialog (not in Edit details), but only to eligible combinations. Percent values are whole numbers 0 to 100; durations are whole seconds; counts and pages are whole numbers. "Finished" means every active bounded track is at its total (percent at 100); "not started" means every active track is zero; anything else is "in progress". Setting a status to completed does not change progress (as today). The "+1" action applies to the plugin's primary count track (the first count or pages track it declares) and is hidden when none exists.
- **Disabling a plugin**: its jobs pause and resume on enabling, its items disappear from every page, search and backup selection screens but all rows stay, and a person page of a disabled plugin is not reachable. People are namespaced per plugin and never shared across plugins. The universal backup includes everything, enabled or not.
- **Backup format**: a versioned archive containing a JSON document with all items, people, credits (with their qualifiers), relations, external identifiers, list entries and progress, plus the file of every item cover stored locally: once a cover is downloaded its remote URL is not retained, so a downloaded cover cannot be fetched again from the backup and is included just like a cover uploaded by the user (a cover that is still a remote URL is kept as that URL). Person photos are not included and are fetched again by the normal sync after a restore. Import merges: items already present (same plugin, source and source id) are skipped, nothing is overwritten or deleted. A backup with a newer major format version is refused with a clear message.
- **Contract versioning**: the core refuses a manifest whose contract major version differs from its own and tells the user which plugin and why; there is no negotiation in this iteration.

## Left to the builder

- Package layout, the Go types behind the contract, the serialisation format of the contract, how the registry finds plugins, how enabling/disabling is persisted and applied, the table design for generic fields and fast filters, the building-block templates, the progress value representation, how the core exposes plugin pages and routes (for example `/{plugin}/...`), URL compatibility with today's routes, test layout, and how the existing tests are migrated.
