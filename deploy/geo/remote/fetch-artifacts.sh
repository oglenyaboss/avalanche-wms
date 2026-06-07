#!/usr/bin/env bash
set -euo pipefail
AVAGO_VER="${AVAGO_VER:-v1.14.0}"; SE_VER="${SE_VER:-0.8.0}"
VMID="srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy"
DEST="${1:-/opt/avalanchego}"; mkdir -p "$DEST/plugins"
curl -fsSL -o /tmp/avago.tar.gz \
  "https://github.com/ava-labs/avalanchego/releases/download/${AVAGO_VER}/avalanchego-linux-amd64-${AVAGO_VER}.tar.gz"
tar -xzf /tmp/avago.tar.gz -C /tmp
find /tmp -maxdepth 2 -name avalanchego -type f -exec cp {} "$DEST/avalanchego" \;
curl -fsSL -o /tmp/se.tar.gz \
  "https://github.com/ava-labs/subnet-evm/releases/download/v${SE_VER}/subnet-evm_${SE_VER}_linux_amd64.tar.gz"
tar -xzf /tmp/se.tar.gz -C /tmp subnet-evm
cp /tmp/subnet-evm "$DEST/plugins/$VMID"
chmod +x "$DEST/avalanchego" "$DEST/plugins/$VMID"
"$DEST/avalanchego" --version
