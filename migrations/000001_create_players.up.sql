CREATE TABLE players (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    steam_id   VARCHAR(20) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login TIMESTAMPTZ
);
