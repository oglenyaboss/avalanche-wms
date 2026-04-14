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
// Позволяет подменить моком в unit-тестах.
type ChainCaller interface {
	BatchCall(ctx context.Context, topic string, eventIDs, itemIDs []*big.Int) (common.Hash, error)
	TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error)
}

// Store — minimal interface к onchain_events repo.
type Store interface {
	Exists(ctx context.Context, eventID uuid.UUID) (bool, error)
	Status(ctx context.Context, eventID uuid.UUID) (string, error)
	InsertPending(ctx context.Context, eventID uuid.UUID, aggType string) error
	MarkSent(ctx context.Context, ids []uuid.UUID, txHash string) error
	MarkCommitted(ctx context.Context, ids []uuid.UUID) error
	MarkFailed(ctx context.Context, ids []uuid.UUID, txHash, reason string) error
}

// DLQPublisher — отправка фейленых сообщений в DLQ-topic.
type DLQPublisher interface {
	PublishAll(ctx context.Context, msgs []*Message, reason string) error
}

// Flusher — pipeline: idempotency-filter → InsertPending → chain.BatchCall →
// MarkSent → WaitReceipt → MarkCommitted / MarkFailed+DLQ.
//
// Ошибки делятся на transient (БД/kafka недоступны) и terminal (chain revert).
// Transient → вернуть error, offsets не коммитим, retry при следующем tick.
// Terminal → MarkFailed + DLQ; если любая из этих операций fails — тоже
// transient (нельзя тихо проглотить — event потеряется).
type Flusher struct {
	chain          ChainCaller
	store          Store
	dlq            DLQPublisher
	receiptTimeout time.Duration
	log            *slog.Logger
}

// NewFlusher конструктор с дефолтным slog если log не задан.
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

// Flush обрабатывает один batch сообщений из заданного topic'а.
func (f *Flusher) Flush(ctx context.Context, topic string, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}

	// 1. Idempotency filter — пропускаем уже виденные event_id.
	pending, err := f.filterAndMarkPending(ctx, msgs)
	if err != nil {
		return err // transient (БД), ретраим
	}
	if len(pending) == 0 {
		f.log.Info("all events already processed, skipping batch", "topic", topic, "size", len(msgs))
		return nil
	}

	// 2. Convert IDs + send tx.
	ids, eventIDs, itemIDs := buildBatchArgs(pending)
	txHash, err := f.chain.BatchCall(ctx, topic, eventIDs, itemIDs)
	if err != nil {
		// Transient (RPC unreachable) → возвращаем error, не пишем FAILED/DLQ,
		// offset не коммитим — ретрай.
		if errors.Is(err, chain.ErrChainTransient) {
			f.log.Warn("chain call transient error — will retry", "topic", topic, "err", err)
			return fmt.Errorf("chain call transient: %w", err)
		}
		// Terminal (revert / bad tx / unknown) — DLQ + offset commit.
		f.log.Error("chain call failed", "topic", topic, "err", err, "size", len(pending))
		return f.recordFailure(ctx, pending, ids, "", err.Error())
	}

	// 3. Sent.
	if err := f.store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
		return fmt.Errorf("mark sent: %w", err) // transient
	}

	// 4. Receipt polling.
	receipt, err := chain.WaitReceipt(ctx, f.chain, txHash, f.receiptTimeout)
	if err != nil {
		reason := "receipt timeout: " + err.Error()
		f.log.Error("receipt timeout", "topic", topic, "tx", txHash.Hex(), "err", err)
		return f.recordFailure(ctx, pending, ids, txHash.Hex(), reason)
	}
	if receipt.Status == 0 {
		reason := "execution reverted"
		f.log.Error(reason, "topic", topic, "tx", txHash.Hex())
		return f.recordFailure(ctx, pending, ids, txHash.Hex(), reason)
	}

	// 5. Committed.
	if err := f.store.MarkCommitted(ctx, ids); err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	f.log.Info("batch committed", "topic", topic, "tx", txHash.Hex(), "size", len(pending))
	return nil
}

// recordFailure публикует в DLQ ПЕРВЫМ (DLQ = permanent record of failure),
// потом помечает row FAILED. Если любая из операций fails — возвращаем error
// → offset НЕ коммитим → retry на следующем цикле. При сбое MarkFailed после
// успешного DLQ событие имеет DLQ-запись но остаётся PENDING в DB — это
// self-consistent (retry перезапишет DB state через MarkFailed). Обратный
// порядок (MarkFailed first) мог бы оставить row=FAILED без DLQ-записи, что
// хуже: после H3 filterAndMarkPending скипает FAILED, операторы не увидели
// бы event в DLQ и узнавали о сбое только через БД-запрос.
//
// Контракт идемпотентен (processedEventIds), поэтому повторный chain call на
// уже revert'нувшемся event снова revert'нется → снова recordFailure.
func (f *Flusher) recordFailure(ctx context.Context, pending []*Message, ids []uuid.UUID, txHash, reason string) error {
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

// filterAndMarkPending делит входящий батч на (a) terminal — пропускаем,
// (b) non-terminal (PENDING/SENT от предыдущего зафейленного flush'а) или
// новые — InsertPending + добавляем к pending для повторной попытки.
//
// Non-terminal retry: InsertPending идемпотентен (ON CONFLICT DO NOTHING),
// повторная отправка SENT row'a приведёт к revert'у (contract.processedEventIds)
// → DLQ → оператор видит залипший event в DLQ с понятным reason'ом. Лучше
// видимого dup-error'а чем silent stranded row.
func (f *Flusher) filterAndMarkPending(ctx context.Context, msgs []*Message) ([]*Message, error) {
	pending := make([]*Message, 0, len(msgs))
	for _, m := range msgs {
		ok, err := f.store.Exists(ctx, m.EventID)
		if err != nil {
			return nil, fmt.Errorf("store.exists %s: %w", m.EventID, err)
		}
		if ok {
			status, serr := f.store.Status(ctx, m.EventID)
			if serr != nil {
				return nil, fmt.Errorf("store.status %s: %w", m.EventID, serr)
			}
			if status == "COMMITTED" || status == "FAILED" {
				f.log.Info("skip terminal event", "event_id", m.EventID, "topic", m.Topic, "status", status)
				continue
			}
			// PENDING или SENT — retry. Идёт на повторный chain.BatchCall;
			// для SENT контракт revert'нет (processedEventIds) → DLQ.
			f.log.Warn("retrying non-terminal event", "event_id", m.EventID, "topic", m.Topic, "status", status)
		} else {
			if err := f.store.InsertPending(ctx, m.EventID, m.AggregateType); err != nil {
				return nil, fmt.Errorf("insert pending %s: %w", m.EventID, err)
			}
		}
		pending = append(pending, m)
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
