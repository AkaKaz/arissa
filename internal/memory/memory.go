// Package memory implements arissa's SQLite-backed persistence.
//
// Two tables:
//
//	turns    — conversation history per Slack user (rolling window).
//	memories — long-term key-value facts ("remember X"). NULL user_id
//	           denotes a global fact.
//
// Backed by modernc.org/sqlite so no CGo is required.
package memory

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"arissa/internal/config"
)

const schema = `
CREATE TABLE IF NOT EXISTS turns (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    TEXT    NOT NULL,
  role       TEXT    NOT NULL,
  content    TEXT    NOT NULL,
  created_at TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS memories (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    TEXT,
  key        TEXT    NOT NULL,
  value      TEXT    NOT NULL,
  created_at TEXT    DEFAULT (datetime('now')),
  updated_at TEXT    DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_turns_user       ON turns(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_memories_user_key ON memories(user_id, key);
`

// Memory wraps the SQLite store.
type Memory struct {
	db *sql.DB
}

// Open opens (or creates) the database file and ensures the schema.
func Open(cfg *config.Config) (*Memory, error) {
	db, err := sql.Open("sqlite", cfg.Memory.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set WAL: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Memory{db: db}, nil
}

// Close releases the database handle.
func (m *Memory) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

// SaveTurn persists one conversation turn.
func (m *Memory) SaveTurn(userID, role, content string) error {
	if m == nil {
		return nil
	}
	_, err := m.db.Exec(
		`INSERT INTO turns (user_id, role, content) VALUES (?, ?, ?)`,
		userID, role, content,
	)
	return err
}

// ClearTurns deletes all stored turns for a user.
func (m *Memory) ClearTurns(userID string) error {
	if m == nil {
		return nil
	}
	_, err := m.db.Exec(`DELETE FROM turns WHERE user_id = ?`, userID)
	return err
}

// Remember upserts a long-term fact. userID may be empty for global.
func (m *Memory) Remember(userID, key, value string) error {
	if m == nil {
		return nil
	}
	var uid any = userID
	if userID == "" {
		uid = nil
	}
	var existing int64
	err := m.db.QueryRow(
		`SELECT id FROM memories WHERE user_id IS ? AND key = ?`,
		uid, key,
	).Scan(&existing)
	switch {
	case err == sql.ErrNoRows:
		_, err = m.db.Exec(
			`INSERT INTO memories (user_id, key, value) VALUES (?, ?, ?)`,
			uid, key, value,
		)
	case err == nil:
		_, err = m.db.Exec(
			`UPDATE memories SET value = ?, updated_at = datetime('now') WHERE id = ?`,
			value, existing,
		)
	}
	return err
}

// Recall returns the value for a key. Looks up the user-specific
// entry first, falls back to the global one.
func (m *Memory) Recall(userID, key string) (string, error) {
	if m == nil {
		return "", nil
	}
	var uid any = userID
	if userID == "" {
		uid = nil
	}
	var value string
	err := m.db.QueryRow(
		`SELECT value FROM memories
		 WHERE (user_id IS ? OR user_id IS NULL) AND key = ?
		 ORDER BY user_id DESC LIMIT 1`,
		uid, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// Forget removes a user-specific memory. Returns true if a row was
// deleted.
func (m *Memory) Forget(userID, key string) (bool, error) {
	if m == nil {
		return false, nil
	}
	var uid any = userID
	if userID == "" {
		uid = nil
	}
	res, err := m.db.Exec(
		`DELETE FROM memories WHERE user_id IS ? AND key = ?`,
		uid, key,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
