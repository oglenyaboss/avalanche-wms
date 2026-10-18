package consumer

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
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

// ActionResolver — разбор исходов подписанных действий из receipt'а
// ConsortiumRegistry (плюс адрес контракта для SendCalldata).
type ActionResolver interface {
	ActionOutcomes(ctx context.Context, receipt *types.Receipt, ids []uuid.UUID) (map[uuid.UUID]chain.ActionOutcome, error)
	Address() common.Address
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

// maxActionCalldata — потолок calldata одной applyKeyActions-tx (ADR-0001 §9):
// узел subnet-evm отклоняет tx больше 131 072 байт, газ при таком объёме не
// упирается в лимит блока. Package var — тесты занижают его для проверки нарезки.
var maxActionCalldata = 120_000

// Flusher — pipeline: группировка по aggregate_type → обработка в FSM-порядке →
// для каждого sub-batch: idempotency-filter → InsertPending → шардинг по signer'у →
// chain.BatchCall → MarkSent → WaitReceipt → MarkCommitted / MarkFailed+DLQ.
// Подписанные действия (signed_action) идут в сегментах окна между legacy-стадиями
// в Kafka-порядке через ConsortiumRegistry.applyKeyActions.
type Flusher struct {
	chains         []ChainCaller // N signer-аккаунтов; Send шардит по ним по ProductID
	receiptReader  ChainCaller   // = chains[0]; receipt-чтения node-global (Confirm/reconcile)
	store          Store
	dlq            DLQPublisher
	registry       ActionResolver // nil = подписанные действия не отправляются (recordFailure)
	receiptTimeout time.Duration
	log            *slog.Logger
}

// NewFlusher принимает один или несколько signer-клиентов (≥1). Send шардит
// in-flight tx по ProductID между ними (signerIndex) — каждый item остаётся на
// одном signer'е, поэтому per-signer nonce-порядок = FSM-порядок (контракт требует
// Accepted→PutAway→Picked→Shipped на item). N signer'ов = N× in-flight ёмкость
// (каждый ограничен TxPoolAccountSlots) → насыщение WAN-цепи. receipt-чтения идут
// через chains[0] (любой узел возвращает любой receipt).
func NewFlusher(chains []ChainCaller, store Store, dlq DLQPublisher, receiptTimeout time.Duration, log *slog.Logger) *Flusher {
	if log == nil {
		log = slog.Default()
	}
	if len(chains) == 0 {
		panic("NewFlusher: need ≥1 signer client")
	}
	return &Flusher{
		chains:         chains,
		receiptReader:  chains[0],
		store:          store,
		dlq:            dlq,
		receiptTimeout: receiptTimeout,
		log:            log,
	}
}

// WithRegistry подключает разбор исходов подписанных действий (nil = действия
// терминально фейлятся с «registry not configured»). Возвращает f для цепочки.
func (f *Flusher) WithRegistry(r ActionResolver) *Flusher {
	f.registry = r
	return f
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

		// Шардим pending по signer'у (ProductID → signerIndex): item остаётся на
		// одном signer'е → per-signer nonce-порядок = FSM-порядок. Один aggregate
		// одного batch'а → до N txs (по shard'у на signer), in-flight ёмкость ×N.
		shards := shardBySigner(pending, len(f.chains))
		for si, shard := range shards {
			if len(shard) == 0 {
				continue
			}
			ids, eventIDs, itemIDs := buildBatchArgs(shard)
			txHash, err := f.chains[si].BatchCall(ctx, agg, eventIDs, itemIDs)
			if err != nil {
				if errors.Is(err, chain.ErrChainTransient) {
					f.log.Warn("chain call transient error — will retry", "aggregate", agg, "signer", si, "err", err)
					return inflight, fmt.Errorf("chain call transient: %w", err)
				}
				f.log.Error("chain call failed", "aggregate", agg, "signer", si, "err", err, "size", len(shard))
				if rerr := f.recordFailure(ctx, shard, ids, "", err.Error()); rerr != nil {
					return inflight, rerr
				}
				continue
			}
			if err := f.store.MarkSent(ctx, ids, txHash.Hex()); err != nil {
				return inflight, fmt.Errorf("mark sent: %w", err)
			}
			inflight = append(inflight, InflightTx{Aggregate: agg, TxHash: txHash, IDs: ids, Msgs: shard})
		}
	}

	for agg, sub := range grouped {
		f.log.Error("unknown aggregate_type in batch — sending to DLQ", "aggregate", agg, "size", len(sub))
		if err := f.dlq.PublishAll(ctx, sub, fmt.Sprintf("unknown aggregate_type: %s", agg)); err != nil {
			return inflight, fmt.Errorf("dlq unknown aggregate %s: %w", agg, err)
		}
	}
	return inflight, nil
}

// sendKeyActions отправляет подписанные действия в ConsortiumRegistry через
// applyKeyActions. Signer один (f.chains[0]): действия уходят одной nonce-
// цепочкой, поэтому жадная упаковка в calldata-лимит сохраняет Kafka-порядок
// и внутри чанка, и между чанками/tx. inflight — накопленные tx предыдущих
// сегментов, возвращается обновлённым.
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
		n, perr := maxActionChunk(pending[i:], maxActionCalldata)
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
			data, err := chain.PackApplyKeyActions(actionsOf(chunk))
			if err != nil {
				// Парс уже отвалидировал поля; сюда не ждём. Логика maxActionChunk
				// гарантирует, что чанк пакуется — на всякий случай терминальный фейл.
				f.log.Error("action packing failed", "size", len(chunk), "err", err)
				if rerr := f.recordFailure(ctx, chunk, idsOf(chunk), "", err.Error()); rerr != nil {
					return inflight, rerr
				}
				i += n
				continue
			}
			txHash, err := f.chains[0].SendCalldata(ctx, f.registry.Address(), data)
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
// (жадно, в порядке следования). Возвращает 0, если даже одиночное действие
// превышает лимит; err — если первое действие вообще не пакуется. О(n²) по
// перепаковке — приемлемо: сегмент ограничен размером Kafka-окна (BATCH_SIZE).
func maxActionChunk(msgs []*Message, limit int) (int, error) {
	for n := 1; n <= len(msgs); n++ {
		data, err := chain.PackApplyKeyActions(actionsOf(msgs[:n]))
		if err != nil {
			if n == 1 {
				return 0, err
			}
			return n - 1, nil // префикс валиден; сломанное действие дойдёт следующим шагом
		}
		if len(data) > limit {
			return n - 1, nil
		}
	}
	return len(msgs), nil
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

// Confirm дожидается receipt'а одной in-flight tx и применяет терминальный
// результат: success → MarkCommitted (для подписанных действий — разбор исходов
// реестра: Applied → COMMITTED, отклонённые → MarkRejected с reject_reason);
// revert (status 0) → recordFailure (DLQ+MarkFailed); timeout/RPC-fail →
// ErrConfirmStalled (строки остаются SENT, без DLQ — pipeline запустит recovery,
// reconcile-loop подтянет).
func (f *Flusher) Confirm(ctx context.Context, tx *InflightTx) error {
	receipt, err := chain.WaitReceipt(ctx, f.receiptReader, tx.TxHash, f.receiptTimeout)
	if err != nil {
		f.log.Warn("receipt not confirmed — stall", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex(), "err", err)
		return fmt.Errorf("%w: tx %s: %v", ErrConfirmStalled, tx.TxHash.Hex(), err)
	}
	if receipt.Status == 0 {
		f.log.Error("execution reverted", "aggregate", tx.Aggregate, "tx", tx.TxHash.Hex())
		return f.recordFailure(ctx, tx.Msgs, tx.IDs, tx.TxHash.Hex(), "execution reverted")
	}
	if tx.Aggregate == aggregateSignedAction {
		if err := applyActionOutcomes(ctx, f.registry, f.store, receipt, tx.IDs, tx.TxHash.Hex()); err != nil {
			return fmt.Errorf("apply action outcomes: %w", err)
		}
		f.log.Info("signed actions resolved from receipt", "tx", tx.TxHash.Hex(), "size", len(tx.IDs))
		return nil
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
			terminal, rerr := reconcileReceipt(ctx, f.receiptReader, f.store, []uuid.UUID{m.EventID}, txHash, m.AggregateType, f.registry, f.log)
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

// shardBySigner делит сообщения на n групп по signerIndex(ProductID). Каждый item
// (все его FSM-стадии разделяют ProductID) всегда попадает в одну группу → один
// signer → монотонный per-signer nonce = FSM-порядок (cross-batch и intra-batch).
func shardBySigner(msgs []*Message, n int) [][]*Message {
	shards := make([][]*Message, n)
	for _, m := range msgs {
		shards[signerIndex(m.ProductID, n)] = append(shards[signerIndex(m.ProductID, n)], m)
	}
	return shards
}

// signerIndex — детерминированный ProductID → [0,n). FNV-1a по 16 байтам UUID
// равномерно распределяет товары; uuid.Nil маппится консистентно (item'ы с
// незаданным ProductID идут к одному signer'у, FSM-порядок сохраняется).
func signerIndex(productID uuid.UUID, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write(productID[:])
	return int(h.Sum32() % uint32(n))
}
