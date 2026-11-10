#!/usr/bin/env bash
# Run the CI checks locally, exactly as the GitHub workflows run them. Every
# tool runs from a container image pinned by digest (below, recorded in
# PINS.md; scripts/update-pins.sh refreshes the digests) or is built from the
# tools module (tools/go.mod and tools/go.sum pin govulncheck and gremlins).
# Go itself is the pinned image of scripts/go.sh. Only docker is required.
#
# Usage: scripts/ci.sh <check> [args]
#   lint                 golangci-lint (.golangci.yml)
#   fmt                  gofmt: no file may need reformatting
#   tidy                 go mod tidy must not change go.mod/go.sum (also tools/)
#   vet                  go vet ./...
#   test                 go test with coverage; writes .ci/coverage.out and
#                        .ci/coverage.txt (per-function summary)
#   vuln                 govulncheck on the source and on the built binary
#   mutation [pkg[=N]...] gremlins on the given packages (default: the list
#                        in MUTATION_PACKAGES with its thresholds); writes
#                        .ci/mutation/
#   hadolint             lint the Dockerfile
#   actionlint           lint .github/workflows (with shellcheck)
#   shell                shellcheck scripts/*.sh
#   gitleaks             scan the git history for secrets
#   trivy <image>        scan a local image; fails on fixable HIGH/CRITICAL
#   trivy-sarif <image>  the same findings as SARIF on stdout (never fails)
#   sbom <image> <file>  write an SPDX JSON SBOM of a local image
#   smoke <image>        run the image, check /healthz and /version, remove it
#   all                  fmt tidy vet lint test vuln hadolint actionlint
#                        shell gitleaks
set -euo pipefail

# --- pinned tool images (tag for humans, digest for docker)
GOLANGCI_IMAGE=golangci/golangci-lint:v2.15.0@sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481
HADOLINT_IMAGE=hadolint/hadolint:v2.15.1@sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d
ACTIONLINT_IMAGE=rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667
GITLEAKS_IMAGE=ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f
TRIVY_IMAGE=aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c

# Packages under mutation test, most valuable first, each with its minimum
# test efficacy (killed / (killed + lived), in percent). The thresholds sit a
# few points under the values measured when they were set (docs/ci.md): a
# ratchet, raise them as tests improve. internal/web is left out (a run of its
# tests takes about 20 s, too slow for every mutant).
MUTATION_PACKAGES=(
	internal/plugin=87
	internal/settings=95
	internal/covers=82
	internal/jobs=83
	internal/importer=79
	internal/backup=76
	internal/library=91
)
# for packages given on the command line (or MUTATION_THRESHOLD=N to override all)
MUTATION_DEFAULT_THRESHOLD=75

cd "$(dirname "$0")/.."
root=$PWD
out=$root/.ci
# the Go image scripts/go.sh runs (the build stage image of the Dockerfile)
GO_IMAGE=$(grep -oE 'golang:[0-9.]+-[a-z]+@sha256:[0-9a-f]{64}' scripts/go.sh)
uid=$(id -u)
gid=$(id -g)

# Go caches: named volumes locally (shared with scripts/go.sh); a directory
# when GO_CACHE_DIR is set (CI restores and saves it with actions/cache).
go_cache_mounts() {
	if [ -n "${GO_CACHE_DIR:-}" ]; then
		mkdir -p "$GO_CACHE_DIR/mod" "$GO_CACHE_DIR/build"
		echo "-v $GO_CACHE_DIR/mod:/go/pkg/mod -v $GO_CACHE_DIR/build:/root/.cache/go-build"
	else
		echo "-v allister-gomod:/go/pkg/mod -v allister-gocache:/root/.cache/go-build"
	fi
}

# in_go SCRIPT: run a shell script in the pinned Go image with the repository
# at /src; files it leaves in /src/.ci are handed back to the calling user.
in_go() {
	# shellcheck disable=SC2046 # the mount options are meant to split
	docker run --rm $(go_cache_mounts) -v "$root":/src -w /src -e CGO_ENABLED=0 \
		-e HOST_UID="$uid" -e HOST_GID="$gid" "$GO_IMAGE" \
		sh -c "set -e; trap 'chown -R \$HOST_UID:\$HOST_GID /src/.ci 2>/dev/null || true' EXIT; $1"
}

# build_tools: govulncheck and gremlins from tools/go.mod into /src/.ci/bin
build_tools='mkdir -p /src/.ci/bin && (cd tools && go build -o /src/.ci/bin/ tool)'

summary() {
	# GitHub job summary when running in Actions, else stdout
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then cat >>"$GITHUB_STEP_SUMMARY"; else cat; fi
}

check=${1:-}
[ "$#" -gt 0 ] && shift
mkdir -p "$out"
case "$check" in
lint)
	# shellcheck disable=SC2046
	docker run --rm $(go_cache_mounts) -v allister-golangci-cache:/root/.cache/golangci-lint \
		-v "$root":/src -w /src "$GOLANGCI_IMAGE" golangci-lint run ./...
	;;
fmt)
	# shellcheck disable=SC2016 # expanded inside the container
	in_go 'files=$(find . -name "*.go" -not -path "./.ci/*" -exec gofmt -l {} +); if [ -n "$files" ]; then echo "gofmt needed:"; echo "$files"; exit 1; fi; echo "gofmt: ok"'
	;;
tidy)
	in_go 'go mod tidy -diff && (cd tools && go mod tidy -diff) && echo "go mod tidy: no drift"'
	;;
vet)
	in_go 'go vet ./...'
	;;
test)
	in_go 'go test -count=1 -covermode=atomic -coverprofile=.ci/coverage.out ./... && go tool cover -func=.ci/coverage.out > .ci/coverage.txt'
	total=$(awk '/^total:/ { print $3 }' "$out/coverage.txt")
	{
		echo "### Test coverage: ${total}"
		echo
		echo "| Package | Coverage |"
		echo "|---|---|"
		# per package: statement-weighted from the profile
		awk 'NR > 1 { split($1, a, ":"); pkg = a[1]; sub(/\/[^\/]*$/, "", pkg); sub(/^github\.com\/allister\/allister\//, "", pkg)
				n[pkg] += $2; if ($3 > 0) c[pkg] += $2 }
			END { for (k in n) printf "| %s | %.1f%% |\n", k, (n[k] ? 100 * c[k] / n[k] : 0) }' "$out/coverage.out" | sort
	} | summary
	;;
vuln)
	# the source (only vulnerabilities in code that is actually called fail)
	# and the built binary (the exact module versions and Go standard library
	# linked into it)
	in_go "$build_tools && .ci/bin/govulncheck ./... && go build -trimpath -o .ci/allister ./cmd/allister && .ci/bin/govulncheck -mode=binary .ci/allister"
	;;
mutation)
	entries=("$@")
	[ "${#entries[@]}" -eq 0 ] && entries=("${MUTATION_PACKAGES[@]}")
	pkgs=()
	declare -A threshold=()
	for e in "${entries[@]}"; do
		p=${e%%=*}
		t=$MUTATION_DEFAULT_THRESHOLD
		[[ $e == *=* ]] && t=${e#*=}
		for d in "${MUTATION_PACKAGES[@]}"; do
			[ "${d%%=*}" = "$p" ] && [[ $e != *=* ]] && t=${d#*=}
		done
		t=${MUTATION_THRESHOLD:-$t}
		# package directories only: the names end up in a shell script
		if ! [[ $p =~ ^[a-z][a-z0-9_/-]*$ ]] || [[ $p == *..* ]] || [ ! -d "$p" ] || ! [[ $t =~ ^[0-9]+$ ]]; then
			echo "mutation: '$e' is not a package directory with an optional =threshold (e.g. internal/plugin=85)" >&2
			exit 2
		fi
		pkgs+=("$p")
		threshold[$p]=$t
	done
	workers=${MUTATION_WORKERS:-2}
	rm -rf "$out/mutation"
	mkdir -p "$out/mutation"
	script="$build_tools; status=0"
	for p in "${pkgs[@]}"; do
		name=${p//\//-}
		script="$script; start=\$(date +%s); GOFLAGS=-count=1 .ci/bin/gremlins unleash --workers $workers --timeout-coefficient 5 -o .ci/mutation/$name.json ./$p > .ci/mutation/$name.log 2>&1 || status=1; echo \"$p \$(( \$(date +%s) - start ))\" >> .ci/mutation/times.txt; tail -n 6 .ci/mutation/$name.log"
	done
	set +e
	in_go "$script; exit \$status"
	rc=$?
	set -e
	{
		echo "### Mutation testing (gremlins)"
		echo
		echo "Efficacy = killed / (killed + lived); a package below its threshold fails the job."
		echo
		echo "| Package | Killed | Lived | Not covered | Timed out | Efficacy | Threshold | Mutant coverage | Seconds |"
		echo "|---|---|---|---|---|---|---|---|---|"
		for p in "${pkgs[@]}"; do
			name=${p//\//-}
			secs=$(awk -v p="$p" '$1 == p { print $2 }' "$out/mutation/times.txt")
			if [ -f "$out/mutation/$name.json" ]; then
				# the report is JSON; pick the totals without needing jq (the
				# timed-out count is only in the log)
				timedout=$(sed -n 's/^Timed out: \([0-9]*\).*/\1/p' "$out/mutation/$name.log")
				eff=$(tr -d ' \n' <"$out/mutation/$name.json" | grep -oE '"test_efficacy":[0-9.]+' | cut -d: -f2)
				# gremlins' own --threshold-efficacy flag does not fail in
				# v0.6.0 (the value is never read back as a number), so the
				# threshold is checked here
				if ! awk -v e="${eff:-0}" -v t="${threshold[$p]}" 'BEGIN { exit !(e + 0 >= t + 0) }'; then
					echo "mutation: $p efficacy ${eff:-?}% is below its threshold ${threshold[$p]}%" >&2
					rc=1
				fi
				tr -d ' \n' <"$out/mutation/$name.json" | grep -oE '"(test_efficacy|mutations_coverage|mutants_killed|mutants_lived|mutants_not_covered)":[0-9.]+' |
					awk -F: -v p="$p" -v s="$secs" -v to="${timedout:-0}" -v th="${threshold[$p]}" '{ gsub(/"/, "", $1); v[$1] = $2 }
						END { printf "| %s | %d | %d | %d | %d | %.2f%% | %s%% | %.2f%% | %s |\n", p, v["mutants_killed"], v["mutants_lived"], v["mutants_not_covered"], to, v["test_efficacy"], th, v["mutations_coverage"], s }'
			else
				rc=1
				echo "| $p | no report (see $name.log) | | | | | ${threshold[$p]}% | | $secs |"
			fi
		done
	} >"$out/mutation/summary.md"
	summary <"$out/mutation/summary.md"
	exit "$rc"
	;;
hadolint)
	docker run --rm -i -v "$root/.hadolint.yaml":/hadolint.yaml:ro "$HADOLINT_IMAGE" hadolint --config /hadolint.yaml - <Dockerfile
	echo "hadolint: ok"
	;;
shell)
	# the shellcheck that ships in the actionlint image
	docker run --rm -v "$root":/repo -w /repo --entrypoint shellcheck "$ACTIONLINT_IMAGE" scripts/*.sh
	echo "shellcheck: ok"
	;;
actionlint)
	docker run --rm -v "$root":/repo -w /repo "$ACTIONLINT_IMAGE" -color
	echo "actionlint: ok"
	;;
gitleaks)
	# A git worktree keeps its objects in the main repository: mount that
	# too, at the same path, so git inside the container finds it.
	common=$(cd "$(git rev-parse --git-common-dir)" && pwd)
	extra=()
	case "$common" in "$root"/*) ;; *) extra=(-v "$common:$common:ro") ;; esac
	docker run --rm -v "$root:$root:ro" "${extra[@]}" -w "$root" -e GIT_CONFIG_COUNT=1 \
		-e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' \
		"$GITLEAKS_IMAGE" git --redact --no-banner --exit-code 1 .
	;;
trivy | trivy-sarif | sbom)
	image=${1:?usage: scripts/ci.sh $check <image>}
	tar=$(mktemp "${TMPDIR:-/tmp}/allister-image.XXXXXX.tar")
	trap 'rm -f "$tar"' EXIT
	docker save -o "$tar" "$image"
	chmod 644 "$tar"
	trivy=(docker run --rm -v allister-trivy-cache:/root/.cache/trivy -v "$tar":/image.tar:ro "$TRIVY_IMAGE" image --input /image.tar --no-progress)
	if [ "$check" = trivy ]; then
		# --ignore-unfixed: only vulnerabilities with a fix available fail
		"${trivy[@]}" --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1
	elif [ "$check" = trivy-sarif ]; then
		# the same findings as SARIF on stdout, for code scanning (never fails)
		"${trivy[@]}" --quiet --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --format sarif
	else
		file=${2:?usage: scripts/ci.sh sbom <image> <file>}
		"${trivy[@]}" --quiet --format spdx-json >"$file"
		echo "sbom: wrote $file"
	fi
	;;
smoke)
	image=${1:?usage: scripts/ci.sh smoke <image>}
	port=${SMOKE_PORT:-18393}
	name=allister-ci-smoke-$$
	docker run -d --name "$name" -p "127.0.0.1:$port:8080" "$image" >/dev/null
	trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
	ok=""
	for _ in $(seq 1 30); do
		if [ "$(curl -fsS "http://127.0.0.1:$port/healthz" 2>/dev/null)" = ok ]; then
			ok=1
			break
		fi
		sleep 1
	done
	if [ -z "$ok" ]; then
		docker logs "$name" >&2 || true
		echo "smoke: /healthz did not answer ok" >&2
		exit 1
	fi
	echo "smoke: /healthz ok"
	version=$(curl -fsS "http://127.0.0.1:$port/version")
	echo "smoke: /version $version"
	case "$version" in *'"version":'*) ;; *)
		echo "smoke: /version is not the version JSON" >&2
		exit 1
		;;
	esac
	;;
all)
	for c in fmt tidy vet lint test vuln hadolint actionlint shell gitleaks; do
		echo "== $c"
		"$0" "$c"
	done
	;;
*)
	sed -n '2,28s/^# \{0,1\}//p' "$0" >&2
	exit 2
	;;
esac
