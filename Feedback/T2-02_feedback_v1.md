# T2-02 Feedback v1

- Reviewed at: `2026-05-05`
- Task: [T2-02.md](/F:/stone_server/Task/T2-02.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: first review of `POST /api/v1/time/sync`

## Verdict

보류. `time/sync` 핸들러 자체는 드리프트 계산과 warning 판정이 맞지만, 현재 nginx가 설정 오류로 crash-loop 중이라 의도된 공개 HTTPS 경로에서 엔드포인트를 실제로 사용할 수 없습니다.

## Findings

1. [P1] 현재 공개 `/api/v1/time/sync` 경로는 nginx 설정 오류로 내려가 있습니다.
   - [default.conf.template](/F:/stone_server/nginx/templates/default.conf.template:15) 의 redirect/proxy 변수 이스케이프가 현재 nginx entrypoint의 envsubst 흐름과 맞지 않아, 재기동 후 nginx가 `invalid variable name in /etc/nginx/conf.d/default.conf:15` 로 반복 재시작했습니다.
   - 실검증에서도 `stone_server-nginx-1` 상태가 `Restarting` 이었고, `https://127.0.0.1/api/v1/time/sync` 호출은 `curl: (7) Failed to connect to 127.0.0.1 port 443` 로 실패했습니다.
   - T2-02는 인증 없는 공개 엔드포인트라 핸들러 로직뿐 아니라 실제 공개 경로 접근성도 중요하므로, 현재 상태는 완료로 보기 어렵습니다.

## Re-check Summary

- 서버-클라이언트 시간 정상 범위 → `warning: false`: 통과
  - 앱 내부 네트워크 직접 호출 기준 `drift_seconds: 3`, `warning: false`
- 5분 이상 차이 → `warning: true`, 올바른 `drift_seconds`: 통과
  - 앱 내부 네트워크 직접 호출 기준 `drift_seconds: 361`, `warning: true`
- 인증 헤더 없이 호출 가능 확인: 부분 통과
  - JWT 없이 앱에 직접 호출했을 때 정상 응답
  - 다만 공개 HTTPS 경로는 nginx crash-loop로 확인 실패

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 후 앱 로그에서 route 등록 확인
  - `POST /api/v1/time/sync`
- 앱 내부 네트워크 직접 호출 결과
  - `NORMAL_BODY={"server_time":"...","drift_seconds":3,"warning":false}`
  - `WARN_BODY={"server_time":"...","drift_seconds":361,"warning":true}`
- nginx 상태/로그
  - `docker inspect stone_server-nginx-1 --format '{{.State.Status}} {{.State.Restarting}} {{.RestartCount}}'`
  - 결과: `restarting true ...`
  - 로그: `invalid variable name in /etc/nginx/conf.d/default.conf:15`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 현재 `T2-02: 리뷰 중`으로 되어 있어 추가 수정은 하지 않았습니다.
