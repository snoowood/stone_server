@echo off
setlocal
cd /d "%~dp0"

echo === Stone Server (tester) — updating ===
echo.
echo [git] Pulling latest release files...
git pull --ff-only
if errorlevel 1 (
  echo [error] git pull failed. Resolve conflicts manually and re-run update.bat.
  pause
  exit /b 1
)

echo [pull] Fetching the latest server image...
docker compose -f docker-compose.tester.yml pull
if errorlevel 1 (
  echo [error] docker compose pull failed.
  pause
  exit /b 1
)

echo [up] Restarting services with the new image...
docker compose -f docker-compose.tester.yml up -d
echo.
echo === Update complete ===
pause
