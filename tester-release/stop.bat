@echo off
setlocal
cd /d "%~dp0"
echo === Stone Server (tester) — stopping ===
docker compose -f docker-compose.tester.yml down
echo.
echo Server stopped. Data is preserved in Docker volumes.
echo To wipe data too, run: docker compose -f docker-compose.tester.yml down -v
pause
