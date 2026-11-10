# Pins

Everything Allister's build and CI pull from the network is pinned to an exact version and verified by a digest or checksum. The tests in `internal/guard/pins_test.go` and `internal/web/static_test.go` fail when an image is unpinned, a checksum (Tailwind, htmx, the icon font, the bundled app fonts) is missing or wrong, or this file does not record a pin that is in use (or records one that is not).

Pinned on 2026-11-10.

## Images

The digest is the multi-architecture index digest of the tag at the time of pinning (amd64 and arm64 both resolve through it). The tag stays in front of the digest for humans; Docker pulls by digest. The distroless runtime image has no version tags upstream (the named tags are `latest`, `debug` and `nonroot`, besides commit hashes), so `latest` with a digest is its canonical pin; switching to `nonroot` would change the runtime user and the data volume's permissions, a deliberate decision not taken here.

| Where | Image |
|---|---|
| `Dockerfile` css stage | `debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc` |
| `Dockerfile` build stage, `scripts/go.sh` | `golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0` |
| `Dockerfile` runtime | `gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2` |
| `README.md` backup helpers | `alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf` |
| `README.md` live backup helper | `keinos/sqlite3:3.54.1@sha256:b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17` |

Each digest was resolved with `docker buildx imagetools inspect <tag>` and cross-checked against the registry's `Docker-Content-Digest` header (registry v2 API).

## Go

- Toolchain: Go 1.27.3 (`toolchain go1.27.3` in `go.mod`, the `golang:1.27.3-bookworm` image above in the Dockerfile and `scripts/go.sh`).
- `go.mod` keeps `go 1.27.0` as the language minimum: `go mod tidy` removes a `toolchain` line that equals the `go` line, so the exact patch lives in the `toolchain` line.

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
- Icon names (test data, not embedded): `internal/web/testdata/material-symbols-outlined.codepoints`, upstream's name list `https://raw.githubusercontent.com/google/material-design-icons/10ecc14790612ca8fd164e3f4ffe9c16fc50d5b7/variablefont/MaterialSymbolsOutlined%5BFILL,GRAD,opsz,wght%5D.codepoints` (the commit above), sha256 `a7404d9e5a1b63f496cf05ace45d386075886b5a7f123fba9f29527cb6006fde` (recorded in `internal/web/testdata/material-symbols-outlined.codepoints.sha256`), 4304 names. Downloaded twice on 2026-11-10 with the same sum; its codepoints agree with the font's (for example `settings` e8b8, `search` ef7a, `live_tv` e63a). `TestIconNamesExistInThePinnedFont` checks every icon name in the core templates and every compiled-in manifest icon against it. Update it together with the font (same commit as the licence).
- Served from the embedded static files as `font/woff2` with `Cache-Control: public, max-age=31536000, immutable` (the version is in the file name), declared in `internal/web/static/input.css` with `font-display: block`. Nothing is fetched from a CDN at run time.

## App fonts

Three fonts can be chosen as the app font in Settings > Appearance (besides system font stacks and uploaded fonts). Each is the variable font (weight axis) from the Fontsource npm package `@fontsource-variable/<name>`, as the two subsets Google Fonts publishes for Latin script: Latin and Latin Extended, declared with their `unicode-range` so a browser downloads the Latin Extended file only for text that needs it. All three are under the SIL Open Font License 1.1 (OFL-1.1). The licence texts are the upstream projects' own files, copied unchanged with their copyright lines (the npm packages' `LICENSE` files carry generated copyright lines, for Source Serif 4 a wrong one, so they are not used). Each was downloaded twice with identical content:

| Licence file | Upstream |
|---|---|
| `LICENSES/inter-OFL-1.1.txt` | `https://raw.githubusercontent.com/rsms/inter/353b61b9f4430d5f420d56605a6e7993e0941470/LICENSE.txt` ("Copyright (c) 2016 The Inter Project Authors") |
| `LICENSES/source-serif-4-OFL-1.1.txt` | `https://raw.githubusercontent.com/adobe-fonts/source-serif/80d3f8894c09c937bebfa9011247d2e1c79fd6f4/LICENSE.md` ("Copyright 2014 - 2023 Adobe (http://www.adobe.com/), with Reserved Font Name ‘Source’") |
| `LICENSES/jetbrains-mono-OFL-1.1.txt` | `https://raw.githubusercontent.com/JetBrains/JetBrainsMono/19371302b95d218af43299bce79ddbddd0bc364d/OFL.txt` ("Copyright 2020 The JetBrains Mono Project Authors") |

The image carries every licence: the Dockerfile's final stage copies `LICENSES/` to `/licenses` (checked by `TestLicencesShipWithTheImage`).

**Reserved Font Name.** Source Serif's licence reserves the name "Source": a Modified Version of the font may not use it. Subsetting counts as modifying under the OFL. Allister does not modify the files: it redistributes, byte for byte, the Latin and Latin Extended subsets that Google Fonts publishes (Fontsource repackages Google's files; Google Fonts distributes Source Serif 4 under that name with its subsets). The fonts' internal names are unchanged ("Source Serif 4"). To stay clear of the reserved name on our side, the CSS family alias is `Allister Bundled Serif`, never "Source"; the picker labels the choice "Source Serif 4" as attribution of the upstream font. This reading is a judgement, not legal advice: if the owner prefers no doubt at all, replace Source Serif 4 by a serif without a Reserved Font Name (for example Literata, also OFL) following "Refreshing" below. Inter and JetBrains Mono declare no Reserved Font Name.

| Font | Package | Weights |
|---|---|---|
| Inter | `@fontsource-variable/inter@5.3.0` | 100 to 900 |
| Source Serif 4 | `@fontsource-variable/source-serif-4@5.3.1` | 200 to 900 |
| JetBrains Mono | `@fontsource-variable/jetbrains-mono@5.3.0` | 100 to 800 |

| File | sha256 | Bytes |
|---|---|---|
| `internal/web/static/fonts/inter-latin-ext-wght-v5.3.0.woff2` | `34b9c504cab7a73e37b746343a449132e56cf7b5481af2cb81dc74dcff25c956` | 85068 |
| `internal/web/static/fonts/inter-latin-wght-v5.3.0.woff2` | `3100e775e8616cd2611beecfa23a4263d7037586789b43f035236a2e6fbd4c62` | 48256 |
| `internal/web/static/fonts/jetbrains-mono-latin-ext-wght-v5.3.0.woff2` | `79bfdab9ba467e26eea4122e6f2567e188dd8a09a8c730d501fc487c4ab99c6e` | 15196 |
| `internal/web/static/fonts/jetbrains-mono-latin-wght-v5.3.0.woff2` | `18be452724bfdc236c074ca94a249a7f41a86752c7d04ab258ce9ed5651f6a7e` | 40404 |
| `internal/web/static/fonts/source-serif-4-latin-ext-wght-v5.3.1.woff2` | `d10ccbbd0dc4ebae2938958dc1f622a53460fe297e9c958564742dd684309c3f` | 42212 |
| `internal/web/static/fonts/source-serif-4-latin-wght-v5.3.1.woff2` | `382475f38242c52d42a3e5ff8dc69b5d73f10d0d21e4550b5fe232984688779a` | 50936 |

- Source: `files/<name>-latin-wght-normal.woff2` and `files/<name>-latin-ext-wght-normal.woff2` of each package version above. Downloaded twice from jsDelivr (`https://cdn.jsdelivr.net/npm/@fontsource-variable/<name>@<version>/files/...`) on 2026-11-10 with identical sums, and cross-checked against the npm registry tarball of the same version, whose `dist.integrity` (sha512) matched; the files inside the tarball had the same sha256. The licence files came from the same tarballs.
- Each sum is recorded next to its file (`<file>.sha256`, sha256sum format). The version is in the file name, so cached copies are never reused for a new file. Served from the embedded static files as `font/woff2` with the same immutable cache as the icon font, declared by the generated `/appearance.css` with `font-display: swap`. Nothing is fetched from a CDN at run time.
- Refreshing: download the new package version's two files per font (twice; the sums must match; compare with the npm tarball), save them under the new version's names, regenerate the `.sha256` files (`cd internal/web/static/fonts && sha256sum <file> > <file>.sha256`), check the upstream licence file (copy it unchanged when it changed, update the table above), and update the version in `internal/appearance/bundled.go`, `internal/web/static_test.go` and this section. `scripts/update-pins.sh` never rewrites the fonts; it only checks them.

## CI tools

The checks in `scripts/ci.sh` (run by the GitHub workflows and locally, see `docs/ci.md`) use these images, pinned the same way as above (multi-architecture index digest, resolved with `docker buildx imagetools inspect` and cross-checked against the registry's `Docker-Content-Digest` header). `TestCIScriptImagesPinned` fails when one is unpinned or a `docker run` in the script uses a literal image. Versions released less than a week before pinning were skipped for the previous release (a short cooldown against a compromised fresh release).

| Tool | Image |
|---|---|
| golangci-lint (Go 1.27) | `golangci/golangci-lint:v2.15.0@sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481` |
| hadolint | `hadolint/hadolint:v2.15.1@sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d` |
| actionlint (with shellcheck) | `rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667` |
| gitleaks | `ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f` |
| Trivy (image scan and SBOM) | `aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c` |

Go itself is the build image above (`scripts/ci.sh` reads it from `scripts/go.sh`).

## GitHub Actions

Every third-party action in `.github/workflows` is referenced by its full commit SHA with the release in a comment (`uses: actions/checkout@<sha> # v7.0.1`): a tag can be moved, a commit cannot. Each SHA was resolved from the release tag twice, through the GitHub API (`git/ref/tags/<tag>`, dereferencing annotated tags) and with `git ls-remote` (the peeled `^{}` commit); both agreed. Releases younger than a week at pinning time were skipped for the previous one. `TestWorkflowActionsPinnedBySHA` and `TestPinsDocumentListsEveryAction` fail when a reference is not pinned or this table disagrees with the workflows. Dependabot (`github-actions`) proposes updates and keeps the SHA and the comment in step; update this table in the same pull request.

| Action | Release | Commit |
|---|---|---|
| `actions/checkout` | `v7.0.1` | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| `actions/cache` | `v6.1.0` | `55cc8345863c7cc4c66a329aec7e433d2d1c52a9` |
| `actions/upload-artifact` | `v7.0.2` | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |
| `docker/setup-buildx-action` | `v4.4.1` | `f87e5991a6d7451dcb8d9637bfbc97413f497069` |
| `docker/build-push-action` | `v7.4.0` | `c3c9e263c25d99ce0380d002d59b67737d91b0dc` |
| `github/codeql-action` | `v4.39.1` | `9b3254085494b55a0f7c4c2b3d2d52cc712ce370` |

## Go tools

Built from the separate module `tools/` (`tools/go.mod` with `tool` directives; `tools/go.sum` holds the hashes, verified against the Go checksum database when downloaded). It is a module of its own so the tools' dependencies never enter the application's `go.mod`. `TestPinsDocumentListsTools` checks this table against `tools/go.mod` and `tools/go.sum`.

| Module | Version | Tool |
|---|---|---|
| `golang.org/x/vuln` | `v1.9.0` | `govulncheck` (security advisories) |
| `github.com/go-gremlins/gremlins` | `v0.6.0` | `gremlins` (mutation testing) |

Update with `scripts/go.sh -C tools get -tool <module>/cmd/<tool>@<version>` and `scripts/go.sh -C tools mod tidy`, then edit this table (Dependabot proposes updates of `tools/` too).

## Refreshing

- **Security updates of the same tags** (new digests, new Tailwind sums of the same release, also the CI tool images in `scripts/ci.sh`): run `scripts/update-pins.sh`. It resolves every pinned tag again, hashes the Tailwind release, checks htmx, the icon font and its name list and the bundled app fonts, and prints the changes as a diff. `scripts/update-pins.sh --write` applies them to the Dockerfile, `scripts/go.sh`, `scripts/ci.sh`, `README.md` and this file. Then run `scripts/go.sh test ./...` and `docker compose build`.
- **A new version** (image tag, Go patch, Tailwind release): edit the tag (or `TAILWIND_VERSION`) everywhere it appears, including this file, and for Go also the `toolchain` line of `go.mod`; then run `scripts/update-pins.sh --write` to resolve the new digests and checksums, and test as above.
- **htmx**: replace `htmx.min.js` with the upstream file, regenerate the sum (`cd internal/web/static && sha256sum htmx.min.js > htmx.min.js.sha256`), compare it with upstream, and update the version and sum above. The script never rewrites htmx.
- **Material Symbols**: fetch the stylesheet URL above again, download the woff2 it names (twice; the sums must match), save it as `material-symbols-outlined-v<version>.woff2` (the new version in the name, so cached copies are never reused), regenerate the `.sha256` file, and update the file name in `input.css`, `base.html`, `internal/web/static_test.go`, `internal/guard/pins_test.go`, `scripts/update-pins.sh` and this section. The script never rewrites the font; it only checks it.
- Not pinned: the Debian packages installed with `apt-get` in the css stage (`curl`, `ca-certificates`) come from the pinned base image's signed repositories at build time, and Go modules are verified by `go.sum`.
