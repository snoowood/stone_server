/**
 * k6 load test — stone-server T5-03
 *
 * Scenario per VU:
 *   setup()        → mint JWTs via dev-token (one per VU, no rate-limit overhead)
 *   iter 0         → POST /auth/steam  (exercises the endpoint; falls back to
 *                    setup JWT on 429 caused by IP-based rate limit)
 *   every iter     → GET  /player/state
 *                    POST /player/clicks
 *                    POST /gacha/pull
 *
 * Usage:
 *   TARGET_URL=https://your-server ./scripts/load-test/run.sh [smoke|load|stress]
 *
 * DB pool + memory verification (P1 criterion):
 *   Start server with DIAG_INTERVAL_SECS=5 and inspect log lines tagged "diag":
 *     docker compose logs -f app | grep '"message":"diag"'
 *   pool_total_conns must not exceed 50 during the load phase.
 *   heap_alloc_bytes should return to near the pre-test baseline after ramp-down.
 *
 * Prerequisites:
 *   - k6: https://k6.io/docs/get-started/installation/
 *   - Server running with APP_ENV != production (enables /api/v1/internal/dev-token)
 */

import http from 'k6/http';
import { check, sleep } from 'k6';

// ── Configuration ─────────────────────────────────────────────────────────────

const BASE_URL    = __ENV.TARGET_URL   || 'https://localhost';
const VU_COUNT    = parseInt(__ENV.VU_COUNT    || '200', 10);
const RAMP_SECS   = parseInt(__ENV.RAMP_SECS   || '30', 10);
const STEADY_SECS = parseInt(__ENV.STEADY_SECS || '180', 10);

// Steam ticket / ID used for this VU. The dev mock returns the ticket value
// as steam_id, so each VU gets a distinct player account.
function vuSteamID(vuNum) {
    return `7656119800${String(vuNum).padStart(6, '0')}`;
}

// ── Options ───────────────────────────────────────────────────────────────────

// Mark 200, 409 (cooldown / insufficient pts / duplicate batch) and 429
// (IP/player rate limit) as expected outcomes. Only 5xx responses count as
// failures for the http_req_failed threshold.
http.setResponseCallback(
    http.expectedStatuses(200, 201, 409, 429)
);

export const options = {
    // Allow self-signed TLS certificates (localhost / dev environment).
    insecureSkipTLSVerify: true,

    stages: [
        { duration: `${RAMP_SECS}s`,   target: VU_COUNT },  // ramp-up
        { duration: `${STEADY_SECS}s`, target: VU_COUNT },  // steady load
        { duration: '30s',             target: 0 },          // ramp-down
    ],
    thresholds: {
        // P95 across all endpoints < 300 ms (T5-03 review target)
        'http_req_duration':                  ['p(95)<300'],
        // Genuine errors (5xx) < 1%
        'http_req_failed':                    ['rate<0.01'],
        // Per-endpoint targets
        'http_req_duration{endpoint:auth}':   ['p(95)<300'],
        'http_req_duration{endpoint:state}':  ['p(95)<200'],
        'http_req_duration{endpoint:clicks}': ['p(95)<250'],
        'http_req_duration{endpoint:gacha}':  ['p(95)<300'],
    },
};

// ── Setup: pre-mint one JWT per VU ────────────────────────────────────────────
// Uses the dev-only endpoint to avoid the 10-req/min IP-based rate limit on
// /auth/steam, which would otherwise serialize 200 VU logins over 20 minutes.

export function setup() {
    const tokens = [];
    const jsonHeaders = { 'Content-Type': 'application/json' };

    for (let i = 1; i <= VU_COUNT; i++) {
        const steamID = vuSteamID(i);
        const res = http.get(
            `${BASE_URL}/api/v1/internal/dev-token?steam_id=${steamID}`,
            { tags: { endpoint: 'setup' } }
        );
        if (res.status === 200) {
            tokens.push(JSON.parse(res.body).jwt);
        } else {
            console.error(`setup: dev-token failed for VU ${i} (status ${res.status})`);
            tokens.push(null);
        }
    }

    const ok = tokens.filter(t => t !== null).length;
    console.log(`setup: minted ${ok}/${VU_COUNT} JWTs`);
    return { tokens };
}

// ── VU-local JWT state ────────────────────────────────────────────────────────
// Module-level vars are per-VU in k6; safe to use as iteration-local state.
let activeJWT = null;

// ── Default VU scenario ───────────────────────────────────────────────────────

export default function (data) {
    const vuIdx     = (__VU - 1) % data.tokens.length;
    const setupJWT  = data.tokens[vuIdx];
    const steamID   = vuSteamID(__VU);
    const jsonHeaders = { 'Content-Type': 'application/json' };

    // ── First iteration: exercise POST /auth/steam ──────────────────────────
    // The mock client returns steamID == ticket, so each VU authenticates as a
    // distinct player. On 429 (IP rate-limit) the VU falls back to the setup JWT.
    if (__ITER === 0) {
        const authRes = http.post(
            `${BASE_URL}/api/v1/auth/steam`,
            JSON.stringify({ ticket: steamID }),
            { headers: jsonHeaders, tags: { endpoint: 'auth' } }
        );
        check(authRes, {
            // 429: IP rate-limited; 409: ticket already claimed (rerun with same mock tickets)
            'auth/steam: 200|409|429': r => [200, 409, 429].includes(r.status),
        });
        if (authRes.status === 200) {
            activeJWT = JSON.parse(authRes.body).jwt;
        } else {
            // Rate-limited: fall back to pre-minted JWT so the VU can continue.
            activeJWT = setupJWT;
        }
    }

    if (!activeJWT) {
        activeJWT = setupJWT;
    }
    if (!activeJWT) {
        sleep(1);
        return;
    }

    const authHeaders = {
        Authorization:  `Bearer ${activeJWT}`,
        'Content-Type': 'application/json',
    };

    // ── 1. GET /player/state ────────────────────────────────────────────────
    const stateRes = http.get(
        `${BASE_URL}/api/v1/player/state`,
        { headers: authHeaders, tags: { endpoint: 'state' } }
    );
    check(stateRes, { 'state: 200': r => r.status === 200 });

    sleep(0.3);

    // ── 2. POST /player/clicks ──────────────────────────────────────────────
    // count=100 → earns 100 enlightenment_pts (exact cost of one gacha pull).
    // Unique batch_id per VU per iteration prevents duplicate-batch 409s.
    const batchID = `${__VU}-${__ITER}`;
    const clicksRes = http.post(
        `${BASE_URL}/api/v1/player/clicks`,
        JSON.stringify({ batch_id: batchID, count: 100 }),
        { headers: authHeaders, tags: { endpoint: 'clicks' } }
    );
    check(clicksRes, {
        'clicks: 200|409|429': r => [200, 409, 429].includes(r.status),
    });

    // Respect the server-side 1-second minimum interval between click batches.
    sleep(1.1);

    // ── 3. POST /gacha/pull ─────────────────────────────────────────────────
    // Succeeds when pts >= 100. After the first successful pull per VU this
    // returns 429 COOLDOWN_ACTIVE (30-min cooldown) for the rest of the test.
    const gachaRes = http.post(
        `${BASE_URL}/api/v1/gacha/pull`,
        null,
        { headers: authHeaders, tags: { endpoint: 'gacha' } }
    );
    check(gachaRes, {
        'gacha: 200|409|429': r => [200, 409, 429].includes(r.status),
    });

    // Think time between iterations (simulates player reviewing results).
    sleep(1 + Math.random() * 2);
}

export function teardown() {
    console.log('Load test complete. Check server logs for "diag" entries to verify pool/memory.');
}
