# T5-02 Feedback v1

- Task: `T5-02`
- Reviewed on: `2026-05-06`
- Verdict: `보류`

## Findings

### 1. The cron install path does not actually load the documented monitor.env secrets

- Severity: `P1`
- File: [scripts/healthcheck.cron](/F:/stone_server/scripts/healthcheck.cron:8)
- Summary: The install comment says operators should put `HEALTH_URL` and `SLACK_WEBHOOK_URL` in `/etc/stone-server/monitor.env`, but the cron job never sources that file. Instead it hard-codes placeholder values directly in the cron line, and [healthcheck.sh](/F:/stone_server/scripts/healthcheck.sh:21) only sources `COMPOSE_DIR/.env`.
- Impact: If production follows the documented setup, the monitor will still call `https://yourdomain.com/...` and the placeholder Slack webhook, so real outage alerts will not be delivered to the configured channel.

### 2. Lightsail alarm setup omits the memory/connection coverage promised by the task

- Severity: `P2`
- File: [scripts/setup-lightsail-alarms.sh](/F:/stone_server/scripts/setup-lightsail-alarms.sh:4)
- Summary: The task goal calls for Lightsail metric alerts covering CPU, memory, and connections, but the script only creates `CPUUtilization`, `StatusCheckFailed`, and `NetworkOut` alarms.
- Impact: The implementation leaves the operational alert coverage incomplete versus the task definition, especially for resource-pressure cases that do not immediately trip status checks.

## Verification

- Code review:
  - [healthcheck.sh](/F:/stone_server/scripts/healthcheck.sh:20) sources project `.env` only, not `/etc/stone-server/monitor.env`
  - [healthcheck.cron](/F:/stone_server/scripts/healthcheck.cron:14) embeds placeholder `HEALTH_URL` and `SLACK_WEBHOOK_URL`
  - [setup-lightsail-alarms.sh](/F:/stone_server/scripts/setup-lightsail-alarms.sh:53) creates three alarms only: CPU, status check, network out
  - [setup-uptimerobot.sh](/F:/stone_server/scripts/setup-uptimerobot.sh:38) creates a 5-minute `/health` monitor registration script

### Local script checks

- `healthcheck.sh` down-path output:
  - `ALERT: stone-server-test is DOWN ...` log line emitted, confirming service name + detected timestamp format exist in the script path
- `curl -skf -o /dev/null -w "%{http_code}" https://127.0.0.1/nope || echo "000"`:
  - produced `404000`, showing the current failure-path status formatting is a bit noisy, though I did not count that as a blocking finding for this review

## Notes

- I could not directly verify real Slack delivery, real UptimeRobot monitor creation, or real AWS Lightsail alarm registration in this environment because no production webhook/API/AWS credentials were available.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T5-02 = review in progress`.
