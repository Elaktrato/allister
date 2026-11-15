# Changelog

All notable changes to Allister. Versions follow [semantic versioning](https://semver.org) (`vMAJOR.MINOR.PATCH`, release candidates `-rc.N`); while the major version is 0, a minor release may break compatibility.

How a release is cut: move the entries under "Unreleased" to a new `## vX.Y.Z - YYYY-MM-DD` section on `main`, run `scripts/release-check.sh vX.Y.Z`, then push an annotated tag (`git tag -a vX.Y.Z -m "Allister vX.Y.Z"`, `git push origin vX.Y.Z`). The Release workflow publishes the image and the GitHub release; its notes list every commit since the previous tag, grouped by Conventional Commit prefix. Details: [docs/ci.md](docs/ci.md#releases).

## Unreleased

The first tagged release (suggested: v0.1.0). Everything until now: the generic core with the anime plugin (AniList search and overviews, your list with filters and sorting, edit details, manual entries and matching, character and voice-actor pages, Yamtrack import, JSON export, universal backup and restore, plugins switchable in Settings), and:

### Added

- `GET /version` (JSON: version, commit, build date, Go version) and the version in Settings > General; the Docker build takes `VERSION`, `COMMIT` and `DATE` build args.
- GitHub Actions: lint (golangci-lint, gofmt, go mod tidy, go vet), govulncheck, tests with coverage, hadolint, actionlint, shellcheck, gitleaks, an image smoke test and Trivy scan, CodeQL, dependency review, OpenSSF Scorecard, mutation testing on `main` and `dev`, Dependabot, and a release workflow (multi-architecture image on GHCR with provenance, SBOM and cosign signature; binaries, SBOM and signed checksums on the GitHub release).
- `scripts/ci.sh` runs every check locally with the same pinned tools; `scripts/release-check.sh` checks a release before tagging.

### Changed

- The Docker build compiles on the build machine's platform and cross-compiles for the target, so linux/arm64 images build without emulation.
- "version" is a reserved plugin id (the `/version` route).

### Fixed

- Request paths are logged quoted, so a path with an encoded newline cannot forge log lines.
