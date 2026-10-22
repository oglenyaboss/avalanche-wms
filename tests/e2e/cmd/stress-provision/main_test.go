package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	require.NoError(t, err)
	return b
}

func mustB64url32(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	require.NoError(t, err)
	require.Len(t, b, 32)
	return b
}

// TestJWKRoundTrip: x/y/d decode back to the exact big-endian coordinates and
// are always exactly 32 bytes, including scalars with leading zero bytes
// (WebCrypto importKey fails on 31-byte coordinates).
func TestJWKRoundTrip(t *testing.T) {
	for i := 0; i < 200; i++ {
		key := newSoftKey()
		j := buildJWK(key)
		require.Equal(t, "EC", j.Kty)
		require.Equal(t, "P-256", j.Crv)
		require.Equal(t, key.X.FillBytes(make([]byte, 32)), mustB64url32(t, j.X), "x, key %d", i)
		require.Equal(t, key.Y.FillBytes(make([]byte, 32)), mustB64url32(t, j.Y), "y, key %d", i)
		require.Equal(t, key.D.FillBytes(make([]byte, 32)), mustB64url32(t, j.D), "d, key %d", i)
	}

	// D with a leading zero byte: build it by setting a small scalar and
	// deriving the point, so D.BitLen() <= 248.
	k := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()}, D: big.NewInt(1)}
	k.X, k.Y = k.Curve.ScalarBaseMult(k.D.Bytes())
	require.LessOrEqual(t, k.D.BitLen(), 248)
	j := buildJWK(k)
	require.Equal(t, k.D.FillBytes(make([]byte, 32)), mustB64url32(t, j.D), "d of the small scalar")
	require.Equal(t, k.X.FillBytes(make([]byte, 32)), mustB64url32(t, j.X), "x of the small scalar")
	require.Equal(t, k.Y.FillBytes(make([]byte, 32)), mustB64url32(t, j.Y), "y of the small scalar")
}

// TestSoftSignShape: the assertion is browser-shaped exactly as the WMS expects
// (origin, rpIdHash of "localhost", flags UP|UV, signCount 1) and verifies.
func TestSoftSignShape(t *testing.T) {
	key := newSoftKey()
	var challenge [32]byte
	_, err := rand.Read(challenge[:])
	require.NoError(t, err)

	a := softSign(key, challenge)

	wantCDJ := `{"type":"webauthn.get","challenge":"` +
		base64.RawURLEncoding.EncodeToString(challenge[:]) +
		`","origin":"http://localhost:5173","crossOrigin":false}`
	require.Equal(t, wantCDJ, string(a.ClientDataJSON))

	rpHash := sha256.Sum256([]byte("localhost"))
	wantAuth := append(rpHash[:], 0x05)
	wantAuth = binary.BigEndian.AppendUint32(wantAuth, 1)
	require.Len(t, a.AuthenticatorData, 37)
	require.Equal(t, wantAuth, a.AuthenticatorData)

	clientHash := sha256.Sum256(a.ClientDataJSON)
	h := sha256.New()
	h.Write(a.AuthenticatorData)
	h.Write(clientHash[:])
	require.True(t, ecdsa.VerifyASN1(&key.PublicKey, h.Sum(nil), a.Signature), "signature does not verify")
}

// TestSoftSignMatchesVector: softSign over a contract vector's challenge with
// the vector signer's key reproduces the vector's authenticatorData and
// clientDataJSON byte for byte (signatures differ: random k).
func TestSoftSignMatchesVector(t *testing.T) {
	raw, err := os.ReadFile("../../../../testdata/signed-actions/vectors.v1.json")
	require.NoError(t, err)

	type vectorWebauthn struct {
		AuthenticatorData string `json:"authenticatorData"`
		ClientDataJSONHex string `json:"clientDataJSONHex"`
	}
	type vectorAction struct {
		Challenge string          `json:"challenge"`
		WebAuthn  *vectorWebauthn `json:"webauthn"`
	}
	var file struct {
		Signer struct {
			PrivateKey string `json:"privateKey"`
		} `json:"signer"`
		Actions []vectorAction `json:"actions"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))

	var chosen *vectorAction
	for i := range file.Actions {
		if file.Actions[i].WebAuthn != nil {
			chosen = &file.Actions[i]
			break
		}
	}
	require.NotNil(t, chosen, "no action with webauthn in the vectors")

	key, err := crypto.HexToECDSA(strings.TrimPrefix(file.Signer.PrivateKey, "0x"))
	require.NoError(t, err)

	chal := mustHex(t, chosen.Challenge)
	require.Len(t, chal, 32)
	var challenge [32]byte
	copy(challenge[:], chal)
	a := softSign(key, challenge)

	require.Equal(t, mustHex(t, chosen.WebAuthn.AuthenticatorData), a.AuthenticatorData, "authenticatorData")
	require.Equal(t, mustHex(t, chosen.WebAuthn.ClientDataJSONHex), a.ClientDataJSON, "clientDataJSON")
}

// TestUsernames: 2-digit padding up to 99 operators, 3 digits beyond.
func TestUsernames(t *testing.T) {
	ops := operatorUsernames("a1b2c3", 70)
	require.Len(t, ops, 70)
	require.Equal(t, "stress-sop-a1b2c3-01", ops[0])
	require.Equal(t, "stress-sop-a1b2c3-02", ops[1])
	require.Equal(t, "stress-sop-a1b2c3-70", ops[69])

	ops = operatorUsernames("a1b2c3", 120)
	require.Len(t, ops, 120)
	require.Equal(t, "stress-sop-a1b2c3-001", ops[0])
	require.Equal(t, "stress-sop-a1b2c3-099", ops[98])
	require.Equal(t, "stress-sop-a1b2c3-120", ops[119])
}

// TestDefaultOutDirFromRepoRoot: без явного -out ключи пишутся в гит-игнорируемый
// tests/stress/.signed от корня репозитория, а не от cwd; явный -out не трогаем.
func TestDefaultOutDirFromRepoRoot(t *testing.T) {
	root, err := repoRoot()
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(root), "repo root must be absolute: %s", root)

	got := resolveOutDir("", false, root)
	require.True(t, filepath.IsAbs(got), "default out dir must be absolute, got %s", got)
	require.True(t, strings.HasSuffix(got, "tests/stress/.signed"), "got %s", got)
	require.Equal(t, filepath.Join(root, "tests/stress/.signed"), got)

	require.Equal(t, "custom/dir", resolveOutDir("custom/dir", true, root))
}

func TestCallAcceptsNullError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"organization":{"org_id":"o1","name":"n"},"members":[]},"error":null}`))
	}))
	defer srv.Close()
	c := &wmsClient{baseURL: srv.URL, http: srv.Client(), token: "t"}

	var org organizationDTO
	require.NoError(t, c.call(context.Background(), http.MethodGet, "/passkeys/organization", nil, &org))
	require.Equal(t, "o1", org.Organization.OrgID)
}

func TestCallRejectsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"data":null,"error":{"code":"X","message":"m"}}`))
	}))
	defer srv.Close()
	c := &wmsClient{baseURL: srv.URL, http: srv.Client(), token: "t"}

	require.Error(t, c.call(context.Background(), http.MethodGet, "/x", nil, nil))
}
