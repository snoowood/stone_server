# T5-01 Feedback v1

- Task: `T5-01`
- Reviewed on: `2026-05-06`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `docker compose ps`: `postgres`/`app` 포함 스택 정상 기동 확인
- `scripts/backup.sh`: 실행 확인
- `scripts/backup.cron`: 일 1회 `02:00 UTC` cron 등록 형식 확인
- `scripts/stone-server-backup.service`: systemd oneshot 등록 형식 확인
- `scripts/stone-server-backup.timer`: 일 1회 `02:00 UTC` timer 등록 형식 확인

### Runtime checks

- 백업 파일 생성:
  - `bash -lc 'BACKUP_DIR=/mnt/f/stone_server/tmp_backups ./scripts/backup.sh'`
  - 결과: `stone_server_20260506_130916.pgdump` 등 실제 `.pgdump` 파일 생성 확인
- 7일 보관 / 오래된 파일 삭제:
  - `stone_server_old8.pgdump`를 8일 전 시각으로, `stone_server_old6.pgdump`를 6일 전 시각으로 준비
  - 스크립트 재실행 후 `Pruned 1 backup(s) older than 7 days)` 로그 확인
  - 결과: 8일 지난 `stone_server_old8.pgdump`는 삭제, 6일 지난 `stone_server_old6.pgdump`는 유지
- 복원 성공:
  - 최신 백업을 `stone_restore_test` 임시 DB에 `pg_restore`로 복원
  - 복원 후 확인값:
    - `players=2`
    - `player_states=2`
    - `gacha_logs=29`
    - `schema=7`
  - 원본 DB 조회값과 일치 확인

## Notes

- 로컬 검증 환경에서는 일반 사용자 권한으로 `./scripts/backup.sh`를 그대로 실행하면 기본 경로 `/var/backups/stone-server` 생성 권한이 없어 실패했습니다.
- 실제 배포 경로에서는 `cron`/`systemd service`가 `root`로 실행되도록 작성돼 있어 이 점은 차단 이슈로 보지 않았습니다.
- S3 업로드 분기는 코드상 존재하지만, 이 환경에는 실제 `S3_BUCKET`/AWS 자격증명이 없어 실업로드까지는 검증하지 못했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기준으로 `T5-01` 기본 상태는 이미 `완료`로 반영돼 있었습니다.
