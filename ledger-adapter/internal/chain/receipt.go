package chain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ReceiptFetcher — интерфейс для polling'а receipt'а. Позволяет подменить
// ethclient моком в тестах (stubReceiptFetcher).
type ReceiptFetcher interface {
	TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}

// receiptDelays — exponential backoff для polling'а receipt'а.
// Последний элемент используется повторно пока timeout не выйдет.
var receiptDelays = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
}

// WaitReceipt опрашивает fetcher пока не вернётся успешный receipt, или не
// выйдет timeout. ethereum.NotFound (tx ещё не замайнен) — продолжаем poll.
// Другая ошибка — возвращаем немедленно.
//
// Backoff: 50ms → 100ms → 200ms → 500ms → 1s → 2s → 2s → ...
func WaitReceipt(
	ctx context.Context,
	fetcher ReceiptFetcher,
	txHash common.Hash,
	timeout time.Duration,
) (*types.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	delayIdx := 0
	for {
		r, err := fetcher.TransactionReceipt(ctx, txHash)
		if err == nil {
			return r, nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			return nil, fmt.Errorf("fetch receipt %s: %w", txHash.Hex(), err)
		}

		d := receiptDelays[delayIdx]
		if delayIdx < len(receiptDelays)-1 {
			delayIdx++
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("receipt timeout for %s: %w", txHash.Hex(), ctx.Err())
		case <-time.After(d):
		}
	}
}
