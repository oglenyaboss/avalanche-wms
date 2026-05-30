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

// Flush обрабатывает mixed-batch: группирует по AggregateType, обрабатывает
// sub-batch'и в FSM-порядке. При partial failure (например recv COMMITTED,
// putaway FAILED) — возвращает error чтобы Kafka offsets не закоммитились;
// на retry filterAndMarkPending корректно скипнет уже COMMITTED events.
func (f *Flusher) Flush(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}

	grouped := groupByAggregate(msgs)

	for _, agg := range fsmOrder {
		sub, ok := grouped[agg]
		if !ok {
			continue
		}
		delete(grouped, agg)
		if err := f.flushSubBatch(ctx, agg, sub); err != nil {
			return err
		}
	}
	for agg, sub := range grouped {
		f.log.Error("unknown aggregate_type in batch — sending to DLQ", "aggregate", agg, "size", len(sub))
		if err := f.dlq.PublishAll(ctx, sub, fmt.Sprintf("unknown aggregate_type: %s", agg)); err != nil {
			return fmt.Errorf("dlq unknown aggregate %s: %w", agg, err)
		}
	}
	return nil
}

// flushSubBatch обрабатывает один sub-batch одного aggregate_type.
func (f *Flusher) flushSubBatch(ctx context.Context, aggregateType string, msgs []*Message) error {
	pending, err := f.filterAndMarkPending(ctx, msgs)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		f.log.Info("all events already processed, skipping sub-batch", "aggregate", aggregateType, "size", len(msgs))
		return nil
	}

	ids, eventIDs, itemIDs := buildBatchArgs(pending)
	txHash, err := f.chain.BatchCall(ctx, aggregateType, eventIDs, itemIDs)
	if err != nil {
		if errors.Is(err, chain.ErrChainTransient) {
			f.log.Warn("chain call transient error — will retry", "aggregate", aggregateType, "err", err)
			return fmt.Errorf("chain call transient: %w", err)
		}
		f.log.Error("chain call failed", "aggregate", aggregateType, "err", err, "size", len(pending))
		return f.recordFailure(ctx, pending, ids, "", err.Error())
	}

	if err := f.store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}

	receipt, err := chain.WaitReceipt(ctx, f.chain, txHash, f.receiptTimeout)
	if err != nil {
		reason := "receipt timeout: " + err.Error()
		f.log.Error("receipt timeout", "aggregate", aggregateType, "tx", txHash.Hex(), "err", err)
		return f.recordFailure(ctx, pending, ids, txHash.Hex(), reason)
	}
	if receipt.Status == 0 {
		reason := "execution reverted"
		f.log.Error(reason, "aggregate", aggregateType, "tx", txHash.Hex())
		return f.recordFailure(ctx, pending, ids, txHash.Hex(), reason)
	}

	if err := f.store.MarkCommitted(ctx, ids); err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	f.log.Info("sub-batch committed", "aggregate", aggregateType, "tx", txHash.Hex(), "size", len(pending))
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
			// PENDING без tx_hash — tx ещё не уходила, безопасно отправить.
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
