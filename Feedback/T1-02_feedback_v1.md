# T1-02 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-02.md](/F:/stone_server/Task/T1-02.md)
- Related goal: [00_overview.md](/F:/stone_server/Goal/00_overview.md)
- Scope: environment configuration and structured request logging

## Verdict

통과. T1-02 요구사항 범위에서 확인 가능한 항목들은 충족했습니다.

## Checklist Review

- `.env` 없이 실행 시 명확한 오류 메시지 출력 후 종료: 통과
  - 확인 결과: `configuration error: required environment variable DB_URL is not set`
- 요청 로그에 `status`, `latency` 포함: 통과
  - 확인 결과: 서버 실행 후 요청 로그에 `request_id`, `method`, `path`, `status`, `latency`가 구조화되어 출력됨
- 민감값 로그 미노출: 통과
  - 설정 로드 실패 시 환경변수 이름만 출력하고 값은 로그에 남기지 않음
  - 서버 시작 로그도 `port`만 남김
- `.env` 파일 `.gitignore` 포함: 통과

## Evidence

- Example env file exists: [.env.example](/F:/stone_server/.env.example)
- Config loader uses `godotenv` and required env validation: [config.go](/F:/stone_server/pkg/config/config.go:1)
- Zerolog initialization exists: [logger.go](/F:/stone_server/pkg/logger/logger.go:1)
- Request logging middleware exists: [logging.go](/F:/stone_server/internal/middleware/logging.go:1)
- Server wiring includes config load, logger init, and middleware registration: [main.go](/F:/stone_server/cmd/server/main.go:1)
- `go build ./...`: 통과

## Notes

- 현재 검증 로그에서는 인증 전 요청만 호출했기 때문에 `player_id` 필드는 나타나지 않았습니다.
- 다만 미들웨어 구현상 `player_id`가 컨텍스트에 설정되면 함께 로깅되도록 준비되어 있어, 이후 인증 미들웨어나 핸들러가 붙으면 요구사항을 만족하는 방향입니다.
- 이전 T1-01에서 지적된 Go 버전 불일치(`go.mod` vs `Dockerfile`)는 여전히 별도 정리 필요하지만, 이번 T1-02 범위의 설정/로깅 리뷰 판정에는 직접적인 차단 이슈로 보지 않았습니다.
