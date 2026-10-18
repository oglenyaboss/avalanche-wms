package kms

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"ledger-adapter/internal/chain"
)

// fakeWeb3Signer эмулирует eth1 API Web3Signer поверх локального ecdsa-ключа:
// POST /api/v1/eth1/sign/{identifier} с applyHash=false → text/plain "0x<r||s||v>".
// v отдаётся в web3signer-конвенции 27/28, чтобы клиент был вынужден
// восстанавливать parity recover'ом, а не верить байту.
type fakeWeb3Signer struct {
	mu  sync.Mutex
	key *ecdsa.PrivateKey
	// knobs
	status    int   // если != 0 — всегда этот статус
	highS     bool  // отдавать s > N/2 (проверка low-s нормализации)
	badBody   bool  // отдавать мусор вместо hex
	applyHash *bool // если задано — валидировать applyHash в запросе
	calls     int
}

func (f *fakeWeb3Signer) identifier() string {
	pub := f.key.Public().(*ecdsa.PublicKey)
	return "0x" + hex.EncodeToString(crypto.FromECDSAPub(pub)[1:])
}

func (f *fakeWeb3Signer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if !strings.HasPrefix(r.URL.Path, "/api/v1/eth1/sign/") {
		http.NotFound(w, r)
		return
	}
	ident := strings.TrimPrefix(r.URL.Path, "/api/v1/eth1/sign/")
	if ident != strings.ToLower(f.identifier()) {
		http.Error(w, "Public Key not found", http.StatusNotFound)
		return
	}
	var req signRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if f.applyHash != nil && req.ApplyHash != *f.applyHash {
		http.Error(w, "unexpected applyHash", http.StatusBadRequest)
		return
	}
	if f.status != 0 {
		http.Error(w, "boom", f.status)
		return
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(req.Data, "0x"))
	if err != nil || len(digest) != 32 {
		http.Error(w, "data must be 32-byte digest", http.StatusBadRequest)
		return
	}
	sig, err := crypto.Sign(digest, f.key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if f.highS {
		high := new(big.Int).Sub(secp256k1N, new(big.Int).SetBytes(sig[32:64]))
		high.FillBytes(sig[32:64])
	}
	if f.badBody {
		sig = append([]byte("garbage"), make([]byte, 65)...)[:65]
	}
	// web3signer-конвенция: v ∈ {27,28}
	sig[64] += 27
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "0x%s", hex.EncodeToString(sig))
}

func newSignerTx() *types.Transaction {
	return types.NewTransaction(7, common.HexToAddress("0x1234"), big.NewInt(0), 30000, big.NewInt(25e9), []byte{0xde, 0xad})
}

func TestSignTx_SenderMatchesKMSAddress(t *testing.T) {
	// Обе parity recovery id должны покрыться на случайных ключах: клиент
	// обязан корректно строить v для 0 и для 1.
	seen := map[int64]bool{}
	for i := 0; i < 30; i++ {
		fake := &fakeWeb3Signer{key: mustKey(t)}
		srv := httptest.NewServer(fake)
		s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
		if err != nil {
			t.Fatalf("NewSigner: %v", err)
		}
		signed, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(1337))
		if err != nil {
			t.Fatalf("iter %d SignTx: %v", i, err)
		}
		sender, err := types.Sender(types.NewEIP155Signer(big.NewInt(1337)), signed)
		if err != nil {
			t.Fatalf("iter %d Sender: %v", i, err)
		}
		if sender != s.Address() {
			t.Fatalf("iter %d: sender %s != kms address %s", i, sender, s.Address())
		}
		v, _, _ := signed.RawSignatureValues()
		// v = 35 + 2*chainID + parity → parity = v - 35 - 2*1337
		p := new(big.Int).Sub(new(big.Int).Sub(v, big.NewInt(35)), big.NewInt(2*1337))
		seen[p.Int64()] = true
		srv.Close()
	}
	if !seen[0] || !seen[1] {
		t.Fatalf("expected both recovery parities covered, got %v", seen)
	}
}

func TestSignTx_HighSNormalized(t *testing.T) {
	fake := &fakeWeb3Signer{key: mustKey(t), highS: true}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	signed, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(99999))
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	_, _, sVal := signed.RawSignatureValues()
	if sVal.Cmp(halfN) > 0 {
		t.Fatalf("s not normalized to low-s: %s", sVal)
	}
	sender, err := types.Sender(types.NewEIP155Signer(big.NewInt(99999)), signed)
	if err != nil || sender != s.Address() {
		t.Fatalf("sender recovery after high-s flip: %v %s != %s", err, sender, s.Address())
	}
}

func TestSignTx_TransientErrors(t *testing.T) {
	// 5xx и обрыв соединения — transient: клиент обязан ретраить, не DLQ'ить.
	for _, status := range []int{500, 503, 429} {
		fake := &fakeWeb3Signer{key: mustKey(t), status: status}
		srv := httptest.NewServer(fake)
		s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
		if err != nil {
			t.Fatalf("NewSigner: %v", err)
		}
		if _, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(1)); !errors.Is(err, chain.ErrSignerTransient) {
			t.Fatalf("status %d: want ErrSignerTransient, got %v", status, err)
		}
		srv.Close()
	}

	// сеть недоступна — тоже transient
	fake := &fakeWeb3Signer{key: mustKey(t)}
	srv := httptest.NewServer(fake)
	s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	srv.Close()
	if _, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(1)); !errors.Is(err, chain.ErrSignerTransient) {
		t.Fatalf("connection refused: want ErrSignerTransient, got %v", err)
	}
}

func TestSignTx_TerminalErrors(t *testing.T) {
	// 404 (ключ не загружен) и мусорное тело — конфигурационные/протокольные
	// ошибки: ретраи бессмысленны, ErrSignerTransient быть не должно.
	fake := &fakeWeb3Signer{key: mustKey(t)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	other := &fakeWeb3Signer{key: mustKey(t)}
	unknown, err := NewSigner(srv.URL, other.identifier(), srv.Client())
	if err != nil {
		t.Fatalf("NewSigner unknown: %v", err)
	}
	if _, err := unknown.SignTx(context.Background(), newSignerTx(), big.NewInt(1)); errors.Is(err, chain.ErrSignerTransient) {
		t.Fatalf("404 must be terminal, got %v", err)
	}

	fake.mu.Lock()
	fake.badBody = true
	fake.mu.Unlock()
	if _, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(1)); errors.Is(err, chain.ErrSignerTransient) {
		t.Fatalf("bad body must be terminal, got %v", err)
	}
}

func TestNewSigner_IdentifierValidation(t *testing.T) {
	if _, err := NewSigner("http://x", "0xzz", nil); err == nil {
		t.Fatal("non-hex identifier accepted")
	}
	if _, err := NewSigner("http://x", "0x"+strings.Repeat("ab", 63), nil); err == nil {
		t.Fatal("63-byte identifier accepted")
	}
	// Валидный X||Y, но не точка на кривой — тоже отказ.
	if _, err := NewSigner("http://x", "0x"+strings.Repeat("ff", 64), nil); err == nil {
		t.Fatal("non-curve point accepted")
	}
}

func TestSigner_AddressMatchesKey(t *testing.T) {
	key := mustKey(t)
	fake := &fakeWeb3Signer{key: key}
	want := crypto.PubkeyToAddress(*key.Public().(*ecdsa.PublicKey))
	s, err := NewSigner("http://x", fake.identifier(), nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if s.Address() != want {
		t.Fatalf("address %s != %s", s.Address(), want)
	}
}

func TestSignTx_ApplyHashFalseSent(t *testing.T) {
	f := false
	fake := &fakeWeb3Signer{key: mustKey(t), applyHash: &f}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, err := NewSigner(srv.URL, fake.identifier(), srv.Client())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if _, err := s.SignTx(context.Background(), newSignerTx(), big.NewInt(1)); err != nil {
		t.Fatalf("SignTx with applyHash=false validation: %v", err)
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return k
}
