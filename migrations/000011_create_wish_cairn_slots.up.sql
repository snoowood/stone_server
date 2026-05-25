-- M2: WishCairn 서버-gated 전환. 슬롯별 독립 started_at + claimed_at 만 저장하고
-- layer_count / status 는 매 read 시 (now - started_at) / spawn_interval 로 derive.
-- 동시 완성 방지를 위해 신규 플레이어 슬롯 초기화 시 phase_offset 으로 시차를 부여한다.
CREATE TABLE wish_cairn_slots (
    player_id  UUID NOT NULL REFERENCES players(id),
    slot_index SMALLINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    claimed_at TIMESTAMPTZ,
    PRIMARY KEY (player_id, slot_index)
);
