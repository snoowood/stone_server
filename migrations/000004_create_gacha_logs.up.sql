CREATE TABLE gacha_logs (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id     UUID         NOT NULL REFERENCES players(id),
    item_id       VARCHAR(100) NOT NULL,
    rarity        VARCHAR(20)  NOT NULL,
    is_duplicate  BOOLEAN      NOT NULL DEFAULT false,
    refund_points NUMERIC(12, 2) NOT NULL DEFAULT 0,
    pulled_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_gacha_logs_player_pulled_at ON gacha_logs (player_id, pulled_at DESC);
