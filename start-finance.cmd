@echo off
rem One-click start for the family finance service (Windows scheduled task).
rem Double-click this file. Nothing is installed; it only triggers the task.
powershell -NoProfile -Command "Start-ScheduledTask -TaskName 'FamilyFinance'"
echo.
echo Starting FamilyFinance (service + Cloudflare tunnel) ...
echo Wait about 20-30 seconds, then open the fixed URL.
echo Logs: logs\service.log
timeout /t 6 >nul
