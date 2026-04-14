package chain

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// stubReceiptFetcher имитирует ethclient: возвращает NotFound первые okAfter-1
// вызовов, потом receipt. При notFoundAlways=true всегда возвращает NotFound
// (для timeout-тестов).
type stubReceiptFetcher struct {
	calls          atomic.Int32
	okAfter        int32
	receipt        *types.Receipt
	notFoundAlways bool
	customErr      error
}

func (s *stubReceiptFetcher) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	n := s.calls.Add(1)
	if s.customErr != nil {
		return nil, s.customErr
	}
	if s.notFoundAlways {
		return nil, ethereum.NotFound
	}
	if n >= s.okAfter {
		return s.receipt, nil
	}
	return nil, ethereum.NotFound
}

func TestWaitReceipt_SucceedsFirstTry(t *testing.T) {
	f := &stubReceiptFetcher{okAfter: 1, receipt: &types.Receipt{Status: 1}}
	r, err := WaitReceipt(context.Background(), f, common.HexToHash("0xabc"), 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.Status != 1 {
		t.Errorf("status: got %d", r.Status)
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("calls: got %d, want 1", got)
	}
}

func TestWaitReceipt_SucceedsAfterRetries(t *testing.T) {
	f := &stubReceiptFetcher{okAfter: 3, receipt: &types.Receipt{Status: 1}}
	r, err := WaitReceipt(context.Background(), f, common.HexToHash("0xabc"), 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.Status != 1 {
		t.Errorf("status: got %d", r.Status)
	}
	if got := f.calls.Load(); got < 3 {
		t.Errorf("expected at least 3 calls, got %d", got)
	}
}

func TestWaitReceipt_Timeout(t *testing.T) {
	f := &stubReceiptFetcher{notFoundAlways: true}
	start := time.Now()
	_, err := WaitReceipt(context.Background(), f, common.HexToHash("0xabc"), 150*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded wrapped, got %v", err)
	}
	// Должны уложиться примерно в timeout (± backoff последнего sleep'а)
	if elapsed > 2500*time.Millisecond {
		t.Errorf("elapsed too long: %s", elapsed)
	}
}

func TestWaitReceipt_NonNotFoundErrorReturnsImmediately(t *testing.T) {
	customErr := errors.New("rpc connection refused")
	f := &stubReceiptFetcher{customErr: customErr}
	_, err := WaitReceipt(context.Background(), f, common.HexToHash("0xabc"), 5*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, customErr) {
		t.Errorf("expected wrapped %q, got %v", customErr, err)
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("should bail after first non-NotFound error, got %d calls", got)
	}
}

func TestWaitReceipt_RespectsCanceledParentContext(t *testing.T) {
	f := &stubReceiptFetcher{notFoundAlways: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // сразу отменим

	_, err := WaitReceipt(ctx, f, common.HexToHash("0xabc"), 5*time.Second)
	if err == nil {
		t.Fatal("expected error from canceled parent")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context error, got %v", err)
	}
}
