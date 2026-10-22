package passkeys

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"wms/internal/domain"
	"wms/internal/signedaction"
)

const (
	vectorX     = "7f5426c29688122ff64414e79d553f18e05ebadb5e2b782e426c15917bedb42e"
	vectorY     = "1b7e077c58d025360a28537cf4c272f3b35c448dbd57ec3281524bd517558ac2"
	vectorKeyID = "59c89f8ece38815f18358db501a0d83e949694c2bf2e183fb0de9d6344f30b8b"
)

func mustHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

func coseKey(t *testing.T, alg, curve int64, x, y []byte) []byte {
	t.Helper()
	b, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: alg},
		Curve:         curve,
		XCoord:        x,
		YCoord:        y,
	})
	if err != nil {
		t.Fatalf("marshal cose key: %v", err)
	}
	return b
}

func TestKeyIDMatchesVector(t *testing.T) {
	got := signedaction.KeyID(domain.P256Point{X: mustHex32(t, vectorX), Y: mustHex32(t, vectorY)})
	if hex.EncodeToString(got[:]) != vectorKeyID {
		t.Fatalf("keyID = %x, want %s", got, vectorKeyID)
	}
}

func TestP256Coordinates(t *testing.T) {
	x := mustHex32(t, vectorX)
	y := mustHex32(t, vectorY)

	got, err := p256Coordinates(coseKey(t, int64(webauthncose.AlgES256), 1, x[:], y[:]))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != x || got.Y != y {
		t.Fatalf("coordinates mismatch: x=%x y=%x", got.X, got.Y)
	}

	shortX := []byte{0x01, 0x02, 0x03}
	got, err = p256Coordinates(coseKey(t, int64(webauthncose.AlgES256), 1, shortX, y[:]))
	if err != nil {
		t.Fatalf("unexpected error for short coordinate: %v", err)
	}
	if !bytes.Equal(got.X[:29], make([]byte, 29)) || !bytes.Equal(got.X[29:], shortX) {
		t.Fatalf("short coordinate not right-aligned: %x", got.X)
	}
}

func TestP256CoordinatesRejectsOtherCurvesAndAlgorithms(t *testing.T) {
	x := mustHex32(t, vectorX)
	y := mustHex32(t, vectorY)

	cases := map[string][]byte{
		"ES384 algorithm": coseKey(t, int64(webauthncose.AlgES384), 1, x[:], y[:]),
		"curve P-384":     coseKey(t, int64(webauthncose.AlgES256), 2, x[:], y[:]),
	}
	for name, key := range cases {
		if _, err := p256Coordinates(key); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
