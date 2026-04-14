package consumer

import (
	"sync"
	"time"
)

// Batcher аккумулирует *Message до тех пор пока не наберётся maxSize или
// не пройдёт timeout с момента первого добавления. Потокобезопасен.
//
// Возвращает true из ShouldFlush — вызывающий должен Drain() и обработать.
// Drain очищает внутреннее состояние (включая firstAt).
type Batcher struct {
	mu      sync.Mutex
	maxSize int
	timeout time.Duration
	msgs    []*Message
	firstAt time.Time
}

// NewBatcher создаёт batcher с заданными max-size и timeout'ом.
func NewBatcher(maxSize int, timeout time.Duration) *Batcher {
	return &Batcher{maxSize: maxSize, timeout: timeout}
}

// Add добавляет сообщение. Если batcher пуст — запоминает время для timeout'а.
func (b *Batcher) Add(m *Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.msgs) == 0 {
		b.firstAt = time.Now()
	}
	b.msgs = append(b.msgs, m)
}

// ShouldFlush возвращает true если batch набрал maxSize ИЛИ прошло >= timeout
// с момента первого Add. Пустой batcher не flush'ится.
func (b *Batcher) ShouldFlush() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.msgs) == 0 {
		return false
	}
	if len(b.msgs) >= b.maxSize {
		return true
	}
	return time.Since(b.firstAt) >= b.timeout
}

// Drain возвращает накопленные сообщения и сбрасывает состояние. Вызывающий
// становится владельцем возвращённого slice.
func (b *Batcher) Drain() []*Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.msgs
	b.msgs = nil
	b.firstAt = time.Time{}
	return out
}

// Len — количество накопленных сообщений. Для тестов/логов.
func (b *Batcher) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.msgs)
}
