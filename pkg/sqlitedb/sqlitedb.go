package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
    count       INTEGER NOT NULL DEFAULT 1,
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

CREATE TABLE IF NOT EXISTS wish_cairn_slots (
    player_id  TEXT NOT NULL REFERENCES players(id),
    slot_index INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    claimed_at TEXT,
    PRIMARY KEY (player_id, slot_index)
);

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
// slotCount / phaseOffsetSeconds drive the WishCairn backfill (game-config.xml derived,
// not hardcoded) — see backfillCairnSlots.
func New(path string, slotCount, phaseOffsetSeconds int) (*sql.DB, error) {
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
	// 신규 DB 에서는 컬럼이 이미 없어 "no such column" 에러만 swallow.
	if _, err := db.ExecContext(ctx, "ALTER TABLE player_states DROP COLUMN pity_count"); err != nil {
		if !strings.Contains(err.Error(), "no such column") {
			db.Close()
			return nil, fmt.Errorf("drop pity_count: %w", err)
		}
	}

	// M3 (migrations/000012): inventories.count column on reused DBs.
	// 이미 존재하는 신규 DB 에서는 "duplicate column" 에러만 swallow. 다른 에러는 fail-fast.
	if _, err := db.ExecContext(ctx, "ALTER TABLE inventories ADD COLUMN count INTEGER NOT NULL DEFAULT 1"); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("add inventories.count: %w", err)
		}
	}

	// M2 (migrations/000011): WishCairn 슬롯 backfill for reused DBs.
	// 슬롯 수/시차는 game-config.xml 에서 주입 (하드코딩 제거, T3-S3b).
	if err := backfillCairnSlots(ctx, db, slotCount, phaseOffsetSeconds); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// backfillCairnSlots reconciles the WishCairn slot COUNT for existing players to
// the config-derived dimensions:
//   - INSERT OR IGNORE 로 0..slotCount-1 중 빠진 슬롯만 staggered started_at 으로 보강
//     (신규 생성 슬롯 k 의 started_at = now - k × phaseOffsetSeconds).
//   - slotCount 가 줄어든 경우 slot_index >= slotCount 인 잉여 row 를 DELETE (R5: DB 정리).
//
// slotCount 가 그대로면 둘 다 사실상 no-op 이라 매 부팅 idempotent.
// SQLite 의 modernc parser 가 INSERT ... SELECT ... ON CONFLICT 폼을 거부 → INSERT OR IGNORE.
//
// 의도적 한계 (count-only reconcile): 이미 존재하는 row 의 started_at 은 재정렬하지
// 않는다. spawnInterval/maxLayers 변경으로 phaseOffset 이 바뀌어도 기존 슬롯의 stagger 는
// 유지된다 (started_at 재작성은 플레이어 진행도를 바꾸는 결정이라 자동화하지 않음). 기존
// 플레이어까지 새 phase 로 맞추려면 별도 ops 마이그레이션이 필요하다. PostgreSQL 모드는
// 이 함수를 타지 않으므로, PG 의 차원 축소/재정렬은 hand-written 마이그레이션으로 처리한다.
func backfillCairnSlots(ctx context.Context, db *sql.DB, slotCount, phaseOffsetSeconds int) error {
	if slotCount <= 0 {
		return fmt.Errorf("backfill wish_cairn_slots: slotCount must be > 0, got %d", slotCount)
	}

	// Build "SELECT 0 AS idx UNION ALL SELECT 1 ... UNION ALL SELECT slotCount-1".
	var b strings.Builder
	for i := 0; i < slotCount; i++ {
		if i == 0 {
			b.WriteString("SELECT 0 AS idx")
		} else {
			fmt.Fprintf(&b, " UNION ALL SELECT %d", i)
		}
	}

	insert := fmt.Sprintf(`
		INSERT OR IGNORE INTO wish_cairn_slots (player_id, slot_index, started_at)
		SELECT p.id, s.idx,
		       strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now', '-' || (s.idx * %d) || ' seconds')
		FROM players p
		CROSS JOIN (%s) s
	`, phaseOffsetSeconds, b.String())
	if _, err := db.ExecContext(ctx, insert); err != nil {
		return fmt.Errorf("backfill wish_cairn_slots: %w", err)
	}

	// Shrink cleanup: drop slots beyond the configured count.
	if _, err := db.ExecContext(ctx,
		"DELETE FROM wish_cairn_slots WHERE slot_index >= ?", slotCount,
	); err != nil {
		return fmt.Errorf("prune wish_cairn_slots: %w", err)
	}
	return nil
}
