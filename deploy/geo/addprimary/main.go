// Command addprimary registers a node as a PRIMARY-network validator on the geo custom network
// (networkID 1338) via AddPermissionlessValidatorTx, signed by ewoq.
//
// The 3 original validators are genesis initialStakers, so deploy/geo/addvalidators only needed
// AddSubnetValidatorTx ("no dynamic primary-add, no BLS proof-of-possession dance"). A 4th node
// added post-genesis is NOT in genesis, so it must first become a primary-network validator here
// (BLS PoP + stake) BEFORE addvalidators can register it on the subnet.
//
// Why a 4th validator at all: 3 equal-stake validators -> killing 1 drops connected stake to
// 66.67%, below avalanchego's 73.33% health threshold, which freezes non-validating observer nodes
// (the ledger-adapter's RPC node) and breaks the WMS pipeline under a node fault. 4 equal
// validators -> kill-1 leaves 75% > 73.33%, keeping the observer healthy.
//
// Idempotent: skips if the nodeID is already a primary validator. The PoP is built from the node's
// harvested publicKey+proofOfPossession (no secret key needed) and Verify()'d before submitting.
//
//	AVAGO_URI=http://localhost:9750 NODE_ID=NodeID-... BLS_PUBKEY=0x... BLS_POP=0x... \
//	  STAKE=1000000000000000 END=1812328099 go run .
package main

import (
	"bytes"
	"context"
	"encoding/hex"
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
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func run(ctx context.Context) error {
	uri := getenv("AVAGO_URI", "http://localhost:9750")
	nodeIDStr := strings.TrimSpace(getenv("NODE_ID", ""))
	pkHex := getenv("BLS_PUBKEY", "")
	popHex := getenv("BLS_POP", "")
	stake := getenvUint("STAKE", 1_000_000_000_000_000) // 1M AVAX — match the 3 genesis validators
	end := getenvUint("END", 1812328099)                // match existing primary validators' endTime
	shares := uint32(getenvUint("SHARES", 10000))       // 1% delegation fee (genesis uses 10000)

	if nodeIDStr == "" || pkHex == "" || popHex == "" {
		return fmt.Errorf("NODE_ID, BLS_PUBKEY, BLS_POP are required")
	}
	nodeID, err := ids.NodeIDFromString(nodeIDStr)
	if err != nil {
		return fmt.Errorf("parse NODE_ID %q: %w", nodeIDStr, err)
	}
	pop, err := buildPoP(pkHex, popHex)
	if err != nil {
		return fmt.Errorf("build proof-of-possession: %w", err)
	}
	fmt.Printf("==> addprimary: uri=%s node=%s stake=%d end=%d\n", uri, nodeID, stake, end)

	if already, err := isPrimaryValidator(uri, nodeID); err != nil {
		return fmt.Errorf("idempotency check: %w", err)
	} else if already {
		fmt.Printf("==> %s already a primary-network validator — skip\n", nodeID)
		return nil
	}

	fmt.Println("==> waiting for node health")
	if err := waitHealthy(uri, 120*time.Second); err != nil {
		return err
	}

	key := genesis.EWOQKey
	kc := secp256k1fx.NewKeychain(key)
	wallet, err := primary.MakeWallet(ctx, uri, kc, kc, primary.WalletConfig{})
	if err != nil {
		return fmt.Errorf("make wallet: %w", err)
	}
	pWallet := wallet.P()
	avaxAssetID := pWallet.Builder().Context().AVAXAssetID
	owner := &secp256k1fx.OutputOwners{Threshold: 1, Addrs: []ids.ShortID{key.Address()}}

	fmt.Println("==> IssueAddPermissionlessValidatorTx")
	tx, err := pWallet.IssueAddPermissionlessValidatorTx(
		&txs.SubnetValidator{
			Validator: txs.Validator{
				NodeID: nodeID,
				Start:  uint64(time.Now().Unix()),
				End:    end,
				Wght:   stake,
			},
			Subnet: constants.PrimaryNetworkID,
		},
		pop,
		avaxAssetID,
		owner, // validation rewards owner
		owner, // delegation rewards owner
		shares,
	)
	if err != nil {
		return fmt.Errorf("add permissionless validator: %w", err)
	}
	fmt.Printf("==> ACCEPTED tx=%s\n   %s is now a primary-network validator (weight %d)\n", tx.ID(), nodeID, stake)
	return nil
}

// buildPoP builds a *signer.ProofOfPossession from the harvested publicKey + proofOfPossession hex
// (info.getNodeID nodePOP), then Verify()s it — no BLS secret key required.
func buildPoP(pkHex, popHex string) (*signer.ProofOfPossession, error) {
	pk, err := hex.DecodeString(strings.TrimPrefix(pkHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode publicKey: %w", err)
	}
	pp, err := hex.DecodeString(strings.TrimPrefix(popHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode proofOfPossession: %w", err)
	}
	if len(pk) != 48 || len(pp) != 96 {
		return nil, fmt.Errorf("bad lengths: publicKey=%d (want 48), pop=%d (want 96)", len(pk), len(pp))
	}
	p := &signer.ProofOfPossession{}
	copy(p.PublicKey[:], pk)
	copy(p.ProofOfPossession[:], pp)
	if err := p.Verify(); err != nil {
		return nil, fmt.Errorf("PoP does not verify against publicKey: %w", err)
	}
	return p, nil
}

func isPrimaryValidator(uri string, nodeID ids.NodeID) (bool, error) {
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
	if err := jsonRPC(uri+"/ext/bc/P", `{"jsonrpc":"2.0","id":1,"method":"platform.getCurrentValidators"}`, &resp); err != nil {
		return false, err
	}
	if resp.Error != nil {
		return false, fmt.Errorf("platform.getCurrentValidators: %s", resp.Error.Message)
	}
	for _, v := range resp.Result.Validators {
		if v.NodeID == nodeID.String() {
			return true, nil
		}
	}
	return false, nil
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
