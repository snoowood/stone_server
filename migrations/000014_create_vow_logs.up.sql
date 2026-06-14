CREATE TABLE vow_logs (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id      UUID           NOT NULL REFERENCES players(id),
    base_rarity    VARCHAR(20)    NOT NULL,
    target_rarity  VARCHAR(20)    NOT NULL,
    success_rate   NUMERIC(6, 2)  NOT NULL DEFAULT 0,
    cost_points    NUMERIC(12, 2) NOT NULL DEFAULT 0,
    result         VARCHAR(20)    NOT NULL,
    reward_item_id VARCHAR(100)   NOT NULL,
    reward_rarity  VARCHAR(20)    NOT NULL,
    is_duplicate   BOOLEAN        NOT NULL DEFAULT false,
    materials      TEXT           NOT NULL DEFAULT '',
    prayed_at      TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_vow_logs_player_prayed_at ON vow_logs (player_id, prayed_at DESC);
