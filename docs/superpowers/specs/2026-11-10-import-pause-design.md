# Import pause on plugin disable (design, 2026-11-10)

Small change, agreed with the owner in conversation.

## Why

Switching a plugin off while its import runs makes the runner return an error
wrapping `jobs.ErrRetryLater` (`internal/importer/runner.go`, the
`IsEnabled` check). The queue counts that as a partial run (bounded by
`maxPartials`) and pushes `run_after` by a backoff. So a deliberate,
indefinite pause burns one of ten partial slots — disabling and re-enabling
the plugin ten times during one import fails the job with "gave up after N
partial runs" — and every resume waits a backoff it does not need, because
the plugin gate already holds the job while the plugin is off. Also, the
disabled check runs before the pending-row check, so a pause right after the
last row was processed leaves the import "running" until the plugin is
enabled again, even though nothing is left to do.

## Change

- `jobs.ErrPaused`: a handler error wrapping it puts the job back to pending
  with its `last_error`, without counting an attempt or a partial and without
  touching `run_after`. The plugin gate holds such a job while the plugin is
  disabled, so a pause can repeat forever without failing, and it runs as
  soon as the plugin is enabled again. A handler must only pause when
  something (the gate) holds the job until then.
- The import runner's disabled check wraps `jobs.ErrPaused` instead of
  `jobs.ErrRetryLater`, and moves after the pending-row check: no rows left
  means the import finishes `done`; rows left means pause.
- `ErrRetryLater` keeps its meaning (the source is not answering): the
  runner's rate-limit path and `sync_item`'s uses are unchanged.

## Approaches considered

- Keep using `ErrRetryLater`: rejected — it burns a partial slot and delays
  the resume for what is a deliberate pause that can repeat any number of
  times.
- Return nil and let the gate re-run the job: rejected — nil marks the job
  done and the import would never resume.

## Tests

`internal/jobs`: an `ErrPaused` run goes back to pending with attempts 0,
partials 0 and an unchanged `run_after`; repeated pauses beyond both caps
never fail the job. `internal/importer`: pausing with rows left keeps the
import running and wraps `ErrPaused`; disabling the plugin during the last
row still finishes the import as done.
