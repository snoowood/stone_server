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

-- 기존 player backfill: 모든 player 에 5 슬롯 INSERT, phase_offset(30s) 시차.
-- 가챠 시점에 wish_cairn_slots 가 비어있어 CAIRN_SLOT_NOT_FOUND 영구 reject 되는 회귀 방지.
INSERT INTO wish_cairn_slots (player_id, slot_index, started_at)
SELECT p.id, s.idx, NOW() - (s.idx * INTERVAL '30 seconds')
FROM players p
CROSS JOIN (
    SELECT 0 AS idx UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
) s
ON CONFLICT (player_id, slot_index) DO NOTHING;
