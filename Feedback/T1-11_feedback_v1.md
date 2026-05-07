# T1-11 Feedback v1

- Reviewed at: `2026-05-04`
- Task: [T1-11.md](/F:/stone_server/Task/T1-11.md)
- Related infra spec: [00_overview.md](/F:/stone_server/Goal/00_overview.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: first review of AWS Lightsail + SSL deployment setup

## Verdict

보류. 로컬 기준 HTTPS 프록시, HTTP → HTTPS 리다이렉트, 내부 DB/Redis 비노출 구성은 맞지만, `deploy.sh`의 Let's Encrypt 초기 발급 단계가 현재 compose 정의와 맞지 않아 실제 프로덕션 인증서 발급이 실패할 가능성이 큽니다.

## Findings

1. [P1] `deploy.sh`의 초기 Certbot 발급 호출이 현재 service entrypoint와 충돌합니다.
   - [deploy.sh](/F:/stone_server/deploy.sh:58) 는 `docker compose run --rm certbot certonly ...` 로 최초 인증서 발급을 시도합니다.
   - 하지만 [docker-compose.yml](/F:/stone_server/docker-compose.yml:76) 에서 `certbot` service는 이미 `entrypoint: /bin/sh -c '... certbot renew ...'` 로 고정돼 있습니다.
   - `docker compose run` 에서 뒤의 `certonly ...` 는 entrypoint를 대체하지 않고 인자로만 붙기 때문에, 현재 구성대로면 최초 발급 대신 renewal loop entrypoint가 실행됩니다.
   - 그러면 `/etc/letsencrypt/live/${DOMAIN}/...` 파일이 생성되지 않은 상태에서 [deploy.sh](/F:/stone_server/deploy.sh:69) 이후 nginx가 LE 경로를 바라보게 되어 실제 배포가 깨질 수 있습니다.

## Re-check Summary

- HTTPS `GET /health` 외부 정상 응답: 외부 실서버는 미검증
  - 이 워크스페이스에는 실제 Lightsail 인스턴스/도메인 접속 정보가 없어 외부 도메인 기준 호출은 확인하지 못함
  - 로컬 스택에서는 `https://127.0.0.1/api/v1/health` 정상 JSON 응답 확인
- HTTP → HTTPS 리다이렉트: 통과
  - `http://127.0.0.1/api/v1/health` -> `301 Location: https://127.0.0.1/api/v1/health`
- SSL 인증서 유효기간 확인 (90일, 자동 갱신 설정): 구성은 있음, 실발급 경로는 보류
  - certbot renewal loop와 볼륨 마운트는 존재
  - 다만 위 finding 때문에 최초 Let's Encrypt 발급 성공은 보장되지 않음
- PostgreSQL, Redis 외부 직접 접속 불가 확인: 통과
  - compose에서 host publish는 nginx의 `80/443`만 존재
  - `postgres`, `redis`는 host port publish 없음
- 서버에서 `.env` 파일 git 미포함 확인: 부분 통과
  - [gitignore](/F:/stone_server/.gitignore:1) 에 `.env` 포함
  - `git ls-files .env` 결과 tracked 아님 확인

## Verification

- `docker compose config` 확인
- 로컬 HTTPS/리다이렉트 실검증
  - `HTTP/1.1 301 Moved Permanently`
  - `Location: https://127.0.0.1/api/v1/health`
  - `HTTPS_BODY={"cache":"ok","db":"ok","status":"ok","version":"1.0.0"}`
- 공개 포트 확인
  - `nginx`: `0.0.0.0:80->80`, `0.0.0.0:443->443`
  - `postgres`, `redis`: host publish 없음

## Notes

- 이번 리뷰는 저장소 기준 배포 구성과 로컬 스택을 검토한 결과입니다.
- 실제 Lightsail 외부 헬스체크, 실제 Let's Encrypt 인증서 만료일, Lightsail 방화벽 상태는 이 환경에서 직접 확인하지 못했습니다.
