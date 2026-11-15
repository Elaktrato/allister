# CI, security checks and releases

Allister's checks run on GitHub Actions and, identically, on your machine: every workflow step calls `scripts/ci.sh <check>`, which runs each tool from a container image pinned by digest (or builds it from the pinned `tools/` module). Only Docker is needed locally. The branches are `main` (releases are tagged here) and `dev` (integration).

## Workflows

| Workflow | When | What it gates |
|---|---|---|
| `ci.yml` (CI) | pull requests; pushes to `main` and `dev`; called by the release | Lint, security advisories, tests, Dockerfile/workflow/script lint, secret scan, image build + smoke test + Trivy |
| `mutation.yml` (Mutation) | pushes to `main` and `dev`; Mondays; on demand | Mutation test efficacy per package |
| `codeql.yml` (CodeQL) | pull requests and pushes to `main`/`dev`; Tuesdays; on demand | Static analysis findings (code scanning) |
| `dependency-review.yml` | pull requests | New dependencies with known vulnerabilities (moderate or worse) |
| `scorecard.yml` (Scorecard) | pushes to `main`; Wednesdays; on demand | Nothing (report only): OpenSSF supply-chain score |
| `release.yml` (Release) | a pushed `v*` tag | Tag format and branch, then CI again, then publishing |
| `.github/dependabot.yml` | Mondays | Opens grouped update pull requests (Go modules, tools, Docker images, actions) |

### CI jobs

The job names below are the status checks to require in branch protection (see the checklist at the end).

- **Lint**: `gofmt` (no file needs reformatting), `go mod tidy -diff` for the application and `tools/` (no drift), `go vet`, and golangci-lint with `.golangci.yml`: govet, staticcheck, errcheck, ineffassign, unused, gosec, bodyclose, errorlint, misspell, a minimal revive set, and the gofmt/goimports formatters. The set is chosen for correctness and security, not style; every exclusion in the file states its reason, and real false positives get a narrow `//nolint:<linter> // <reason>` at the line.
- **Security advisories**: govulncheck (the Go vulnerability database) on the source, where only vulnerable code that is actually reachable fails, and on the built binary (the exact module versions and standard library linked in). A Go toolchain advisory is fixed by bumping the Go patch version (see Pins).
- **Test**: `go test ./...` with an atomic coverage profile, uploaded as the `coverage` artifact, and a per-package coverage table in the job summary. The suite includes `internal/guard` (the core stays free of media-specific code; every image, checksum, action and tool is pinned and recorded in `PINS.md`; every workflow job has a timeout and least-privilege permissions) and the static and pin checks in `internal/web`.
- **Dockerfile and workflows**: hadolint (`.hadolint.yaml`), actionlint (with shellcheck on every `run:` script) and shellcheck on `scripts/*.sh`.
- **Secret scan**: gitleaks over the whole git history (a secret committed and later removed is still found).
- **Image build, smoke test and scan**: builds the Dockerfile with buildx (GitHub Actions cache), runs the image, checks that `GET /healthz` answers `ok` and `GET /version` answers the version JSON, and scans it with Trivy. The findings go to code scanning as SARIF; fixable HIGH or CRITICAL vulnerabilities fail the job (unfixed ones cannot be acted on and only show up in code scanning).

Pull request runs are cancelled when the branch gets a newer push; runs on `main` and `dev` always finish. Every job has a timeout, the workflow token is read-only unless a job needs more (code scanning uploads, the release), and checkouts keep no credentials.

### Race detector

The test job does not use `-race`. The project builds with `CGO_ENABLED=0` (pure-Go SQLite, a static binary), and the race detector needs cgo. With cgo switched on it does work in the pinned golang image (it has gcc) together with the pure-Go SQLite driver; this was tried locally (`CGO_ENABLED=1 go test -race`, results under "Measured" below). It is not part of CI because it would test a different build than the one shipped and roughly triples the suite's run time; run it by hand after concurrency changes:

```
docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=1 <the golang image of scripts/go.sh> go test -race -timeout 30m ./...
```

### Mutation testing

[gremlins](https://github.com/go-gremlins/gremlins) changes the code in small ways (a `<` becomes `<=`, a `+` becomes `-`, a condition is negated, ...) and runs the tests against each change. A mutant the tests notice is *killed*, one they miss *lived*; code no test covers is *not covered* and not run at all (gremlins is coverage-guided). **Efficacy** is killed / (killed + lived). Mutation testing finds tests that run code without really checking it, which line coverage cannot show.

gremlins was chosen over go-mutesting (the avito-tech fork) because it is coverage-guided (no time spent on uncovered code), runs mutants in parallel with per-mutant timeouts derived from the measured test time, has efficacy and coverage thresholds as exit codes, and writes a JSON report; both projects had their last release around the end of 2025. It is pinned in `tools/go.mod` (v0.6.0).

Two details of the setup: gremlins v0.6.0 does not fail on its own `--threshold-efficacy` flag (the value is never read back as a number), so `scripts/ci.sh` checks each package's efficacy from the JSON report itself; and mutants run with `GOFLAGS=-count=1`, because a cached baseline test run makes gremlins' per-mutant timeouts far too short (most mutants then "time out" and the efficacy looks perfect).

It runs per package, with its own threshold, for `internal/plugin`, `internal/settings`, `internal/covers`, `internal/jobs`, `internal/importer`, `internal/backup` and `internal/library` (the list and thresholds are `MUTATION_PACKAGES` in `scripts/ci.sh`). `internal/web` is left out: one run of its tests takes about 20 seconds, so its mutants would take hours. The thresholds were set a few points under the values measured when they were introduced (table below) so the job starts green; they are a ratchet: raise a package's threshold when its tests improve, never lower it to make a change pass.

The job does not run on pull requests (the whole list takes about 35 minutes on a 4-core machine, `internal/library` alone 25). On demand: Actions > Mutation > Run workflow, optionally with packages (`internal/plugin internal/jobs`, or `internal/jobs=90` for a different threshold). The report (`.ci/mutation/*.json` and the logs listing every lived mutant) is uploaded as the `mutation-report` artifact, and the job summary has the table.

Locally:

```
scripts/ci.sh mutation                      # the default list and thresholds
scripts/ci.sh mutation internal/plugin      # one package
MUTATION_WORKERS=4 scripts/ci.sh mutation   # more parallel mutants (default 2)
```

### CodeQL, dependency review, Scorecard, Dependabot

- **CodeQL** (Go, `security-and-quality` queries) catches injection, path traversal, unsafe redirects and similar data-flow problems that a linter does not follow across functions. Weekly runs pick up new queries for old code.
- **Dependency review** fails a pull request that adds a dependency with a known vulnerability and lists the new dependencies and their licences in the job summary. It needs the dependency graph (on by default for public repositories).
- **OpenSSF Scorecard** rates the supply-chain practices (pinned dependencies, branch protection, token permissions, signed releases, ...) and reports to code scanning; it gates nothing. For a public repository it also publishes the score (badge).
- **Dependabot** opens one grouped pull request per ecosystem and week: Go modules (`/`), the tools module (`/tools`), Docker images (the Dockerfile's digest-pinned `FROM`s: Dependabot updates the digest, and the tag when a newer one exists) and GitHub Actions (SHA pins, with the version comment). Updates younger than a week are held back (cooldown). The compose file has no image of its own (`build: .`), so there is nothing to update there. **After an image or action update, run `scripts/update-pins.sh --write` (images) or edit the actions table in `PINS.md`, and commit that to the Dependabot branch**: the guard tests fail until `scripts/go.sh`, `README.md` and `PINS.md` agree with the Dockerfile and the workflows. A golang image update also needs `go.mod`'s `toolchain` line.

Secret scanning on GitHub itself (with push protection) complements gitleaks; it is a repository setting (checklist below).

## Running the checks locally

```
scripts/ci.sh all             # fmt tidy vet lint test vuln hadolint actionlint shell gitleaks
scripts/ci.sh lint            # one check; `scripts/ci.sh` alone lists them all
docker build -t allister-ci-check:local .
scripts/ci.sh smoke allister-ci-check:local    # port 18393 (SMOKE_PORT), removed afterwards
scripts/ci.sh trivy allister-ci-check:local
docker image rm allister-ci-check:local
```

Outputs (coverage, mutation reports, the scanned binary) go to `.ci/` (ignored by git and Docker). Go caches are the named volumes of `scripts/go.sh`; golangci-lint and Trivy keep theirs in the volumes `allister-golangci-cache` and `allister-trivy-cache`.

## Pins

Everything is pinned, the CI included, and `PINS.md` records each pin; the guard tests fail when a pin is missing there or the record is stale.

- **Tool images** (`scripts/ci.sh`: golangci-lint, hadolint, actionlint, gitleaks, Trivy): by digest, refreshed together with the other images by `scripts/update-pins.sh` (`--write` applies). For a new version edit the tag in `scripts/ci.sh` and `PINS.md`, then run the script.
- **Go tools** (`tools/go.mod`: govulncheck, gremlins): `scripts/go.sh -C tools get -tool <module>/cmd/<tool>@<version>`, `scripts/go.sh -C tools mod tidy`, then the table in `PINS.md`. Dependabot proposes these too.
- **Actions**: full commit SHA plus the release in a comment. To bump one by hand, resolve the tag twice and compare: `gh api repos/<owner>/<repo>/git/ref/tags/<tag>` (dereference an annotated tag with `gh api repos/<owner>/<repo>/git/tags/<sha>`) and `git ls-remote https://github.com/<owner>/<repo> 'refs/tags/<tag>^{}'`; update every `uses:` of that action and the `PINS.md` row.
- **cosign** (release signing): `cosign-release` in `release.yml`, installed and signature-checked by `sigstore/cosign-installer`; recorded in `PINS.md`.
- **Go toolchain**: `go.mod`'s `toolchain` line, the golang image in the Dockerfile and `scripts/go.sh` (see `PINS.md`).

Versions released less than a week before pinning were skipped for the previous release.

## Releases

Versions are semantic version tags on `main`: `vMAJOR.MINOR.PATCH`, release candidates `vMAJOR.MINOR.PATCH-rc.N`. v0.1.0 (2026-11-12) was tagged and released by hand, before these workflows existed; v0.2.0 is the first one the Release workflow publishes.

1. Merge everything for the release into `main` (through pull requests) and wait for CI on `main` to pass.
2. Move the entries under "Unreleased" in `CHANGELOG.md` to a section for the new version and merge that pull request.
3. Dry run: `scripts/release-check.sh v0.2.0`. It checks the version format, that the tag does not exist (locally and on `origin`), that the working tree is clean, that you are on `main` and level with `origin/main`, and runs `go vet` and the full test suite. It prints the commands and never tags or pushes.
4. Tag and push (an annotated tag carries the tagger and date):
   ```
   git tag -a v0.2.0 -m "Allister v0.2.0"
   git push origin v0.2.0
   ```

The **Release** workflow then:

1. checks the tag again (`scripts/release-check.sh --tag-only`: semantic version, and the tagged commit is on `main`); a tag elsewhere fails here and publishes nothing;
2. runs the whole CI workflow on the tagged commit;
3. builds the image for linux/amd64 and linux/arm64 (the build stages run on the runner's platform and Go cross-compiles, so no emulation is involved) with the version, commit and commit date linked in (`GET /version`, Settings > General), and pushes it to `ghcr.io/<owner>/<repo>` (the name comes from the repository, lower case) with the tags `X.Y.Z`, `X.Y`, `X` (not for `0.y` releases, which may break each other) and `latest`; a release candidate gets only `X.Y.Z-rc.N`. The image carries SLSA provenance (`mode=max`) and SBOM attestations from BuildKit, and is signed keyless with cosign (Sigstore, the workflow's OIDC identity);
4. builds the static binaries (`allister-vX.Y.Z-linux-amd64`, `-linux-arm64`), smoke-tests the amd64 image, writes an SPDX SBOM (`allister-vX.Y.Z.spdx.json`, Trivy) and `checksums.txt`, and signs the checksums keyless (`checksums.txt.sigstore.json`);
5. creates the GitHub release (marked pre-release for `-rc.N`) with all those files and release notes: the commits since the previous tag grouped by their Conventional Commit prefix (`scripts/release-notes.sh`), followed by GitHub's generated notes for merged pull requests, categorised by label (`.github/release.yml`: label pull requests `feat`, `fix`, `docs`, `build`, `ci`, `test` or `breaking`).

To preview the notes: `scripts/release-notes.sh HEAD`.

### Verifying a release

```
cosign verify ghcr.io/<owner>/<repo>:0.2.0 \
  --certificate-identity-regexp '^https://github.com/<owner>/<repo>/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/<owner>/<repo>/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c checksums.txt --ignore-missing
docker buildx imagetools inspect ghcr.io/<owner>/<repo>:0.2.0 --format '{{ json .Provenance }}'
```

### First release on GitHub

The GHCR package is created by the first release and is private until you change it (Package settings > Change visibility) if the repository is public and the image should be pullable without logging in. Link it to the repository there as well.

## Measured

Numbers from setting this up (2026-11-10, a 4-core WSL2 machine shared with other work, `MUTATION_WORKERS=2`):

Mutation testing, first measurement per package (gremlins v0.6.0, coverage-guided):

| Package | Killed | Lived | Not covered | Efficacy | Mutant coverage | Run time | Threshold set |
|---|---|---|---|---|---|---|---|
| `internal/plugin` | 174 | 18 | 123 | 90.62% | 60.95% | 48 s | 87 |
| `internal/settings` | 17 | 0 | 2 | 100.00% | 89.47% | 18 s | 95 |
| `internal/covers` | 37 | 6 | 0 | 86.05% | 100.00% | 14 s | 82 |
| `internal/jobs` | 67 | 10 | 10 | 87.01% | 88.51% | 74 s | 83 |
| `internal/importer` | 167 | 34 | 16 | 83.08% | 92.63% | 184 s | 79 |
| `internal/backup` | 154 | 39 | 19 | 79.79% | 91.04% | 266 s | 76 |
| `internal/library` | 871 | 50 | 35 | 94.57% | 96.34% | 1473 s | 91 |

The lived mutants are listed in each package's log in the `mutation-report` artifact; they are the places where a test runs the code without checking its result. `internal/plugin`'s low mutant coverage comes from validation branches that only other packages' tests reach (gremlins measures coverage per package).

Other checks on the code base at the time (all green):

- golangci-lint: 372 findings with the first configuration, mostly unchecked `Close`/`Rollback`/`Write` errors and test fixtures; after the exclusions with reasons and the fixes listed in the commit, 0.
- govulncheck: no vulnerability in called code. One in a required module that the code does not reach: GO-2026-5024 (`golang.org/x/sys` v0.30.0, Windows only, fixed in v0.44.0); Dependabot will propose the bump.
- Trivy on the image: no fixable HIGH or CRITICAL. Without the filters it lists Debian advisories of unknown severity for `ca-certificates` and `tzdata` in the distroless base (fixed upstream, not yet in the pinned `latest` digest) and the same `golang.org/x/sys` advisory.
- gitleaks: no leaks in the history.
- Race detector (2026-11-15, `CGO_ENABLED=1 go test -race ./...`, Go 1.27.3): no data race in any package. It is slow: `internal/backup` about 7 minutes, `internal/library` about 5.5 minutes (over the default 10-minute `go test` timeout when run alongside the other packages on a busy machine, so pass `-timeout 30m`), `internal/importer` about 5 minutes.

## GitHub settings

These live in the repository settings, not in files. State on 2026-11-15:

- **Applied:** secret scanning with push protection, Dependabot alerts and security updates, private vulnerability reporting (`SECURITY.md` points to it); the release-note labels `feat`, `fix`, `docs`, `build`, `ci`, `test`, `breaking` and `skip-changelog` (`dependencies` is created by Dependabot); Actions require every action to be pinned to a full commit SHA; workflow token permissions are read-only by default and Actions cannot approve pull requests; the tag ruleset **Release tags** (`refs/tags/v*`: only repository admins may create, move or delete a release tag).
- **Branch ruleset `main and dev`** (strict, no bypass): pull request required (zero approvals, squash or rebase merge), status checks required and up to date with the base branch (`Lint`, `Security advisories`, `Test`, `Dockerfile and workflows`, `Secret scan`, `Image build, smoke test and scan`, `Analyze (Go)`, `Dependency review`), linear history, no force pushes, no deletion. With it, nothing is pushed to `main` directly: push a branch and merge its pull request on GitHub.
- **Still to do by hand:** after the first release the workflow publishes, set the GHCR package's visibility and link it to the repository (Package settings).
- **CODEOWNERS** is not included: it only names reviewers by GitHub account, which adds nothing for a single maintainer. When there are more, add `.github/CODEOWNERS` with `* @<account>` and require code owner review in the ruleset.
