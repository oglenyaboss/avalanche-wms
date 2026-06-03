// Команда subnet-init поднимает блокчейн WMS Subnet-EVM на «сыром» узле avalanchego
// (subnet-node1), идемпотентно, и пишет динамический RPC URL в shared-том — его читают
// contract-deploy и ledger-adapter.
//
// Механизм (проверен 2026-06-03): узел стартует, уже трекая детерминированный subnetID
// (2W9boA…, преднабит через --track-subnets), поэтому создание блокчейна здесь
// подхватывается узлом НА ЛЕТУ — рестарт не нужен. Поднятие — это legacy non-sovereign
// flow через Go wallet SDK ключом ewoq (локальная подпись):
// CreateSubnet -> AddSubnetValidator -> CreateChain. Etna fee-state НЕ упирается в
// deadlock на avalanchego v1.14.0 (поэтому это и заменяет старый C-Chain-костыль).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
)

const (
	wantChainIDHex = "0x1869f" // 99999, из chain-genesis.json
	healthTimeout  = 180 * time.Second
	chainTimeout   = 120 * time.Second
	// End валидатора сабнета должен быть <= End валидатора primary-сети (2027-06-02 в
	// закоммиченном network-genesis.json). Буфер 1 час ниже него.
	validatorEnd = uint64(1811958568 - 3600)
)

func main() {
	cfg := config{
		uri:            getenv("AVAGO_URI", "http://subnet-node1:9650"),
		nodeIDStr:      getenv("NODE_ID", "NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg"),
		vmIDStr:        getenv("VM_ID", "srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy"),
		genesisPath:    getenv("CHAIN_GENESIS", "/genesis/chain-genesis.json"),
		chainName:      getenv("CHAIN_NAME", "WMSSubnetEVM"),
		expectedSubnet: getenv("EXPECTED_SUBNET_ID", "2W9boARgCWL25z6pMFNtkCfNA5v28VGg9PmBgUJfuKndEdhrvw"),
		sharedRPCFile:  getenv("SHARED_RPC_FILE", "/shared/rpc_url.txt"),
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

type config struct {
	uri, nodeIDStr, vmIDStr, genesisPath, chainName, expectedSubnet, sharedRPCFile string
}

func run(ctx context.Context, cfg config) error {
	fmt.Printf("==> subnet-init: node=%s chain=%q expectedSubnet=%s\n", cfg.uri, cfg.chainName, cfg.expectedSubnet)

	fmt.Println("==> waiting for node health")
	if err := waitHealthy(cfg.uri, healthTimeout); err != nil {
		return err
	}

	blockchainID, found, err := findChain(cfg.uri, cfg.chainName, cfg.expectedSubnet)
	if err != nil {
		return fmt.Errorf("idempotency check: %w", err)
	}
	if found {
		fmt.Printf("==> chain %q already exists: %s — skipping creation\n", cfg.chainName, blockchainID)
	} else {
		blockchainID, err = createChain(ctx, cfg)
		if err != nil {
			return err
		}
	}

	rpcURL := cfg.uri + "/ext/bc/" + blockchainID + "/rpc"
	fmt.Printf("==> waiting for chain RPC to report chainId %s at %s\n", wantChainIDHex, rpcURL)
	if err := waitChainLive(rpcURL, chainTimeout); err != nil {
		return err
	}

	if err := os.WriteFile(cfg.sharedRPCFile, []byte(rpcURL), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", cfg.sharedRPCFile, err)
	}
	fmt.Printf("==> wrote %s = %s\n", cfg.sharedRPCFile, rpcURL)
	return nil
}

// createChain выполняет CreateSubnet -> AddSubnetValidator -> CreateChain через wallet
// SDK и возвращает новый blockchainID.
func createChain(ctx context.Context, cfg config) (string, error) {
	nodeID, err := ids.NodeIDFromString(cfg.nodeIDStr)
	if err != nil {
		return "", fmt.Errorf("parse NODE_ID: %w", err)
	}
	vmID, err := ids.FromString(cfg.vmIDStr)
	if err != nil {
		return "", fmt.Errorf("parse VM_ID: %w", err)
	}
	genesisBytes, err := os.ReadFile(cfg.genesisPath)
	if err != nil {
		return "", fmt.Errorf("read chain genesis %s: %w", cfg.genesisPath, err)
	}

	key := genesis.EWOQKey
	kc := secp256k1fx.NewKeychain(key)
	wallet, err := primary.MakeWallet(ctx, cfg.uri, kc, kc, primary.WalletConfig{})
	if err != nil {
		return "", fmt.Errorf("make wallet: %w", err)
	}
	pWallet := wallet.P()

	fmt.Println("==> IssueCreateSubnetTx")
	createSubnetTx, err := pWallet.IssueCreateSubnetTx(&secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{key.Address()},
	})
	if err != nil {
		return "", fmt.Errorf("create subnet: %w", err)
	}
	subnetID := createSubnetTx.ID()
	// Громкая проверка: «уплывший» subnetID означает, что преднабитый --track-subnets
	// узла неверен и цепочка никогда не будет отдаваться — падаем явно, а не оставляем
	// тихо мёртвый RPC. (Причина — несвежий data-том в частичном состоянии; лечится
	// сбросом тома subnet_data.)
	if subnetID.String() != cfg.expectedSubnet {
		return "", fmt.Errorf("created subnetID %s != expected %s (node --track-subnets won't match; reset the subnet_data volume)", subnetID, cfg.expectedSubnet)
	}
	fmt.Printf("    subnetID = %s (matches prebaked track-subnets)\n", subnetID)

	fmt.Printf("==> IssueAddSubnetValidatorTx (nodeID = %s)\n", nodeID)
	if _, err := pWallet.IssueAddSubnetValidatorTx(&txs.SubnetValidator{
		Validator: txs.Validator{
			NodeID: nodeID,
			Start:  uint64(time.Now().Add(20 * time.Second).Unix()),
			End:    validatorEnd,
			Wght:   100,
		},
		Subnet: subnetID,
	}); err != nil {
		return "", fmt.Errorf("add subnet validator: %w", err)
	}

	fmt.Printf("==> IssueCreateChainTx (vmID = %s)\n", vmID)
	createChainTx, err := pWallet.IssueCreateChainTx(subnetID, genesisBytes, vmID, nil, cfg.chainName)
	if err != nil {
		return "", fmt.Errorf("create chain: %w", err)
	}
	blockchainID := createChainTx.ID().String()
	fmt.Printf("    blockchainID = %s\n", blockchainID)
	return blockchainID, nil
}

// findChain возвращает blockchainID существующей цепочки с заданным именем либо
// found=false, если её нет. Ошибка — если одноимённая цепочка стоит на неожиданном сабнете.
func findChain(uri, name, expectedSubnet string) (string, bool, error) {
	var resp struct {
		Result struct {
			Blockchains []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				SubnetID string `json:"subnetID"`
			} `json:"blockchains"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := jsonRPC(uri+"/ext/bc/P", `{"jsonrpc":"2.0","id":1,"method":"platform.getBlockchains"}`, &resp); err != nil {
		return "", false, err
	}
	if resp.Error != nil {
		return "", false, fmt.Errorf("platform.getBlockchains: %s", resp.Error.Message)
	}
	for _, b := range resp.Result.Blockchains {
		if b.Name != name {
			continue
		}
		if b.SubnetID != expectedSubnet {
			return "", false, fmt.Errorf("chain %q is on subnet %s, expected %s", name, b.SubnetID, expectedSubnet)
		}
		return b.ID, true, nil
	}
	return "", false, nil
}

func waitHealthy(uri string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(uri + "/ext/health")
		if err == nil {
			var h struct {
				Healthy bool `json:"healthy"`
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if json.Unmarshal(data, &h) == nil && h.Healthy {
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("node %s not healthy within %s", uri, timeout)
}

func waitChainLive(rpcURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var resp struct {
			Result string `json:"result"`
		}
		if err := jsonRPC(rpcURL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`, &resp); err == nil && resp.Result == wantChainIDHex {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("chain RPC %s did not return chainId %s within %s", rpcURL, wantChainIDHex, timeout)
}

func jsonRPC(url, body string, out any) error {
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
