package db

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	store := &Store{DB: db}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS suppliers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			url TEXT NOT NULL,
			method TEXT NOT NULL DEFAULT 'POST',
			headers TEXT NOT NULL DEFAULT '{}',
			retry_max_attempts INTEGER NOT NULL DEFAULT 15,
			retry_base_delay_ms INTEGER NOT NULL DEFAULT 1000,
			retry_max_delay_ms INTEGER NOT NULL DEFAULT 240000,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			supplier TEXT NOT NULL,
			url TEXT NOT NULL,
			method TEXT NOT NULL DEFAULT 'POST',
			headers TEXT NOT NULL DEFAULT '{}',
			body TEXT NOT NULL DEFAULT '{}',
			idempotency_key TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 15,
			next_retry_at TEXT,
			dead_reason TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS delivery_attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			notification_id TEXT NOT NULL,
			attempt_number INTEGER NOT NULL,
			status TEXT NOT NULL,
			response_status INTEGER,
			response_body TEXT,
			error_message TEXT,
			attempted_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_status_next_retry
			ON notifications(status, next_retry_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idempotency
			ON notifications(idempotency_key)`,
	}
	for _, q := range queries {
		if _, err := s.DB.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
