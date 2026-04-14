package consumer

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func makeMsg() *Message {
	return &Message{EventID: uuid.New(), ProductID: uuid.New(), AggregateType: "receiving", Topic: "wms.receiving.v1"}
}

func TestBatcher_EmptyInitialState(t *testing.T) {
	b := NewBatcher(10, time.Second)
	if b.ShouldFlush() {
		t.Error("empty batcher should not flush")
	}
	if got := b.Len(); got != 0 {
		t.Errorf("Len: got %d, want 0", got)
	}
	if got := b.Drain(); len(got) != 0 {
		t.Errorf("Drain empty: got %d msgs", len(got))
	}
}

func TestBatcher_FlushBySize(t *testing.T) {
	b := NewBatcher(3, 10*time.Second)
	for i := 0; i < 2; i++ {
		b.Add(makeMsg())
	}
	if b.ShouldFlush() {
		t.Error("2/3 should not flush")
	}
	b.Add(makeMsg())
	if !b.ShouldFlush() {
		t.Error("3/3 should flush by size")
	}
	msgs := b.Drain()
	if len(msgs) != 3 {
		t.Errorf("drain: got %d, want 3", len(msgs))
	}
	if b.ShouldFlush() {
		t.Error("drained batcher should not flush")
	}
}

func TestBatcher_FlushByTimeout(t *testing.T) {
	b := NewBatcher(100, 50*time.Millisecond)
	b.Add(makeMsg())
	if b.ShouldFlush() {
		t.Error("should not flush immediately")
	}
	time.Sleep(70 * time.Millisecond)
	if !b.ShouldFlush() {
		t.Error("should flush after timeout")
	}
}

func TestBatcher_DrainResetsTimer(t *testing.T) {
	b := NewBatcher(100, 50*time.Millisecond)
	b.Add(makeMsg())
	time.Sleep(70 * time.Millisecond)
	b.Drain() // очистили — firstAt сбросился

	b.Add(makeMsg())
	if b.ShouldFlush() {
		t.Error("after drain + new add, timer should reset")
	}
}

func TestBatcher_ConcurrentAdd(t *testing.T) {
	b := NewBatcher(100, time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Add(makeMsg())
		}()
	}
	wg.Wait()
	if got := b.Len(); got != 50 {
		t.Errorf("concurrent Add: got %d, want 50", got)
	}
}

func TestBatcher_Unshift_PreservesOrder_ResetsTimer(t *testing.T) {
	// Scenario: 2 retry-msgs unshift + 1 new Add → должны быть [retry1, retry2, new].
	// И timer сбрасывается так, что сразу ShouldFlush = false.
	b := NewBatcher(100, 50*time.Millisecond)

	retry := []*Message{makeMsg(), makeMsg()}
	newMsg := makeMsg()
	b.Add(newMsg)

	// Засыпаем достаточно чтобы timeout bы сработал ДО unshift'а
	time.Sleep(70 * time.Millisecond)
	if !b.ShouldFlush() {
		t.Fatal("pre-unshift: should flush after timeout")
	}

	b.Unshift(retry)

	// Unshift сбросил firstAt → timeout переотсчитывается
	if b.ShouldFlush() {
		t.Error("post-unshift: timer should reset, no immediate flush")
	}

	got := b.Drain()
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	if got[0] != retry[0] || got[1] != retry[1] || got[2] != newMsg {
		t.Errorf("order wrong: expected [retry1, retry2, new], got positions %v/%v/%v",
			got[0] == retry[0], got[1] == retry[1], got[2] == newMsg)
	}
}

func TestBatcher_Unshift_EmptyNoOp(t *testing.T) {
	b := NewBatcher(10, time.Second)
	b.Add(makeMsg())
	before := b.Len()
	b.Unshift(nil)
	b.Unshift([]*Message{})
	if got := b.Len(); got != before {
		t.Errorf("empty Unshift should not change Len, got %d → %d", before, got)
	}
}
