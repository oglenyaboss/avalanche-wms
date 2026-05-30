package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/store"
)

// receiptChecker — минимальный интерфейс для одиночной проверки receipt'а.
// Удовлетворяется и chain.Client, и ChainCaller (оба проксируют ethclient).
type receiptChecker interface {
	TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}

// terminalMarker — запись терминального результата reconcile'а в onchain_events.
type terminalMarker interface {
	MarkCommitted(ctx context.Context, ids []uuid.UUID) error
	MarkFailed(ctx context.Context, ids []uuid.UUID, txHash, reason string) error
}

// reconcileStore — то, что нужно фоновому reconcile-loop'у от onchain_events repo.
type reconcileStore interface {
	terminalMarker
	ListReconcilable(ctx context.Context, minAge time.Duration) ([]store.ReconcileRow, error)
}

// reconcileReceipt делает РОВНО ОДНУ проверку receipt'а уже отправленной tx (без
// polling'а, без resubmit'а) и применяет терминальный результат к ids:
//
//   - tx mined + success → MarkCommitted (terminal=true)
//   - tx mined + revert  → MarkFailed   (terminal=true)
//   - tx ещё не mined     → no-op, строка остаётся как есть (terminal=false)
//
// НИКОГДА не отправляет новую tx — tx_hash остаётся стабильным. Общий примитив для
// redelivery-пути flusher'а (S2) и фонового reconcile-loop'а (N1). Идемпотентен:
// повторный вызов на уже-COMMITTED tx просто снова выставит COMMITTED.
func reconcileReceipt(
	ctx context.Context,
	ch receiptChecker,
	st terminalMarker,
	ids []uuid.UUID,
	txHash string,
	log *slog.Logger,
) (terminal bool, err error) {
	receipt, err := ch.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			// Не замайнено — оставляем строку, дотянет следующий tick / redelivery.
			return false, nil
		}
		return false, fmt.Errorf("reconcile receipt %s: %w", txHash, err)
	}
	if receipt.Status == types.ReceiptStatusSuccessful {
		if err := st.MarkCommitted(ctx, ids); err != nil {
			return false, fmt.Errorf("reconcile mark committed: %w", err)
		}
		log.Info("reconciled to committed", "tx", txHash, "n", len(ids))
		return true, nil
	}
	if err := st.MarkFailed(ctx, ids, txHash, "reconcile: tx reverted on-chain"); err != nil {
		return false, fmt.Errorf("reconcile mark failed: %w", err)
	}
	log.Warn("reconciled to failed (tx reverted on-chain)", "tx", txHash, "n", len(ids))
	return true, nil
}

// Reconciler — фоновый цикл, закрывающий N1: событие, ошибочно помеченное FAILED по
// таймауту receipt-poll'а (или зависшее в SENT), сверяется с реальным on-chain
// результатом и подтягивается к нему. Только сверка-и-запись-вперёд: НИКОГДА не
// переотправляет tx (это вернуло бы S2-поверхность, которую мы чиним).
type Reconciler struct {
	chain    receiptChecker
	store    reconcileStore
	interval time.Duration
	minAge   time.Duration
	log      *slog.Logger
}

// NewReconciler собирает фоновый reconciler. interval — период sweep'а; minAge —
// насколько «старой» (по updated_at) должна быть строка, чтобы её трогать: отсекает
// in-flight события, которые flusher ещё дожимает своим WaitReceipt.
func NewReconciler(ch receiptChecker, st reconcileStore, interval, minAge time.Duration, log *slog.Logger) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{chain: ch, store: st, interval: interval, minAge: minAge, log: log}
}

// Run блокирует пока ctx не отменится, запуская sweep каждые interval.
func (r *Reconciler) Run(ctx context.Context) error {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	r.log.Info("reconcile loop started", "interval", r.interval, "min_age", r.minAge)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("reconcile loop stopped")
			return nil
		case <-t.C:
			if err := r.reconcileOnce(ctx); err != nil {
				// Транзитивная ошибка sweep'а (например БД недоступна) — логируем,
				// следующий tick попробует снова. Не валим процесс.
				r.log.Error("reconcile sweep failed", "err", err)
			}
		}
	}
}

// reconcileOnce — один проход: выбрать stuck rows, сгруппировать по tx_hash (один
// receipt-check на batch-tx) и сверить каждую.
func (r *Reconciler) reconcileOnce(ctx context.Context) error {
	rows, err := r.store.ListReconcilable(ctx, r.minAge)
	if err != nil {
		return fmt.Errorf("list reconcilable: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	byTx := make(map[string][]uuid.UUID, len(rows))
	for _, row := range rows {
		byTx[row.TxHash] = append(byTx[row.TxHash], row.EventID)
	}
	r.log.Info("reconcile sweep", "stuck_rows", len(rows), "distinct_tx", len(byTx))

	for txHash, ids := range byTx {
		// Один битый tx (например receipt RPC-блип) не должен останавливать sweep.
		if _, err := reconcileReceipt(ctx, r.chain, r.store, ids, txHash, r.log); err != nil {
			r.log.Error("reconcile tx failed", "tx", txHash, "err", err)
		}
	}
	return nil
}
