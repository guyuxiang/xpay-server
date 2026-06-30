package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/glebarez/sqlite"
)

// DB wraps a *sql.DB for the payment store.
type DB struct {
	sql *sql.DB
}

// Open opens (or creates) the SQLite database at path.
func Open(path string) (*DB, error) {
	if err := ensureDBDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{sql: db}, nil
}

func ensureDBDir(path string) error {
	dir := dbDir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite database directory %q: %w", dir, err)
	}
	return nil
}

func dbDir(path string) string {
	if path == "" || path == ":memory:" {
		return ""
	}
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err != nil {
			return ""
		}
		if strings.Contains(u.RawQuery, "mode=memory") || u.Opaque == ":memory:" || u.Path == ":memory:" {
			return ""
		}
		if u.Opaque != "" {
			return filepath.Dir(u.Opaque)
		}
		return filepath.Dir(u.Path)
	}
	return filepath.Dir(path)
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS payments (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		from_address     TEXT NOT NULL,
		to_address       TEXT NOT NULL,
		amount           INTEGER NOT NULL,
		tx_hash          TEXT NOT NULL,
		model            TEXT NOT NULL,
		prompt_tokens    INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		request_id       TEXT NOT NULL,
		network          TEXT NOT NULL,
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_payments_from_created ON payments(from_address, created_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_network_tx_hash ON payments(network, tx_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_request_id ON payments(request_id)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS model_prices (
			model      TEXT PRIMARY KEY,
			input      TEXT NOT NULL,
			output     TEXT NOT NULL,
			cached_input TEXT NOT NULL DEFAULT '',
			is_default INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE model_prices ADD COLUMN cached_input TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			if stmt == `ALTER TABLE model_prices ADD COLUMN cached_input TEXT NOT NULL DEFAULT ''` && strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return err
		}
	}
	return nil
}

func (d *DB) Close() error { return d.sql.Close() }
