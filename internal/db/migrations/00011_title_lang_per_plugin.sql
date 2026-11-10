-- +goose Up
-- The title language becomes a setting of each plugin
-- (plugin.<id>.title_lang). The old global value was chosen while the anime
-- plugin was the only plugin, so it moves to that plugin; an empty value is
-- dropped (the plugin's default applies).
INSERT INTO settings(key, value)
SELECT 'plugin.anime.title_lang', value FROM settings WHERE key = 'title_lang' AND value <> ''
ON CONFLICT(key) DO NOTHING;
DELETE FROM settings WHERE key = 'title_lang';

-- +goose Down
INSERT INTO settings(key, value)
SELECT 'title_lang', value FROM settings WHERE key = 'plugin.anime.title_lang'
ON CONFLICT(key) DO NOTHING;
DELETE FROM settings WHERE key = 'plugin.anime.title_lang';
