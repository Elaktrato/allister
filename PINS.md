# Pins

Everything Allister's build pulls from the network is pinned to an exact version and verified by a digest or checksum. The tests in `internal/guard/pins_test.go` and `internal/web/static_test.go` fail when an image is unpinned, a checksum (Tailwind, htmx, the icon font) is missing or wrong, or this file does not record a pin that is in use (or records one that is not).

Pinned on 2026-11-10.

## Images

The digest is the multi-architecture index digest of the tag at the time of pinning (amd64 and arm64 both resolve through it). The tag stays in front of the digest for humans; Docker pulls by digest.

| Where | Image |
|---|---|
| `Dockerfile` css stage | `debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc` |
| `Dockerfile` build stage, `scripts/go.sh` | `golang:1.23.12-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db` |
| `Dockerfile` runtime | `gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2` |
| `README.md` backup helpers | `alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf` |
| `README.md` live backup helper | `keinos/sqlite3:3.54.1@sha256:b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17` |

Each digest was resolved with `docker buildx imagetools inspect <tag>` and cross-checked against the registry's `Docker-Content-Digest` header (registry v2 API).

## Go

- Toolchain: Go 1.23.12 (`toolchain go1.23.12` in `go.mod`, the `golang:1.23.12-bookworm` image above in the Dockerfile and `scripts/go.sh`).
- `go.mod` keeps `go 1.23.0` as the language minimum: `go mod tidy` removes a `toolchain` line that equals the `go` line, so the exact patch lives in the `toolchain` line.

## Tailwind CLI

Version `v3.4.17` (`ARG TAILWIND_VERSION` in the Dockerfile), checked with `sha256sum -c` before it is made executable:

| Architecture | Asset | sha256 |
|---|---|---|
| amd64 | `tailwindcss-linux-x64` | `7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4` |
| arm64 | `tailwindcss-linux-arm64` | `69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b` |

Obtained by downloading each asset twice and hashing it; both matched the release's own `sha256sums.txt`.

## htmx

Vendored as `internal/web/static/htmx.min.js`: htmx 2.0.4, sha256 `e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447` (recorded in `internal/web/static/htmx.min.js.sha256`). The file is byte-identical to `htmx.org@2.0.4/dist/htmx.min.js` on unpkg and jsDelivr; its sha384 (`HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+`) is the integrity value htmx publishes for 2.0.4.

## Material Symbols

Vendored as `internal/web/static/material-symbols-outlined-v383.woff2`: Material Symbols Outlined v383 (Google Fonts family version), sha256 `b3adb2da00c7711ece1d017903906b3407f7fde2d7a696f40645d78b12fec9c1` (recorded in `internal/web/static/material-symbols-outlined-v383.woff2.sha256`), 323976 bytes.

- What it is: the static instance at the default axes (optical size 24, weight 400, fill 0, grade 0) with every icon (4012 ligatures), not a subset, so any icon name a plugin manifest declares renders. The variable font (all axes) is about 4 MB; this instance is 324 KB.
- Source: the stylesheet `https://fonts.googleapis.com/css2?family=Material+Symbols+Outlined:opsz,wght,FILL,GRAD@24,400,0,0` (requested with a browser user agent so it answers with woff2) names `https://fonts.gstatic.com/s/materialsymbolsoutlined/v383/kJF1BvYX7BgnkSrUwT8OhrdQw4oELdPIeeII9v6oDMzByHX9rA6RzaxHMPdY43zj-jCxv3fzvRNU22ZXGJpEpjC_1v-p_4MrImHCIJIZrDCvHOej.woff2`. Downloaded twice on 2026-11-10; both copies had the same sha256. Every icon name the core templates use (`collections_bookmark`, `settings`, `category`, `search`, `filter_list`, `arrow_upward`, `arrow_downward`, `unfold_more`, `add`, `edit_note`, `download`, `tune`, `extension`, `upload_file`, `backup`) and the anime plugin's `live_tv` were checked against the font's glyph names (fontTools).
- Licence: Apache License 2.0 (Google). The licence text is in `LICENSES/material-symbols-Apache-2.0.txt` (from `google/material-design-icons` at commit `10ecc14790612ca8fd164e3f4ffe9c16fc50d5b7`).
- Served from the embedded static files as `font/woff2` with `Cache-Control: public, max-age=31536000, immutable` (the version is in the file name), declared in `internal/web/static/input.css` with `font-display: block`. Nothing is fetched from a CDN at run time.

## Refreshing

- **Security updates of the same tags** (new digests, new Tailwind sums of the same release): run `scripts/update-pins.sh`. It resolves every pinned tag again, hashes the Tailwind release, checks htmx and the icon font, and prints the changes as a diff. `scripts/update-pins.sh --write` applies them to the Dockerfile, `scripts/go.sh`, `README.md` and this file. Then run `scripts/go.sh test ./...` and `docker compose build`.
- **A new version** (image tag, Go patch, Tailwind release): edit the tag (or `TAILWIND_VERSION`) everywhere it appears, including this file, and for Go also the `toolchain` line of `go.mod`; then run `scripts/update-pins.sh --write` to resolve the new digests and checksums, and test as above.
- **htmx**: replace `htmx.min.js` with the upstream file, regenerate the sum (`cd internal/web/static && sha256sum htmx.min.js > htmx.min.js.sha256`), compare it with upstream, and update the version and sum above. The script never rewrites htmx.
- **Material Symbols**: fetch the stylesheet URL above again, download the woff2 it names (twice; the sums must match), save it as `material-symbols-outlined-v<version>.woff2` (the new version in the name, so cached copies are never reused), regenerate the `.sha256` file, and update the file name in `input.css`, `base.html`, `internal/web/static_test.go`, `internal/guard/pins_test.go`, `scripts/update-pins.sh` and this section. The script never rewrites the font; it only checks it.
- Not pinned: the Debian packages installed with `apt-get` in the css stage (`curl`, `ca-certificates`) come from the pinned base image's signed repositories at build time, and Go modules are verified by `go.sum`.
