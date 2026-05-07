ALTER TABLE gacha_logs
    ADD COLUMN cost_points     NUMERIC(12, 2) NOT NULL DEFAULT 0,
    ADD COLUMN gacha_seed_hash VARCHAR(64)    NOT NULL DEFAULT '';
