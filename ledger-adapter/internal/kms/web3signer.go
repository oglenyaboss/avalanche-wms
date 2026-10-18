// Package kms — TxSigner поверх внешнего KMS: ConsenSys Web3Signer (eth1 mode).
// Ключ адаптера живёт в Web3Signer (file-raw/file-keystore/hashicorp/aws-kms
// backend — на выбор деплоя), в процесс адаптера попадают только public key
// (identifier) и подпись. Протокол (verified против openapi-specs/eth1):
//
//	POST /api/v1/eth1/sign/{identifier}  {"data":"0x<32B digest>","applyHash":false}
//	  → 200 text/plain "0x<r||s||v 65B>"  (v у file-ключей = 27/28 — не доверяем,
//	    parity восстанавливаем recover'ом и сравнением с адресом)
//	GET  /api/v1/eth1/publicKeys → ["0x<128 hex uncompressed X||Y>", ...]
//
// EIP-155 sighash считается локально (applyHash=false подписывает готовый
// digest), итоговый v (35 + 2*chainID + parity) строит go-ethereum
// tx.WithSignature — как и в static-режиме.
package kms

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"ledger-adapter/internal/chain"
)

// secp256k1N / halfN — для low-s нормализации (EIP-2): geth-валидатор
// пост-Homestead отвергает s > N/2, а внешний signer низость s не гарантирует.
var (
	secp256k1N, _ = new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	halfN         = new(big.Int).Rsh(secp256k1N, 1)
)

// Signer реализует chain.TxSigner поверх Web3Signer.
type Signer struct {
	baseURL string
	ident   string // 0x + 128 hex: uncompressed X||Y без 04-префикса
	addr    common.Address
	hc      *http.Client
}

var _ chain.TxSigner = (*Signer)(nil)

// NewSigner строит signer по базовому URL Web3Signer и identifier ключа
// (public key из GET /api/v1/eth1/publicKeys). Адрес выводится из pubkey —
// ключевой материал в адаптер не попадает никогда.
func NewSigner(baseURL, identifier string, hc *http.Client) (*Signer, error) {
	pub, err := identifierToPubkey(identifier)
	if err != nil {
		return nil, err
	}
	ecdsaPub, err := crypto.UnmarshalPubkey(pub)
	if err != nil {
		return nil, fmt.Errorf("parse secp256k1 public key %q: %w", identifier, err)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Signer{
		baseURL: strings.TrimRight(baseURL, "/"),
		ident:   strings.ToLower(identifier),
		addr:    crypto.PubkeyToAddress(*ecdsaPub),
		hc:      hc,
	}, nil
}

func identifierToPubkey(identifier string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(identifier, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode public key %q: %w", identifier, err)
	}
	if len(raw) != 64 {
		return nil, fmt.Errorf("public key %q: want 64 bytes (uncompressed X||Y), got %d", identifier, len(raw))
	}
	return append([]byte{0x04}, raw...), nil
}

// Address — адрес KMS-ключа (выведен из public key при конструировании).
func (s *Signer) Address() common.Address { return s.addr }

// SignTx подписывает tx в Web3Signer: локальный EIP-155 sighash → remote sign →
// low-s нормализация → parity recover'ом → tx.WithSignature.
func (s *Signer) SignTx(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	eip155 := types.NewEIP155Signer(chainID)
	digest := eip155.Hash(tx)

	body, err := json.Marshal(signRequest{Data: "0x" + hex.EncodeToString(digest[:]), ApplyHash: false})
	if err != nil {
		return nil, fmt.Errorf("marshal sign request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/api/v1/eth1/sign/"+s.ident, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build sign request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		// Сеть/timeout/DNS — KMS недоступен транзитивно.
		return nil, fmt.Errorf("%w: kms request: %v", chain.ErrSignerTransient, err)
	}
	defer resp.Body.Close()

	sigHex, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: kms response: %v", chain.ErrSignerTransient, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("kms: key %q not loaded in web3signer (404)", s.ident)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: kms status %d: %s", chain.ErrSignerTransient, resp.StatusCode, bytes.TrimSpace(sigHex))
	}
	if resp.StatusCode != http.StatusOK {
		// 400 и прочие 4xx — протокольная/конфигурационная ошибка, ретраи бессмысленны.
		return nil, fmt.Errorf("kms: status %d: %s", resp.StatusCode, bytes.TrimSpace(sigHex))
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(string(bytes.TrimSpace(sigHex)), "0x"))
	if err != nil {
		return nil, fmt.Errorf("kms: decode signature %q: %w", string(sigHex), err)
	}
	if len(sig) != 65 {
		return nil, fmt.Errorf("kms: signature length %d, want 65", len(sig))
	}

	r := new(big.Int).SetBytes(sig[0:32])
	sc := new(big.Int).SetBytes(sig[32:64])
	// Low-s (EIP-2): s' = N - s, recovered pubkey не меняется, parity ищем заново.
	if sc.Cmp(halfN) > 0 {
		sc.Sub(secp256k1N, sc)
	}

	// Parity (recovery id): v из ответа не доверяем (27/28 у file-ключей, может
	// отличаться у aws-kms/azure backend'ов) — перебираем 0/1 и сверяем адрес.
	var parity byte
	found := false
	cand := make([]byte, 65)
	r.FillBytes(cand[0:32])
	sc.FillBytes(cand[32:64])
	for p := byte(0); p < 2; p++ {
		cand[64] = p
		pub, perr := crypto.Ecrecover(digest[:], cand)
		if perr != nil {
			continue
		}
		if bytes.Equal(crypto.Keccak256(pub[1:])[12:], s.addr[:]) {
			parity, found = p, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("kms: signature does not recover to signer address %s (corrupt KMS response?)", s.addr.Hex())
	}
	cand[64] = parity

	signed, err := tx.WithSignature(eip155, cand)
	if err != nil {
		return nil, fmt.Errorf("apply signature: %w", err)
	}
	return signed, nil
}

type signRequest struct {
	Data      string `json:"data"`
	ApplyHash bool   `json:"applyHash"`
}

// PublicKeys возвращает identifier'ы всех загруженных в Web3Signer ключей
// (GET /api/v1/eth1/publicKeys). Нужно kms-init/bootstrap-инструментам.
func PublicKeys(ctx context.Context, baseURL string, hc *http.Client) ([]string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/v1/eth1/publicKeys", nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public keys: status %d", resp.StatusCode)
	}
	var keys []string
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return nil, fmt.Errorf("public keys: decode: %w", err)
	}
	return keys, nil
}
