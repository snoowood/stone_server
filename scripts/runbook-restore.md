# stone-server — DB Restore Runbook

RTO target: **백업 → 복원 → 서버 재기동 전체 1시간 이내**

---

## 사전 조건

| 항목 | 확인 |
|------|------|
| 서버 SSH 접속 가능 | `ssh ops@<server-ip>` |
| Docker Compose 스택 실행 중 | `docker compose ps` |
| 복원할 `.pgdump` 파일 확보 | 로컬 백업 또는 S3에서 다운로드 |
| AWS CLI 설정 (S3 백업 사용 시) | `aws sts get-caller-identity` |

---

## 전체 흐름 요약

```
1. 백업 파일 확보      (로컬 /var/backups/stone-server/ 또는 S3)
2. restore.sh 실행     (app 중지 → pg_restore → 일관성 검증 → app 재기동)
3. /health 200 확인    (스크립트가 자동 측정)
4. 결과 기록           (RTO, 레코드 수, 이상 여부)
```

---

## Step 1 — 백업 파일 확보

### 1-a. 로컬 백업 사용

```bash
ls -lh /var/backups/stone-server/
# 파일명: stone_server_YYYYMMDD_HHMMSS.pgdump
BACKUP=/var/backups/stone-server/stone_server_20260506_020001.pgdump
```

### 1-b. S3에서 다운로드

```bash
# 최신 백업 파일명 확인
aws s3 ls s3://<BUCKET>/stone-server/ --recursive | sort | tail -5

# 다운로드
aws s3 cp s3://<BUCKET>/stone-server/stone_server_20260506_020001.pgdump \
    /tmp/stone_server_restore.pgdump

BACKUP=/tmp/stone_server_restore.pgdump
```

---

## Step 2 — restore.sh 실행

```bash
cd /opt/stone-server
./scripts/restore.sh "$BACKUP"
```

스크립트가 자동으로 수행하는 작업:
1. `docker compose stop app` — 신규 DB 쓰기 차단
2. `pg_restore --clean --if-exists` — 기존 테이블 삭제 후 복원
3. 데이터 일관성 검증 (`players`, `player_states`, `inventories`)
4. `docker compose start app` — 서버 재기동
5. `/health` 200 폴링 → RTO 출력

### 환경 변수 재정의가 필요한 경우

```bash
HEALTH_URL=https://your.domain.com/api/v1/health \
HEALTH_TIMEOUT=180 \
    ./scripts/restore.sh "$BACKUP"
```

---

## Step 3 — 결과 확인

스크립트 마지막 출력 예시:

```
[2026-05-06T03:05:12Z] --- Results
[2026-05-06T03:05:12Z]     /health status    : 200 OK
[2026-05-06T03:05:12Z]     consistency       : PASS
[2026-05-06T03:05:12Z]     RTO               : 47s (target: <3600s)
[2026-05-06T03:05:12Z] === restore complete ===
```

### 수동 검증 (필요 시)

```bash
# 레코드 수 확인
docker compose exec postgres psql -U stone -d stone_server -c "
SELECT
    (SELECT COUNT(*) FROM players)       AS players,
    (SELECT COUNT(*) FROM player_states) AS player_states,
    (SELECT COUNT(*) FROM inventories)   AS inventories,
    (SELECT COUNT(*) FROM gacha_logs)    AS gacha_logs;
"

# /health 직접 호출
curl -sk https://localhost/api/v1/health | jq .
# 기대 응답: {"status":"ok","db":"ok","cache":"ok","version":"1.0.0"}
```

---

## 문제 해결

### pg_restore 실패 (exit code 2)

스크립트는 실패 시 `app` 컨테이너를 자동으로 재기동합니다. 이후:

```bash
# pg_restore 수동 재시도
docker compose exec postgres pg_restore \
    -U stone -d stone_server \
    --clean --if-exists --no-owner --no-privileges \
    --verbose \
    < "$BACKUP" 2>&1 | tail -20
```

### /health timeout (exit code 3)

```bash
# 앱 로그 확인
docker compose logs app --tail 50

# 마이그레이션 오류 여부
docker compose logs app | grep -i "migration\|fatal"

# 컨테이너 상태
docker compose ps
```

### 일관성 검증 실패 (exit code 4)

```bash
# 고아 레코드 직접 확인
docker compose exec postgres psql -U stone -d stone_server -c "
SELECT 'orphan_states' AS issue, COUNT(*) AS cnt
FROM player_states ps LEFT JOIN players p ON p.id = ps.player_id WHERE p.id IS NULL
UNION ALL
SELECT 'orphan_inventories', COUNT(*)
FROM inventories i LEFT JOIN players p ON p.id = i.player_id WHERE p.id IS NULL;
"
# 다른 시점의 백업 파일로 재시도하거나, 해당 고아 레코드를 수동 정리 후 서비스 재개
```

### app 컨테이너가 중지된 상태로 남은 경우

```bash
docker compose start app
docker compose logs -f app
```

---

## 복원 검증 기준 (T5-04 리뷰 목표)

| 항목 | 기준 | 확인 방법 |
|------|------|-----------|
| RTO | < 3600초 (1시간) | 스크립트 출력 `RTO: Xs` |
| 레코드 수 일관성 | 고아 레코드 0개 | 스크립트 출력 `orphan states: 0`, `orphan inv.: 0` |
| 서비스 정상화 | `/health` 200 OK | 스크립트 출력 `/health status: 200 OK` |
