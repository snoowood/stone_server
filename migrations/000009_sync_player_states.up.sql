ALTER TABLE player_states
    ADD COLUMN enlightenment_rate FLOAT8 NOT NULL DEFAULT 1.0,
    ADD COLUMN last_sync_at TIMESTAMPTZ;
