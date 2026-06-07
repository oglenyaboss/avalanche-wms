#!/usr/bin/env bash
set -euo pipefail
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 2G /swapfile; chmod 600 /swapfile; mkswap /swapfile; swapon /swapfile
  grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi
swapon --show; free -h
