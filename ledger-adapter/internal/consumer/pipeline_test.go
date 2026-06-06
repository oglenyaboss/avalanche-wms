package consumer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/segmentio/kafka-go"
)

// --- fakes ---------------------------------------------------------------

type fakeConfirmer struct {
	mu        sync.Mutex
	confirmed []common.Hash
	fn        func(tx *InflightTx) error // hook: блокировать/падать на конкретной tx
}

func (c *fakeConfirmer) Confirm(_ context.Context, tx *InflightTx) error {
	if c.fn != nil {
		if err := c.fn(tx); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.confirmed = append(c.confirmed, tx.TxHash)
	c.mu.Unlock()
	return nil
}

type fakeCommitter struct {
	mu      sync.Mutex
	commits [][]kafka.Message // по одному элементу на вызов CommitMessages
}

func (c *fakeCommitter) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]kafka.Message, len(msgs))
	copy(cp, msgs)
	c.commits = append(c.commits, cp)
	return nil
}

func (c *fakeCommitter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.commits)
}

func (c *fakeCommitter) at(i int) []kafka.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commits[i]
}

// --- helpers -------------------------------------------------------------

func tx(hash string) InflightTx {
	return InflightTx{Aggregate: hash, TxHash: common.HexToHash(hash)}
}

func batch(txs []InflightTx, offsets ...int64) drainedBatch {
	kmsgs := make([]kafka.Message, len(offsets))
	for i, o := range offsets {
		kmsgs[i] = kafka.Message{Offset: o}
	}
	return drainedBatch{txs: txs, kafkaMsgs: kmsgs}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// --- tests ---------------------------------------------------------------

func TestPipeline_HappyPathCommitsAllInOrder(t *testing.T) {
	conf := &fakeConfirmer{}
	com := &fakeCommitter{}
	p := NewPipeline(conf, com, 3, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- p.Run(ctx) }()

	if err := p.Submit(ctx, batch([]InflightTx{tx("0x01")}, 1, 2)); err != nil {
		t.Fatalf("submit A: %v", err)
	}
	if err := p.Submit(ctx, batch([]InflightTx{tx("0x02")}, 3, 4)); err != nil {
		t.Fatalf("submit B: %v", err)
	}
	waitFor(t, "both batches committed", func() bool { return com.count() == 2 })

	// FIFO: A (offsets 1,2) committed before B (3,4).
	if got := com.at(0)[0].Offset; got != 1 {
		t.Errorf("first commit should be batch A (offset 1), got %d", got)
	}
	if got := com.at(1)[0].Offset; got != 3 {
		t.Errorf("second commit should be batch B (offset 3), got %d", got)
	}

	p.Close()
	if err := <-runErr; err != nil {
		t.Errorf("Run should exit nil on Close, got %v", err)
	}
}

// TestPipeline_OffsetCommittedOnlyAfterAllSubBatchesConfirm — Hole 2: один
// drained batch с 2 sub-batch'ами; пока второй (putaway) не подтвердился, offsets
// НЕ коммитятся; после — один коммит со ВСЕМ набором.
func TestPipeline_OffsetCommittedOnlyAfterAllSubBatchesConfirm(t *testing.T) {
	release := make(chan struct{})
	conf := &fakeConfirmer{fn: func(tr *InflightTx) error {
		if tr.TxHash == common.HexToHash("0x02") { // putaway sub-batch
			<-release
		}
		return nil
	}}
	com := &fakeCommitter{}
	p := NewPipeline(conf, com, 3, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	// receiving=0x01 (offsets 1,3,5), putaway=0x02 (offsets 2,4,6) — один batch.
	db := batch([]InflightTx{tx("0x01"), tx("0x02")}, 1, 3, 5, 2, 4, 6)
	if err := p.Submit(ctx, db); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// receiving подтвердится, putaway блокируется → commit НЕ должен случиться.
	waitFor(t, "receiving confirmed", func() bool {
		conf.mu.Lock()
		defer conf.mu.Unlock()
		return len(conf.confirmed) == 1
	})
	time.Sleep(50 * time.Millisecond)
	if com.count() != 0 {
		t.Fatalf("offsets must NOT commit while putaway pending, got %d commits", com.count())
	}

	close(release)
	waitFor(t, "batch committed", func() bool { return com.count() == 1 })
	if n := len(com.at(0)); n != 6 {
		t.Errorf("commit must cover ALL 6 msgs of the drained batch, got %d", n)
	}
}

// TestPipeline_WindowBoundsInflight — W=2 (cap=1): при заблокированном collector'е
// 3-й Submit блокируется до освобождения слота.
func TestPipeline_WindowBoundsInflight(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	gate := make(chan struct{})
	conf := &fakeConfirmer{fn: func(_ *InflightTx) error {
		once.Do(func() { close(gate) }) // сигналим что collector взял первый batch
		<-release                       // держим первый batch в confirm
		return nil
	}}
	com := &fakeCommitter{}
	p := NewPipeline(conf, com, 2, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	if err := p.Submit(ctx, batch([]InflightTx{tx("0x01")}, 1)); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	<-gate // collector держит batch 1 в confirm
	if err := p.Submit(ctx, batch([]InflightTx{tx("0x02")}, 2)); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// 3-й Submit должен заблокироваться (окно W=2 заполнено).
	done := make(chan struct{})
	go func() {
		_ = p.Submit(ctx, batch([]InflightTx{tx("0x03")}, 3))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("3rd Submit must block while window is full")
	case <-time.After(150 * time.Millisecond):
		// ожидаемо: заблокирован
	}

	close(release) // освобождаем collector → слот освобождается
	waitFor(t, "3rd submit unblocks", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

// TestPipeline_StalledTxAbandonsWindowNoCommit — Task 5 (restart-модель):
// stall на in-flight tx → Run возвращает ErrPipelineStalled, НИ ОДИН offset не
// закоммичен, done закрыт (последующий Submit не виснет). Без коммита окно
// redeliver'нется на рестарте (invariant 4, safety — НЕ проверяем что stalled
// события дойдут до COMMITTED).
func TestPipeline_StalledTxAbandonsWindowNoCommit(t *testing.T) {
	conf := &fakeConfirmer{fn: func(tr *InflightTx) error {
		if tr.TxHash == common.HexToHash("0x01") {
			return ErrConfirmStalled // первый же batch стопорится
		}
		return nil
	}}
	com := &fakeCommitter{}
	p := NewPipeline(conf, com, 3, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- p.Run(ctx) }()

	// Первый batch стопорится; ещё пара в окне.
	_ = p.Submit(ctx, batch([]InflightTx{tx("0x01")}, 1))

	var err error
	select {
	case err = <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run should return on stall")
	}
	if !isStall(err) {
		t.Fatalf("expected ErrPipelineStalled, got %v", err)
	}
	if com.count() != 0 {
		t.Errorf("stall must commit ZERO offsets, got %d", com.count())
	}
	// done закрыт → Submit больше не виснет, возвращает ErrPipelineStalled.
	if serr := p.Submit(context.Background(), batch([]InflightTx{tx("0x09")}, 9)); !isStallSubmit(serr) {
		t.Errorf("Submit after collector death should return ErrPipelineStalled, got %v", serr)
	}
}

func isStall(err error) bool       { return errors.Is(err, ErrPipelineStalled) }
func isStallSubmit(err error) bool { return errors.Is(err, ErrPipelineStalled) }
