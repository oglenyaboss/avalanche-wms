package chain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// ErrChainRevert — terminal ошибка chain call'а (state transition rejected
// контрактом, EstimateGas не смог запустить tx). DLQ + MarkFailed + commit
// offset — не пытаемся ретраить бесконечно.
var ErrChainRevert = errors.New("chain revert")

// ErrChainTransient — транзитивная ошибка (RPC недоступен, сетевой блип).
// Не коммитим offset, повторим при следующем poll'е.
var ErrChainTransient = errors.New("chain transient")

// isRevertError определяет terminal revert vs transient network error.
// Revert rpc-ошибки реализуют rpc.Error и обычно несут execution-reverted
// текст. Для паранойи добавлен substring-fallback на случай несертифицированных
// backend'ов (Avalanche subnet, Anvil с кастомным сообщением).
func isRevertError(err error) bool {
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "execution reverted") || strings.Contains(msg, "revert")
}

// Client обёртка над ethclient с методами для BatchMappingWMS.
// sendMu сериализует nonce-critical секцию в sendMethod.
type Client struct {
	eth          *ethclient.Client
	privateKey   *ecdsa.PrivateKey
	fromAddress  common.Address
	contractAddr common.Address
	parsedABI    abi.ABI
	sendMu       sync.Mutex
}

// NewClient подключается к RPC, парсит приватник и ABI.
func NewClient(rpcURL, privateKeyHex, contractAddr string) (*Client, error) {
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc %q: %w", rpcURL, err)
	}

	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	pubECDSA, ok := pk.Public().(*ecdsa.PublicKey)
	if !ok {
		eth.Close()
		return nil, fmt.Errorf("cast public key to ECDSA failed")
	}
	from := crypto.PubkeyToAddress(*pubECDSA)

	parsed, err := abi.JSON(strings.NewReader(ContractABI))
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("parse abi: %w", err)
	}

	return &Client{
		eth:          eth,
		privateKey:   pk,
		fromAddress:  from,
		contractAddr: common.HexToAddress(contractAddr),
		parsedABI:    parsed,
	}, nil
}

// Close закрывает RPC-соединение.
func (c *Client) Close() { c.eth.Close() }

// FromAddress — адрес, из-под которого клиент подписывает tx.
func (c *Client) FromAddress() common.Address { return c.fromAddress }

// aggregateToMethod маппит WMS aggregate_type в solidity-методы BatchMappingWMS.
var aggregateToMethod = map[string]string{
	"receiving": "batchAccept",
	"putaway":   "batchPutAway",
	"picking":   "batchPick",
	"shipping":  "batchShip",
}

// AggregateTypes returns the set of aggregate_type strings known to the chain layer.
func AggregateTypes() []string {
	out := make([]string, 0, len(aggregateToMethod))
	for k := range aggregateToMethod {
		out = append(out, k)
	}
	return out
}

// BatchCall отправляет batch-tx по aggregate_type. Диспатчер для flusher.
func (c *Client) BatchCall(ctx context.Context, aggregateType string, eventIDs, itemIDs []*big.Int) (common.Hash, error) {
	method, ok := aggregateToMethod[aggregateType]
	if !ok {
		return common.Hash{}, fmt.Errorf("unknown aggregate_type: %s", aggregateType)
	}
	return c.sendMethod(ctx, method, eventIDs, itemIDs)
}

// BatchAccept / BatchPutAway / BatchPick / BatchShip — explicit wrappers.
// Удобнее для ручного использования (тесты, CLI, ad-hoc) чем BatchCall по строке.

func (c *Client) BatchAccept(ctx context.Context, eventIDs, itemIDs []*big.Int) (common.Hash, error) {
	return c.sendMethod(ctx, "batchAccept", eventIDs, itemIDs)
}

func (c *Client) BatchPutAway(ctx context.Context, eventIDs, itemIDs []*big.Int) (common.Hash, error) {
	return c.sendMethod(ctx, "batchPutAway", eventIDs, itemIDs)
}

func (c *Client) BatchPick(ctx context.Context, eventIDs, itemIDs []*big.Int) (common.Hash, error) {
	return c.sendMethod(ctx, "batchPick", eventIDs, itemIDs)
}

func (c *Client) BatchShip(ctx context.Context, eventIDs, itemIDs []*big.Int) (common.Hash, error) {
	return c.sendMethod(ctx, "batchShip", eventIDs, itemIDs)
}

// TransactionReceipt проксирует ethclient — нужно чтобы Client удовлетворял
// ReceiptFetcher интерфейсу из receipt.go.
func (c *Client) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	return c.eth.TransactionReceipt(ctx, h)
}

// BlockNumber returns the current chain head, used to compute tx confirmations.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.eth.BlockNumber(ctx)
}

func (c *Client) sendMethod(ctx context.Context, method string, args ...any) (common.Hash, error) {
	data, err := c.parsedABI.Pack(method, args...)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack %s: %w", method, err)
	}

	// Сериализуем nonce-fetch + sign + send: один fromAddress.
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	// Все RPC sub-calls (PendingNonceAt, SuggestGasPrice, ChainID,
	// SendTransaction) — чистая сеть/RPC, не contract-level revert. Оборачиваем
	// через ErrChainTransient, чтобы Flusher classified их как retry-able и не
	// слал в DLQ при транзитивном блипе. См. MR !35 M7.
	nonce, err := c.eth.PendingNonceAt(ctx, c.fromAddress)
	if err != nil {
		return common.Hash{}, fmt.Errorf("%w: nonce: %v", ErrChainTransient, err)
	}

	gasPrice, err := c.eth.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("%w: gas price: %v", ErrChainTransient, err)
	}

	gasLimit, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
		From: c.fromAddress,
		To:   &c.contractAddr,
		Data: data,
	})
	if err != nil {
		// Revert (контракт rejected state transition) — terminal, DLQ.
		// Network/RPC blip — transient, retry при следующем tick.
		if isRevertError(err) {
			return common.Hash{}, fmt.Errorf("%w: estimate gas for %s: %v", ErrChainRevert, method, err)
		}
		return common.Hash{}, fmt.Errorf("%w: estimate gas for %s: %v", ErrChainTransient, method, err)
	}

	chainID, err := c.eth.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("%w: chain id: %v", ErrChainTransient, err)
	}

	tx := types.NewTransaction(nonce, c.contractAddr, big.NewInt(0), gasLimit, gasPrice, data)
	signed, err := types.SignTx(tx, types.NewEIP155Signer(chainID), c.privateKey)
	if err != nil {
		// Local crypto error — не chain-related. Оставляем raw, Flusher пометит
		// как terminal (DLQ) — это адекватно: sign не падает transient'но.
		return common.Hash{}, fmt.Errorf("sign tx: %w", err)
	}

	if err := c.eth.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("%w: send tx: %v", ErrChainTransient, err)
	}
	return signed.Hash(), nil
}
