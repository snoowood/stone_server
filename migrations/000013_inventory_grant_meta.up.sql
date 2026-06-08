-- DTO sync: 인벤토리 행에 item_type / source_type provenance 메타 추가.
-- gacha pull(stone_skin/wish_cairn) 및 향후 vow 보상의 출처를 클라이언트
-- RewardGrantDto / InventoryItemDto 필드와 1:1 로 맞추기 위함.
-- 기존 행은 DEFAULT 로 stone_skin / unknown 으로 backfill.
ALTER TABLE inventories
    ADD COLUMN item_type VARCHAR(30) NOT NULL DEFAULT 'stone_skin';
ALTER TABLE inventories
    ADD COLUMN source_type VARCHAR(30) NOT NULL DEFAULT 'unknown';
