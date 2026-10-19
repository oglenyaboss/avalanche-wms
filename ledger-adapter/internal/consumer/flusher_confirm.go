package consumer

import (
	"context"
	"errors"
	"fmt"

	"ledger-adapter/internal/chain"
)

// Flush — serial-совместимая обёртка: Send всех sub-batch'ей, затем Confirm
// каждого по очереди. Сохраняет старую семантику (timeout/stall → recordFailure,
// reconcile-loop потом подтянет, если tx всё же замайнится). Pipeline использует
// Send + Confirm раздельно (см. pipeline.go). При partial failure возвращает
// error, чтобы Kafka offsets не закоммитились; на retry filterAndMarkPending
// корректно скипнет уже COMMITTED / reconcile'ит уже SENT.
func (f *Flusher) Flush(ctx context.Context, msgs []*Message) error {
	inflight, sendErr := f.Send(ctx, msgs)
	for i := range inflight {
		tx := &inflight[i]
		err := f.Confirm(ctx, tx)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrConfirmStalled) {
			// Serial-путь фиксирует stall как failure (старое поведение flushSubBatch);
			// reconcile-loop (N1) подтянет к COMMITTED, если tx всё же замайнится.
			if rerr := f.recordFailure(ctx, tx.Msgs, tx.IDs, tx.TxHash.Hex(), err.Error()); rerr != nil {
				return rerr
			}
			continue
		}
		return err
	}
	return sendErr
}

// Confirm дожидается receipt'а одной in-flight tx и применяет терминальный
// результат: success → MarkCommitted (для подписанных действий — разбор исходов
// реестра: Applied → COMMITTED, отклонённые → MarkRejected с reject_reason);
// revert (status 0) → recordFailure (DLQ+MarkFailed); timeout/RPC-fail →
// ErrConfirmStalled (строки остаются SENT, без DLQ — pipeline запустит recovery,
// reconcile-loop подтянет).
func (f *Flusher) Confirm(ctx context.Context, tx *InflightTx) error {
	receipt, err := chain.WaitReceipt(ctx, f.caller, tx.TxHash, f.receiptTimeout)
	if err != nil {
		f.log.Warn("receipt not confirmed — stall", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex(), "err", err)
		return fmt.Errorf("%w: tx %s: %v", ErrConfirmStalled, tx.TxHash.Hex(), err)
	}
	if receipt.Status == 0 {
		f.log.Error("execution reverted", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex())
		return f.recordFailure(ctx, tx.Msgs, tx.IDs, tx.TxHash.Hex(), "execution reverted")
	}
	if tx.Aggregate == aggregateSignedAction {
		if err := applyActionOutcomes(ctx, f.registry, f.store, receipt, tx.IDs, tx.TxHash.Hex()); err != nil {
			return fmt.Errorf("apply action outcomes: %w", err)
		}
		f.log.Info("signed actions resolved from receipt", "tx", tx.TxHash.Hex(), "size", len(tx.IDs))
		return nil
	}
	if err := f.store.MarkCommitted(ctx, tx.IDs); err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	f.log.Info("sub-batch committed", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex(), "size", len(tx.IDs))
	return nil
}
