package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"ledger-adapter/internal/config"
)

// dialRegistry подключается к RPC и возвращает адрес реестра.
func dialRegistry() (*ethclient.Client, common.Address, error) {
	rpcURL, err := config.GetRequired("RPC_URL")
	if err != nil {
		return nil, common.Address{}, err
	}
	addrHex, err := config.GetRequired("REGISTRY_ADDR")
	if err != nil {
		return nil, common.Address{}, err
	}
	if !common.IsHexAddress(addrHex) {
		return nil, common.Address{}, fmt.Errorf("REGISTRY_ADDR: невалидный адрес %q", addrHex)
	}
	ec, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("dial RPC: %w", err)
	}
	return ec, common.HexToAddress(addrHex), nil
}

func ownerKey() (*ecdsa.PrivateKey, error) {
	v, err := config.GetRequired("OWNER_KEY")
	if err != nil {
		return nil, err
	}
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(v, "0x"))
	if err != nil {
		return nil, fmt.Errorf("OWNER_KEY: %w", err)
	}
	return pk, nil
}

func sendOwnerTx(ctx context.Context, ec *ethclient.Client, key *ecdsa.PrivateKey, to common.Address, data []byte) (common.Hash, error) {
	from := crypto.PubkeyToAddress(key.PublicKey)
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("chain id: %w", err)
	}
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		return common.Hash{}, fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := ec.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("gas price: %w", err)
	}
	gas, err := ec.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil {
		return common.Hash{}, fmt.Errorf("оценка газа (OWNER_KEY не владелец реестра?): %w", err)
	}
	signed, err := types.SignTx(types.NewTransaction(nonce, to, big.NewInt(0), gas, gasPrice, data), types.NewEIP155Signer(chainID), key)
	if err != nil {
		return common.Hash{}, fmt.Errorf("sign: %w", err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("send: %w", err)
	}
	if err := waitReceipt(ctx, ec, signed.Hash()); err != nil {
		return common.Hash{}, fmt.Errorf("wait receipt: %w", err)
	}
	return signed.Hash(), nil
}

func waitReceipt(ctx context.Context, ec *ethclient.Client, hash common.Hash) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		r, err := ec.TransactionReceipt(ctx, hash)
		if err == nil {
			if r.Status != types.ReceiptStatusSuccessful {
				return fmt.Errorf("tx %s reverted", hash.Hex())
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait receipt %s: %w", hash.Hex(), ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("tx %s: нет receipt за 120 с", hash.Hex())
}
