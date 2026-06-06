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

// receiptDelays — polling receipt'а. Tight constant cadence: на быстрых subnet-evm
// блоках (sub-second) большой backoff (...→2s) сам становился бутылочным горлышком —
// receipt 1s-блока детектился только на ~1.85s. Частый poll = round-trip ≈ block_time.
// Это тайминг polling'а, НЕ nonce/идемпотентность. Последний элемент повторяется.
var receiptDelays = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
}

// WaitReceipt опрашивает fetcher пока не вернётся успешный receipt, или не
// выйдет timeout. ethereum.NotFound (tx ещё не замайнен) — продолжаем poll.
// Другая ошибка — возвращаем немедленно.
//
// Каденс: 50ms (первый poll), далее 100ms на каждый следующий (последний элемент receiptDelays повторяется).
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
