# AGENTS.md

Self-hosted, single-user media tracker (Go + htmx + Tailwind, SQLite, Docker). The core is a generic, media-agnostic engine; media types are **data-only plugins** (anime is the first and default one). Read `docs/handoff.md` for the current state and open follow-ups, `README.md` for users, `docs/plugins.md` for the plugin contract and `docs/usage.md` for routes and URL parameters.

## Commands

Go is **not installed on the host**. Everything goes through the pinned Docker image:

```
scripts/go.sh test ./...          # full suite (add -count=1 to bypass the cache)
scripts/go.sh test ./internal/web/
scripts/go.sh vet ./...
scripts/go.sh fmt ./...
```

- `-race` is unavailable (CGO is off, pure-Go SQLite via `modernc.org/sqlite`).
- Run the app: `PORT=18081 docker compose -p <your-own-project> up -d --build`, tear down with `docker compose -p <project> down -v`.
- **Never use broad docker commands** (`docker kill`, `prune`, `rm $(docker ps -aq)`): the machine runs unrelated containers. Always use your own compose project name and a private port. Ports 18080 (old preview), 18081 and 18082 belong to the owner.

## Architecture rules (enforced by tests)

- `internal/plugin` is the contract (manifest, kinds, fields, progress types, sections, sources, importers/exporters). Plugins declare data only: **no plugin HTML or JS**.
- Only `cmd/allister/main.go` may import `internal/plugins/*`. `internal/guard` fails the build if core Go/HTML files contain media-specific words (anime, episode, `manga`, word-bounded `mal`, ...) or import plugins. Test files and `internal/plugin/plugintest` fixtures are exempt. Media words belong in plugins and docs only.
- A future marketplace (loading plugins without a rebuild) must stay possible: contract types stay serialisable data (JSON round trip tested), no compile-time coupling beyond `cmd/allister`.
- Progress types come from a closed core enum (count, pages, percent, duration); each plugin declares which are allowed, the defaults and the primary key.
- Libraries: `internal/library` (generic items, entries, terms, people, query engine), `internal/jobs` (queue with plugin gate), `internal/importer` (paced, resumable import runner), `internal/backup` (zip backup, merge restore), `internal/covers`, `internal/settings` (per-plugin settings keys `plugin.<id>.*`), `internal/web` (handlers, templates, htmx).

## Conventions

- Migrations (`internal/db/migrations`, goose, embedded) must be strictly increasing. **00008 is intentionally unused** (a guard test asserts it); the next number is 00014. Never edit an applied migration's SQL.
- Text hygiene: user input is rejected when invalid (control characters etc.); plugins sanitise source input with `plugin.SingleLine/MultiLine/HTTPSURL`.
- Errors from sources: `plugin.ErrNotFound`, `ErrRateLimited`, `ErrUnavailable` (retryable), `ErrBadResponse` (answered with unusable data: web shows a 502 page, the import records the row failed without retry), `jobs.ErrRetryLater`, `ErrPaused` (an unbounded hold, not counted), `ErrPermanent` for jobs.
- htmx: `hx-history="false"` on `#list-rows` (restore returns the full page), `Vary: HX-Request, HX-Target, HX-History-Restore-Request` on the list, out-of-band swaps update only option lists and links, never focused inputs. No inline scripts: page handlers live in `internal/web/static/app.js` (a strict CSP must stay possible).
- Colours are theme tokens (Settings > Appearance): templates may only use colour classes listed in `internal/appearance/tailwind-map.json` (each maps a class colour such as `slate-800` to a token; `TestTemplatesUseOnlyThemedColours` fails on any other, and Tailwind generates nothing for it). For a new shade map it to an existing token (or add a token with values in every preset), never hard-code a colour in a template or `input.css`, and use no inline `style`. An accent background must carry the plain class `bg-indigo-600` (with `hover:bg-indigo-500`): `input.css` re-points `text-white` to the accent text colour on exactly those classes. The css build stage runs `internal/appearance/css-smoke.sh` on the built CSS.
- Dates are calendar days; progress total <= 0 means unbounded; unrated score (NULL) and explicit 0 are different.
- Everything is pinned: Docker images by digest, Tailwind per-arch sha256, htmx, the Material Symbols font and the bundled app fonts (Inter, Source Serif 4, JetBrains Mono) by sha256 (licences in `LICENSES/`, copied into the image), Go version in `go.mod` and `scripts/go.sh`. `PINS.md` lists them; `scripts/update-pins.sh` shows the diff (`--write` applies). Tests in `internal/guard` and `internal/web` check them.
- The README has a hard cap of 150 lines (docs test); long reference material goes to `docs/usage.md` / `docs/plugins.md`.
- Commit style: `feat: ...`, `fix: ...`, `docs: ...`, `test: ...`, `build: ...`. Work on a branch, keep each commit green, merge locally. The owner pushes to GitHub themselves; the repo has no remote configured.

## Working agreement with the owner

- Interface language of conversation is whatever the owner writes (German or English); code, commits and docs are English.
- Prefer small, verified steps: write the failing test first, show the real RED run, then implement; run the full suite, `vet` and `fmt` before committing; output must be pristine.
- Confirm before anything outward-facing or destructive (pushing, deleting volumes, touching the owner's containers).
