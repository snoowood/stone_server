@echo off
setlocal
cd /d "%~dp0"
echo === Stone Server (tester) — last 200 log lines ===
docker compose -f docker-compose.tester.yml logs --tail=200
echo.
echo (Press Ctrl+C to exit if you streamed live with --follow)
pause
