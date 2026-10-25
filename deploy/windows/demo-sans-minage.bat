@echo off
rem Simule un processeur et XMRig : rien n'est mine, c'est juste pour voir le tableau de bord.
cd /d "%~dp0"
if not exist rig.conf (
  echo Il manque le fichier rig.conf. Voir LISEZMOI.txt.
  pause
  exit /b 1
)
ruche-agent.exe -fake-cpu
pause
