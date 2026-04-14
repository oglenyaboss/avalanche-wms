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
