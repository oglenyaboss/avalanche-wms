package signedaction_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"

	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
)

func signDigest(t *testing.T, k *ecdsa.PrivateKey, authData, cdj []byte) []byte {
	t.Helper()
	clientHash := sha256.Sum256(cdj)
	h := sha256.New()
	h.Write(authData)
	h.Write(clientHash[:])
	sig, err := ecdsa.SignASN1(rand.Reader, k, h.Sum(nil))
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}
	return sig
}

func makeAuthData(rpID string, flags byte) []byte {
	rpHash := sha256.Sum256([]byte(rpID))
	ad := append(rpHash[:], flags)
	return binary.BigEndian.AppendUint32(ad, 1)
}

func fill32(v *big.Int) [32]byte {
	var out [32]byte
	v.FillBytes(out[:])
	return out
}

func TestVerify_AcceptsValidAssertion(t *testing.T) {
	key := signedactiontest.NewKey()
	challenge := [32]byte{1, 2, 3}
	pub := signedactiontest.Coordinates(key)

	got, err := signedaction.Verify(signedactiontest.Sign(key, challenge), challenge, pub, signedactiontest.RPID, []string{signedactiontest.Origin})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.ChallengeIndex < 0 || got.TypeIndex < 0 {
		t.Errorf("indexes = (%d, %d), want non-negative", got.ChallengeIndex, got.TypeIndex)
	}
	if got.ClientDataJSON != string(signedactiontest.Sign(key, challenge).ClientDataJSON) {
		t.Errorf("ClientDataJSON not echoed back")
	}
	if (got.R == [32]byte{}) || (got.S == [32]byte{}) {
		t.Errorf("r/s empty")
	}
}

func TestVerify_Rejects(t *testing.T) {
	key := signedactiontest.NewKey()
	other := signedactiontest.NewKey()
	challenge := [32]byte{9, 9, 9}
	valid := signedactiontest.Sign(key, challenge)
	pub := signedactiontest.Coordinates(key)

	wrongType := signedaction.Assertion{
		AuthenticatorData: valid.AuthenticatorData,
		ClientDataJSON:    []byte(`{"type":"webauthn.create","challenge":"x","origin":"` + signedactiontest.Origin + `","crossOrigin":false}`),
		Signature:         valid.Signature,
	}
	wrongChallenge := signedaction.Assertion{
		AuthenticatorData: valid.AuthenticatorData,
		ClientDataJSON:    []byte(`{"type":"webauthn.get","challenge":"wrong","origin":"` + signedactiontest.Origin + `","crossOrigin":false}`),
		Signature:         valid.Signature,
	}
	otherRPID := signedaction.Assertion{
		AuthenticatorData: makeAuthData("evil.example", 0x05),
		ClientDataJSON:    valid.ClientDataJSON,
		Signature:         signDigest(t, key, makeAuthData("evil.example", 0x05), valid.ClientDataJSON),
	}
	noUV := signedaction.Assertion{
		AuthenticatorData: makeAuthData(signedactiontest.RPID, 0x01),
		ClientDataJSON:    valid.ClientDataJSON,
		Signature:         signDigest(t, key, makeAuthData(signedactiontest.RPID, 0x01), valid.ClientDataJSON),
	}
	otherKey := signedaction.Assertion{
		AuthenticatorData: valid.AuthenticatorData,
		ClientDataJSON:    valid.ClientDataJSON,
		Signature:         signDigest(t, other, valid.AuthenticatorData, valid.ClientDataJSON),
	}
	garbageDER := signedaction.Assertion{
		AuthenticatorData: valid.AuthenticatorData,
		ClientDataJSON:    valid.ClientDataJSON,
		Signature:         []byte("garbage"),
	}

	cases := map[string]struct {
		assertion signedaction.Assertion
		origins   []string
	}{
		"wrong type":          {wrongType, []string{signedactiontest.Origin}},
		"wrong challenge":     {wrongChallenge, []string{signedactiontest.Origin}},
		"origin not allowed":  {valid, []string{"https://other.example"}},
		"rpIdHash of another": {otherRPID, []string{signedactiontest.Origin}},
		"flags without UV":    {noUV, []string{signedactiontest.Origin}},
		"signature by other":  {otherKey, []string{signedactiontest.Origin}},
		"garbage DER":         {garbageDER, []string{signedactiontest.Origin}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := signedaction.Verify(tc.assertion, challenge, pub, signedactiontest.RPID, tc.origins)
			if err == nil {
				t.Fatalf("Verify succeeded, want rejection")
			}
			if !errors.Is(err, signedaction.ErrInvalidAssertion) {
				t.Errorf("error = %v, want wrapped ErrInvalidAssertion", err)
			}
		})
	}
}

func TestVerify_NormalizesHighS(t *testing.T) {
	key := signedactiontest.NewKey()
	challenge := [32]byte{7}
	valid := signedactiontest.Sign(key, challenge)
	pub := signedactiontest.Coordinates(key)

	var sig struct {
		R, S *big.Int
	}
	if _, err := asn1.Unmarshal(valid.Signature, &sig); err != nil {
		t.Fatalf("unmarshal valid signature: %v", err)
	}
	n := elliptic.P256().Params().N
	high := new(big.Int).Sub(n, sig.S)
	der, err := asn1.Marshal(struct{ R, S *big.Int }{sig.R, high})
	if err != nil {
		t.Fatalf("marshal high-s: %v", err)
	}

	got, err := signedaction.Verify(signedaction.Assertion{
		AuthenticatorData: valid.AuthenticatorData,
		ClientDataJSON:    valid.ClientDataJSON,
		Signature:         der,
	}, challenge, pub, signedactiontest.RPID, []string{signedactiontest.Origin})
	if err != nil {
		t.Fatalf("Verify high-s: %v", err)
	}
	if got.R != fill32(sig.R) {
		t.Errorf("r = %x, want %x", got.R, sig.R)
	}
	lowS := sig.S
	if sig.S.Cmp(high) > 0 {
		lowS = high
	}
	if got.S != fill32(lowS) {
		t.Errorf("s = %x, want low-s %x", got.S, lowS)
	}
}
