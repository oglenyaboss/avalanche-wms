# Простой local avalanchego — C-Chain прямо из коробки, без custom subnet.
# Subnet-EVM плагин не нужен, т.к. контракт деплоится на C-Chain (coreth).
# Pin на linux/amd64: команда работает на amd64 (native), у lead'а arm Mac (qemu).
# Общий образ → идентичное поведение у всех, цена — qemu-оверхед на одной машине.
FROM --platform=linux/amd64 avaplatform/avalanchego:v1.12.2
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl jq \
 && rm -rf /var/lib/apt/lists/*
