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

	"ledger-adapter/internal/chain"
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
	MarkRejected(ctx context.Context, ids []uuid.UUID, txHash string, reason uint8) error
}

// reconcileStore — то, что нужно фоновому reconcile-loop'у от onchain_events repo.
type reconcileStore interface {
	terminalMarker
	ListReconcilable(ctx context.Context, minAge time.Duration) ([]store.ReconcileRow, error)
}

// reconcileReceipt делает РОВНО ОДНУ проверку receipt'а уже отправленной tx (без
// polling'а, без resubmit'а) и применяет терминальный результат к ids:
//
//   - tx mined + success → разбор исходов подписанных действий через резолверы
//     (Applied → COMMITTED, отклонённые → MarkRejected с reject_reason);
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
	resolvers []ActionResolver,
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
		// Ошибка резолверов возвращается: строки остаются SENT, reconcile
		// повторит их на следующем tick'е (не терминальный исход).
		if err := applyActionOutcomes(ctx, resolvers, st, receipt, ids, txHash); err != nil {
			return false, fmt.Errorf("reconcile action outcomes: %w", err)
		}
		log.Info("reconciled signed actions from contract", "tx", txHash, "n", len(ids))
		return true, nil
	}
	// NOTE: reconcile-sourced FAILED НЕ публикуется в DLQ (в отличие от синхронного
	// recordFailure). После contract-fix'а reverted mined tx — редкость (валидные
	// transition'ы больше не ревертят); полноценная видимость отказа — это
	// reverse-outbox (#41) / chain-status gate (#45). TODO(#41): DLQ для reconcile-FAILED.
	if err := st.MarkFailed(ctx, ids, txHash, "reconcile: tx reverted on-chain"); err != nil {
		return false, fmt.Errorf("reconcile mark failed: %w", err)
	}
	log.Warn("reconciled to failed (tx reverted on-chain)", "tx", txHash, "n", len(ids))
	return true, nil
}

// resolveActionOutcomes собирает исходы подписанных действий из receipt'а tx:
// сначала логи всех контрактов-адресатов (reconcile знает только aggregate_type
// 'signed_action', не то, в какой контракт ушла tx), затем для id без лога —
// actionState каждого резолвера по порядку, первый найденный исход выигрывает
// (ADR §6: повторная отправка застала действие уже занятым — лога в этом
// receipt'е нет). id, который не знает ни один контракт, — ошибка: строки
// остаются SENT, reconcile повторит их.
func resolveActionOutcomes(ctx context.Context, resolvers []ActionResolver, receipt *types.Receipt, ids []uuid.UUID) (map[uuid.UUID]chain.ActionOutcome, error) {
	outcomes := make(map[uuid.UUID]chain.ActionOutcome, len(ids))
	for _, r := range resolvers {
		fromLogs, err := r.LogOutcomes(receipt)
		if err != nil {
			return nil, err // резолвер уже даёт контекст (unpack/log source)
		}
		for id, o := range fromLogs {
			if _, ok := outcomes[id]; !ok {
				outcomes[id] = o
			}
		}
	}
	for _, id := range ids {
		if _, ok := outcomes[id]; ok {
			continue
		}
		for _, r := range resolvers {
			o, known, err := r.StateOutcome(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("action state %s: %w", id, err)
			}
			if known {
				outcomes[id] = o
				break
			}
		}
		if _, ok := outcomes[id]; !ok {
			return nil, fmt.Errorf("no on-chain outcome for action %s", id)
		}
	}
	return outcomes, nil
}

// applyActionOutcomes применяет исходы подписанных действий из receipt'а tx:
// Applied → COMMITTED; отклонённые группируются по reason → MarkRejected (FAILED +
// reject_reason, контракт §3). Ошибка резолверов возвращается наружу — строки
// остаются SENT, их подберёт следующий reconcile.
func applyActionOutcomes(ctx context.Context, resolvers []ActionResolver, st terminalMarker, receipt *types.Receipt, ids []uuid.UUID, txHash string) error {
	if len(resolvers) == 0 {
		// Достижимо: рестарт адаптера без адресов контрактов действий при живых
		// SENT signed_action-строках — flusher их уже не отправит (без контрактов
		// действия терминально фейлятся), а reconcile дойдёт до их tx. Без этой
		// проверки — panic на nil-интерфейсе в горутине; громкая ошибка вместо него.
		return errors.New("apply action outcomes: action contracts not configured")
	}
	outcomes, err := resolveActionOutcomes(ctx, resolvers, receipt, ids)
	if err != nil {
		return fmt.Errorf("action outcomes: %w", err)
	}
	var applied []uuid.UUID
	byReason := make(map[uint8][]uuid.UUID)
	for _, id := range ids {
		o, ok := outcomes[id]
		if !ok {
			return fmt.Errorf("action outcome missing for %s", id)
		}
		if o.Applied {
			applied = append(applied, id)
			continue
		}
		byReason[o.Reason] = append(byReason[o.Reason], id)
	}
	if len(applied) > 0 {
		if err := st.MarkCommitted(ctx, applied); err != nil {
			return fmt.Errorf("mark committed: %w", err)
		}
	}
	for reason, group := range byReason {
		if err := st.MarkRejected(ctx, group, txHash, reason); err != nil {
			return fmt.Errorf("mark rejected: %w", err)
		}
	}
	return nil
}

// Reconciler — фоновый цикл, закрывающий N1: событие, ошибочно помеченное FAILED по
// таймауту receipt-poll'а (или зависшее в SENT), сверяется с реальным on-chain
// результатом и подтягивается к нему. Только сверка-и-запись-вперёд: НИКОГДА не
// переотправляет tx (это вернуло бы S2-поверхность, которую мы чиним).
type Reconciler struct {
	chain receiptChecker
	store reconcileStore
	// Резолверы исходов подписанных действий; без них исход не разобрать — sweep
	// логирует ошибку, а строки остаются SENT до подключения контрактов.
	warehouse ActionResolver
	registry  ActionResolver
	interval  time.Duration
	minAge    time.Duration
	log       *slog.Logger
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

// WithRegistry подключает разбор исходов ключевых действий. Возвращает r для цепочки.
func (r *Reconciler) WithRegistry(reg ActionResolver) *Reconciler {
	r.registry = reg
	return r
}

// WithWarehouseActions подключает разбор исходов складских действий. Возвращает r для цепочки.
func (r *Reconciler) WithWarehouseActions(w ActionResolver) *Reconciler {
	r.warehouse = w
	return r
}

// actionResolvers — резолверы исходов подписанных действий: складские
// (SignedMappingWMS) первым, затем реестр; nil'ы отфильтрованы.
func (r *Reconciler) actionResolvers() []ActionResolver {
	var resolvers []ActionResolver
	if r.warehouse != nil {
		resolvers = append(resolvers, r.warehouse)
	}
	if r.registry != nil {
		resolvers = append(resolvers, r.registry)
	}
	return resolvers
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

	errCount := 0
	for txHash, ids := range byTx {
		// Один битый tx (например receipt RPC-блип) не должен останавливать sweep.
		if _, err := reconcileReceipt(ctx, r.chain, r.store, ids, txHash, r.actionResolvers(), r.log); err != nil {
			errCount++
			r.log.Error("reconcile tx failed", "tx", txHash, "err", err)
		}
	}
	if errCount > 0 {
		// Видимость для оператора: устойчиво ненулевой счётчик = застрявший batch
		// (RPC стабильно отдаёт не-NotFound ошибку), а не разовый блип.
		r.log.Warn("reconcile sweep completed with errors", "tx_errors", errCount, "distinct_tx", len(byTx))
	}
	return nil
}
