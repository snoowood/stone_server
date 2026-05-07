# T5-03 Feedback v2

- Task: `T5-03`
- Reviewed on: `2026-05-06`
- Verdict: `보류`

## Findings

### 1. The memory-recovery acceptance criterion is still not satisfied in the actual CCU 200 run

- Severity: `P1`
- File: [scripts/load-test/scenario.js](/F:/stone_server/scripts/load-test/scenario.js:15)
- Summary: Re-testing with the updated setup reached the intended `CCU 200` load and the latency/error thresholds passed, but the diagnostic heap metric did not return near baseline after ramp-down. On a diag-enabled app instance, `heap_alloc_bytes` started around `1.7 MB`, peaked around `9.1 MB`, and was still about `7.0 MB` more than 20 seconds after the load had finished.
- Impact: T5-03's last review goal is "no memory leak / memory recovers after load". That acceptance criterion is still not met by the measured result, so this task cannot yet be marked complete even though the other load metrics look good.

## Verification

- `go test ./...`: passed

### Runtime checks

- Smoke run via Docker k6 against a diag-enabled app instance:
  - `VU_COUNT=5`, `RAMP_SECS=5`, `STEADY_SECS=15`
  - scenario executed successfully, including `/auth/steam`, `/player/state`, `/player/clicks`, `/gacha/pull`
- Full load run via Docker k6 against a diag-enabled app instance:
  - `VU_COUNT=200`, `RAMP_SECS=30`, `STEADY_SECS=180`
  - results:
    - overall `http_req_duration p(95)=8.59ms`
    - `http_req_failed rate=0.00%`
    - `http_reqs=37630`, about `162.8 req/s`
    - `pool_total_conns` max observed: `6`
- Memory diagnostics from `diag` logs:
  - first observed `heap_alloc_bytes=1698360`
  - peak observed `heap_alloc_bytes=9102232`
  - more than 20 seconds after load end, last observed `heap_alloc_bytes=7047744`

## Notes

- 지난 코드 이슈였던 기본 타깃/`/auth/steam` 커버리지/DB·메모리 진단 경로는 소스 기준으로 모두 반영됐습니다.
- 이번 보류 사유는 코드 구조보다는 실제 검증 결과입니다. P95, 에러율, DB 커넥션 수는 기준을 만족했지만, 메모리 회복 기준은 아직 충족했다고 보기 어려웠습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T5-03 = review in progress`.
