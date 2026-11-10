#!/usr/bin/env bash
# Print release notes (Markdown) for a tag: the commits since the previous
# version tag, grouped by their Conventional Commit prefix (feat, fix, docs,
# build/ci, test; a "!" before the colon or a BREAKING CHANGE footer marks a
# breaking change). Merge commits are left out.
#
# Usage: scripts/release-notes.sh <tag-or-ref> [<previous-ref>]
#   Without <previous-ref>, the newest v* tag before <tag-or-ref> is used
#   (the whole history for the first release).
set -euo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	sed -n '7,9s/^# \{0,1\}//p' "$0" >&2
	exit 2
fi
ref=$1
cd "$(dirname "$0")/.."
git rev-parse -q --verify "$ref^{commit}" >/dev/null || {
	echo "release-notes: unknown ref '$ref'" >&2
	exit 1
}
prev=${2:-$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$ref^" 2>/dev/null || true)}
range=$ref
[ -n "$prev" ] && range="$prev..$ref"

declare -A titles=(
	[breaking]="Breaking changes" [feat]="Features" [fix]="Fixes" [docs]="Documentation"
	[build]="Build and CI" [test]="Tests" [other]="Other changes"
)
order=(breaking feat fix docs build test other)
declare -A lines=()

# %x1f separates subject, short hash and body; %x1e ends a commit
while IFS=$'\x1f' read -r -d $'\x1e' subject hash body; do
	subject=${subject#$'\n'}
	kind=other
	if [[ $subject =~ ^([a-z]+)(\([^\)]*\))?(!)?:\ (.*)$ ]]; then
		case "${BASH_REMATCH[1]}" in
		feat) kind=feat ;;
		fix) kind=fix ;;
		docs) kind=docs ;;
		build | ci | chore) kind=build ;;
		test) kind="test" ;;
		esac
		[ -n "${BASH_REMATCH[3]}" ] && kind=breaking
	fi
	[[ $body == *"BREAKING CHANGE:"* ]] && kind=breaking
	lines[$kind]+="- ${subject} (${hash})"$'\n'
done < <(git log --no-merges --format='%s%x1f%h%x1f%b%x1e' "$range")

echo "## Changes${prev:+ since $prev}"
any=0
for k in "${order[@]}"; do
	[ -n "${lines[$k]:-}" ] || continue
	any=1
	printf '\n### %s\n\n%s' "${titles[$k]}" "${lines[$k]}"
done
[ "$any" -eq 1 ] || printf '\nNo changes.\n'
