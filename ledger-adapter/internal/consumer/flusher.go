package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

// ChainCaller — minimal interface что нужен flusher'у от chain client'а.
type ChainCaller interface {
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

// Flusher — pipeline: окно Kafka-сообщений режется на сегменты складских и
// ключевых подписанных действий; каждый сегмент проходит
// idempotency-filter → InsertPending → SendCalldata → MarkSent;
// складские уходят в SignedMappingWMS.applyActions, ключевые в
// ConsortiumRegistry.applyKeyActions. Confirm разбирает исходы действий по
// receipt'у и пишет COMMITTED/FAILED+reject_reason.
type Flusher struct {
	caller         ChainCaller // единственный signer-клиент; он же читает receipt'ы (node-global)
	store          Store
	dlq            DLQPublisher
	warehouse      ActionResolver // nil = складские действия не отправляются (recordFailure)
	registry       ActionResolver // nil = ключевые действия не отправляются (recordFailure)
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

// Send режет batch на подряд идущие сегменты складских и ключевых действий и
// обрабатывает их в Kafka-порядке окна (все tx уходят с одного signer'а одной
// nonce-цепочкой; порядок внутри сегмента сохраняется — см. splitSegments).
//
// Семантика ошибок:
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
		switch segmentOf(seg[0]) {
		case segmentWarehouse:
			inflight, err = f.sendActions(ctx, seg, inflight, f.warehouseTarget())
		case segmentKey:
			inflight, err = f.sendActions(ctx, seg, inflight, f.keyTarget())
		}
		if err != nil {
			return inflight, err
		}
	}
	return inflight, nil
}

// actionResolvers — резолверы исходов подписанных действий: складские
// (SignedMappingWMS) первым, затем реестр; nil'ы отфильтрованы.
func (f *Flusher) actionResolvers() []ActionResolver {
	var resolvers []ActionResolver
	if f.warehouse != nil {
		resolvers = append(resolvers, f.warehouse)
	}
	if f.registry != nil {
		resolvers = append(resolvers, f.registry)
	}
	return resolvers
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
			terminal, rerr := reconcileReceipt(ctx, f.caller, f.store, []uuid.UUID{m.EventID}, txHash, f.actionResolvers(), f.log)
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
			// безопасно отправить. Краевой случай: если отправка прошла, а MarkSent
			// упал → redelivery отправит повторно; контракт пропустит повтор actionId
			// (ADR §6), первая tx осиротеет в БД.
			f.log.Warn("resubmitting pending event without tx", "event_id", m.EventID, "aggregate", m.AggregateType, "status", status)
			pending = append(pending, m)
		}
	}
	return pending, nil
}
