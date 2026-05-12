@echo off
setlocal
cd /d "%~dp0"

echo === Stone Server (tester) — starting ===
echo.

docker info >NUL 2>&1
if not errorlevel 1 goto :docker_ok

echo [init] Docker Desktop is not running — trying to launch it...
set "DOCKER_EXE=%ProgramFiles%\Docker\Docker\Docker Desktop.exe"
if not exist "%DOCKER_EXE%" set "DOCKER_EXE=%LocalAppData%\Docker\Docker\Docker Desktop.exe"
if not exist "%DOCKER_EXE%" (
  echo [error] Docker Desktop is not installed.
  echo         Install it from https://www.docker.com/products/docker-desktop/
  pause
  exit /b 1
)
start "" "%DOCKER_EXE%"
echo [init] Waiting for Docker daemon (up to 90s)...
set /a DTRIES=0
:wait_docker
set /a DTRIES+=1
timeout /t 3 /nobreak >NUL
docker info >NUL 2>&1
if not errorlevel 1 goto :docker_ok
if %DTRIES% GEQ 30 (
  echo [error] Docker did not become ready within 90 seconds.
  echo         Launch Docker Desktop manually, wait for the whale to stop animating, then re-run start.bat.
  pause
  exit /b 1
)
goto wait_docker

:docker_ok
echo [init] Docker is ready.

if not exist .env (
  echo [init] First run — generating .env with random secrets...
  docker run --rm -v "%CD%:/work" -w /work alpine:3.19 sh /work/_internal/init-env.sh
  if errorlevel 1 (
    echo [error] Failed to generate .env.
    pause
    exit /b 1
  )
)

echo [pull] Fetching the latest server image...
docker compose -f docker-compose.tester.yml pull
if errorlevel 1 (
  echo [error] docker compose pull failed.
  echo         If you see "denied" / "unauthorized", run this once:
  echo             docker login ghcr.io
  pause
  exit /b 1
)

echo [up] Starting services...
docker compose -f docker-compose.tester.yml up -d
if errorlevel 1 (
  echo [error] docker compose up failed. Check logs.bat for details.
  pause
  exit /b 1
)

echo [wait] Waiting for the server to become healthy...
set /a TRIES=0
:wait_loop
set /a TRIES+=1
curl -sf -k https://localhost:8443/api/v1/health >NUL 2>&1
if not errorlevel 1 goto :ok
if %TRIES% GEQ 30 goto :timeout
timeout /t 2 /nobreak >NUL
goto wait_loop

:ok
echo.
echo === Server ready ===
echo   Health check : https://localhost:8443/api/v1/health
echo   Client URL   : https://localhost:8443
echo.
echo Use stop.bat to shut down, logs.bat to view logs.
pause
exit /b 0

:timeout
echo [error] Server did not become healthy within 60 seconds.
echo         Run logs.bat and send the output to the dev team.
pause
exit /b 1
