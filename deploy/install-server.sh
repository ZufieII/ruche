#!/usr/bin/env bash
# Installe le serveur Ruche comme service sur Debian ou Ubuntu (VM, LXC Proxmox, etc.).
# Usage : sudo ./install-server.sh ruche-server [ruche.db à reprendre]
set -euo pipefail

SERVER_BIN="${1:?chemin du binaire ruche-server (Linux)}"
OLD_DB="${2:-}"
[[ $EUID -eq 0 ]] || { echo "Lance ce script avec sudo." >&2; exit 1; }

id ruche >/dev/null 2>&1 || useradd --system --home /var/lib/ruche --shell /usr/sbin/nologin ruche
install -d -o ruche -g ruche -m 0750 /var/lib/ruche
install -m 0755 "$SERVER_BIN" /usr/local/bin/ruche-server

if [[ -n "$OLD_DB" ]]; then
  systemctl stop ruche-server 2>/dev/null || true
  install -o ruche -g ruche -m 0640 "$OLD_DB" /var/lib/ruche/ruche.db
  rm -f /var/lib/ruche/ruche.db-wal /var/lib/ruche/ruche.db-shm
fi

install -d -m 0750 /etc/ruche
if [[ ! -f /etc/ruche/server.env ]]; then
  read -rsp "Mot de passe du tableau de bord (8 caractères minimum) : " pass; echo
  printf 'RUCHE_ADMIN_PASSWORD=%s\nRUCHE_LISTEN=:8080\nRUCHE_MAX_TEMP=80\n' "$pass" > /etc/ruche/server.env
  chmod 0640 /etc/ruche/server.env
  chgrp ruche /etc/ruche/server.env
fi

install -m 0644 "$(dirname "$0")/ruche-server.service" /etc/systemd/system/ruche-server.service
systemctl daemon-reload
systemctl enable --now ruche-server
systemctl restart ruche-server
echo "Serveur installé sur le port 8080. Suivi : journalctl -u ruche-server -f"
