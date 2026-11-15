# Security policy

## Supported versions

Only the latest release (the newest `vX.Y.Z` tag and its image `ghcr.io/<owner>/<repo>:latest`) receives security fixes. Allister is single-user software without accounts: run it on a private network or behind a reverse proxy that authenticates (see the README).

## Reporting a vulnerability

Please do **not** open a public issue for a security problem. Report it privately through GitHub's private vulnerability reporting: on the repository page open **Security > Advisories > Report a vulnerability** (or `https://github.com/<owner>/<repo>/security/advisories/new`). Only the maintainer sees the report.

Include what you found, how to reproduce it (version from Settings > General or `GET /version`), and its impact as you see it. You will get an answer in the advisory thread; a fix is released as a new patch version, and the advisory is published with credit unless you prefer otherwise.

## What is checked automatically

Every change runs govulncheck, CodeQL, gosec (in golangci-lint), gitleaks and a Trivy scan of the image; dependencies are reviewed on pull requests and updated by Dependabot. Release images are signed with cosign (keyless) and carry SBOM and provenance attestations. Details and verification commands: [docs/ci.md](docs/ci.md).
