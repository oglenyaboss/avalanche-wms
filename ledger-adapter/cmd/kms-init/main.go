// kms-init — одноразовый bootstrap для kms-профиля compose: генерирует ключи
// адаптера как file-raw конфиги Web3Signer (идемпотентно — существующие не
// трогаем), заставляет Web3Signer их перезагрузить (/reload), выводит адреса
// из public key'ей и фондирует их с genesis-аккаунта ewoq, после чего публикует
// identifier'ы в shared-файл для KMS_PUBLIC_KEYS_FILE адаптера.
//
// ponytail: file-raw = dev-режим (ключ на диске KMS-машины). Prod-путь без
// изменения адаптера — config-файлы type: hashicorp (ключ в Vault KV v2) или
// type: aws-kms: тогда kms-init не генерит ключи, а только фондирует/публикует.
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"ledger-adapter/internal/kms"
)

// ewoqKey — публичный тест-ключ Avalanche (см. .gitleaks.toml allowlist):
// финансирует только throwaway dev/geo-цепи.
const ewoqKey = "0x56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027"

// httpKMS — общий клиент с таймаутом (DefaultClient без таймаута подвисает
// до 5-минутного ctx).
var httpKMS = &http.Client{Timeout: 10 * time.Second}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("kms-init failed", "err", err)
		os.Exit(1)
	}
	log.Info("kms-init done")
}

func run(log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	keysDir := envOr("KMS_KEYS_DIR", "/keys")
	kmsURL := os.Getenv("KMS_URL")
	if kmsURL == "" {
		return fmt.Errorf("KMS_URL is required")
	}
	rpcURL := envOrFile("RPC_URL")
	if rpcURL == "" {
		return fmt.Errorf("RPC_URL (or RPC_URL_FILE) is required")
	}
	count := 1
	if v := os.Getenv("KMS_SIGNER_COUNT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("bad KMS_SIGNER_COUNT %q", v)
		}
		count = n
	}
	fundKey := envOr("FUND_KEY", ewoqKey)
	fundAmount, ok := new(big.Int).SetString(envOr("FUND_AMOUNT", "1000000000000000000000"), 10)
	if !ok || fundAmount.Sign() <= 0 {
		return fmt.Errorf("bad FUND_AMOUNT")
	}
	sharedOut := os.Getenv("SHARED_OUT")

	created, err := ensureKeyConfigs(keysDir, count)
	if err != nil {
		return err
	}
	log.Info("key configs ready", "dir", keysDir, "count", count, "created", created)

	if err := waitUpcheck(ctx, kmsURL); err != nil {
		return err
	}
	// /reload всегда (не только при created>0): прошлый запуск мог упасть между
	// записью конфигов и reload'ом — иначе ключи так и не подхватятся.
	if err := reloadKeys(ctx, kmsURL); err != nil {
		return err
	}
	// reload может быть асинхронным — поллим до появления нужного числа ключей.
	ids, err := pollPublicKeys(ctx, kmsURL, count)
	if err != nil {
		return err
	}
	// KMS_SIGNER_COUNT — не пол, а точное число signer'ов: лишние ключи
	// (напр., после скейл-дауна) не публикуем. Сортировка = детерминированный
	// выбор при повторных запусках.
	sort.Strings(ids)
	if len(ids) > count {
		ids = ids[:count]
	}
	log.Info("kms keys loaded", "identifiers", ids)

	addrs := make([]common.Address, 0, len(ids))
	for _, id := range ids {
		a, err := addressOfIdentifier(id)
		if err != nil {
			return err
		}
		addrs = append(addrs, a)
	}

	if err := fundSigners(ctx, rpcURL, fundKey, addrs, fundAmount, log); err != nil {
		return err
	}

	if sharedOut != "" {
		if err := os.WriteFile(sharedOut, []byte(strings.Join(ids, ",")+"\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", sharedOut, err)
		}
		log.Info("identifiers published", "path", sharedOut)
	}
	return nil
}

// ensureKeyConfigs идемпотентно создаёт signer-<i>.yaml (file-raw, SECP256K1).
// Существующий файл не перезаписывается: адрес ключа стабилен между рестартами
// volume, фандинг одноразовый.
func ensureKeyConfigs(dir string, count int) (created int, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	for i := 0; i < count; i++ {
		path := filepath.Join(dir, fmt.Sprintf("signer-%d.yaml", i))
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return created, err
		}
		pk, err := crypto.GenerateKey()
		if err != nil {
			return created, fmt.Errorf("gen key %d: %w", i, err)
		}
		cfg := fmt.Sprintf("type: \"file-raw\"\nkeyType: \"SECP256K1\"\nprivateKey: \"0x%s\"\n",
			hex.EncodeToString(crypto.FromECDSA(pk)))
		// Атомарно (tmp+rename): обрыв посреди записи оставил бы битый yaml,
		// который re-run молча скипает как «существующий».
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(cfg), 0o600); err != nil {
			return created, fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return created, fmt.Errorf("rename %s: %w", path, err)
		}
		created++
	}
	return created, nil
}

func waitUpcheck(ctx context.Context, kmsURL string) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(kmsURL, "/")+"/upcheck", nil)
		if err == nil {
			resp, err := httpKMS.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("web3signer %s: /upcheck not ready in 120s", kmsURL)
}

func reloadKeys(ctx context.Context, kmsURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(kmsURL, "/")+"/reload", nil)
	if err != nil {
		return err
	}
	resp, err := httpKMS.Do(req)
	if err != nil {
		return fmt.Errorf("kms reload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kms reload: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// pollPublicKeys ждёт, пока Web3Signer отдаст не менее want ключей
// (после /reload загрузка не мгновенная).
func pollPublicKeys(ctx context.Context, kmsURL string, want int) ([]string, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ids, err := kms.PublicKeys(ctx, kmsURL, httpKMS)
		if err == nil && len(ids) >= want {
			return ids, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, fmt.Errorf("kms public keys: %w", err)
			}
			return nil, fmt.Errorf("web3signer loaded %d keys, want >= %d", len(ids), want)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// addressOfIdentifier выводит EVM-адрес из identifier Web3Signer
// (0x + 128 hex несжатого X||Y).
func addressOfIdentifier(identifier string) (common.Address, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(identifier, "0x"))
	if err != nil || len(raw) != 64 {
		return common.Address{}, fmt.Errorf("bad identifier %q", identifier)
	}
	pub, err := crypto.UnmarshalPubkey(append([]byte{0x04}, raw...))
	if err != nil {
		return common.Address{}, fmt.Errorf("identifier %q: %w", identifier, err)
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// fundSigners пополняет каждый signer-адрес до fundAmount с аккаунта fundKey.
// Идемпотентно: уже профинансированные (balance >= fundAmount/2) пропускаются.
func fundSigners(ctx context.Context, rpcURL, fundKeyHex string, addrs []common.Address, fundAmount *big.Int, log *slog.Logger) error {
	ec, err := ethclient.Dial(rpcURL)
	if err != nil {
		return fmt.Errorf("fund dial %s: %w", rpcURL, err)
	}
	defer ec.Close()

	pk, err := crypto.HexToECDSA(strings.TrimPrefix(fundKeyHex, "0x"))
	if err != nil {
		return fmt.Errorf("parse FUND_KEY: %w", err)
	}
	from := crypto.PubkeyToAddress(*pk.Public().(*ecdsa.PublicKey))

	chainID, err := ec.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("fund chain id: %w", err)
	}
	signer := types.NewEIP155Signer(chainID)

	threshold := new(big.Int).Div(fundAmount, big.NewInt(2))
	for i, addr := range addrs {
		bal, err := ec.BalanceAt(ctx, addr, nil)
		if err != nil {
			return fmt.Errorf("balance %s: %w", addr.Hex(), err)
		}
		if bal.Cmp(threshold) >= 0 {
			log.Info("signer already funded", "index", i, "addr", addr.Hex(), "balance_wei", bal.String())
			continue
		}
		nonce, err := ec.PendingNonceAt(ctx, from)
		if err != nil {
			return fmt.Errorf("funder nonce: %w", err)
		}
		gasPrice, err := ec.SuggestGasPrice(ctx)
		if err != nil {
			return fmt.Errorf("funder gas price: %w", err)
		}
		tx := types.NewTransaction(nonce, addr, fundAmount, 21000, gasPrice, nil)
		signed, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return fmt.Errorf("fund sign: %w", err)
		}
		if err := ec.SendTransaction(ctx, signed); err != nil {
			return fmt.Errorf("fund send to %s: %w", addr.Hex(), err)
		}
		if err := waitReceipt(ctx, ec, signed.Hash()); err != nil {
			return fmt.Errorf("fund %s receipt: %w", addr.Hex(), err)
		}
		log.Info("signer funded", "index", i, "addr", addr.Hex(), "amount_wei", fundAmount.String(), "tx", signed.Hash().Hex())
	}
	return nil
}

func waitReceipt(ctx context.Context, ec *ethclient.Client, hash common.Hash) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		r, err := ec.TransactionReceipt(ctx, hash)
		if err == nil {
			if r.Status != 1 {
				return fmt.Errorf("tx %s reverted", hash.Hex())
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("timeout")
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envOrFile(name string) string {
	if path := os.Getenv(name + "_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(name)
}
