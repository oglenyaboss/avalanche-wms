package signedaction

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"wms/internal/domain"
)

// ErrInvalidAssertion is wrapped by every error returned from Verify.
var ErrInvalidAssertion = errors.New("invalid assertion")

// Assertion is a browser WebAuthn assertion, all parts base64url-decoded.
type Assertion struct {
	CredentialID      []byte
	AuthenticatorData []byte
	ClientDataJSON    []byte
	Signature         []byte // ASN.1 DER, as browsers return it
}

// WebAuthn is the contract's WebAuthn.WebAuthnAuth, ready for the envelope.
type WebAuthn struct {
	AuthenticatorData []byte
	ClientDataJSON    string
	ChallengeIndex    int
	TypeIndex         int
	R, S              [32]byte // S is low-s normalized
}

type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

type ecdsaSignature struct {
	R, S *big.Int
}

// Verify checks an assertion against the challenge and the signer's P-256 key,
// enforcing type, challenge, origin, rpIdHash, UP/UV flags and low-s.
func Verify(a Assertion, challenge [32]byte, key domain.P256Point, rpID string, origins []string) (WebAuthn, error) {
	var cdj clientData
	if err := json.Unmarshal(a.ClientDataJSON, &cdj); err != nil {
		return WebAuthn{}, fmt.Errorf("%w: clientDataJSON: %s", ErrInvalidAssertion, err)
	}
	if cdj.Type != "webauthn.get" {
		return WebAuthn{}, fmt.Errorf("%w: clientData type %q", ErrInvalidAssertion, cdj.Type)
	}
	if cdj.Challenge != base64.RawURLEncoding.EncodeToString(challenge[:]) {
		return WebAuthn{}, fmt.Errorf("%w: challenge mismatch", ErrInvalidAssertion)
	}
	allowed := false
	for _, o := range origins {
		if cdj.Origin == o {
			allowed = true
			break
		}
	}
	if !allowed {
		return WebAuthn{}, fmt.Errorf("%w: origin %q not allowed", ErrInvalidAssertion, cdj.Origin)
	}

	if len(a.AuthenticatorData) < 37 {
		return WebAuthn{}, fmt.Errorf("%w: authenticatorData too short", ErrInvalidAssertion)
	}
	rpHash := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(a.AuthenticatorData[:32], rpHash[:]) {
		return WebAuthn{}, fmt.Errorf("%w: rpIdHash mismatch", ErrInvalidAssertion)
	}
	flags := a.AuthenticatorData[32]
	if flags&0x01 == 0 {
		return WebAuthn{}, fmt.Errorf("%w: user presence flag missing", ErrInvalidAssertion)
	}
	if flags&0x04 == 0 {
		return WebAuthn{}, fmt.Errorf("%w: user verification flag missing", ErrInvalidAssertion)
	}
	if flags&0x10 != 0 && flags&0x08 == 0 {
		return WebAuthn{}, fmt.Errorf("%w: backed up flag without backup eligible", ErrInvalidAssertion)
	}

	pub := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(key.X[:]),
		Y:     new(big.Int).SetBytes(key.Y[:]),
	}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return WebAuthn{}, fmt.Errorf("%w: public key not on curve", ErrInvalidAssertion)
	}

	clientHash := sha256.Sum256(a.ClientDataJSON)
	h := sha256.New()
	h.Write(a.AuthenticatorData)
	h.Write(clientHash[:])

	var sig ecdsaSignature
	rest, err := asn1.Unmarshal(a.Signature, &sig)
	if err != nil {
		return WebAuthn{}, fmt.Errorf("%w: signature DER: %s", ErrInvalidAssertion, err)
	}
	if len(rest) != 0 || sig.R == nil || sig.S == nil {
		return WebAuthn{}, fmt.Errorf("%w: signature DER trailing bytes", ErrInvalidAssertion)
	}
	if !ecdsa.Verify(pub, h.Sum(nil), sig.R, sig.S) {
		return WebAuthn{}, fmt.Errorf("%w: signature does not verify", ErrInvalidAssertion)
	}

	n := pub.Curve.Params().N
	s := sig.S
	if s.Cmp(new(big.Int).Rsh(n, 1)) > 0 {
		s = new(big.Int).Sub(n, s)
	}

	typeIndex := strings.Index(string(a.ClientDataJSON), `"type":"webauthn.get"`)
	challengeIndex := strings.Index(string(a.ClientDataJSON), `"challenge":"`)
	if typeIndex < 0 || challengeIndex < 0 {
		return WebAuthn{}, fmt.Errorf("%w: clientDataJSON lacks type or challenge", ErrInvalidAssertion)
	}

	return WebAuthn{
		AuthenticatorData: a.AuthenticatorData,
		ClientDataJSON:    string(a.ClientDataJSON),
		ChallengeIndex:    challengeIndex,
		TypeIndex:         typeIndex,
		R:                 fill32(sig.R),
		S:                 fill32(s),
	}, nil
}

func fill32(v *big.Int) [32]byte {
	var out [32]byte
	v.FillBytes(out[:])
	return out
}
