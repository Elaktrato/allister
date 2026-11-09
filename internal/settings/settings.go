// Package settings is a key/value store backed by the settings table.
package settings

import (
	"context"
	"database/sql"
	"errors"
)

const (
	// KeyTitleLang is the setting holding the preferred title language tag.
	KeyTitleLang = "title_lang"
	// DefaultTitleLang is the display language when none is stored.
	DefaultTitleLang = "ja"
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

// TitleLang returns the preferred title language, defaulting to "ja" when
// unset, empty or unreadable.
func (s *Store) TitleLang(ctx context.Context) string {
	v, ok, err := s.Get(ctx, KeyTitleLang)
	if err != nil || !ok || v == "" {
		return DefaultTitleLang
	}
	return v
}

// KnownLanguages returns the distinct language tags found in stored anime
// titles, sorted. The settings page uses it to offer languages beyond the
// built-in ja, ja-latn and en.
func (s *Store) KnownLanguages(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT lang FROM anime_titles ORDER BY lang`)
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
