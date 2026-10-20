package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// fakeEthBackend подменяет *ethclient.Client в unit-тестах SendCalldata/ReseedNonce.
// Считает вызовы PendingNonceAt и записывает все отправленные tx, чтобы тесты
// могли проверить nonce/gasPrice подписанной транзакции без живого RPC.
type fakeEthBackend struct {
	mu            sync.Mutex
	nonce         uint64
	pendingNonceN int
	gasPrice      *big.Int
	estimateGas   uint64
	chainID       *big.Int
	sentTxs       []*types.Transaction
	sendErr       error // если != nil — SendTransaction падает (и НЕ записывает tx)
	sendErrOnce   bool  // если true — sendErr срабатывает только на первом вызове
	sendCalls     int
}

func (f *fakeEthBackend) PendingNonceAt(_ context.Context, _ common.Address) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingNonceN++
	return f.nonce, nil
}

func (f *fakeEthBackend) SuggestGasPrice(_ context.Context) (*big.Int, error) {
	if f.gasPrice == nil {
		return big.NewInt(1), nil
	}
	return new(big.Int).Set(f.gasPrice), nil
}

func (f *fakeEthBackend) EstimateGas(_ context.Context, _ ethereum.CallMsg) (uint64, error) {
	if f.estimateGas == 0 {
		return 21000, nil
	}
	return f.estimateGas, nil
}

func (f *fakeEthBackend) ChainID(_ context.Context) (*big.Int, error) {
	if f.chainID == nil {
		return big.NewInt(1337), nil
	}
	return new(big.Int).Set(f.chainID), nil
}

func (f *fakeEthBackend) SendTransaction(_ context.Context, tx *types.Transaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls++
	if f.sendErr != nil && (!f.sendErrOnce || f.sendCalls == 1) {
		return f.sendErr
	}
	f.sentTxs = append(f.sentTxs, tx)
	return nil
}

func (f *fakeEthBackend) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	return nil, ethereum.NotFound
}

func (f *fakeEthBackend) BlockNumber(_ context.Context) (uint64, error) { return 0, nil }

func (f *fakeEthBackend) Close() {}

// newTestClient собирает Client с поддельным backend'ом и свежим ключом.
func newTestClient(t *testing.T, eth ethBackend) *Client {
	t.Helper()
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return &Client{
		eth:    eth,
		signer: &staticSigner{pk: pk, addr: crypto.PubkeyToAddress(pk.PublicKey)},
	}
}

// sendOneGas — единица send-пути с явной нижней границей gas-лимита (minGas);
// calldata фиктивные (паковка больше не часть Client'а).
func sendOneGas(ctx context.Context, c *Client, minGas uint64) (common.Hash, error) {
	return c.SendCalldata(ctx, common.HexToAddress("0x0000000000000000000000000000000000000001"), []byte{0x01}, minGas)
}

// sendOne — sendOneGas с minGas = 0 (без нижней границы).
func sendOne(ctx context.Context, c *Client) (common.Hash, error) {
	return sendOneGas(ctx, c, 0)
}

func TestClient_NonceMonotonicAcrossSends(t *testing.T) {
	eth := &fakeEthBackend{nonce: 5}
	c := newTestClient(t, eth)
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send 2: %v", err)
	}

	if len(eth.sentTxs) != 2 {
		t.Fatalf("expected 2 sent txs, got %d", len(eth.sentTxs))
	}
	if eth.sentTxs[0].Nonce() != 5 {
		t.Errorf("first tx nonce: want 5, got %d", eth.sentTxs[0].Nonce())
	}
	if eth.sentTxs[1].Nonce() != 6 {
		t.Errorf("second tx nonce: want 6, got %d", eth.sentTxs[1].Nonce())
	}
	if eth.pendingNonceN != 1 {
		t.Errorf("PendingNonceAt should be called once (seed only), got %d", eth.pendingNonceN)
	}
}

func TestClient_NonceUnchangedOnSendFailure(t *testing.T) {
	eth := &fakeEthBackend{nonce: 10, sendErr: errors.New("dial tcp: connection refused"), sendErrOnce: true}
	c := newTestClient(t, eth)
	// Первый send падает (transient) → nonce НЕ должен продвинуться.
	if _, err := sendOne(context.Background(), c); err == nil {
		t.Fatal("expected send to fail")
	}
	// Второй send (backend больше не падает) должен переиспользовать nonce=10.
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	if len(eth.sentTxs) != 1 {
		t.Fatalf("expected 1 successful tx, got %d", len(eth.sentTxs))
	}
	if eth.sentTxs[0].Nonce() != 10 {
		t.Errorf("nonce must be reused after failure: want 10, got %d", eth.sentTxs[0].Nonce())
	}
}

func TestClient_GasPriceHeadroom(t *testing.T) {
	eth := &fakeEthBackend{nonce: 0, gasPrice: big.NewInt(100)}
	c := newTestClient(t, eth)
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(eth.sentTxs) != 1 {
		t.Fatalf("expected 1 sent tx, got %d", len(eth.sentTxs))
	}
	want := int64(100) * gasPriceHeadroomNum / gasPriceHeadroomDen
	if got := eth.sentTxs[0].GasPrice().Int64(); got != want {
		t.Errorf("gas price headroom: want %d (100×%d/%d), got %d", want, gasPriceHeadroomNum, gasPriceHeadroomDen, got)
	}
}

// TestClient_GasLimitHeadroom: gasLimit подписанной tx = estimateGas × headroom.
// Под пайплайном estimateGas может прийтись на дешёвый skip-путь контракта
// (prior-stage transition того же товара ещё in-flight, не в блоке), а реальное
// исполнение — на более дорогой путь (item уже Accepted/PutAway → реальный
// SSTORE). Запас на gas-лимит предотвращает OutOfGas-реверты под смешанной
// нагрузкой full-FSM. Серийный flusher этого не ловил (prior tx домайнивался
// до estimate).
func TestClient_GasLimitHeadroom(t *testing.T) {
	eth := &fakeEthBackend{nonce: 0, estimateGas: 1000}
	c := newTestClient(t, eth)
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(eth.sentTxs) != 1 {
		t.Fatalf("expected 1 sent tx, got %d", len(eth.sentTxs))
	}
	want := uint64(1000) * gasLimitHeadroomNum / gasLimitHeadroomDen
	if got := eth.sentTxs[0].Gas(); got != want {
		t.Errorf("gas limit headroom: want %d (1000×%d/%d), got %d", want, gasLimitHeadroomNum, gasLimitHeadroomDen, got)
	}
}

func TestClient_ReseedNonce(t *testing.T) {
	eth := &fakeEthBackend{nonce: 5}
	c := newTestClient(t, eth)
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	// Симулируем продвижение on-chain nonce'а (другой источник / mempool drop).
	eth.nonce = 20
	if err := c.ReseedNonce(context.Background()); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if _, err := sendOne(context.Background(), c); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	if eth.sentTxs[1].Nonce() != 20 {
		t.Errorf("after reseed nonce: want 20, got %d", eth.sentTxs[1].Nonce())
	}
}

// fakeRPCError реализует rpc.Error — тип который go-ethereum возвращает для
// revert ошибок (-32000 execution reverted).
type fakeRPCError struct {
	msg  string
	code int
}

func (e *fakeRPCError) Error() string  { return e.msg }
func (e *fakeRPCError) ErrorCode() int { return e.code }

func TestIsRevertError_RPCErrorInterface(t *testing.T) {
	// Любой err реализующий rpc.Error считаем revert (terminal).
	err := &fakeRPCError{msg: "execution reverted: Duplicate eventId", code: -32000}
	if !isRevertError(err) {
		t.Error("rpc.Error interface should be classified as revert")
	}
}

func TestIsRevertError_WrappedRPCError(t *testing.T) {
	// errors.As должен пройти через fmt.Errorf wrap.
	inner := &fakeRPCError{msg: "execution reverted", code: -32000}
	wrapped := errors.New("outer: " + inner.Error())
	// Не сработает через errors.As (ничего не оборачивали), fallback substring
	if !isRevertError(wrapped) {
		t.Error("substring fallback should catch 'execution reverted'")
	}
}

func TestIsRevertError_TransientNetworkError(t *testing.T) {
	// Простая не-rpc ошибка без "revert" текста — transient.
	err := errors.New("connection refused")
	if isRevertError(err) {
		t.Error("'connection refused' should NOT be classified as revert")
	}
}

func TestIsRevertError_SubstringRevert(t *testing.T) {
	// Фолбэк на substring для нестандартных backend'ов.
	err := errors.New("transaction would revert: bad input")
	if !isRevertError(err) {
		t.Error("'revert' substring should be caught")
	}
}

func TestIsRevertError_UnrelatedError(t *testing.T) {
	err := errors.New("context deadline exceeded")
	if isRevertError(err) {
		t.Error("'context deadline exceeded' should be transient")
	}
}

func TestErrChainSentinels_DistinctFromEachOther(t *testing.T) {
	// Убедимся что ErrChainRevert и ErrChainTransient — разные errors.Is буквы.
	if errors.Is(ErrChainRevert, ErrChainTransient) {
		t.Error("ErrChainRevert should not satisfy Is(ErrChainTransient)")
	}
	if errors.Is(ErrChainTransient, ErrChainRevert) {
		t.Error("ErrChainTransient should not satisfy Is(ErrChainRevert)")
	}
}

func TestClient_SendMu_SerializesConcurrentCalls(t *testing.T) {
	// Смоук-тест для C1 фикса (MR !35): мьютекс sendMu в *Client должен
	// реально блокировать параллельный доступ. Прямо тестировать send-путь
	// без мокания concrete *ethclient.Client нельзя, поэтому проверяем сам
	// мьютекс через прямое взаимодействие с полем (in-package test).
	// Race-detector в остальных тестах ловит реальные гонки; этот тест —
	// гарантия что поле осталось в struct и что Lock()/Unlock() работают.
	var c Client
	locked := make(chan struct{})
	released := make(chan struct{})

	go func() {
		c.sendMu.Lock()
		close(locked)
		time.Sleep(50 * time.Millisecond)
		c.sendMu.Unlock()
		close(released)
	}()

	<-locked
	// Try to acquire from main goroutine — should block until released.
	start := time.Now()
	c.sendMu.Lock()
	elapsed := time.Since(start)
	c.sendMu.Unlock()
	<-released

	if elapsed < 40*time.Millisecond {
		t.Errorf("sendMu should have blocked ~50ms; blocked for %s", elapsed)
	}
}

// failingSigner имитирует удалённый KMS-signer с заданной ошибкой подписи.
type failingSigner struct{ err error }

func (f failingSigner) Address() common.Address { return common.Address{1} }

func (f failingSigner) SignTx(context.Context, *types.Transaction, *big.Int) (*types.Transaction, error) {
	return nil, f.err
}

func TestSendMethod_SignerTransientIsChainTransient(t *testing.T) {
	// KMS недоступен → ErrSignerTransient должен выйти как ErrChainTransient
	// (flusher ретраит, DLQ не уходит) и НЕ сжечь nonce (invariant 3).
	eth := &fakeEthBackend{nonce: 5}
	c := &Client{
		eth:    eth,
		signer: failingSigner{fmt.Errorf("%w: kms down", ErrSignerTransient)},
	}
	if _, err := sendOne(context.Background(), c); err == nil {
		t.Fatal("expected error from failing signer")
	} else if !errors.Is(err, ErrChainTransient) {
		t.Fatalf("signer transient must classify as ErrChainTransient, got %v", err)
	}
	if eth.pendingNonceN != 1 {
		t.Fatalf("PendingNonceAt called %d times, want 1 (seed only)", eth.pendingNonceN)
	}

	// Локальная (terminal) ошибка подписи не должна становиться transient.
	c2 := &Client{
		eth:    &fakeEthBackend{nonce: 5},
		signer: failingSigner{errors.New("local crypto broke")},
	}
	if _, err := sendOne(context.Background(), c2); errors.Is(err, ErrChainTransient) {
		t.Fatalf("local sign error must stay terminal, got %v", err)
	}
}

// TestClient_GasFloorRaisesLimit: minGas выше estimate×3/2 поднимает gas-лимит
// до нижней границы — защита от OOG, когда estimate видел дешёвый reject-путь,
// а к майнингу действие применяется с записью на каждый товар.
func TestClient_GasFloorRaisesLimit(t *testing.T) {
	eth := &fakeEthBackend{nonce: 0, estimateGas: 100_000}
	c := newTestClient(t, eth)
	if _, err := sendOneGas(context.Background(), c, 2_000_000); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(eth.sentTxs) != 1 {
		t.Fatalf("expected 1 sent tx, got %d", len(eth.sentTxs))
	}
	if got := eth.sentTxs[0].Gas(); got != 2_000_000 {
		t.Errorf("gas limit: want 2_000_000 (minGas), got %d", got)
	}
}

// TestClient_GasFloorBelowHeadroom: minGas ниже estimate×3/2 ничего не меняет —
// действует обычный headroom, итог = max(estimate×3/2, minGas).
func TestClient_GasFloorBelowHeadroom(t *testing.T) {
	eth := &fakeEthBackend{nonce: 0, estimateGas: 100_000}
	c := newTestClient(t, eth)
	if _, err := sendOneGas(context.Background(), c, 120_000); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(eth.sentTxs) != 1 {
		t.Fatalf("expected 1 sent tx, got %d", len(eth.sentTxs))
	}
	want := uint64(100_000) * gasLimitHeadroomNum / gasLimitHeadroomDen // 150_000
	if got := eth.sentTxs[0].Gas(); got != want {
		t.Errorf("gas limit: want %d (headroom), got %d", want, got)
	}
}
