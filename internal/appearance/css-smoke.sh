#!/bin/sh
# Smoke check of the BUILT Tailwind stylesheet (run by the Dockerfile's css
# stage, usage: css-smoke.sh app.css): the colour classes resolve to theme
# tokens (an opacity variant, a divide, a ring, a hover variant), the accent
# rule re-points text-white, and no fixed colour is left except Tailwind's
# preflight defaults that input.css overrides with tokens.
set -eu
css="$1"
fail=0
need() {
	if ! grep -qF -- "$1" "$css"; then
		echo "css-smoke: $css lacks: $1" >&2
		fail=1
	fi
}
need '.bg-slate-900\/40{background-color:rgb(var(--al-surface)/.4)}'
need '.divide-slate-800>:not([hidden])~:not([hidden]){--tw-divide-opacity:1;border-color:rgb(var(--al-border-muted)/'
need '.focus-visible\:ring-indigo-500:focus-visible{--tw-ring-opacity:1;--tw-ring-color:rgb(var(--al-focus)/'
need '.hover\:bg-slate-800:hover{--tw-bg-opacity:1;background-color:rgb(var(--al-raised)/'
need '.bg-indigo-600{--al-text-strong:var(--al-accent-text);color:rgb(var(--al-accent-text))}'
need '.text-white{--tw-text-opacity:1;color:rgb(var(--al-text-strong)/'
# fixed colours: only the preflight defaults (#0000 transparent shadows,
# the ring offset #fff, the placeholder #9ca3af and the default ring colour,
# all replaced by token rules in input.css) may remain
left=$(grep -oE '#[0-9a-fA-F]{3,8}\b|rgba?\([0-9][^)]*\)|hsla?\([0-9][^)]*\)' "$css" | grep -vxE '#0000|#fff|#9ca3af|rgba\(147,197,253,\.5\)' | sort -u || true)
if [ -n "$left" ]; then
	echo "css-smoke: fixed colours left in $css:" $left >&2
	fail=1
fi
[ "$fail" -eq 0 ] && echo "css-smoke: ok" >&2
exit "$fail"
