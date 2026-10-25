@echo off
rem Comme 1-demarrer-serveur.bat, mais accessible depuis les autres machines du reseau (rigs).
rem Au premier lancement, Windows demande d'autoriser le pare-feu : coche "Reseaux prives".
cd /d "%~dp0"
set RUCHE_ADMIN_PASSWORD=ruche1234
set RUCHE_LISTEN=:8080
echo Adresse(s) de ce PC sur le reseau :
ipconfig | findstr /i "IPv4"
echo Ouvre le tableau de bord avec http://ADRESSE:8080 avant de creer un rig.
ruche-server.exe
pause
