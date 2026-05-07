CREATE TABLE player_achievements (
    player_id       UUID        NOT NULL REFERENCES players(id),
    achievement_id  VARCHAR(64) NOT NULL,
    unlocked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    steam_synced    BOOLEAN     NOT NULL DEFAULT FALSE,

    PRIMARY KEY (player_id, achievement_id)
);

CREATE INDEX idx_player_achievements_player ON player_achievements(player_id);
