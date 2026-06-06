package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/segmentio/kafka-go"
)

// drainedBatch — единица offset-commit'а: ВСЕ kafka-сообщения одного слитого
// batch'а вместе с in-flight tx'ами, на которые Send их разбил. Offsets
// коммитятся только когда все txs терминально успешны — и строго в FIFO порядке
// drained batch'ей (collector — одна горутина).
//
// Пустой txs валиден (всё уже COMMITTED/DLQ'd/reconciled на этапе Send или это
// parse-fail). Такой batch ВСЁ РАВНО проходит через collector, чтобы его offsets
// закоммитились в очереди, а не в обход: kafka-go CommitMessages — это per-
// partition high-water mark, и коммит offset N в обход перескочил бы
// незаконфирменные более ранние offset'ы < N → потеря.
type drainedBatch struct {
	txs       []InflightTx
	kafkaMsgs []kafka.Message
}

// confirmer — то, что pipeline'у нужно от Flusher'а: дождаться receipt'а и
// применить терминальный результат одной in-flight tx.
type confirmer interface {
	Confirm(ctx context.Context, tx *InflightTx) error
}

// offsetCommitter — то, что нужно от kafka.Reader'а: коммит offset'ов.
type offsetCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// ErrPipelineStalled — in-flight tx не подтвердилась (ErrConfirmStalled).
// Collector бросает окно БЕЗ коммита offset'ов и возвращает эту ошибку. Consumer
// эскалирует её как фатальную → процесс рестартует → свежий nonce + Kafka
// redelivery незакоммиченного окна + reconcile-by-txhash (invariant 4 плана).
//
// Recovery = рестарт процесса, НЕ in-place reopen: happy-path бенч этот путь не
// исполняет (gas-headroom + окно < TxPoolAccountSlots делают stall редчайшим), а
// рестарт без потерь — это ноль нового кода и ноль race-surface.
var ErrPipelineStalled = errors.New("pipeline stalled")

// Pipeline — окно in-flight batch'ей + единственная горутина-collector,
// подтверждающая их FIFO и коммитящая offsets по contiguous-confirmed префиксу.
type Pipeline struct {
	confirmer confirmer
	committer offsetCommitter
	inflight  chan drainedBatch
	done      chan struct{} // закрыт когда collector вышел — разблокирует Submit
	log       *slog.Logger
}

// NewPipeline. window — максимум in-flight batch'ей (backpressure). Ёмкость
// канала = window-1: один batch «в канале» + один в обработке collector'ом =
// ровно window in-flight до блокировки Submit.
func NewPipeline(c confirmer, committer offsetCommitter, window int, log *slog.Logger) *Pipeline {
	if log == nil {
		log = slog.Default()
	}
	if window < 1 {
		window = 1
	}
	return &Pipeline{
		confirmer: c,
		committer: committer,
		inflight:  make(chan drainedBatch, window-1),
		done:      make(chan struct{}),
		log:       log,
	}
}

// Submit ставит drained batch в очередь на подтверждение. Блокируется при
// заполненном окне (backpressure). Возвращает ErrPipelineStalled если collector
// уже умер (чтобы заблокированный Submit не завис навсегда), либо ctx-ошибку.
func (p *Pipeline) Submit(ctx context.Context, db drainedBatch) error {
	// ctx первым: на shutdown'е не маскируем отмену под stall.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// done вперёд буфера: если collector уже умер, не суём batch в осиротевший
	// канал (его никто не сольёт). Без этого buffered-send в select выиграл бы
	// гонку у <-done, и consumer слал бы tx'ы в мёртвый pipeline ещё cap раз.
	select {
	case <-p.done:
		return ErrPipelineStalled
	default:
	}
	select {
	case p.inflight <- db:
		return nil
	case <-p.done:
		return ErrPipelineStalled
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close сигналит collector'у, что новых batch'ей не будет (graceful drain):
// collector дослит оставшиеся в канале, подтвердит, закоммитит и выйдет.
func (p *Pipeline) Close() { close(p.inflight) }

// Run — collector. ОДНА горутина (это load-bearing: per-partition offsets
// остаются монотонными — никогда не коммитим offset вне FIFO-порядка). Для
// каждого drained batch подтверждает все его txs в nonce-порядке; коммитит его
// offsets только когда ВСЕ успешны. Stall → бросаем окно без коммита →
// ErrPipelineStalled (фатально → рестарт). На отмене ctx (graceful shutdown)
// выходим без коммита in-flight: незакоммиченное redeliver'нется и reconcile'ится.
func (p *Pipeline) Run(ctx context.Context) error {
	defer close(p.done)
	for {
		select {
		case <-ctx.Done():
			return nil
		case db, ok := <-p.inflight:
			if !ok {
				return nil // Close() — канал исчерпан, graceful.
			}
			if err := p.confirmAndCommit(ctx, db); err != nil {
				if ctx.Err() != nil {
					// shutdown отменил in-flight confirm — безопасный abandon.
					return nil
				}
				return err
			}
		}
	}
}

// confirmAndCommit подтверждает все txs одного batch'а FIFO и коммитит его
// offsets целиком только при полном успехе.
func (p *Pipeline) confirmAndCommit(ctx context.Context, db drainedBatch) error {
	for i := range db.txs {
		tx := &db.txs[i]
		err := p.confirmer.Confirm(ctx, tx)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrConfirmStalled) {
			p.log.Error("in-flight tx stalled — abandoning window, offsets NOT committed (redelivery reconciles)",
				"tx", tx.TxHash.Hex(), "aggregate", tx.Aggregate)
			return fmt.Errorf("%w: %v", ErrPipelineStalled, err)
		}
		// Не-stall (transient store/DLQ ошибка в Confirm) — тоже бросаем без
		// коммита; рестарт + redelivery — безопасное восстановление.
		return fmt.Errorf("confirm %s: %w", tx.Aggregate, err)
	}
	if err := p.committer.CommitMessages(ctx, db.kafkaMsgs...); err != nil {
		return fmt.Errorf("commit offsets: %w", err)
	}
	return nil
}
