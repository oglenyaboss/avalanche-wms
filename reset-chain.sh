#!/bin/bash
# reset-chain.sh — recreate ONLY the Subnet-EVM chain with the edited genesis (targetGas=2B),
# preserving postgres (OLTP tuning + base data) and kafka. Targeted reset = wipe subnet_data +
# shared_state, rebuild subnet trio (new chain + contract), restart adapter to repoint.
set -u
cd /Users/lenya/Projects/university/blockchain_project/.claude/worktrees/stress_tests
export COMPOSE_PROJECT_NAME=stresstest
export PATH="$HOME/.foundry/bin:$PATH"

echo "=== 0. genesis check (targetGas) ==="
grep -E '"targetGas"' deploy/subnet/chain-genesis.json

echo "=== 1. stop subnet trio + adapter (release shared_state mount) ==="
docker compose --profile test rm -sf subnet-node1 subnet-init contract-deploy >/dev/null 2>&1
docker stop ledger-adapter >/dev/null 2>&1
docker rm ledger-adapter >/dev/null 2>&1

echo "=== 2. wipe chain volumes (subnet_data + shared_state); KEEP postgres_data + kafka_data ==="
docker volume rm stresstest_subnet_data stresstest_shared_state 2>&1 | sed 's/^/  /'

echo "=== 3. bring subnet back up with NEW genesis ==="
docker compose --profile test up -d subnet-node1 subnet-init contract-deploy >/dev/null 2>&1

echo "=== 4. wait for node healthy + subnet-init + contract-deploy ==="
for i in $(seq 1 60); do
  st=$(docker inspect subnet-node1 --format '{{.State.Health.Status}}' 2>/dev/null)
  ci=$(docker inspect subnet_init --format '{{.State.Status}}:{{.State.ExitCode}}' 2>/dev/null)
  cd_=$(docker inspect contract_deploy --format '{{.State.Status}}:{{.State.ExitCode}}' 2>/dev/null)
  echo "  try $i: node=$st subnet_init=$ci contract_deploy=$cd_"
  [ "$st" = "healthy" ] && echo "$cd_" | grep -q 'exited:0' && { echo "  chain ready"; break; }
  sleep 5
done
echo "  contract-deploy log tail:"; docker logs contract_deploy 2>&1 | tail -3 | sed 's/^/    /'

echo "=== 5. new RPC + contract from shared_state ==="
VOL=stresstest_shared_state
RPC=$(docker run --rm -v ${VOL}:/s busybox sh -c 'cat /s/rpc_url.txt 2>/dev/null')
ADDR=$(docker run --rm -v ${VOL}:/s busybox sh -c 'cat /s/contract_addr.txt 2>/dev/null')
echo "  RPC=$RPC"
echo "  CONTRACT=$ADDR"
echo "$RPC" | sed -E 's#http://[^/]+#http://127.0.0.1:9650#' > /tmp/hrpc.txt
echo "  host RPC: $(cat /tmp/hrpc.txt)"

echo "=== 6. restart adapter (strong profile) → repoint to new chain + reseed nonce ==="
BATCH_SIZE=900 BATCH_TIMEOUT=2s PIPELINE_WINDOW=8 docker compose up -d ledger-adapter >/dev/null 2>&1
for i in $(seq 1 20); do
  docker logs ledger-adapter 2>&1 | grep -q '"batch_size":900' && { echo "  adapter up"; break; }
  sleep 2
done
docker logs ledger-adapter 2>&1 | grep 'config loaded' | tail -1 | sed 's/^/  /'

echo "=== 7. verify baseFee at floor on fresh chain ==="
python3 /tmp/feecheck.py 2>&1 | sed 's/^/  /'
echo "######## RESET DONE ########"
