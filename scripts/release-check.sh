#!/usr/bin/env bash
# Check that a release tag can be cut, then print the commands. It never
# creates or pushes anything.
#
# Usage: scripts/release-check.sh vMAJOR.MINOR.PATCH[-rc.N]
#          dry run before tagging: the tag is a semantic version that does
#          not exist yet, the working tree is clean, HEAD is main (and up to
#          date with origin/main when there is a remote), go vet and the full
#          test suite pass (scripts/go.sh); then prints the tag and push
#          commands
#        scripts/release-check.sh --tag-only vX.Y.Z[-rc.N]
#          only the tag checks the release workflow runs: semantic version,
#          and the tagged commit (HEAD) is on main (origin/main, else main)
set -euo pipefail

usage() {
	sed -n '5,14s/^# \{0,1\}//p' "$0"
}

tag_only=0
if [ "${1:-}" = --tag-only ]; then
	tag_only=1
	shift
fi
if [ "$#" -ne 1 ] || [ "${1:-}" = -h ] || [ "${1:-}" = --help ]; then
	usage >&2
	exit 2
fi
tag=$1
cd "$(dirname "$0")/.."

fail() {
	echo "release-check: $*" >&2
	exit 1
}

# vMAJOR.MINOR.PATCH, optionally -rc.N; no leading zeros (semver 2.0.0)
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$'
[[ $tag =~ $semver ]] || fail "'$tag' is not vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N"
echo "release-check: $tag is a semantic version"

main_ref=main
if git rev-parse -q --verify refs/remotes/origin/main >/dev/null; then
	main_ref=origin/main
fi
git rev-parse -q --verify "$main_ref" >/dev/null || fail "no $main_ref branch"
git merge-base --is-ancestor HEAD "$main_ref" || fail "HEAD ($(git rev-parse --short HEAD)) is not on $main_ref"
echo "release-check: $(git rev-parse --short HEAD) is on $main_ref"

if [ "$tag_only" -eq 1 ]; then
	exit 0
fi

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	fail "tag $tag exists already"
fi
if git remote get-url origin >/dev/null 2>&1; then
	if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
		fail "tag $tag exists on origin already"
	fi
else
	echo "release-check: no remote 'origin' (nothing to compare with)"
fi
[ -z "$(git status --porcelain)" ] || fail "the working tree is not clean (commit or stash first)"
branch=$(git symbolic-ref --quiet --short HEAD || true)
[ "$branch" = main ] || fail "on '${branch:-detached HEAD}', not main"
if [ "$main_ref" = origin/main ] && [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
	fail "main and origin/main differ (pull or push first)"
fi
latest=$(git tag --list 'v*' --sort=-v:refname | head -n 1)
echo "release-check: latest tag ${latest:-none}"

echo "release-check: go vet ./..."
scripts/go.sh vet ./...
echo "release-check: go test ./..."
scripts/go.sh test -count=1 ./...

cat <<EOF

release-check: ready. To release $tag:

  git tag -a $tag -m "Allister $tag"
  git push origin $tag

The Release workflow then checks the tag again, runs CI, publishes the image
and the GitHub release. Nothing was tagged or pushed.
EOF
