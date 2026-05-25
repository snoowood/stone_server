package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// schema mirrors the PostgreSQL migrations with SQLite-compatible types.
// TEXT stores UUIDs and timestamps (RFC3339); REAL stores NUMERIC; INTEGER stores BOOLEAN.
const schema = `
CREATE TABLE IF NOT EXISTS players (
    id         TEXT PRIMARY KEY,
    steam_id   TEXT UNIQUE NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    last_login TEXT
);

CREATE TABLE IF NOT EXISTS player_states (
    player_id         TEXT PRIMARY KEY REFERENCES players(id),
    enlightenment_pts REAL    NOT NULL DEFAULT 0,
    time_stone_count  INTEGER NOT NULL DEFAULT 0 CHECK (time_stone_count <= 3),
    streak_days       INTEGER NOT NULL DEFAULT 0,
    last_login_date   TEXT,
    next_gacha_at     TEXT,
    enlightenment_rate REAL   NOT NULL DEFAULT 1.0,
    last_sync_at      TEXT,
    updated_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE IF NOT EXISTS inventories (
    id          TEXT PRIMARY KEY,
    player_id   TEXT NOT NULL REFERENCES players(id),
    item_id     TEXT NOT NULL,
    rarity      TEXT NOT NULL,
    acquired_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (player_id, item_id)
);

CREATE TABLE IF NOT EXISTS gacha_logs (
    id              TEXT PRIMARY KEY,
    player_id       TEXT NOT NULL REFERENCES players(id),
    item_id         TEXT NOT NULL,
    rarity          TEXT NOT NULL,
    is_duplicate    INTEGER NOT NULL DEFAULT 0,
    refund_points   REAL    NOT NULL DEFAULT 0,
    cost_points     REAL    NOT NULL DEFAULT 0,
    gacha_seed_hash TEXT    NOT NULL DEFAULT '',
    pulled_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_gacha_logs_player_pulled_at
    ON gacha_logs (player_id, pulled_at DESC);

CREATE TABLE IF NOT EXISTS player_achievements (
    player_id      TEXT NOT NULL REFERENCES players(id),
    achievement_id TEXT NOT NULL,
    unlocked_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    steam_synced   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (player_id, achievement_id)
);

CREATE INDEX IF NOT EXISTS idx_player_achievements_player
    ON player_achievements(player_id);

CREATE TABLE IF NOT EXISTS achievement_retry_queue (
    id             TEXT PRIMARY KEY,
    player_id      TEXT NOT NULL REFERENCES players(id),
    achievement_id TEXT NOT NULL,
    retry_count    INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    next_retry_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    resolved_at    TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_ach_retry_next
    ON achievement_retry_queue(next_retry_at)
    WHERE resolved_at IS NULL;
`

// New opens (or creates) a SQLite database at path, enables WAL mode, and applies the schema.
func New(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	ctx := context.Background()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite %s: %w", pragma, err)
		}
	}

	// Serialize writes; WAL allows concurrent readers.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// Legacy cleanup: M0 (migrations/000010) dropped player_states.pity_count.
	// SQLite mode bypasses migrations/, so apply the same change here for reused DBs.
	// On fresh DBs the column is absent; the error is intentionally swallowed.
	_, _ = db.ExecContext(ctx, "ALTER TABLE player_states DROP COLUMN pity_count")

	return db, nil
}
