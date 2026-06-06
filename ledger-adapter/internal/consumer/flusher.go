package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

// ChainCaller — minimal interface что нужен flusher'у от chain client'а.
type ChainCaller interface {
	BatchCall(ctx context.Context, aggregateType string, eventIDs, itemIDs []*big.Int) (common.Hash, error)
	TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}

// Store — minimal interface к onchain_events repo.
type Store interface {
	Exists(ctx context.Context, eventID uuid.UUID) (bool, error)
	StatusAndTx(ctx context.Context, eventID uuid.UUID) (status, txHash string, err error)
	InsertPending(ctx context.Context, eventID uuid.UUID, aggType string) error
	MarkSent(ctx context.Context, ids []uuid.UUID, txHash string) error
	MarkCommitted(ctx context.Context, ids []uuid.UUID) error
	MarkFailed(ctx context.Context, ids []uuid.UUID, txHash, reason string) error
}

// DLQPublisher — отправка фейленых сообщений в DLQ-topic.
type DLQPublisher interface {
	PublishAll(ctx context.Context, msgs []*Message, reason string) error
}

// fsmOrder определяет порядок обработки sub-batch'ей: контракт требует
// None→Accepted→PutAway→Picked→Shipped, поэтому receiving (→Accepted) ДОЛЖЕН
// быть committed ДО putaway (→PutAway) и т.д.
var fsmOrder = []string{"receiving", "putaway", "picking", "shipping"}

// InflightTx — отправленная (broadcast) но ещё не подтверждённая batch-tx.
// Возвращается Send'ом и передаётся в Confirm (serial) либо в pipeline-окно.
type InflightTx struct {
	Aggregate string
	TxHash    common.Hash
	IDs       []uuid.UUID
	Msgs      []*Message // для DLQ при терминальном фейле в Confirm
}

// ErrConfirmStalled — tx не замайнилась и не реверта в пределах receiptTimeout
// (или receipt RPC недоступен). В pipeline'е это триггерит recovery (reseed nonce
// + abandon окна), НЕ DLQ: tx может ещё быть в mempool, строки остаются SENT
// (видимы, не потеряны) для redelivery/reconcile (invariant 4).
var ErrConfirmStalled = errors.New("confirm stalled")

// Flusher — pipeline: группировка по aggregate_type → обработка в FSM-порядке →
// для каждого sub-batch: idempotency-filter → InsertPending → chain.BatchCall →
// MarkSent → WaitReceipt → MarkCommitted / MarkFailed+DLQ.
type Flusher struct {
	chain          ChainCaller
	store          Store
	dlq            DLQPublisher
	receiptTimeout time.Duration
	log            *slog.Logger
}

func NewFlusher(chainCaller ChainCaller, store Store, dlq DLQPublisher, receiptTimeout time.Duration, log *slog.Logger) *Flusher {
	if log == nil {
		log = slog.Default()
	}
	return &Flusher{
		chain:          chainCaller,
		store:          store,
		dlq:            dlq,
		receiptTimeout: receiptTimeout,
		log:            log,
	}
}

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

// Send группирует mixed-batch по aggregate_type, обрабатывает sub-batch'и в
// FSM-порядке и БЕЗ ожидания receipt'а broadcast'ит каждую batch-tx. Возвращает
// in-flight tx в FSM = nonce порядке.
//
// Семантика ошибок (как у старого flushSubBatch):
//   - transient BatchCall error → возвращает (уже собранный inflight, error);
//     caller ретраит весь drained-batch (Unshift). Уже-SENT sub-batch'и
//     reconcile'ятся на redelivery, не переотправляются.
//   - terminal revert → recordFailure (DLQ+MarkFailed), sub-batch НЕ попадает в
//     inflight, переходим к следующему aggregate.
//   - success → MarkSent + append в inflight.
//
// Partial inflight возвращается даже вместе с error — serial Flush дожимает уже
// отправленные sub-batch'и; pipeline на ошибке Send просто делает Unshift и не
// сабмитит окно (reconcile-путь подхватит SENT-строки).
func (f *Flusher) Send(ctx context.Context, msgs []*Message) ([]InflightTx, error) {
	if len(msgs) == 0 {
		return nil, nil
	}

	grouped := groupByAggregate(msgs)
	inflight := make([]InflightTx, 0, len(fsmOrder))

	for _, agg := range fsmOrder {
		sub, ok := grouped[agg]
		if !ok {
			continue
		}
		delete(grouped, agg)

		pending, err := f.filterAndMarkPending(ctx, sub)
		if err != nil {
			return inflight, err
		}
		if len(pending) == 0 {
			f.log.Info("all events already processed, skipping sub-batch", "aggregate", agg, "size", len(sub))
			continue
		}

		ids, eventIDs, itemIDs := buildBatchArgs(pending)
		txHash, err := f.chain.BatchCall(ctx, agg, eventIDs, itemIDs)
		if err != nil {
			if errors.Is(err, chain.ErrChainTransient) {
				f.log.Warn("chain call transient error — will retry", "aggregate", agg, "err", err)
				return inflight, fmt.Errorf("chain call transient: %w", err)
			}
			f.log.Error("chain call failed", "aggregate", agg, "err", err, "size", len(pending))
			if rerr := f.recordFailure(ctx, pending, ids, "", err.Error()); rerr != nil {
				return inflight, rerr
			}
			continue
		}

		if err := f.store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
			return inflight, fmt.Errorf("mark sent: %w", err)
		}
		inflight = append(inflight, InflightTx{Aggregate: agg, TxHash: txHash, IDs: ids, Msgs: pending})
	}

	for agg, sub := range grouped {
		f.log.Error("unknown aggregate_type in batch — sending to DLQ", "aggregate", agg, "size", len(sub))
		if err := f.dlq.PublishAll(ctx, sub, fmt.Sprintf("unknown aggregate_type: %s", agg)); err != nil {
			return inflight, fmt.Errorf("dlq unknown aggregate %s: %w", agg, err)
		}
	}
	return inflight, nil
}

// Confirm дожидается receipt'а одной in-flight tx и применяет терминальный
// результат: success → MarkCommitted; revert (status 0) → recordFailure
// (DLQ+MarkFailed); timeout/RPC-fail → ErrConfirmStalled (строки остаются SENT,
// без DLQ — pipeline запустит recovery, reconcile-loop подтянет).
func (f *Flusher) Confirm(ctx context.Context, tx *InflightTx) error {
	receipt, err := chain.WaitReceipt(ctx, f.chain, tx.TxHash, f.receiptTimeout)
	if err != nil {
		f.log.Warn("receipt not confirmed — stall", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex(), "err", err)
		return fmt.Errorf("%w: tx %s: %v", ErrConfirmStalled, tx.TxHash.Hex(), err)
	}
	if receipt.Status == 0 {
		f.log.Error("execution reverted", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex())
		return f.recordFailure(ctx, tx.Msgs, tx.IDs, tx.TxHash.Hex(), "execution reverted")
	}
	if err := f.store.MarkCommitted(ctx, tx.IDs); err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	f.log.Info("sub-batch committed", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex(), "size", len(tx.IDs))
	return nil
}

func (f *Flusher) recordFailure(ctx context.Context, pending []*Message, ids []uuid.UUID, txHash, reason string) error {
	// DLQ first, then MarkFailed. Trade-off: if MarkFailed fails after a successful
	// DLQ publish, the retry re-publishes to DLQ (noisy double-publish). The reverse
	// order (MarkFailed first) silently loses the DLQ entry when DLQ publish fails,
	// because filterAndMarkPending skips FAILED events — unrecoverable without manual
	// intervention. Noisy-but-recoverable beats silent-loss.
	if err := f.dlq.PublishAll(ctx, pending, reason); err != nil {
		f.log.Error("dlq publish", "reason", reason, "err", err)
		return fmt.Errorf("dlq publish: %w", err)
	}
	if err := f.store.MarkFailed(ctx, ids, txHash, reason); err != nil {
		f.log.Error("mark failed", "reason", reason, "err", err)
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// filterAndMarkPending решает для каждого сообщения: отправить (resubmit/новое),
// reconcile уже-broadcast'нутую tx, или скипнуть. Ключевое отличие от наивного
// resubmit'а — событие с уже записанным tx_hash НЕ переотправляется (это сменило бы
// tx_hash и пометило бы уже-успешную on-chain tx как FAILED — S2). Вместо этого
// reconcile'им существующую tx по её receipt'у.
func (f *Flusher) filterAndMarkPending(ctx context.Context, msgs []*Message) ([]*Message, error) {
	pending := make([]*Message, 0, len(msgs))
	// N9: Kafka at-least-once может переотправить тот же event_id в пределах одного
	// flush-окна. Дедупим, чтобы [id,id] не попал в batch (контракт его и так
	// скипнет, но грязно — и MarkCommitted([id,id]) трогал бы строку дважды).
	seen := make(map[uuid.UUID]struct{}, len(msgs))
	for _, m := range msgs {
		if _, dup := seen[m.EventID]; dup {
			f.log.Warn("intra-batch duplicate event_id skipped", "event_id", m.EventID, "aggregate", m.AggregateType)
			continue
		}
		seen[m.EventID] = struct{}{}

		ok, err := f.store.Exists(ctx, m.EventID)
		if err != nil {
			return nil, fmt.Errorf("store.exists %s: %w", m.EventID, err)
		}
		if !ok {
			// Новое событие: PENDING (tx_hash NULL) → отправляем.
			if err := f.store.InsertPending(ctx, m.EventID, m.AggregateType); err != nil {
				return nil, fmt.Errorf("insert pending %s: %w", m.EventID, err)
			}
			pending = append(pending, m)
			continue
		}

		status, txHash, err := f.store.StatusAndTx(ctx, m.EventID)
		if err != nil {
			return nil, fmt.Errorf("store.statusAndTx %s: %w", m.EventID, err)
		}
		switch {
		case status == "COMMITTED":
			f.log.Info("skip committed event", "event_id", m.EventID, "aggregate", m.AggregateType)
		case txHash != "":
			// Уже broadcast'нуто (crash-recovery S2 / receipt-timeout N1). НЕ resubmit:
			// reconcile существующую tx, tx_hash остаётся стабильным.
			terminal, rerr := reconcileReceipt(ctx, f.chain, f.store, []uuid.UUID{m.EventID}, txHash, f.log)
			if rerr != nil {
				return nil, fmt.Errorf("reconcile %s: %w", m.EventID, rerr)
			}
			if !terminal {
				f.log.Info("redelivered event still mining — left for reconcile loop", "event_id", m.EventID, "tx", txHash)
			}
		case status == "FAILED":
			// FAILED без tx_hash — tx никогда не отправлялась (например EstimateGas
			// revert). Терминально, не ретраим.
			f.log.Info("skip failed event without tx", "event_id", m.EventID, "aggregate", m.AggregateType)
		default:
			// PENDING без tx_hash — InsertPending был, MarkSent нет, tx ещё не уходила:
			// безопасно отправить. Краевой случай: если BatchCall прошёл, но MarkSent
			// упал → redelivery попадёт сюда и отправит повторно. Contract idempotency
			// (#44) не даст двойной transition, но первая tx осиротеет в DB (всплывёт
			// через chain-status gate #45). Без распределённой транзакции окно неустранимо.
			f.log.Warn("resubmitting pending event without tx", "event_id", m.EventID, "aggregate", m.AggregateType, "status", status)
			pending = append(pending, m)
		}
	}
	return pending, nil
}

func buildBatchArgs(msgs []*Message) (ids []uuid.UUID, eventIDs, itemIDs []*big.Int) {
	ids = make([]uuid.UUID, len(msgs))
	eventIDs = make([]*big.Int, len(msgs))
	itemIDs = make([]*big.Int, len(msgs))
	for i, m := range msgs {
		ids[i] = m.EventID
		eventIDs[i] = chain.UUIDToUint256(m.EventID.String())
		itemIDs[i] = chain.UUIDToUint256(m.ProductID.String())
	}
	return ids, eventIDs, itemIDs
}

func groupByAggregate(msgs []*Message) map[string][]*Message {
	groups := make(map[string][]*Message, len(fsmOrder))
	for _, m := range msgs {
		groups[m.AggregateType] = append(groups[m.AggregateType], m)
	}
	return groups
}
