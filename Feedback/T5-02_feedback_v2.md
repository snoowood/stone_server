# T5-02 Feedback v2

- Task: `T5-02`
- Reviewed on: `2026-05-06`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./...`: passed

### Code review checks

- [healthcheck.cron](/F:/stone_server/scripts/healthcheck.cron:8) now documents `/etc/stone-server/monitor.env` creation clearly and no longer embeds committed placeholder webhook values in the actual cron command.
- [healthcheck.sh](/F:/stone_server/scripts/healthcheck.sh:31) now loads `MONITOR_ENV` from `/etc/stone-server/monitor.env` by default, after project `.env`, so production secrets can override repo defaults as intended.
- [setup-lightsail-alarms.sh](/F:/stone_server/scripts/setup-lightsail-alarms.sh:4) now covers the missing operational areas by adding proxy alarms for sustained pressure and inbound traffic, alongside CPU, status check, and outbound spike alarms.
- [setup-uptimerobot.sh](/F:/stone_server/scripts/setup-uptimerobot.sh:38) still provides a 5-minute `/health` monitor registration path for external failure detection.

### Review conclusion

- 지난 1차 finding이었던 `monitor.env` 미로딩 문제는 해소됐습니다.
- 실제 cron 실행 줄에는 더 이상 `HEALTH_URL` / `SLACK_WEBHOOK_URL` placeholder가 박혀 있지 않고, 운영 서버 비밀값은 외부 env 파일에서 읽는 구조로 정리됐습니다.
- Lightsail 쪽도 플랫폼 제약을 주석으로 설명하면서 CPU, 상태 체크, 트래픽 기반 프록시 알람을 포함하도록 보완돼, 이번 태스크의 운영 알림 구성 목적에는 부합한다고 판단했습니다.

## Notes

- 이 환경에는 실제 Slack webhook, UptimeRobot API 키, AWS 자격증명이 없어 실알림 발송과 실제 콘솔 등록까지는 직접 검증하지 못했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기준으로 `T5-02` 기본 상태는 이미 `완료`로 반영돼 있었습니다.
