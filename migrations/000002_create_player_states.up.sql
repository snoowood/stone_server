CREATE TABLE player_states (
    player_id         UUID PRIMARY KEY REFERENCES players(id),
    enlightenment_pts NUMERIC(12, 2) NOT NULL DEFAULT 0,
    time_stone_count  SMALLINT NOT NULL DEFAULT 0 CHECK (time_stone_count <= 3),
    streak_days       INT NOT NULL DEFAULT 0,
    last_login_date   DATE,
    next_gacha_at     TIMESTAMPTZ,
    pity_count        INT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
