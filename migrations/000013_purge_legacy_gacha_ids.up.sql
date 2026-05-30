-- 가챠 풀이 CSV(skins.csv) 기반 skinId 로 전환됨에 따라, 이전 rng.go 의
-- 더미 풀(common_stone_01 등)에서 나왔던 legacy item_id 행을 정리한다.
-- 이 ID 들은 새 마스터 데이터에 매핑이 없는 플레이스홀더라, 그대로 두면
-- 클라가 skinId lookup 시 미스되어 표시 불가/혼합 카탈로그 상태가 된다.
DELETE FROM inventories WHERE item_id IN (
    'common_stone_01', 'common_stone_02', 'common_stone_03',
    'uncommon_crystal_01', 'uncommon_crystal_02',
    'rare_gem_01', 'rare_gem_02',
    'unique_relic_01',
    'legendary_timepiece_01'
);
DELETE FROM gacha_logs WHERE item_id IN (
    'common_stone_01', 'common_stone_02', 'common_stone_03',
    'uncommon_crystal_01', 'uncommon_crystal_02',
    'rare_gem_01', 'rare_gem_02',
    'unique_relic_01',
    'legendary_timepiece_01'
);
