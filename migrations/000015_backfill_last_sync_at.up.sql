-- E2E-3: backfill last_sync_at for player_states rows created before initPlayerState
-- started anchoring it. A NULL last_sync_at makes the passive-accrual formula
-- COALESCE(now - last_sync_at, 0) evaluate to 0, so those accounts accrue nothing
-- and their first gacha hits 409 INSUFFICIENT_POINTS. Anchor existing NULLs to now so
-- accrual starts immediately (the un-accrued closed window is intentionally dropped,
-- consistent with ECON-3 "offline window not accrued").
-- Idempotent: only NULL rows are touched. No-op on a fresh DB.
UPDATE player_states SET last_sync_at = NOW() WHERE last_sync_at IS NULL;
