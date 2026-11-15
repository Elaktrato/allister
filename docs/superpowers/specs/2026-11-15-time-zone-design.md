# Time zone for day grouping (short design, 2026-11-15)

Owner asked to fix the known gap: the home page's "Coming up" headings and the
calendar page's day columns are grouped in the server's zone (UTC in Docker)
while app.js rewrites each time to the browser's zone, so a 23:30 UTC release
shows "00:30" under the wrong day.

- **Setting** "Time zone" under Settings > General: an IANA zone name
  (`settings` key `time_zone`), validated with `time.LoadLocation`. Default:
  the `TZ` environment variable if it names a valid zone, else UTC. A
  "Use this browser's zone" button (app.js, no inline script) fills the field
  from `Intl.DateTimeFormat().resolvedOptions().timeZone`.
- The Go binary embeds the zone database (`import _ "time/tzdata"` in
  cmd/allister) because the distroless image has none.
- **Server-side grouping and labels** in that zone: home "Coming up" (day
  headings and times), calendar page (days and times). The browser-local
  rewrite of those times (`data-local` in app.js) is removed for them so
  headings and times always agree. The `.ics` feed stays UTC (clients
  convert).
- Backup carries the setting like the other general settings.
- Approaches: browser-side regrouping in app.js was rejected (rebuilds the
  grouping in JS, still wrong without JS); a per-request zone from the
  browser was rejected (no stable server state, the feed and the page could
  differ).
