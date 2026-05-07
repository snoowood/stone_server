CREATE TABLE achievement_retry_queue (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id       UUID        NOT NULL REFERENCES players(id),
    achievement_id  VARCHAR(64) NOT NULL,
    retry_count     INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_retry_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ach_retry_next ON achievement_retry_queue(next_retry_at)
    WHERE resolved_at IS NULL;
