package consumer

import (
	"context"
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
// Ошибки делятся на transient (БД недоступна) и terminal (chain revert).
// Transient → вернуть error, offsets не коммитим, retry при следующем tick.
// Terminal → MarkFailed + DLQ + nil error (offsets коммитим, чтобы не crasch-
// loop на том же сообщении).
type Flusher struct {
	Chain          ChainCaller
	Store          Store
	DLQ            DLQPublisher
	ReceiptTimeout time.Duration
	Log            *slog.Logger
}

// NewFlusher конструктор с дефолтным slog если Log не задан.
func NewFlusher(chainCaller ChainCaller, store Store, dlq DLQPublisher, receiptTimeout time.Duration, log *slog.Logger) *Flusher {
	if log == nil {
		log = slog.Default()
	}
	return &Flusher{
		Chain:          chainCaller,
		Store:          store,
		DLQ:            dlq,
		ReceiptTimeout: receiptTimeout,
		Log:            log,
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
		f.Log.Info("all events already processed, skipping batch", "topic", topic, "size", len(msgs))
		return nil
	}

	// 2. Convert IDs + send tx.
	ids, eventIDs, itemIDs := buildBatchArgs(pending)
	txHash, err := f.Chain.BatchCall(ctx, topic, eventIDs, itemIDs)
	if err != nil {
		// Terminal (revert при EstimateGas / bad tx) — DLQ + offset commit.
		f.Log.Error("chain call failed", "topic", topic, "err", err, "size", len(pending))
		_ = f.Store.MarkFailed(ctx, ids, "", err.Error())
		_ = f.DLQ.PublishAll(ctx, pending, err.Error())
		return nil
	}

	// 3. Sent.
	if err := f.Store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
		return fmt.Errorf("mark sent: %w", err) // transient
	}

	// 4. Receipt polling.
	receipt, err := chain.WaitReceipt(ctx, f.Chain, txHash, f.ReceiptTimeout)
	if err != nil {
		reason := "receipt timeout: " + err.Error()
		f.Log.Error("receipt timeout", "topic", topic, "tx", txHash.Hex(), "err", err)
		_ = f.Store.MarkFailed(ctx, ids, txHash.Hex(), reason)
		_ = f.DLQ.PublishAll(ctx, pending, reason)
		return nil
	}
	if receipt.Status == 0 {
		reason := "execution reverted"
		f.Log.Error(reason, "topic", topic, "tx", txHash.Hex())
		_ = f.Store.MarkFailed(ctx, ids, txHash.Hex(), reason)
		_ = f.DLQ.PublishAll(ctx, pending, reason)
		return nil
	}

	// 5. Committed.
	if err := f.Store.MarkCommitted(ctx, ids); err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	f.Log.Info("batch committed", "topic", topic, "tx", txHash.Hex(), "size", len(pending))
	return nil
}

func (f *Flusher) filterAndMarkPending(ctx context.Context, msgs []*Message) ([]*Message, error) {
	pending := make([]*Message, 0, len(msgs))
	for _, m := range msgs {
		ok, err := f.Store.Exists(ctx, m.EventID)
		if err != nil {
			return nil, fmt.Errorf("store.exists %s: %w", m.EventID, err)
		}
		if ok {
			f.Log.Info("skip duplicate event", "event_id", m.EventID, "topic", m.Topic)
			continue
		}
		if err := f.Store.InsertPending(ctx, m.EventID, m.AggregateType); err != nil {
			return nil, fmt.Errorf("insert pending %s: %w", m.EventID, err)
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
