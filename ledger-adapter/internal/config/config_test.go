package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("RPC_URL", "")
	t.Setenv("PRIVATE_KEY", "")
	t.Setenv("CONTRACT_ADDR", "")

	cfg := Load()
	if cfg.Port != "8085" {
		t.Errorf("Port default: want 8085, got %q", cfg.Port)
	}
	if cfg.RpcURL != "" || cfg.PrivateKey != "" || cfg.ContractAddr != "" {
		t.Errorf("expected empty secret fields, got %+v", cfg)
	}
}

func TestLoad_EnvValues(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("RPC_URL", "http://rpc:1234")
	t.Setenv("PRIVATE_KEY", "0xdeadbeef")
	t.Setenv("CONTRACT_ADDR", "0xabc")

	cfg := Load()
	if cfg.Port != "9000" {
		t.Errorf("Port: want 9000, got %q", cfg.Port)
	}
	if cfg.RpcURL != "http://rpc:1234" {
		t.Errorf("RpcURL: got %q", cfg.RpcURL)
	}
	if cfg.PrivateKey != "0xdeadbeef" {
		t.Errorf("PrivateKey: got %q", cfg.PrivateKey)
	}
	if cfg.ContractAddr != "0xabc" {
		t.Errorf("ContractAddr: got %q", cfg.ContractAddr)
	}
}

func TestLoad_FileOverride(t *testing.T) {
	dir := t.TempDir()
	rpcFile := filepath.Join(dir, "rpc.txt")
	if err := os.WriteFile(rpcFile, []byte("http://from-file:9650\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv("RPC_URL", "http://from-env:1234")
	t.Setenv("RPC_URL_FILE", rpcFile)
	t.Setenv("PRIVATE_KEY", "")
	t.Setenv("CONTRACT_ADDR", "")

	cfg := Load()
	if cfg.RpcURL != "http://from-file:9650" {
		t.Errorf("_FILE override: want http://from-file:9650 (trimmed), got %q", cfg.RpcURL)
	}
}

func TestLoad_FileMissing_FallsBackToEnv(t *testing.T) {
	t.Setenv("RPC_URL_FILE", "/nonexistent/does-not-exist.txt")
	t.Setenv("RPC_URL", "http://fallback:1234")
	t.Setenv("PRIVATE_KEY", "")
	t.Setenv("CONTRACT_ADDR", "")

	cfg := Load()
	if cfg.RpcURL != "http://fallback:1234" {
		t.Errorf("missing file fallback: want env value, got %q", cfg.RpcURL)
	}
}

func TestLoad_FileTakesPriorityOverEnv(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(keyFile, []byte("0xFROMFILE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv("PRIVATE_KEY_FILE", keyFile)
	t.Setenv("PRIVATE_KEY", "0xFROMENV")
	t.Setenv("RPC_URL", "")
	t.Setenv("CONTRACT_ADDR", "")

	cfg := Load()
	if cfg.PrivateKey != "0xFROMFILE" {
		t.Errorf("_FILE should win over env: got %q", cfg.PrivateKey)
	}
}
