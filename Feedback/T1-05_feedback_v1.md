# T1-05 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-05.md](/F:/stone_server/Task/T1-05.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: `GET /api/v1/health`

## Verdict

통과. T1-05 요구사항 범위에서 확인 가능한 항목들은 충족했습니다.

## Checklist Review

- DB와 Redis가 모두 정상일 때 `200` 응답: 통과
  - 확인 결과: `{"cache":"ok","db":"ok","status":"ok","version":"1.0.0"}`
- DB 장애 시 `503` + `db: error`: 통과
  - Postgres 컨테이너 중지 후 확인 결과: `{"cache":"ok","db":"error","status":"degraded","version":"1.0.0"}`
- Redis 장애 시 `503` + `cache: error`: 통과
  - Redis 컨테이너 중지 후 확인 결과: `{"cache":"error","db":"ok","status":"degraded","version":"1.0.0"}`
- 인증 없이 공개 엔드포인트 접근 가능: 통과
  - Authorization 헤더 유무와 무관하게 동일한 `200` 응답 확인

## Evidence

- Handler implementation: [handler.go](/F:/stone_server/internal/health/handler.go:1)
- Route wiring: [main.go](/F:/stone_server/cmd/server/main.go:50)
- Redis client init: [cache.go](/F:/stone_server/pkg/cache/cache.go:1)
- Build check: `go build ./...` 통과
- Nginx path check:
  - `http://127.0.0.1/api/v1/health` -> `301` to HTTPS
  - `https://127.0.0.1/api/v1/health` -> 정상 JSON 응답 확인

## Notes

- 이번 검증은 self-signed 인증서를 사용하는 Compose 스택에서 진행해서 HTTPS 호출은 `curl -k`로 확인했습니다.
- 리뷰용 격리 Compose 스택은 검증 후 `down -v`로 정리했습니다.
