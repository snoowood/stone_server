ALTER TABLE gacha_logs
    DROP COLUMN balance_before,
    DROP COLUMN balance_after,
    DROP COLUMN accrued_pts;

ALTER TABLE vow_logs
    DROP COLUMN balance_before,
    DROP COLUMN balance_after,
    DROP COLUMN accrued_pts;
