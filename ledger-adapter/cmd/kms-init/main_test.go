package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestEnsureKeyConfigs_Idempotent(t *testing.T) {
	dir := t.TempDir()

	created, err := ensureKeyConfigs(dir, 3)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if created != 3 {
		t.Fatalf("first run created %d, want 3", created)
	}
	first := readFile(t, filepath.Join(dir, "signer-0.yaml"))
	if !strings.Contains(first, "type: \"file-raw\"") || !strings.Contains(first, "keyType: \"SECP256K1\"") {
		t.Fatalf("bad config format: %q", first)
	}

	// Повторный запуск не должен перегенерировать существующие ключи:
	// адреса стабильны, фандинг одноразовый.
	created, err = ensureKeyConfigs(dir, 3)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if created != 0 {
		t.Fatalf("second run created %d, want 0", created)
	}
	if got := readFile(t, filepath.Join(dir, "signer-0.yaml")); got != first {
		t.Fatal("existing key config was rewritten")
	}

	// Рост числа signer'ов добавляет только недостающие.
	created, err = ensureKeyConfigs(dir, 4)
	if err != nil {
		t.Fatalf("grow run: %v", err)
	}
	if created != 1 {
		t.Fatalf("grow run created %d, want 1", created)
	}
}

func TestEnsureKeyConfigs_KeyIsParsable(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureKeyConfigs(dir, 1); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(dir, "signer-0.yaml"))
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, "privateKey:") {
			line = strings.Trim(strings.TrimPrefix(l, "privateKey:"), " \"\r")
		}
	}
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(line, "0x"))
	if err != nil {
		t.Fatalf("generated key not parsable (%q): %v", line, err)
	}
	if _, ok := pk.Public().(*ecdsa.PublicKey); !ok {
		t.Fatal("generated key has no ecdsa public part")
	}
}

func TestAddressOfIdentifier(t *testing.T) {
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ident := "0x" + hex.EncodeToString(crypto.FromECDSAPub(&pk.PublicKey)[1:]) // 64B uncompressed X||Y
	want := crypto.PubkeyToAddress(pk.PublicKey)
	got, err := addressOfIdentifier(ident)
	if err != nil {
		t.Fatalf("addressOfIdentifier: %v", err)
	}
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	if _, err := addressOfIdentifier("0x1234"); err == nil {
		t.Fatal("short identifier accepted")
	}
	if _, err := addressOfIdentifier("0x" + strings.Repeat("zz", 64)); err == nil {
		t.Fatal("non-hex identifier accepted")
	}
}

func TestAllowListCall(t *testing.T) {
	addr := common.HexToAddress("0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC")
	pad := "0000000000000000000000008db97c7cece249c2b98bdc0226cc4c2a57bf52fc"
	for sig, selector := range map[string]string{
		"readAllowList(address)": "eb54dae1",
		"setEnabled(address)":    "0aaf7043",
	} {
		if got := hex.EncodeToString(allowListCall(sig, addr)); got != selector+pad {
			t.Errorf("%s: got %s, want %s", sig, got, selector+pad)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
