package signedaction

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"

	"wms/internal/domain"
)

// VerifyOnChain checks a WebAuthn tuple as SignedMappingWMS does (ADR §5): no origin and no rpIdHash check.
func VerifyOnChain(wa WebAuthn, challenge [32]byte, key domain.P256Point) error {
	if wa.TypeIndex < 0 || wa.TypeIndex > len(wa.ClientDataJSON) ||
		!strings.HasPrefix(wa.ClientDataJSON[wa.TypeIndex:], `"type":"webauthn.get"`) {
		return fmt.Errorf(`%w: clientDataJSON lacks "type":"webauthn.get" at typeIndex %d`, ErrInvalidAssertion, wa.TypeIndex)
	}
	want := `"challenge":"` + base64.RawURLEncoding.EncodeToString(challenge[:]) + `"`
	if wa.ChallengeIndex < 0 || wa.ChallengeIndex > len(wa.ClientDataJSON) ||
		!strings.HasPrefix(wa.ClientDataJSON[wa.ChallengeIndex:], want) {
		return fmt.Errorf("%w: clientDataJSON lacks challenge at challengeIndex %d", ErrInvalidAssertion, wa.ChallengeIndex)
	}
	if len(wa.AuthenticatorData) < 37 {
		return fmt.Errorf("%w: authenticatorData too short", ErrInvalidAssertion)
	}
	flags := wa.AuthenticatorData[32]
	if flags&0x01 == 0 {
		return fmt.Errorf("%w: user presence flag missing", ErrInvalidAssertion)
	}
	if flags&0x04 == 0 {
		return fmt.Errorf("%w: user verification flag missing", ErrInvalidAssertion)
	}
	pub := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(key.X[:]),
		Y:     new(big.Int).SetBytes(key.Y[:]),
	}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return fmt.Errorf("%w: public key not on curve", ErrInvalidAssertion)
	}
	s := new(big.Int).SetBytes(wa.S[:])
	if s.Cmp(new(big.Int).Rsh(pub.Curve.Params().N, 1)) > 0 {
		return fmt.Errorf("%w: high-s signature", ErrInvalidAssertion)
	}
	clientHash := sha256.Sum256([]byte(wa.ClientDataJSON))
	h := sha256.New()
	h.Write(wa.AuthenticatorData)
	h.Write(clientHash[:])
	if !ecdsa.Verify(pub, h.Sum(nil), new(big.Int).SetBytes(wa.R[:]), s) {
		return fmt.Errorf("%w: signature does not verify", ErrInvalidAssertion)
	}
	return nil
}
