package consumer

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

// ActionResolver — разбор исходов подписанных действий из receipt'а
// ConsortiumRegistry (плюс адрес контракта для SendCalldata).
type ActionResolver interface {
	ActionOutcomes(ctx context.Context, receipt *types.Receipt, ids []uuid.UUID) (map[uuid.UUID]chain.ActionOutcome, error)
	Address() common.Address
}

// maxActionCalldata — потолок calldata одной applyKeyActions-tx (ADR-0001 §9):
// узел subnet-evm отклоняет tx больше 131 072 байт, газ при таком объёме не
// упирается в лимит блока. Package var — тесты занижают его для проверки нарезки.
var maxActionCalldata = 120_000

// WithRegistry подключает разбор исходов подписанных действий (nil = действия
// терминально фейлятся с «registry not configured»). Возвращает f для цепочки.
func (f *Flusher) WithRegistry(r ActionResolver) *Flusher {
	f.registry = r
	return f
}

// sendKeyActions отправляет подписанные действия в ConsortiumRegistry через
// applyKeyActions. Signer один: действия уходят одной nonce-цепочкой, поэтому
// жадная упаковка в calldata-лимит сохраняет Kafka-порядок и внутри чанка, и
// между чанками/tx. inflight — накопленные tx предыдущих сегментов, возвращается
// обновлённым.
func (f *Flusher) sendKeyActions(ctx context.Context, msgs []*Message, inflight []InflightTx) ([]InflightTx, error) {
	pending, err := f.filterAndMarkPending(ctx, msgs)
	if err != nil {
		return inflight, err
	}
	if len(pending) == 0 {
		f.log.Info("all actions already processed, skipping", "aggregate", aggregateSignedAction, "size", len(msgs))
		return inflight, nil
	}

	if f.registry == nil {
		// Реестр не сконфигурирован (REGISTRY_ADDR пуст) — отправить некуда;
		// терминальный фейл (DLQ + FAILED), чтобы действия не зависали в PENDING.
		f.log.Error("registry not configured — failing signed actions", "size", len(pending))
		if rerr := f.recordFailure(ctx, pending, idsOf(pending), "", "registry not configured"); rerr != nil {
			return inflight, rerr
		}
		return inflight, nil
	}

	for i := 0; i < len(pending); {
		n, data, perr := maxActionChunk(pending[i:], maxActionCalldata)
		switch {
		case perr != nil:
			// Действие не пакуется в ABI (например отрицательный индекс) —
			// терминальный фейл его одного; остальные пойдут следующим шагом.
			f.log.Error("action packing failed", "event_id", pending[i].EventID, "err", perr)
			if rerr := f.recordFailure(ctx, pending[i:i+1], []uuid.UUID{pending[i].EventID}, "", perr.Error()); rerr != nil {
				return inflight, rerr
			}
			i++
		case n == 0:
			// Одиночное действие превышает calldata-лимит — терминальный фейл.
			f.log.Error("action exceeds calldata limit", "event_id", pending[i].EventID, "limit", maxActionCalldata)
			if rerr := f.recordFailure(ctx, pending[i:i+1], []uuid.UUID{pending[i].EventID}, "", "action exceeds calldata limit"); rerr != nil {
				return inflight, rerr
			}
			i++
		default:
			chunk := pending[i : i+n]
			txHash, err := f.caller.SendCalldata(ctx, f.registry.Address(), data)
			if err != nil {
				if errors.Is(err, chain.ErrChainTransient) {
					f.log.Warn("applyKeyActions transient error — will retry", "size", len(chunk), "err", err)
					return inflight, fmt.Errorf("chain call transient: %w", err)
				}
				f.log.Error("applyKeyActions failed", "size", len(chunk), "err", err)
				if rerr := f.recordFailure(ctx, chunk, idsOf(chunk), "", err.Error()); rerr != nil {
					return inflight, rerr
				}
				i += n
				continue
			}
			ids := idsOf(chunk)
			if err := f.store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
				return inflight, fmt.Errorf("mark sent: %w", err)
			}
			inflight = append(inflight, InflightTx{Aggregate: aggregateSignedAction, TxHash: txHash, IDs: ids, Msgs: chunk})
			i += n
		}
	}
	return inflight, nil
}

// maxActionChunk — сколько действий с начала msgs влезает в calldata-лимит
// (жадно, в порядке следования) и упакованный calldata этого префикса.
// Возвращает 0, если даже одиночное действие превышает лимит; err — если первое
// действие вообще не пакуется.
// ponytail: O(n²) по перепаковке — приемлемо, пока сегмент ограничен размером
// Kafka-окна (BATCH_SIZE); апгрейд — считать размер calldata инкрементально
// (ABI-размер одного действия) вместо перепаковки префикса.
func maxActionChunk(msgs []*Message, limit int) (int, []byte, error) {
	var data []byte
	for n := 1; n <= len(msgs); n++ {
		next, err := chain.PackApplyKeyActions(actionsOf(msgs[:n]))
		if err != nil {
			if n == 1 {
				return 0, nil, err
			}
			return n - 1, data, nil // префикс валиден; сломанное действие дойдёт следующим шагом
		}
		if len(next) > limit {
			return n - 1, data, nil
		}
		data = next
	}
	return len(msgs), data, nil
}

func actionsOf(msgs []*Message) []chain.KeyAction {
	actions := make([]chain.KeyAction, len(msgs))
	for i, m := range msgs {
		actions[i] = *m.Action
	}
	return actions
}

func idsOf(msgs []*Message) []uuid.UUID {
	ids := make([]uuid.UUID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.EventID
	}
	return ids
}
