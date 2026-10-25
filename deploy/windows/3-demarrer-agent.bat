@echo off
rem Connecte ce PC au serveur Ruche et lance le minage CPU.
cd /d "%~dp0"
if not exist rig.conf (
  echo Il manque le fichier rig.conf. Cree un rig dans le tableau de bord,
  echo clique sur "Telecharger rig.conf" et place le fichier dans ce dossier.
  pause
  exit /b 1
)
ruche-agent.exe
pause
