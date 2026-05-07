# T2-02 Feedback v2

- Reviewed at: `2026-05-05`
- Task: [T2-02.md](/F:/stone_server/Task/T2-02.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: second review of `POST /api/v1/time/sync` after feedback reflection

## Verdict

통과. 지난 리뷰에서 막혔던 공개 HTTPS 경로가 복구됐고, `time/sync` 공개 엔드포인트의 drift 계산과 warning 응답도 스펙대로 동작했습니다.

## Re-check Summary

- 서버-클라이언트 시간 정상 범위 → `warning: false`: 통과
  - 공개 HTTPS 호출 기준 `drift_seconds: 3`, `warning: false`
- 5분 이상 차이 → `warning: true`, 올바른 `drift_seconds`: 통과
  - 공개 HTTPS 호출 기준 `drift_seconds: 360`, `warning: true`
- 인증 헤더 없이 호출 가능 확인: 통과
  - 별도 Authorization 헤더 없이 `POST /api/v1/time/sync` 정상 `200`
- 공개 경로 복구 확인: 통과
  - nginx가 정상 기동 상태
  - HTTP 요청은 HTTPS로 `301` 리다이렉트

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- 공개 HTTPS 실검증
  - `NORMAL_STATUS=200`
  - `NORMAL_BODY={"server_time":"...","drift_seconds":3,"warning":false}`
  - `WARN_STATUS=200`
  - `WARN_BODY={"server_time":"...","drift_seconds":360,"warning":true}`
- 공개 HTTP 리다이렉트 확인
  - `HTTP/1.1 301 Moved Permanently`
  - `Location: https://127.0.0.1/api/v1/time/sync`
- 컨테이너 상태 확인
  - `stone_server-nginx-1` -> `Up`

## Notes

- 이번 2차 리뷰에서는 추가 findings가 없습니다.
