#!/usr/bin/env bash
set -euo pipefail
bash /root/fetch-artifacts.sh /opt/avalanchego
mkdir -p /etc/avalanchego /var/lib/avalanchego
install -m 0644 /root/avalanchego.service /etc/systemd/system/avalanchego.service
systemctl daemon-reload
echo "installed: $(/opt/avalanchego/avalanchego --version | head -1)"
