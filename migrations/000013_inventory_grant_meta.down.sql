ALTER TABLE inventories
    DROP COLUMN IF EXISTS source_type;
ALTER TABLE inventories
    DROP COLUMN IF EXISTS item_type;
