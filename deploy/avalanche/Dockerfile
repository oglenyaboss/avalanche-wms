# Простой local avalanchego — C-Chain прямо из коробки, без custom subnet.
# Subnet-EVM плагин не нужен, т.к. контракт деплоится на C-Chain (coreth).
FROM --platform=linux/amd64 avaplatform/avalanchego:v1.12.2
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl jq \
 && rm -rf /var/lib/apt/lists/*
