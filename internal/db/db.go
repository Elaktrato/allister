// Package db opens the SQLite database and applies embedded migrations.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"net/url"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens (creating if needed) the SQLite database at path, applies
// pragmas to every pooled connection (foreign keys, WAL, busy timeout) and
// runs all pending migrations.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()

	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		d.Close()
		return nil, err
	}
	if err := goose.Up(d, "migrations"); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}
