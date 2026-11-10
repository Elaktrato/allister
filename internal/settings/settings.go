// Package settings is a key/value store backed by the settings table.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Get returns the value for key; ok is false when the key is not set.
func (s *Store) Get(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// Set stores value for key, overwriting any existing value.
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// titleLangKey is the settings key holding a plugin's title language.
func titleLangKey(id string) string { return "plugin." + id + ".title_lang" }

// TitleLang returns the title language chosen for plugin id, or "" when none
// is stored (or it is unreadable): the caller then uses the plugin's default.
func (s *Store) TitleLang(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, titleLangKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using the plugin default)", titleLangKey(id), err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

// SetTitleLang stores the title language of plugin id. The caller checks it
// against the languages the plugin offers.
func (s *Store) SetTitleLang(ctx context.Context, id, lang string) error {
	return s.Set(ctx, titleLangKey(id), lang)
}

// KnownLanguages returns the distinct language tags found in the stored
// titles of plugin id's items, sorted. A plugin's settings page offers them
// next to the languages its manifest declares.
func (s *Store) KnownLanguages(ctx context.Context, pluginID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT t.lang FROM item_titles t JOIN items i ON i.id = t.item_id
		WHERE i.plugin_id = ? ORDER BY t.lang`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// columnsKey is the settings key holding the visible table columns of a
// plugin's kind.
func columnsKey(id, kind string) string { return "plugin." + id + ".columns." + kind }

// homeViewKey and homeStatusesKey are the settings keys holding a plugin's
// home list configuration: the view its list opens in and the status it
// opens with.
func homeViewKey(id string) string     { return "plugin." + id + ".home.view" }
func homeStatusesKey(id string) string { return "plugin." + id + ".home.statuses" }

// HomeView returns the view the plugin's list opens in, "cards" or "table".
// Nothing stored (or unreadable) means "cards".
func (s *Store) HomeView(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, homeViewKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using cards)", homeViewKey(id), err)
		return "cards"
	}
	if !ok || (v != "cards" && v != "table") {
		return "cards"
	}
	return v
}

// SetHomeView stores the view the plugin's list opens in. The caller checks
// the value ("cards" or "table").
func (s *Store) SetHomeView(ctx context.Context, id, view string) error {
	return s.Set(ctx, homeViewKey(id), view)
}

// HomeStatuses returns the status class the plugin's list opens with, ""
// for every status. The caller validates the value against the core
// statuses (it is stored checked).
func (s *Store) HomeStatuses(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, homeStatusesKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using every status)", homeStatusesKey(id), err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

// SetHomeStatuses stores the status class the plugin's list opens with (""
// for every status). The caller checks the value.
func (s *Store) SetHomeStatuses(ctx context.Context, id, status string) error {
	return s.Set(ctx, homeStatusesKey(id), status)
}

// Columns returns the visible table columns chosen for kind of plugin id, in
// order (stored as a JSON list of column keys). ok is false when nothing is
// stored or the value is not a JSON list of strings: the kind's defaults
// apply. The caller checks the keys against the columns the kind offers.
func (s *Store) Columns(ctx context.Context, id, kind string) (keys []string, ok bool) {
	v, found, err := s.Get(ctx, columnsKey(id, kind))
	if err != nil {
		log.Printf("settings: read %s: %v (using the defaults)", columnsKey(id, kind), err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	if err := json.Unmarshal([]byte(v), &keys); err != nil || keys == nil {
		log.Printf("settings: %s is not a list of column keys (using the defaults)", columnsKey(id, kind))
		return nil, false
	}
	return keys, true
}

// SetColumns stores the visible table columns of kind of plugin id (an empty
// list shows the title only). The caller has checked the keys.
func (s *Store) SetColumns(ctx context.Context, id, kind string, keys []string) error {
	if keys == nil {
		keys = []string{}
	}
	b, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return s.Set(ctx, columnsKey(id, kind), string(b))
}

// ResetColumns removes the column choice of kind of plugin id (back to the
// kind's defaults).
func (s *Store) ResetColumns(ctx context.Context, id, kind string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, columnsKey(id, kind))
	return err
}

// enabledKey is the settings key holding a plugin's enabled flag.
func enabledKey(id string) string { return "plugin." + id + ".enabled" }

// IsEnabled reports whether plugin id is switched on. A missing (or
// unrecognised) value means "use def", the manifest default. It implements
// plugin.EnabledStore.
func (s *Store) IsEnabled(ctx context.Context, id string, def bool) (bool, error) {
	v, ok, err := s.Get(ctx, enabledKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using default %v)", enabledKey(id), err, def)
		return def, err
	}
	switch {
	case !ok:
		return def, nil
	case v == "1":
		return true, nil
	case v == "0":
		return false, nil
	}
	return def, nil
}

// SetEnabled stores the plugin's enabled flag as "1" or "0".
func (s *Store) SetEnabled(ctx context.Context, id string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.Set(ctx, enabledKey(id), v)
}
