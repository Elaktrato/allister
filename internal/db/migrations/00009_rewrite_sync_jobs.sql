-- +goose Up
-- The anime-only sync job (kind sync_anime, payload {"anime_id","plugin_id"})
-- became the generic sync_item job (payload {"item_id","plugin_id"}); no
-- legacy handling stays in code, so queued legacy rows are rewritten here.
--
-- Every row still in the queue (pending, running or failed: a failed job
-- can be revived by the startup sweep's item, and a running one is reset by
-- Recover) is rewritten; a missing plugin_id becomes 'anime', the only
-- plugin that ever had legacy jobs. Done rows are history and stay as they
-- are. A row whose payload is not JSON or has no integer anime_id is left
-- untouched: no handler serves sync_anime any more, so it fails as an
-- unknown kind instead of syncing a wrong item.
--
-- 00008 is unused; later migrations continue at 00010 (goose cannot apply a
-- lower number to a database already at a higher version).
UPDATE jobs
SET kind = 'sync_item',
    payload_json = json_object(
        'item_id', json_extract(payload_json, '$.anime_id'),
        'plugin_id', COALESCE(json_extract(payload_json, '$.plugin_id'), 'anime'))
WHERE kind = 'sync_anime'
  AND state IN ('pending', 'running', 'failed')
  AND CASE WHEN json_valid(payload_json)
           THEN json_type(payload_json, '$.anime_id') = 'integer'
           ELSE 0 END;

-- +goose Down
-- Restores the legacy shape for PENDING rows of the anime plugin only: the
-- legacy handler never ran items of other plugins, and running or failed
-- rows are left generic (Recover resets running rows to pending on the next
-- start anyway; after a down they then fail as an unknown kind).
UPDATE jobs
SET kind = 'sync_anime',
    payload_json = json_object(
        'anime_id', json_extract(payload_json, '$.item_id'),
        'plugin_id', 'anime')
WHERE kind = 'sync_item'
  AND state = 'pending'
  AND CASE WHEN json_valid(payload_json)
           THEN json_type(payload_json, '$.item_id') = 'integer'
                AND json_extract(payload_json, '$.plugin_id') = 'anime'
           ELSE 0 END;
