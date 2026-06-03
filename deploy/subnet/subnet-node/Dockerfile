# «Сырой» узел avalanchego, отдающий цепочку WMS Subnet-EVM.
# Заменяет старый C-Chain-узел (deploy/avalanche). Container-native:
# --http-host=0.0.0.0 — чтобы dockerized ledger-adapter достучался напрямую;
# --track-subnets с преднабитым детерминированным subnetID — чтобы цепочка, созданная
# subnet-init, подхватывалась на лету (без рестарта). См. deploy/subnet/node-config.json.

ARG SUBNET_EVM_VERSION=0.8.0

# Тянем запиненный плагин subnet-evm под нужную архитектуру (нативно arm64 / amd64,
# без QEMU). Воспроизводимее, чем сборка из исходников.
FROM curlimages/curl:8.11.1 AS plugin
ARG TARGETARCH
ARG SUBNET_EVM_VERSION
RUN curl -fsSL -o /tmp/se.tar.gz \
      "https://github.com/ava-labs/subnet-evm/releases/download/v${SUBNET_EVM_VERSION}/subnet-evm_${SUBNET_EVM_VERSION}_linux_${TARGETARCH}.tar.gz" \
 && tar -xzf /tmp/se.tar.gz -C /tmp subnet-evm

FROM avaplatform/avalanchego:v1.14.0
# curl нужен для healthcheck в compose; в базовом образе (Debian 12) его нет.
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# Канонический VMID subnet-evm — файл плагина ОБЯЗАН называться так; subnet-init в
# CreateChainTx ссылается на тот же VMID, а node-config.json задаёт plugin-dir=/plugins.
COPY --from=plugin --chmod=0755 /tmp/subnet-evm /plugins/srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy
