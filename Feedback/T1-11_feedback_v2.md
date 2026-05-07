# T1-11 Feedback v2

- Reviewed at: `2026-05-05`
- Task: [T1-11.md](/F:/stone_server/Task/T1-11.md)
- Related infra spec: [00_overview.md](/F:/stone_server/Goal/00_overview.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: second review of AWS Lightsail + SSL deployment setup after feedback reflection

## Verdict

통과. 지난 리뷰에서 지적한 Certbot 초기 발급 진입점 문제는 해소됐고, 저장소 기준 배포 구성과 로컬 HTTPS 동작에서도 추가 blocking issue는 보이지 않았습니다.

## Re-check Summary

- Certbot 초기 발급 경로: 개선 확인
  - `deploy.sh`가 이제 `docker compose run --rm --entrypoint certbot certbot certonly ...` 형태로 실행
  - 실검증으로 `docker compose run --rm --entrypoint certbot certbot --version` 성공
- HTTPS `GET /health`: 로컬 스택 기준 통과
  - `https://127.0.0.1/api/v1/health` 정상 JSON 응답 확인
- HTTP → HTTPS 리다이렉트: 통과
  - `http://127.0.0.1/api/v1/health` -> `301 Location: https://127.0.0.1/api/v1/health`
- SSL 인증서 유효기간/자동 갱신 설정: 구성 확인
  - `certbot` renewal loop와 `/etc/letsencrypt`, `/var/www/certbot` 볼륨 연결 존재
  - `deploy.sh`에 90일 인증서 및 12시간 갱신 안내 존재
- PostgreSQL, Redis 외부 직접 접속 불가: 구성 확인
  - compose host publish는 nginx `80/443`만 존재
  - `postgres`, `redis`는 host port publish 없음
- `.env` git 미포함: 통과
  - `.gitignore`에 `.env` 포함
  - `git ls-files .env` 결과 tracked 아님 확인

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- `docker compose run --rm --entrypoint certbot certbot --version` -> `certbot 5.5.0`
- 로컬 HTTPS/리다이렉트 실검증
  - `HTTP/1.1 301 Moved Permanently`
  - `Location: https://127.0.0.1/api/v1/health`
  - `HTTPS_BODY={"cache":"ok","db":"ok","status":"ok","version":"1.0.0"}`
- 공개 포트 확인
  - `nginx`: `0.0.0.0:80->80`, `0.0.0.0:443->443`
  - `postgres`, `redis`: host publish 없음

## Notes

- 이번 2차 리뷰에서는 추가 findings가 없습니다.
- 실제 Lightsail 인스턴스, 실도메인 DNS, 실발급된 Let's Encrypt 만료일, Lightsail 방화벽 콘솔 상태는 이 환경에서 직접 조회하지는 못했습니다.
