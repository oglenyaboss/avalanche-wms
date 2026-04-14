package consumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

// Consumer читает один topic, аккумулирует в Batcher, flush'ит через Flusher.
// Один Consumer на topic — параллелизм обеспечивает main.go запуском N goroutine.
//
// Цикл: fetch → Parse → Batcher.Add → (если ShouldFlush) flush + commit offsets.
// Плюс ticker для timeout-based flush'а.
type Consumer struct {
	reader  *kafka.Reader
	batcher *Batcher
	flusher *Flusher
	dlq     DLQPublisher
	topic   string
	log     *slog.Logger
	tickInt time.Duration
}

// NewConsumer собирает kafka.Reader + Batcher. dlq используется для parse-fail
// сообщений — они не попадают в flusher'ский pipeline (нельзя распарсить
// event_id), но должны быть видимы downstream'у для анализа/репроцесса.
func NewConsumer(
	brokers []string,
	topic, groupID string,
	flusher *Flusher,
	dlq DLQPublisher,
	batchSize int,
	batchTimeout time.Duration,
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
		reader:  r,
		batcher: NewBatcher(batchSize, batchTimeout),
		flusher: flusher,
		dlq:     dlq,
		topic:   topic,
		log:     log,
		tickInt: 50 * time.Millisecond,
	}
}

// Run блокирует пока ctx не отменится или reader не вернёт фатальную ошибку.
// На shutdown делает финальный flush (с отдельным shortCtx).
func (c *Consumer) Run(ctx context.Context) error {
	tick := time.NewTicker(c.tickInt)
	defer tick.Stop()
	defer func() { _ = c.reader.Close() }()

	msgCh := make(chan kafka.Message)
	errCh := make(chan error, 1)

	go c.fetchLoop(ctx, msgCh, errCh)

	for {
		select {
		case <-ctx.Done():
			return c.finalFlush()

		case err := <-errCh:
			_ = c.finalFlush()
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err

		case m := <-msgCh:
			parsed, err := Parse(&m)
			if err != nil {
				if dlqErr := c.handleParseFailure(ctx, &m, err); dlqErr != nil {
					// DLQ fail — offset НЕ коммитим, сообщение re-fetch'нется.
					c.log.Error("dlq publish parse-failed msg — NOT committing offset", "err", dlqErr)
					continue
				}
				if cerr := c.reader.CommitMessages(ctx, m); cerr != nil {
					c.log.Error("commit offsets after parse fail", "err", cerr)
				}
				continue
			}
			c.batcher.Add(parsed)
			if c.batcher.ShouldFlush() {
				if err := c.flush(ctx); err != nil {
					c.log.Error("flush failed", "err", err)
				}
			}

		case <-tick.C:
			if c.batcher.ShouldFlush() {
				if err := c.flush(ctx); err != nil {
					c.log.Error("flush failed", "err", err)
				}
			}
		}
	}
}

// handleParseFailure публикует невалидное kafka-сообщение в DLQ. Если DLQ
// publisher вернул ошибку — возвращаем её, вызывающий НЕ коммитит offset
// (сообщение re-fetch'нется на следующем poll'е).
func (c *Consumer) handleParseFailure(ctx context.Context, m *kafka.Message, parseErr error) error {
	c.log.Warn("parse failed — sending to DLQ", "err", parseErr, "offset", m.Offset, "topic", c.topic)
	// Parse-fail даёт zero UUIDs; downstream'у достаточно original payload +
	// headers + reason header'а от DLQ producer'а.
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

// flush дренирует batcher, отдаёт в flusher, коммитит kafka offsets. Offsets
// коммитятся ТОЛЬКО после успешного flush. Если flush вернул error (transient):
// сообщения unshift'аются обратно в batcher → будут retried при следующем
// ShouldFlush. Без этого Drain выкидывал их навсегда до рестарта процесса.
func (c *Consumer) flush(ctx context.Context) error {
	msgs := c.batcher.Drain()
	if len(msgs) == 0 {
		return nil
	}
	if err := c.flusher.Flush(ctx, c.topic, msgs); err != nil {
		c.log.Warn("flush failed — unshifting messages back for retry", "err", err, "n", len(msgs))
		c.batcher.Unshift(msgs)
		return err
	}
	kmsgs := make([]kafka.Message, len(msgs))
	for i, m := range msgs {
		kmsgs[i] = m.KafkaMsg
	}
	if err := c.reader.CommitMessages(ctx, kmsgs...); err != nil {
		return err
	}
	return nil
}

// finalFlush — при shutdown используем background ctx с таймаутом чтобы успеть
// слить batch даже если основной ctx уже отменён.
func (c *Consumer) finalFlush() error {
	msgs := c.batcher.Drain()
	if len(msgs) == 0 {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.flusher.Flush(shutdownCtx, c.topic, msgs); err != nil {
		c.log.Error("final flush failed", "err", err)
		return err
	}
	kmsgs := make([]kafka.Message, len(msgs))
	for i, m := range msgs {
		kmsgs[i] = m.KafkaMsg
	}
	return c.reader.CommitMessages(shutdownCtx, kmsgs...)
}
