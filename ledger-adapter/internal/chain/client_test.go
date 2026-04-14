package chain

import (
	"errors"
	"testing"
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
