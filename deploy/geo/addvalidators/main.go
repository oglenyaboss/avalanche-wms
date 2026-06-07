// Command addvalidators brings up the geo subnet-evm chain on a FRESH avalanche network
// (networkID 1338) and registers the given primary-network validators as subnet validators.
//
// Unlike deploy/subnet/subnet-init (which expects a deterministic, prebaked subnetID because
// its node tracks it from boot), here the subnetID is NOT known in advance: a fresh network
// has different ewoq UTXOs, so CreateSubnetTx yields a fresh ID. We therefore PRINT the
// resulting SUBNET_ID / BLOCKCHAIN_ID for wiring into each validator's --track-subnets, after
// which the nodes are restarted to start serving the chain.
//
// Flow (ewoq-signed, local signing via wallet SDK): CreateSubnet -> CreateChain ->
// AddSubnetValidator (per nodeID). All validators are already primary-network validators
// (baked into genesis as initialStakers), so only AddSubnetValidatorTx is needed — no dynamic
// primary-add, no BLS proof-of-possession dance. Idempotent: skips creation if the named chain
// exists, skips a nodeID already registered on the subnet. Each Issue* blocks until the tx is
// accepted, so a clean run also proves the live validator set finalizes blocks.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
)

const (
	wantChainIDHex = "0x1869f" // 99999, from chain-genesis.json
	healthTimeout  = 180 * time.Second
	validatorWght  = 100 // subnet validator weight (non-sovereign subnet; arbitrary)
)

type config struct {
	uri, nodeIDsCSV, vmIDStr, genesisPath, chainName string
	valEnd                                           uint64
}

func main() {
	cfg := config{
		uri:         getenv("AVAGO_URI", "http://localhost:19650"),
		nodeIDsCSV:  getenv("NODE_IDS", ""),
		vmIDStr:     getenv("VM_ID", "srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy"),
		genesisPath: getenv("CHAIN_GENESIS", "../genesis/chain-genesis.json"),
		chainName:   getenv("CHAIN_NAME", "GeoSubnetEVM"),
		valEnd:      getenvUint("SUBNET_VALIDATOR_END", 0), // 0 => derive from primary validators
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func run(ctx context.Context, cfg config) error {
	if strings.TrimSpace(cfg.nodeIDsCSV) == "" {
		return fmt.Errorf("NODE_IDS is required (comma-separated nodeIDs to register as subnet validators)")
	}
	var nodeIDs []ids.NodeID
	for _, s := range strings.Split(cfg.nodeIDsCSV, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		n, err := ids.NodeIDFromString(s)
		if err != nil {
			return fmt.Errorf("parse nodeID %q: %w", s, err)
		}
		nodeIDs = append(nodeIDs, n)
	}
	fmt.Printf("==> addvalidators: uri=%s chain=%q nodeIDs=%v\n", cfg.uri, cfg.chainName, nodeIDs)

	fmt.Println("==> waiting for node health")
	if err := waitHealthy(cfg.uri, healthTimeout); err != nil {
		return err
	}

	valEnd := cfg.valEnd
	if valEnd == 0 {
		minEnd, err := primaryMinEnd(cfg.uri, nodeIDs)
		if err != nil {
			return fmt.Errorf("derive subnet validator end: %w", err)
		}
		valEnd = minEnd - 3600 // 1h below the earliest primary-validator end
	}
	fmt.Printf("==> subnet validator End = %d\n", valEnd)

	vmID, err := ids.FromString(cfg.vmIDStr)
	if err != nil {
		return fmt.Errorf("parse VM_ID: %w", err)
	}

	key := genesis.EWOQKey
	kc := secp256k1fx.NewKeychain(key)
	wallet, err := primary.MakeWallet(ctx, cfg.uri, kc, kc, primary.WalletConfig{})
	if err != nil {
		return fmt.Errorf("make wallet: %w", err)
	}
	pWallet := wallet.P()

	// Idempotency: reuse an existing chain of this name, else create subnet + chain.
	blockchainID, subnetStr, found, err := findChain(cfg.uri, cfg.chainName)
	if err != nil {
		return fmt.Errorf("idempotency check: %w", err)
	}
	var subnetID ids.ID
	if found {
		if subnetID, err = ids.FromString(subnetStr); err != nil {
			return fmt.Errorf("parse existing subnetID %q: %w", subnetStr, err)
		}
		fmt.Printf("==> chain %q exists: blockchain=%s subnet=%s — skipping creation\n", cfg.chainName, blockchainID, subnetID)
	} else {
		fmt.Println("==> IssueCreateSubnetTx")
		createSubnetTx, err := pWallet.IssueCreateSubnetTx(&secp256k1fx.OutputOwners{
			Threshold: 1,
			Addrs:     []ids.ShortID{key.Address()},
		})
		if err != nil {
			return fmt.Errorf("create subnet: %w", err)
		}
		subnetID = createSubnetTx.ID()
		fmt.Printf("    subnetID = %s\n", subnetID)

		genesisBytes, err := os.ReadFile(cfg.genesisPath)
		if err != nil {
			return fmt.Errorf("read chain genesis %s: %w", cfg.genesisPath, err)
		}
		fmt.Printf("==> IssueCreateChainTx (vmID = %s)\n", vmID)
		createChainTx, err := pWallet.IssueCreateChainTx(subnetID, genesisBytes, vmID, nil, cfg.chainName)
		if err != nil {
			return fmt.Errorf("create chain: %w", err)
		}
		blockchainID = createChainTx.ID().String()
		fmt.Printf("    blockchainID = %s\n", blockchainID)
	}

	// Register each nodeID as a subnet validator (idempotent).
	existing, err := subnetValidators(cfg.uri, subnetID.String())
	if err != nil {
		return fmt.Errorf("list subnet validators: %w", err)
	}
	for _, nodeID := range nodeIDs {
		if existing[nodeID.String()] {
			fmt.Printf("==> %s already a subnet validator — skip\n", nodeID)
			continue
		}
		fmt.Printf("==> IssueAddSubnetValidatorTx (%s)\n", nodeID)
		if _, err := pWallet.IssueAddSubnetValidatorTx(&txs.SubnetValidator{
			Validator: txs.Validator{
				NodeID: nodeID,
				Start:  uint64(time.Now().Add(20 * time.Second).Unix()),
				End:    valEnd,
				Wght:   validatorWght,
			},
			Subnet: subnetID,
		}); err != nil {
			return fmt.Errorf("add subnet validator %s: %w", nodeID, err)
		}
	}

	fmt.Printf("\n==> RESULT\nSUBNET_ID=%s\nBLOCKCHAIN_ID=%s\nRPC_PATH=/ext/bc/%s/rpc\nWANT_CHAINID=%s\n",
		subnetID, blockchainID, blockchainID, wantChainIDHex)
	return nil
}

// findChain returns (blockchainID, subnetID, found) for a chain with the given name.
func findChain(uri, name string) (string, string, bool, error) {
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
		return "", "", false, err
	}
	if resp.Error != nil {
		return "", "", false, fmt.Errorf("platform.getBlockchains: %s", resp.Error.Message)
	}
	for _, b := range resp.Result.Blockchains {
		if b.Name == name {
			return b.ID, b.SubnetID, true, nil
		}
	}
	return "", "", false, nil
}

// subnetValidators returns the set of nodeIDs currently validating the given subnet.
func subnetValidators(uri, subnetID string) (map[string]bool, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"platform.getCurrentValidators","params":{"subnetID":%q}}`, subnetID)
	var resp struct {
		Result struct {
			Validators []struct {
				NodeID string `json:"nodeID"`
			} `json:"validators"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := jsonRPC(uri+"/ext/bc/P", body, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("platform.getCurrentValidators(subnet): %s", resp.Error.Message)
	}
	set := make(map[string]bool, len(resp.Result.Validators))
	for _, v := range resp.Result.Validators {
		set[v.NodeID] = true
	}
	return set, nil
}

// primaryMinEnd returns the earliest endTime among the given nodeIDs on the primary network.
func primaryMinEnd(uri string, nodeIDs []ids.NodeID) (uint64, error) {
	var resp struct {
		Result struct {
			Validators []struct {
				NodeID  string `json:"nodeID"`
				EndTime string `json:"endTime"`
			} `json:"validators"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := jsonRPC(uri+"/ext/bc/P", `{"jsonrpc":"2.0","id":1,"method":"platform.getCurrentValidators","params":{}}`, &resp); err != nil {
		return 0, err
	}
	if resp.Error != nil {
		return 0, fmt.Errorf("platform.getCurrentValidators: %s", resp.Error.Message)
	}
	want := make(map[string]bool, len(nodeIDs))
	for _, n := range nodeIDs {
		want[n.String()] = true
	}
	var min uint64
	for _, v := range resp.Result.Validators {
		if !want[v.NodeID] {
			continue
		}
		e, err := strconv.ParseUint(v.EndTime, 10, 64)
		if err != nil {
			continue
		}
		if min == 0 || e < min {
			min = e
		}
	}
	if min == 0 {
		return 0, fmt.Errorf("none of %v found among primary validators (are they primary validators yet?)", nodeIDs)
	}
	return min, nil
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

func getenvUint(k string, d uint64) uint64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return d
}
