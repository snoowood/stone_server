# Stone Server — Tester Build

Local server for testing the Stone client. **Source code is not included** — only
prebuilt images from the team's container registry are used.

---

## 1. One-time setup

### 1.1 Install Docker Desktop

| OS | Download |
|---|---|
| Windows 10/11 | https://www.docker.com/products/docker-desktop/ |
| macOS (Apple Silicon or Intel) | https://www.docker.com/products/docker-desktop/ |

After install:
- **Windows:** launch Docker Desktop. Wait until the whale icon in the tray
  stops animating (≈30 s).
- **macOS:** launch Docker.app from Applications. Allow it to install its helper
  on first launch.

### 1.2 Get the release files

The release lives on the `deploy` branch of the server repo (no source code,
only the launcher scripts and the pinned image tag).

```
git clone -b deploy --single-branch <server-repo-url> stone-server-tester
cd stone-server-tester
```

You should see `start.bat` (Windows), `start.command` (macOS), and friends.

---

## 2. Run the server

| OS | Action |
|---|---|
| **Windows** | Double-click `start.bat` |
| **macOS** | Right-click `start.command` → **Open** (Gatekeeper blocks plain double-click on first launch). After the first time, double-click works. |
| **Linux** | `./start.sh` |

If Docker Desktop isn't running, the script will try to launch it for you and
wait up to 90 seconds for it to be ready. First launch then takes ~1 minute
(image download). You'll see:

```
=== Server ready ===
  Health check : https://localhost:8443/api/v1/health
  Client URL   : https://localhost:8443
```

The client should be configured to connect to `https://localhost:8443`.

### Other actions

| File | Purpose |
|---|---|
| `start.*` | Boot the server |
| `stop.*` | Shut down (data is preserved) |
| `logs.*` | Show last 200 lines of server logs |
| `update.*` | Pull the latest release files + image and restart |

---

## 3. How auth works in the tester build

Steam authentication is **disabled** in the tester build. Use the stub endpoint:

```
POST https://localhost:8443/api/v1/auth/dev
Content-Type: application/json

{ "uuid": "<a UUID v4 the client persists locally>" }
```

The same UUID always logs into the same player. To start a fresh test account,
have the client generate a new UUID.

The live build uses `POST /api/v1/auth/steam` instead — the client should pick
the endpoint at build time.

---

## 4. Sharing the server with another tester on the LAN

By default the server is only reachable from your own machine (`127.0.0.1`).
To let another PC on the same network connect:

1. Edit `.env` and change:
   ```
   LAN_BIND=0.0.0.0
   ```
2. Run `stop.*`, then `start.*`.
3. The other PC connects to `https://<your-LAN-IP>:8443`.

> **Do not expose the server to the internet.** This build has Steam auth
> disabled and is intended for local/LAN testing only.

---

## 5. Troubleshooting

**"Docker Desktop is not running"**
Start Docker Desktop and wait for the whale icon to stop animating.

**"port already in use"**
Something else on your PC is using port 8080 or 8443. Edit `.env` and change
`HTTP_PORT` / `HTTPS_PORT` to free ports.

**Browser warns about the SSL certificate**
Expected — the tester uses a self-signed cert. The Unity client in test mode
is configured to skip TLS verification.

**Anything else**
Run `logs.*` and send the output to the dev team.

---

## 6. Privacy & security

- `.env` is generated on your machine and never leaves it. The DB password and
  JWT signing key inside are random per-machine values.
- This build contains no production secrets — leaking `.env` from a tester
  machine has no impact on the live server.
- Postgres and Redis are not exposed outside Docker's internal network.
