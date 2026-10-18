package chain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ErrSignerTransient — удалённый signer недоступен (сеть/5xx/timeout): tx не
// отправлен, nonce не потрачен — retry при следующем poll'е, НЕ DLQ.
// Локальные crypto-ошибки static-подписи этим не оборачиваются (они terminal).
var ErrSignerTransient = errors.New("signer transient")

// TxSigner — источник подписи для Client. Абстракция над «ключ в процессе»
// (static: ecdsa из env) и «ключ во внешнем KMS» (web3signer): Client не знает,
// где живёт приватник, — только адрес и способ подписать готовый tx.
type TxSigner interface {
	// Address — адрес, из-под которого signer подписывает tx.
	Address() common.Address
	// SignTx подписывает legacy/dynamic tx под данным chainID (EIP-155).
	SignTx(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
}

// staticSigner — локальный ecdsa-ключ: прежнее поведение Client до выноса
// подписи в KMS. types.SignTx сам считает EIP-155 v и нормализует low-s.
type staticSigner struct {
	pk   *ecdsa.PrivateKey
	addr common.Address
}

var _ TxSigner = (*staticSigner)(nil)

// NewStaticSigner парсит hex-приватник (0x опционален) в TxSigner.
func NewStaticSigner(privateKeyHex string) (TxSigner, error) {
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return &staticSigner{pk: pk, addr: crypto.PubkeyToAddress(*pk.Public().(*ecdsa.PublicKey))}, nil
}

func (s *staticSigner) Address() common.Address { return s.addr }

func (s *staticSigner) SignTx(_ context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	return types.SignTx(tx, types.NewEIP155Signer(chainID), s.pk)
}
