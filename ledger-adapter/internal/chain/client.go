package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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

// gasPriceHeadroomNum/Den масштабируют SuggestGasPrice вверх. Под нагрузкой при
// пайплайне base fee растёт, и in-flight legacy-tx по старой цене underpriced'ится
// и зависает (триггер invariant 4 — sent-tx stall). ×3 даёт запас. Type-2
// (dynamic fee) tx с щедрым GasFeeCap — более надёжная долгосрочная форма;
// legacy×headroom — минимальный фикс.
const (
	gasPriceHeadroomNum = 3
	gasPriceHeadroomDen = 1
)

// gasLimitHeadroomNum/Den масштабируют estimateGas вверх. Под пайплайном tx
// оценивается, пока предыдущее действие с теми же товарами ещё in-flight:
// estimateGas видит товар в раннем статусе и идёт по пути отклонения
// (ActionRejected), а к майнингу товар уже в нужном статусе и действие
// применяется с записью на каждый товар — дороже. ×3/2 — запас на этот разрыв.
const (
	gasLimitHeadroomNum = 3
	gasLimitHeadroomDen = 2
)

// ethBackend — подмножество *ethclient.Client, которое использует Client.
// Вынесено в интерфейс, чтобы в unit-тестах инжектить fake (реальный
// *ethclient.Client удовлетворяет интерфейсу).
type ethBackend interface {
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error)
	ChainID(ctx context.Context) (*big.Int, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	BlockNumber(ctx context.Context) (uint64, error)
	Close()
}

// Client обёртка над ethclient с методами для SignedMappingWMS.
// sendMu сериализует nonce-critical секцию в send: локальный монотонный
// nonce (nonce++ только при успешном SendTransaction) позволяет держать
// несколько tx in-flight в одном блоке без per-send PendingNonceAt — иначе
// pipeline сериализовался бы на скорости майнинга блоков (старое поведение).
type Client struct {
	eth         ethBackend
	signer      TxSigner
	sendMu      sync.Mutex
	nonce       uint64 // следующий nonce для отправки; guarded by sendMu
	nonceSeeded bool   // seed произошёл; guarded by sendMu
}

// NewClient подключается к RPC и подписывает локальным hex-ключом (static-режим).
// КМС-режим использует NewClientWithSigner.
func NewClient(rpcURL, privateKeyHex string) (*Client, error) {
	signer, err := NewStaticSigner(privateKeyHex)
	if err != nil {
		return nil, err
	}
	return NewClientWithSigner(rpcURL, signer)
}

// NewClientWithSigner подключается к RPC и подписывает через внешний TxSigner
// (static-ключ или удалённый KMS — Client'у без разницы).
func NewClientWithSigner(rpcURL string, signer TxSigner) (*Client, error) {
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc %q: %w", rpcURL, err)
	}
	return &Client{
		eth:    eth,
		signer: signer,
	}, nil
}

// Close закрывает RPC-соединение.
func (c *Client) Close() { c.eth.Close() }

// FromAddress — адрес, из-под которого клиент подписывает tx.
func (c *Client) FromAddress() common.Address { return c.signer.Address() }

// TransactionReceipt проксирует ethclient — нужно чтобы Client удовлетворял
// ReceiptFetcher интерфейсу из receipt.go.
func (c *Client) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	return c.eth.TransactionReceipt(ctx, h)
}

// BlockNumber returns the current chain head, used to compute tx confirmations.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.eth.BlockNumber(ctx)
}

// SendCalldata отправляет произвольные готовые calldata на указанный адрес:
// applyActions на SignedMappingWMS и applyKeyActions на ConsortiumRegistry
// пакуются заранее (PackApplyActions / PackApplyKeyActions). Идёт по общему
// nonce/gas/sign/send-пути send: одна nonce-последовательность подписанта и те
// же классы ошибок (ErrChainTransient/ErrChainRevert).
func (c *Client) SendCalldata(ctx context.Context, to common.Address, data []byte) (common.Hash, error) {
	return c.send(ctx, to, data, "calldata")
}

// send строит, подписывает и отправляет legacy-tx с готовыми calldata на to.
// label попадает в текст ошибок estimate-gas для диагностики.
func (c *Client) send(ctx context.Context, to common.Address, data []byte, label string) (common.Hash, error) {
	// Сериализуем nonce-allocation + sign + send: один fromAddress.
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	// Локальный монотонный nonce: seed один раз из PendingNonceAt, дальше ++.
	// Все RPC sub-calls (PendingNonceAt, SuggestGasPrice, ChainID,
	// SendTransaction) — чистая сеть/RPC, не contract-level revert. Оборачиваем
	// через ErrChainTransient, чтобы Flusher classified их как retry-able и не
	// слал в DLQ при транзитивном блипе. См. MR !35 M7.
	if !c.nonceSeeded {
		n, nerr := c.eth.PendingNonceAt(ctx, c.signer.Address())
		if nerr != nil {
			return common.Hash{}, fmt.Errorf("%w: nonce: %v", ErrChainTransient, nerr)
		}
		c.nonce = n
		c.nonceSeeded = true
	}
	nonce := c.nonce

	gasPrice, err := c.eth.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("%w: gas price: %v", ErrChainTransient, err)
	}
	// Headroom против underpricing in-flight tx при росте base fee под нагрузкой.
	gasPrice = new(big.Int).Div(new(big.Int).Mul(gasPrice, big.NewInt(gasPriceHeadroomNum)), big.NewInt(gasPriceHeadroomDen))

	gasLimit, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
		From: c.signer.Address(),
		To:   &to,
		Data: data,
	})
	if err != nil {
		// Revert (контракт rejected state transition) — terminal, DLQ.
		// Network/RPC blip — transient, retry при следующем tick.
		if isRevertError(err) {
			return common.Hash{}, fmt.Errorf("%w: estimate gas for %s: %v", ErrChainRevert, label, err)
		}
		return common.Hash{}, fmt.Errorf("%w: estimate gas for %s: %v", ErrChainTransient, label, err)
	}
	// Запас на gas-лимит против разрыва отклонение→применение под пайплайном (см.
	// gasLimitHeadroomNum/Den) — иначе stale-оценка → OutOfGas-реверт.
	gasLimit = gasLimit * gasLimitHeadroomNum / gasLimitHeadroomDen

	chainID, err := c.eth.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("%w: chain id: %v", ErrChainTransient, err)
	}

	tx := types.NewTransaction(nonce, to, big.NewInt(0), gasLimit, gasPrice, data)
	signed, err := c.signer.SignTx(ctx, tx, chainID)
	if err != nil {
		// Удалённый signer недоступен — tx не построен, nonce не потрачен:
		// transient, retry при следующем poll'е (не DLQ). Локальная crypto-ошибка
		// static-ключа — terminal, как и раньше.
		if errors.Is(err, ErrSignerTransient) {
			return common.Hash{}, fmt.Errorf("%w: sign tx: %v", ErrChainTransient, err)
		}
		return common.Hash{}, fmt.Errorf("sign tx: %w", err)
	}

	if err := c.eth.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("%w: send tx: %v", ErrChainTransient, err)
	}
	// nonce++ ТОЛЬКО после успешного SendTransaction — никаких дыр в nonce
	// (invariant 3). При любой ошибке выше nonce не трогался → retry
	// переиспользует то же значение.
	c.nonce++
	return signed.Hash(), nil
}

// ReseedNonce пере-инициализирует локальный nonce из on-chain PendingNonceAt.
// Нужно pipeline-recovery'ю (invariant 4): после stall'а in-flight tx
// перечитываем актуальный nonce, чтобы не продолжать слать с устаревшим
// значением и не плодить дыры/коллизии.
func (c *Client) ReseedNonce(ctx context.Context) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	n, err := c.eth.PendingNonceAt(ctx, c.signer.Address())
	if err != nil {
		return fmt.Errorf("reseed nonce: %w", err)
	}
	c.nonce = n
	c.nonceSeeded = true
	return nil
}
