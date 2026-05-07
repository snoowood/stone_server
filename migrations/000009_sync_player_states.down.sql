ALTER TABLE player_states
    DROP COLUMN IF EXISTS enlightenment_rate,
    DROP COLUMN IF EXISTS last_sync_at;
