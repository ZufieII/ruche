@echo off
rem Lance le serveur Ruche et ouvre le tableau de bord.
cd /d "%~dp0"
set RUCHE_ADMIN_PASSWORD=ruche1234
set RUCHE_LISTEN=127.0.0.1:8080
start "" http://localhost:8080
ruche-server.exe
pause
