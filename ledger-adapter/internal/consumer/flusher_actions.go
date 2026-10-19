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

// ActionResolver — двухфазный разбор исходов подписанных действий: логи
// receipt'а своего контракта, а для actionId без лога — fallback по actionState
// (ADR §6). Две фазы нужны reconcile'у: он знает только aggregate_type
// 'signed_action', но не то, в какой контракт ушла tx.
type ActionResolver interface {
	Address() common.Address
	// LogOutcomes — исходы действий из логов receipt'а этого контракта (Address()).
	LogOutcomes(receipt *types.Receipt) (map[uuid.UUID]chain.ActionOutcome, error)
	// StateOutcome — исход по actionState; false — State.None (контракт действие не знает).
	StateOutcome(ctx context.Context, id uuid.UUID) (chain.ActionOutcome, bool, error)
}

// maxActionCalldata — потолок calldata одной tx с действиями (ADR-0001 §9):
// узел subnet-evm отклоняет tx больше 131 072 байт, газ при таком объёме не
// упирается в лимит блока. Package var — тесты занижают его для проверки нарезки.
var maxActionCalldata = 120_000

// segmentKind — вид подряд идущих сообщений окна Send'а; смена вида режет окно
// на сегменты (ADR-0001 §9).
type segmentKind int

const (
	segmentLegacy    segmentKind = iota // legacy-события FSM-агрегатов
	segmentWarehouse                    // складские действия → SignedMappingWMS.applyActions
	segmentKey                          // ключевые действия → ConsortiumRegistry.applyKeyActions
)

// segmentOf определяет вид сообщения: складское действие, ключевое действие
// или legacy-событие.
func segmentOf(m *Message) segmentKind {
	switch {
	case m.WarehouseAction != nil:
		return segmentWarehouse
	case m.Action != nil:
		return segmentKey
	default:
		return segmentLegacy
	}
}

// splitSegments режет batch на подряд идущие сегменты одного вида: legacy-события,
// складские действия (WarehouseAction != nil), ключевые действия (Action != nil).
// Порядок сообщений внутри сегмента сохраняется: отзыв ключа нельзя переставить
// раньше действий, которые были до него.
func splitSegments(msgs []*Message) [][]*Message {
	segs := make([][]*Message, 0, 3)
	start := 0
	kind := segmentOf(msgs[0])
	for i := 1; i < len(msgs); i++ {
		if k := segmentOf(msgs[i]); k != kind {
			segs = append(segs, msgs[start:i])
			start = i
			kind = k
		}
	}
	return append(segs, msgs[start:])
}

// actionTarget — куда и как уходит один вид подписанных действий.
type actionTarget struct {
	name     string         // "applyActions" | "applyKeyActions" — для логов и причины фейла
	resolver ActionResolver // nil → терминальный фейл "<name>: contract not configured"
	pack     func([]*Message) ([]byte, error)
}

// WithRegistry подключает разбор исходов ключевых действий (nil = они
// терминально фейлятся с «applyKeyActions: contract not configured»).
// Возвращает f для цепочки.
func (f *Flusher) WithRegistry(r ActionResolver) *Flusher {
	f.registry = r
	return f
}

// WithWarehouseActions подключает разбор исходов складских действий (nil = они
// терминально фейлятся с «applyActions: contract not configured»).
// Возвращает f для цепочки.
func (f *Flusher) WithWarehouseActions(w ActionResolver) *Flusher {
	f.warehouse = w
	return f
}

// warehouseTarget — цель складских действий: SignedMappingWMS.applyActions.
func (f *Flusher) warehouseTarget() actionTarget {
	return actionTarget{
		name:     "applyActions",
		resolver: f.warehouse,
		pack: func(msgs []*Message) ([]byte, error) {
			return chain.PackApplyActions(warehouseActionsOf(msgs))
		},
	}
}

// keyTarget — цель ключевых действий: ConsortiumRegistry.applyKeyActions.
func (f *Flusher) keyTarget() actionTarget {
	return actionTarget{
		name:     "applyKeyActions",
		resolver: f.registry,
		pack: func(msgs []*Message) ([]byte, error) {
			return chain.PackApplyKeyActions(actionsOf(msgs))
		},
	}
}

// sendActions отправляет подписанные действия одного вида: t.pack → SendCalldata
// на адрес t.resolver. Signer один: действия уходят одной nonce-цепочкой, поэтому
// жадная упаковка в calldata-лимит сохраняет Kafka-порядок и внутри чанка, и
// между чанками/tx. inflight — накопленные tx предыдущих сегментов, возвращается
// обновлённым.
func (f *Flusher) sendActions(ctx context.Context, msgs []*Message, inflight []InflightTx, t actionTarget) ([]InflightTx, error) {
	pending, err := f.filterAndMarkPending(ctx, msgs)
	if err != nil {
		return inflight, err
	}
	if len(pending) == 0 {
		f.log.Info("all actions already processed, skipping", "aggregate", aggregateSignedAction, "size", len(msgs))
		return inflight, nil
	}

	if t.resolver == nil {
		// Контракт не сконфигурирован — отправить некуда; терминальный фейл
		// (DLQ + FAILED), чтобы действия не зависали в PENDING.
		f.log.Error(t.name + ": contract not configured — failing signed actions")
		if rerr := f.recordFailure(ctx, pending, idsOf(pending), "", t.name+": contract not configured"); rerr != nil {
			return inflight, rerr
		}
		return inflight, nil
	}

	for i := 0; i < len(pending); {
		n, data, perr := maxActionChunk(pending[i:], maxActionCalldata, t.pack)
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
			txHash, err := f.caller.SendCalldata(ctx, t.resolver.Address(), data)
			if err != nil {
				if errors.Is(err, chain.ErrChainTransient) {
					f.log.Warn(t.name+" transient error — will retry", "size", len(chunk), "err", err)
					return inflight, fmt.Errorf("chain call transient: %w", err)
				}
				f.log.Error(t.name+" failed", "size", len(chunk), "err", err)
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
func maxActionChunk(msgs []*Message, limit int, pack func([]*Message) ([]byte, error)) (int, []byte, error) {
	var data []byte
	for n := 1; n <= len(msgs); n++ {
		next, err := pack(msgs[:n])
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

func warehouseActionsOf(msgs []*Message) []chain.WarehouseAction {
	actions := make([]chain.WarehouseAction, len(msgs))
	for i, m := range msgs {
		actions[i] = *m.WarehouseAction
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
