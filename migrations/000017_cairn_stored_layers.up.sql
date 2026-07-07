-- issue #34: WishCairn 저장형(stateful) 모델 전환. layer_count 를 실제 저장하고
-- (now - started_at) / interval derive 를 폐기한다. 성장은 접속 중에만(오프라인 미성장)
-- interval 마다 미완성 슬롯 중 무작위 1곳을 +1층 하며, player_states.cairn_last_growth_at
-- 이 성장 앵커다(auth 시 now 로 리셋 → 오프라인 구간 폐기).
-- 마이그레이션 정책 = 전원 리셋: layer_count 는 DEFAULT 0 으로 시작하고 started_at→층수
-- 환산 백필은 하지 않는다(미출시·실유저 없음). SQLite 미러는 pkg/sqlitedb/sqlitedb.go.
ALTER TABLE wish_cairn_slots ADD COLUMN layer_count SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE player_states ADD COLUMN cairn_last_growth_at TIMESTAMPTZ;
