-- M3: 가챠에서 중복 아이템 획득 시 stack count 증가 모델로 전환.
-- 기존 행은 count=1 로 시작 (UNIQUE (player_id, item_id) 제약상 1행=1소유 이므로 자연 일치).
ALTER TABLE inventories
    ADD COLUMN count INT NOT NULL DEFAULT 1;
