@echo off
rem Telecharge XMRig (mineur CPU) dans le dossier xmrig a cote de ce fichier.
cd /d "%~dp0"
set XMRIG_VERSION=6.22.2
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop';" ^
  "$v='%XMRIG_VERSION%';" ^
  "Invoke-WebRequest -Uri ('https://github.com/xmrig/xmrig/releases/download/v'+$v+'/xmrig-'+$v+'-msvc-win64.zip') -OutFile xmrig.zip;" ^
  "Expand-Archive xmrig.zip -DestinationPath xmrig-tmp -Force;" ^
  "if (Test-Path xmrig) { Remove-Item xmrig -Recurse -Force };" ^
  "Move-Item (Get-ChildItem xmrig-tmp -Directory | Select-Object -First 1).FullName xmrig;" ^
  "Remove-Item xmrig.zip, xmrig-tmp -Recurse -Force"
if exist xmrig\xmrig.exe (echo XMRig est installe.) else (echo Echec : Windows Defender a peut-etre bloque XMRig. Voir LISEZMOI.txt.)
pause
