#!/usr/bin/env bash
# Refresh the pins recorded in PINS.md: the digest of every pinned image
# (name:tag@sha256:...) in the Dockerfile, scripts/go.sh and README.md, and the
# per-architecture sha256 of the Tailwind CLI release named by the Dockerfile's
# TAILWIND_VERSION. It also checks the vendored htmx file, icon font and
# bundled app fonts against their .sha256 files.
#
# Usage: scripts/update-pins.sh          print the changes as a diff (no
#                                        writes; exits 1 when a pin is stale)
#        scripts/update-pins.sh --write  apply them (each file is written
#                                        through a rename, never half-written)
#
# Tags are kept: to move to another version, edit the tag by hand first (and
# for Go also go.mod's toolchain line), then run this script to resolve the
# digest. Needs docker (buildx), curl, sha256sum, awk and diff.
set -euo pipefail

usage() {
	sed -n '2,13s/^# \{0,1\}//p' "$0"
}

write=0
case "${1:-}" in
"") ;;
--write) write=1 ;;
-h | --help)
	usage
	exit 0
	;;
*)
	usage >&2
	exit 2
	;;
esac
if [ "$#" -gt 1 ]; then
	usage >&2
	exit 2
fi

cd "$(dirname "$0")/.."

for tool in docker curl sha256sum awk diff grep sed stat mktemp; do
	command -v "$tool" >/dev/null || {
		echo "update-pins: $tool is required" >&2
		exit 1
	}
done

files=(Dockerfile scripts/go.sh README.md PINS.md)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for f in "${files[@]}"; do
	mkdir -p "$tmp/new/$(dirname "$f")"
	cp "$f" "$tmp/new/$f"
done

# replace OLD NEW: literal (not regex) replacement in every copy
replace() {
	local f
	for f in "${files[@]}"; do
		OLD="$1" NEW="$2" awk '
			BEGIN { o = ENVIRON["OLD"]; n = ENVIRON["NEW"] }
			{
				out = ""; s = $0
				while ((i = index(s, o)) > 0) { out = out substr(s, 1, i - 1) n; s = substr(s, i + length(o)) }
				print out s
			}' "$tmp/new/$f" >"$tmp/new/$f.tmp"
		mv "$tmp/new/$f.tmp" "$tmp/new/$f"
	done
}

hex64='^[0-9a-f]{64}$'

# --- images: resolve each name:tag to its current (multi-arch index) digest
refs=$(grep -ohE '[a-z0-9][a-z0-9._/:-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}' Dockerfile scripts/go.sh README.md | sort -u)
if [ -z "$refs" ]; then
	echo "update-pins: no pinned image references found" >&2
	exit 1
fi
for ref in $refs; do
	tag=${ref%@*}
	old=${ref#*@}
	new=$(docker buildx imagetools inspect "$tag" | awk '$1 == "Digest:" && d == "" { d = $2 } END { print d }')
	if ! [[ ${new#sha256:} =~ $hex64 ]] || [ "${new%%:*}" != sha256 ]; then
		echo "update-pins: could not resolve $tag (got '$new')" >&2
		exit 1
	fi
	echo "image $tag: $new" >&2
	if [ "$new" != "$old" ]; then
		replace "$tag@$old" "$tag@$new"
	fi
done

# --- Tailwind CLI: hash each release binary, cross-checked against the
# release's sha256sums.txt when it has one, else against a second download
version=$(sed -n 's/^ARG TAILWIND_VERSION=//p' Dockerfile)
if ! [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "update-pins: TAILWIND_VERSION '$version' is not an exact release" >&2
	exit 1
fi
base="https://github.com/tailwindlabs/tailwindcss/releases/download/$version"
mkdir -p "$tmp/tw"
published=""
if curl -fsSL -o "$tmp/tw/sha256sums.txt" "$base/sha256sums.txt"; then
	published="$tmp/tw/sha256sums.txt"
fi
for pair in AMD64:x64 ARM64:arm64; do
	arg=TAILWIND_SHA256_${pair%%:*}
	asset=tailwindcss-linux-${pair#*:}
	curl -fsSL -o "$tmp/tw/$asset" "$base/$asset"
	sum=$(sha256sum "$tmp/tw/$asset" | awk '{ print $1 }')
	if [ -n "$published" ]; then
		want=$(awk -v a="$asset" '{ n = $2; sub(/^\.\//, "", n) } n == a { print $1 }' "$published")
		if [ "$sum" != "$want" ]; then
			echo "update-pins: $asset sha256 $sum does not match sha256sums.txt ('$want')" >&2
			exit 1
		fi
	else
		curl -fsSL -o "$tmp/tw/$asset.2" "$base/$asset"
		if [ "$sum" != "$(sha256sum "$tmp/tw/$asset.2" | awk '{ print $1 }')" ]; then
			echo "update-pins: two downloads of $asset differ" >&2
			exit 1
		fi
	fi
	old=$(sed -n "s/^ARG $arg=//p" Dockerfile)
	if ! [[ $old =~ $hex64 ]] || ! [[ $sum =~ $hex64 ]]; then
		echo "update-pins: bad checksum for $arg (old '$old', new '$sum')" >&2
		exit 1
	fi
	echo "tailwind $version $asset: $sum" >&2
	if [ "$sum" != "$old" ]; then
		replace "$old" "$sum"
	fi
done

# --- htmx is vendored: never rewritten here, only checked
(cd internal/web/static && sha256sum -c --quiet htmx.min.js.sha256) || {
	echo "update-pins: internal/web/static/htmx.min.js does not match htmx.min.js.sha256" >&2
	exit 1
}
echo "htmx $(grep -o 'version:"[0-9.]*"' internal/web/static/htmx.min.js | head -n 1): checksum ok" >&2

# --- the Material Symbols font is vendored too: never rewritten, only checked
(cd internal/web/static && sha256sum -c --quiet material-symbols-outlined-v383.woff2.sha256) || {
	echo "update-pins: internal/web/static/material-symbols-outlined-v383.woff2 does not match its .sha256" >&2
	exit 1
}
(cd internal/web/testdata && sha256sum -c --quiet material-symbols-outlined.codepoints.sha256) || {
	echo "update-pins: internal/web/testdata/material-symbols-outlined.codepoints does not match its .sha256" >&2
	exit 1
}
echo "material symbols v383: checksum ok (font and name list)" >&2

# --- the bundled app fonts are vendored: never rewritten, only checked
for sum in internal/web/static/fonts/*.woff2.sha256; do
	(cd internal/web/static/fonts && sha256sum -c --quiet "$(basename "$sum")") || {
		echo "update-pins: ${sum%.sha256} does not match its .sha256" >&2
		exit 1
	}
done
echo "bundled fonts (Inter, Source Serif 4, JetBrains Mono): checksums ok" >&2

changed=0
for f in "${files[@]}"; do
	if ! diff -u --label "a/$f" --label "b/$f" "$f" "$tmp/new/$f"; then
		changed=1
	fi
done
if [ "$changed" -eq 0 ]; then
	echo "update-pins: all pins are current" >&2
	exit 0
fi
if [ "$write" -eq 1 ]; then
	for f in "${files[@]}"; do
		# write through a rename (never a half-written file) and keep the
		# original file's mode (go.sh stays executable)
		if ! cp "$tmp/new/$f" "$f.tmp" || ! chmod "$(stat -c '%a' "$f")" "$f.tmp" || ! mv "$f.tmp" "$f"; then
			rm -f "$f.tmp"
			echo "update-pins: could not write $f" >&2
			exit 1
		fi
	done
	echo "update-pins: written. Run scripts/go.sh test ./internal/guard/ ./internal/web/ and docker compose build." >&2
else
	echo "update-pins: dry run, nothing written; pins are stale (pass --write to apply)" >&2
	exit 1
fi
