#!/usr/bin/env bash
# Installe l'agent Ruche sur un rig Debian ou Ubuntu qui a déjà ses pilotes GPU.
# Usage : sudo ./install-agent.sh ruche-agent rig.conf
set -euo pipefail

# Vérifie la dernière version sur https://github.com/Lolliedieb/lolMiner-releases/releases
LOLMINER_VERSION="${LOLMINER_VERSION:-1.88}"

AGENT_BIN="${1:?chemin du binaire ruche-agent}"
RIG_CONF="${2:?chemin du fichier rig.conf affiché par le tableau de bord}"
[[ $EUID -eq 0 ]] || { echo "Lance ce script avec sudo." >&2; exit 1; }

install -m 0755 "$AGENT_BIN" /usr/local/bin/ruche-agent
install -d -m 0700 /etc/ruche
install -m 0600 "$RIG_CONF" /etc/ruche/rig.conf

if [[ ! -x /opt/ruche/miners/lolminer/lolMiner ]]; then
  echo "Téléchargement de lolMiner ${LOLMINER_VERSION}…"
  tmp="$(mktemp -d)"
  curl -fsSL -o "$tmp/lolminer.tar.gz" \
    "https://github.com/Lolliedieb/lolMiner-releases/releases/download/${LOLMINER_VERSION}/lolMiner_v${LOLMINER_VERSION}_Lin64.tar.gz"
  tar -xzf "$tmp/lolminer.tar.gz" -C "$tmp"
  install -d /opt/ruche/miners/lolminer
  install -m 0755 "$(find "$tmp" -type f -name lolMiner | head -n1)" /opt/ruche/miners/lolminer/lolMiner
  rm -rf "$tmp"
fi

install -m 0644 "$(dirname "$0")/ruche-agent.service" /etc/systemd/system/ruche-agent.service
systemctl daemon-reload
systemctl enable --now ruche-agent
echo "Agent installé. Suivi : journalctl -u ruche-agent -f"
