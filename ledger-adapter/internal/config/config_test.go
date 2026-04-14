package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setRequiredEnv заполняет все обязательные поля чтобы Load() не упал — удобно
// для тестов, проверяющих конкретный отдельный кейс.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("DB_URL", "postgres://u:p@h/db")
	t.Setenv("RPC_URL", "http://rpc")
	t.Setenv("CONTRACT_ADDR", "0xabc")
	t.Setenv("PRIVATE_KEY", "0xdef")
	// Очистить _FILE чтобы не протекло между тестами
	for _, k := range []string{"KAFKA_BROKERS", "DB_URL", "RPC_URL", "CONTRACT_ADDR", "PRIVATE_KEY"} {
		t.Setenv(k+"_FILE", "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "8085" {
		t.Errorf("Port default: got %q", cfg.Port)
	}
	if cfg.BatchSize != 10 {
		t.Errorf("BatchSize default: got %d", cfg.BatchSize)
	}
	if cfg.BatchTimeout != 100*time.Millisecond {
		t.Errorf("BatchTimeout default: got %s", cfg.BatchTimeout)
	}
	if cfg.DLQTopic != "wms.dlq.v1" {
		t.Errorf("DLQTopic default: got %q", cfg.DLQTopic)
	}
	if cfg.ReceiptPollTimeout != 30*time.Second {
		t.Errorf("ReceiptPollTimeout default: got %s", cfg.ReceiptPollTimeout)
	}
	if cfg.KafkaGroupID != "ledger-adapter" {
		t.Errorf("KafkaGroupID default: got %q", cfg.KafkaGroupID)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default: got %q", cfg.LogLevel)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	// Очистить всё
	for _, k := range []string{"KAFKA_BROKERS", "DB_URL", "RPC_URL", "CONTRACT_ADDR", "PRIVATE_KEY"} {
		t.Setenv(k, "")
		t.Setenv(k+"_FILE", "")
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required, got nil")
	}
	// Первый в списке required — KAFKA_BROKERS
	if want := "KAFKA_BROKERS"; !contains(err.Error(), want) {
		t.Errorf("expected error to mention %q, got %v", want, err)
	}
}

func TestLoad_OnlyKafkaSet_OtherRequiredStillFails(t *testing.T) {
	for _, k := range []string{"KAFKA_BROKERS", "DB_URL", "RPC_URL", "CONTRACT_ADDR", "PRIVATE_KEY"} {
		t.Setenv(k, "")
		t.Setenv(k+"_FILE", "")
	}
	t.Setenv("KAFKA_BROKERS", "kafka:9092")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "DB_URL") {
		t.Errorf("expected DB_URL in error, got %v", err)
	}
}

func TestLoad_EnvValues(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", "9000")
	t.Setenv("RPC_URL", "http://rpc:1234")
	t.Setenv("PRIVATE_KEY", "0xdeadbeef")
	t.Setenv("CONTRACT_ADDR", "0xfeed")
	t.Setenv("BATCH_SIZE", "25")
	t.Setenv("BATCH_TIMEOUT", "250ms")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "9000" {
		t.Errorf("Port: got %q", cfg.Port)
	}
	if cfg.RpcURL != "http://rpc:1234" {
		t.Errorf("RpcURL: got %q", cfg.RpcURL)
	}
	if cfg.PrivateKey != "0xdeadbeef" {
		t.Errorf("PrivateKey: got %q", cfg.PrivateKey)
	}
	if cfg.ContractAddr != "0xfeed" {
		t.Errorf("ContractAddr: got %q", cfg.ContractAddr)
	}
	if cfg.BatchSize != 25 {
		t.Errorf("BatchSize: got %d", cfg.BatchSize)
	}
	if cfg.BatchTimeout != 250*time.Millisecond {
		t.Errorf("BatchTimeout: got %s", cfg.BatchTimeout)
	}
}

func TestLoad_FileOverride(t *testing.T) {
	setRequiredEnv(t)

	dir := t.TempDir()
	rpcFile := filepath.Join(dir, "rpc.txt")
	if err := os.WriteFile(rpcFile, []byte("http://from-file:9650\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv("RPC_URL", "http://from-env:1234")
	t.Setenv("RPC_URL_FILE", rpcFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RpcURL != "http://from-file:9650" {
		t.Errorf("_FILE override: want http://from-file:9650 (trimmed), got %q", cfg.RpcURL)
	}
}

func TestLoad_FileMissing_FallsBackToEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RPC_URL_FILE", "/nonexistent/missing.txt")
	t.Setenv("RPC_URL", "http://fallback:1234")

	stderr := captureStderr(t, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.RpcURL != "http://fallback:1234" {
			t.Errorf("missing file fallback: got %q", cfg.RpcURL)
		}
	})

	if !strings.Contains(stderr, "RPC_URL_FILE") || !strings.Contains(stderr, "not found") {
		t.Errorf("expected stderr warning about missing RPC_URL_FILE, got %q", stderr)
	}
}

// captureStderr временно перенаправляет os.Stderr в pipe и возвращает всё
// что туда записал f. Используется для проверки warning'ов в config.Load.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	f()
	_ = w.Close()
	<-done
	_ = r.Close()
	return buf.String()
}

func TestLoad_FileTakesPriorityOverEnv(t *testing.T) {
	setRequiredEnv(t)

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(keyFile, []byte("0xFROMFILE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv("PRIVATE_KEY_FILE", keyFile)
	t.Setenv("PRIVATE_KEY", "0xFROMENV")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PrivateKey != "0xFROMFILE" {
		t.Errorf("_FILE should win over env: got %q", cfg.PrivateKey)
	}
}

func TestLoad_InvalidIntFallsBackToDefault(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BATCH_SIZE", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BatchSize != 10 {
		t.Errorf("invalid int should fall back to default: got %d", cfg.BatchSize)
	}
}

func TestLoad_InvalidDurationFallsBackToDefault(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BATCH_TIMEOUT", "not-a-duration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BatchTimeout != 100*time.Millisecond {
		t.Errorf("invalid duration should fall back: got %s", cfg.BatchTimeout)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
