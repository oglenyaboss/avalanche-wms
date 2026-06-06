package consumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

// Consumer читает один topic, аккумулирует в Batcher, и через Flusher.Send
// broadcast'ит batch-tx'ы в Pipeline (окно in-flight + FIFO collector), который
// дожидается receipt'ов и коммитит kafka offsets по подтверждённому префиксу.
//
// Старая модель была serial: flush блокировался на WaitReceipt каждого batch'а
// (≈один batch на блок ≈900/s). Pipeline держит несколько tx in-flight в одном
// блоке (см. pipeline.go), снимая этот потолок.
//
// Цикл: fetch → Parse → Batcher.Add → (ShouldFlush) Send → Pipeline.Submit.
// Collector (отдельная горутина) подтверждает и коммитит. Stall in-flight tx →
// фатально → процесс рестартует (recovery = рестарт).
type Consumer struct {
	reader   *kafka.Reader
	batcher  *Batcher
	flusher  *Flusher
	pipeline *Pipeline
	dlq      DLQPublisher
	topic    string
	log      *slog.Logger
	tickInt  time.Duration
}

// NewConsumer собирает kafka.Reader + Batcher + Pipeline. pipelineWindow —
// сколько batch-tx держать in-flight одновременно (1 = старое serial-поведение).
func NewConsumer(
	brokers []string,
	topic, groupID string,
	flusher *Flusher,
	dlq DLQPublisher,
	batchSize int,
	batchTimeout time.Duration,
	pipelineWindow int,
	log *slog.Logger,
) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 1,
		MaxBytes: 10e6, // 10 MB
		MaxWait:  100 * time.Millisecond,
	})
	return &Consumer{
		reader:   r,
		batcher:  NewBatcher(batchSize, batchTimeout),
		flusher:  flusher,
		pipeline: NewPipeline(flusher, r, pipelineWindow, log),
		dlq:      dlq,
		topic:    topic,
		log:      log,
		tickInt:  50 * time.Millisecond,
	}
}

// Run блокирует пока ctx не отменится или не случится фатальная ошибка
// (fetch-фейл или stall pipeline'а). На shutdown — best-effort: in-flight SENT
// строки redeliver'нутся и reconcile'ятся на следующем старте.
func (c *Consumer) Run(ctx context.Context) error {
	tick := time.NewTicker(c.tickInt)
	defer tick.Stop()
	defer func() { _ = c.reader.Close() }()

	// Collector — единственная горутина, дренирующая окно и коммитящая offsets.
	collErr := make(chan error, 1)
	go func() { collErr <- c.pipeline.Run(ctx) }()

	msgCh := make(chan kafka.Message)
	fetchErrCh := make(chan error, 1)
	go c.fetchLoop(ctx, msgCh, fetchErrCh)

	for {
		select {
		case <-ctx.Done():
			return c.drainAndWait(collErr)

		case err := <-collErr:
			// Collector завершился сам: stall (ErrPipelineStalled, фатально) или
			// commit-fail. Эскалируем — main рестартует процесс. Graceful shutdown
			// сюда шлёт nil.
			if err != nil {
				c.log.Error("pipeline collector stopped — process will restart", "err", err)
			}
			return err

		case err := <-fetchErrCh:
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return c.drainAndWait(collErr)
			}
			return err

		case m := <-msgCh:
			if stop, ret := c.classify(ctx, c.onMessage(ctx, &m), collErr); stop {
				return ret
			}

		case <-tick.C:
			if c.batcher.ShouldFlush() {
				if stop, ret := c.classify(ctx, c.drainSendSubmit(ctx), collErr); stop {
					return ret
				}
			}
		}
	}
}

// classify решает судьбу ошибки шага цикла:
//   - ErrPipelineStalled → вернуть фатальную причину collector'а (→ рестарт)
//   - ctx отменён → graceful shutdown
//   - иначе transient → залогировать и продолжить
func (c *Consumer) classify(ctx context.Context, err error, collErr <-chan error) (stop bool, ret error) {
	if err == nil {
		return false, nil
	}
	if errors.Is(err, ErrPipelineStalled) {
		return true, c.collectorCause(collErr)
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return true, c.drainAndWait(collErr)
	}
	c.log.Error("consumer step failed — will retry", "err", err)
	return false, nil
}

// onMessage парсит сообщение, кладёт в batcher и flush'ит при ShouldFlush.
func (c *Consumer) onMessage(ctx context.Context, m *kafka.Message) error {
	parsed, err := Parse(m)
	if err != nil {
		return c.onParseFailure(ctx, m, err)
	}
	c.batcher.Add(parsed)
	if c.batcher.ShouldFlush() {
		return c.drainSendSubmit(ctx)
	}
	return nil
}

// onParseFailure публикует битое сообщение в DLQ и коммитит его offset В FIFO-
// ПОРЯДКЕ через pipeline (zero-tx batch). Сперва дренирует накопленное, чтобы
// offset битого не перескочил более ранние ещё-не-сабмиченные сообщения
// (kafka-go коммитит max-offset → перескок = потеря).
func (c *Consumer) onParseFailure(ctx context.Context, m *kafka.Message, parseErr error) error {
	if err := c.drainSendSubmit(ctx); err != nil {
		return err // stall/ctx — наверх; transient уже обработан внутри (nil)
	}
	if err := c.handleParseFailure(ctx, m, parseErr); err != nil {
		// DLQ недоступен — offset НЕ коммитим, сообщение re-fetch'нется.
		c.log.Error("dlq publish parse-failed msg — NOT committing offset", "err", err)
		return nil
	}
	return c.pipeline.Submit(ctx, drainedBatch{kafkaMsgs: []kafka.Message{*m}})
}

// drainSendSubmit дренирует batcher, broadcast'ит через Send и сабмитит в
// pipeline. Send-transient → unshift + retry на следующем tick (не фатал).
// Пустой inflight всё равно сабмитится (offsets коммитятся в FIFO-порядке).
func (c *Consumer) drainSendSubmit(ctx context.Context) error {
	msgs := c.batcher.Drain()
	if len(msgs) == 0 {
		return nil
	}
	inflight, err := c.flusher.Send(ctx, msgs)
	if err != nil {
		c.log.Warn("send failed — unshifting for retry", "err", err, "n", len(msgs))
		c.batcher.Unshift(msgs)
		return nil
	}
	return c.pipeline.Submit(ctx, drainedBatch{txs: inflight, kafkaMsgs: toKafkaMsgs(msgs)})
}

// collectorCause достаёт реальную ошибку завершившегося collector'а (причину
// stall'а) для эскалации; фолбэк — ErrPipelineStalled.
func (c *Consumer) collectorCause(collErr <-chan error) error {
	select {
	case err := <-collErr:
		if err != nil {
			return err
		}
	case <-time.After(2 * time.Second):
	}
	return ErrPipelineStalled
}

// drainAndWait — graceful shutdown: закрыть pipeline и дождаться выхода
// collector'а. Накопленное в batcher и in-flight окно НЕ коммитятся (их offsets
// так и не закоммичены) → redeliver на рестарте; SENT-строки reconcile'ятся.
func (c *Consumer) drainAndWait(collErr <-chan error) error {
	c.pipeline.Close()
	select {
	case <-collErr:
	case <-time.After(7 * time.Second):
		c.log.Warn("collector did not exit in time on shutdown")
	}
	return nil
}

// handleParseFailure публикует невалидное kafka-сообщение в DLQ. Ошибка DLQ
// publisher'а → возвращаем её, вызывающий НЕ коммитит offset.
func (c *Consumer) handleParseFailure(ctx context.Context, m *kafka.Message, parseErr error) error {
	c.log.Warn("parse failed — sending to DLQ", "err", parseErr, "offset", m.Offset, "topic", c.topic)
	stub := &Message{Topic: c.topic, KafkaMsg: *m}
	return c.dlq.PublishAll(ctx, []*Message{stub}, "parse failed: "+parseErr.Error())
}

func (c *Consumer) fetchLoop(ctx context.Context, msgCh chan<- kafka.Message, errCh chan<- error) {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
			return
		}
		select {
		case msgCh <- m:
		case <-ctx.Done():
			return
		}
	}
}

func toKafkaMsgs(msgs []*Message) []kafka.Message {
	out := make([]kafka.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m.KafkaMsg
	}
	return out
}
