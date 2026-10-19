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
	SendCalldata(ctx context.Context, to common.Address, data []byte) (common.Hash, error)
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
	MarkRejected(ctx context.Context, ids []uuid.UUID, txHash string, reason uint8) error
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
// Подписанные действия (signed_action) идут в сегментах окна между legacy-стадиями
// в Kafka-порядке через ConsortiumRegistry.applyKeyActions.
type Flusher struct {
	caller         ChainCaller // единственный signer-клиент; он же читает receipt'ы (node-global)
	store          Store
	dlq            DLQPublisher
	registry       ActionResolver // nil = подписанные действия не отправляются (recordFailure)
	receiptTimeout time.Duration
	log            *slog.Logger
}

// NewFlusher принимает единственного signer-клиента: config.Load гарантирует
// ровно одного signer'а (multi-signer отклоняется на старте — подписанные
// действия держат Kafka-порядок одной nonce-цепочкой). Receipt-чтения
// (Confirm/reconcile) идут через тот же клиент: любой узел возвращает любой receipt.
func NewFlusher(caller ChainCaller, store Store, dlq DLQPublisher, receiptTimeout time.Duration, log *slog.Logger) *Flusher {
	if log == nil {
		log = slog.Default()
	}
	if caller == nil {
		panic("NewFlusher: need a signer client")
	}
	return &Flusher{
		caller:         caller,
		store:          store,
		dlq:            dlq,
		receiptTimeout: receiptTimeout,
		log:            log,
	}
}

// Send режет batch на подряд идущие сегменты (подписанные действия / legacy) и
// обрабатывает их в Kafka-порядке окна: действия не перепрыгивают legacy-стадии
// между ними (все tx уходят с одного signer'а одной nonce-цепочкой). Legacy-сегмент
// внутри себя группируется и отправляется в FSM-порядке (см. sendLegacy).
//
// Семантика ошибок (как у старого flushSubBatch):
//   - transient chain error → возвращает (уже собранный inflight, error);
//     caller ретраит весь drained-batch (Unshift). Уже-SENT sub-batch'и
//     reconcile'ятся на redelivery, не переотправляются.
//   - terminal revert / терминальный фейл действия → recordFailure (DLQ+MarkFailed),
//     сегмент/чанк НЕ попадает в inflight, переходим к следующему.
//   - success → MarkSent + append в inflight.
//
// Partial inflight возвращается даже вместе с error — serial Flush дожимает уже
// отправленные sub-batch'и; pipeline на ошибке Send просто делает Unshift и не
// сабмитит окно (reconcile-путь подхватит SENT-строки).
func (f *Flusher) Send(ctx context.Context, msgs []*Message) ([]InflightTx, error) {
	if len(msgs) == 0 {
		return nil, nil
	}

	var inflight []InflightTx
	for _, seg := range splitSegments(msgs) {
		var err error
		if seg[0].Action != nil {
			inflight, err = f.sendKeyActions(ctx, seg, inflight)
		} else {
			inflight, err = f.sendLegacy(ctx, seg, inflight)
		}
		if err != nil {
			return inflight, err
		}
	}
	return inflight, nil
}

// splitSegments режет batch на подряд идущие сегменты: действия (Action != nil)
// и legacy-сообщения. Порядок сообщений внутри сегмента сохраняется.
func splitSegments(msgs []*Message) [][]*Message {
	segs := make([][]*Message, 0, 2)
	start := 0
	isAction := msgs[0].Action != nil
	for i := 1; i < len(msgs); i++ {
		if (msgs[i].Action != nil) != isAction {
			segs = append(segs, msgs[start:i])
			start = i
			isAction = msgs[i].Action != nil
		}
	}
	return append(segs, msgs[start:])
}

// sendLegacy — прежнее тело Send для FSM-агрегатов (receiving/putaway/picking/
// shipping): группировка по aggregate_type, отправка в FSM-порядке; неизвестные
// агрегаты сегмента — DLQ. inflight — накопленные tx предыдущих сегментов,
// возвращается обновлённым.
func (f *Flusher) sendLegacy(ctx context.Context, msgs []*Message, inflight []InflightTx) ([]InflightTx, error) {
	grouped := groupByAggregate(msgs)

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
		txHash, err := f.caller.BatchCall(ctx, agg, eventIDs, itemIDs)
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
			terminal, rerr := reconcileReceipt(ctx, f.caller, f.store, []uuid.UUID{m.EventID}, txHash, m.AggregateType, f.registry, f.log)
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
