package chain

import (
	"errors"
	"testing"
	"time"
)

// fakeRPCError реализует rpc.Error — тип который go-ethereum возвращает для
// revert ошибок (-32000 execution reverted).
type fakeRPCError struct {
	msg  string
	code int
}

func (e *fakeRPCError) Error() string  { return e.msg }
func (e *fakeRPCError) ErrorCode() int { return e.code }

func TestIsRevertError_RPCErrorInterface(t *testing.T) {
	// Любой err реализующий rpc.Error считаем revert (terminal).
	err := &fakeRPCError{msg: "execution reverted: Duplicate eventId", code: -32000}
	if !isRevertError(err) {
		t.Error("rpc.Error interface should be classified as revert")
	}
}

func TestIsRevertError_WrappedRPCError(t *testing.T) {
	// errors.As должен пройти через fmt.Errorf wrap.
	inner := &fakeRPCError{msg: "execution reverted", code: -32000}
	wrapped := errors.New("outer: " + inner.Error())
	// Не сработает через errors.As (ничего не оборачивали), fallback substring
	if !isRevertError(wrapped) {
		t.Error("substring fallback should catch 'execution reverted'")
	}
}

func TestIsRevertError_TransientNetworkError(t *testing.T) {
	// Простая не-rpc ошибка без "revert" текста — transient.
	err := errors.New("connection refused")
	if isRevertError(err) {
		t.Error("'connection refused' should NOT be classified as revert")
	}
}

func TestIsRevertError_SubstringRevert(t *testing.T) {
	// Фолбэк на substring для нестандартных backend'ов.
	err := errors.New("transaction would revert: bad input")
	if !isRevertError(err) {
		t.Error("'revert' substring should be caught")
	}
}

func TestIsRevertError_UnrelatedError(t *testing.T) {
	err := errors.New("context deadline exceeded")
	if isRevertError(err) {
		t.Error("'context deadline exceeded' should be transient")
	}
}

func TestErrChainSentinels_DistinctFromEachOther(t *testing.T) {
	// Убедимся что ErrChainRevert и ErrChainTransient — разные errors.Is буквы.
	if errors.Is(ErrChainRevert, ErrChainTransient) {
		t.Error("ErrChainRevert should not satisfy Is(ErrChainTransient)")
	}
	if errors.Is(ErrChainTransient, ErrChainRevert) {
		t.Error("ErrChainTransient should not satisfy Is(ErrChainRevert)")
	}
}

func TestClient_SendMu_SerializesConcurrentCalls(t *testing.T) {
	// Смоук-тест для C1 фикса (MR !35): мьютекс sendMu в *Client должен
	// реально блокировать параллельный доступ. Прямо тестировать sendMethod
	// без мокания concrete *ethclient.Client нельзя, поэтому проверяем сам
	// мьютекс через прямое взаимодействие с полем (in-package test).
	// Race-detector в остальных тестах ловит реальные гонки; этот тест —
	// гарантия что поле осталось в struct и что Lock()/Unlock() работают.
	var c Client
	locked := make(chan struct{})
	released := make(chan struct{})

	go func() {
		c.sendMu.Lock()
		close(locked)
		time.Sleep(50 * time.Millisecond)
		c.sendMu.Unlock()
		close(released)
	}()

	<-locked
	// Try to acquire from main goroutine — should block until released.
	start := time.Now()
	c.sendMu.Lock()
	elapsed := time.Since(start)
	c.sendMu.Unlock()
	<-released

	if elapsed < 40*time.Millisecond {
		t.Errorf("sendMu should have blocked ~50ms; blocked for %s", elapsed)
	}
}
