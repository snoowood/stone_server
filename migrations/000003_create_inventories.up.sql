CREATE TABLE inventories (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id   UUID        NOT NULL REFERENCES players(id),
    item_id     VARCHAR(100) NOT NULL,
    rarity      VARCHAR(20)  NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (player_id, item_id)
);
