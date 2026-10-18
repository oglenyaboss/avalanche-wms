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
	if cfg.PipelineWindow != 3 {
		t.Errorf("PipelineWindow default: got %d, want 3", cfg.PipelineWindow)
	}
}

func TestLoad_PipelineWindow(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"1", 1},
		{"5", 5},
		{"8", 8},
		{"0", 1},  // clamp up
		{"-3", 1}, // clamp up
		{"99", 8}, // clamp down to TxPoolAccountSlots ceiling
		{"", 3},   // default
	}
	for _, tc := range cases {
		t.Run("PIPELINE_WINDOW="+tc.env, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("PIPELINE_WINDOW", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.PipelineWindow != tc.want {
				t.Errorf("PIPELINE_WINDOW=%q → got %d, want %d", tc.env, cfg.PipelineWindow, tc.want)
			}
		})
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
	if want := "KAFKA_BROKERS"; !strings.Contains(err.Error(), want) {
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
	if !strings.Contains(err.Error(), "DB_URL") {
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

func TestLoad_KMSMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "kms")
	t.Setenv("KMS_URL", "http://web3signer:9000")
	t.Setenv("KMS_PUBLIC_KEYS", "0xaaa, 0xbbb")
	for _, k := range []string{"KMS_URL", "KMS_PUBLIC_KEYS"} {
		t.Setenv(k+"_FILE", "")
	}
	// В kms-режиме PRIVATE_KEY не нужен.
	t.Setenv("PRIVATE_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SignerMode != "kms" {
		t.Errorf("SignerMode: got %q", cfg.SignerMode)
	}
	if cfg.KMSURL != "http://web3signer:9000" {
		t.Errorf("KMSURL: got %q", cfg.KMSURL)
	}
	if len(cfg.KMSPublicKeys) != 2 || cfg.KMSPublicKeys[0] != "0xaaa" || cfg.KMSPublicKeys[1] != "0xbbb" {
		t.Errorf("KMSPublicKeys: got %v", cfg.KMSPublicKeys)
	}
	if len(cfg.PrivateKeys) != 0 {
		t.Errorf("PrivateKeys must stay empty in kms mode, got %v", cfg.PrivateKeys)
	}
	// Окно пайплайна масштабируется от числа KMS-ключей: 2 ключа → потолок 16.
	if cfg.PipelineWindow != 3 {
		t.Errorf("PipelineWindow default: got %d", cfg.PipelineWindow)
	}
}

func TestLoad_KMSModeWindowScalesWithSigners(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "kms")
	t.Setenv("KMS_URL", "http://web3signer:9000")
	t.Setenv("KMS_PUBLIC_KEYS", "0xaaa,0xbbb,0xccc,0xddd,0xeee")
	t.Setenv("PIPELINE_WINDOW", "99")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PipelineWindow != perSignerWindow*5 {
		t.Errorf("PipelineWindow must clamp to perSignerWindow×5=%d, got %d", perSignerWindow*5, cfg.PipelineWindow)
	}
}

func TestLoad_KMSModeMissingRequired(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "kms")
	t.Setenv("KMS_URL", "http://web3signer:9000")
	t.Setenv("KMS_PUBLIC_KEYS", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "KMS_PUBLIC_KEYS") {
		t.Fatalf("want KMS_PUBLIC_KEYS required error, got %v", err)
	}
}

func TestLoad_KMSModeWithoutPrivateKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PRIVATE_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("static mode without PRIVATE_KEY must fail")
	}
}

func TestLoad_BadSignerMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "hsm")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SIGNER_MODE") {
		t.Fatalf("want SIGNER_MODE validation error, got %v", err)
	}
}

func TestLoad_KMSPublicKeysFileOverride(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "kms")
	t.Setenv("KMS_URL", "http://web3signer:9000")
	t.Setenv("KMS_PUBLIC_KEYS", "")
	dir := t.TempDir()
	f := filepath.Join(dir, "kms_keys.txt")
	if err := os.WriteFile(f, []byte("0xaaa,0xbbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KMS_PUBLIC_KEYS_FILE", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.KMSPublicKeys) != 2 {
		t.Errorf("KMSPublicKeys from file: got %v", cfg.KMSPublicKeys)
	}
}

func TestLoad_KMSModeRequiresCommonEnv(t *testing.T) {
	// Регрессия P1: kms-режим не должен выкидывать общие обязательные
	// переменные. Пустой CONTRACT_ADDR → tx в 0x0 → молчаливые no-op коммиты.
	for _, missing := range []string{"KAFKA_BROKERS", "DB_URL", "RPC_URL", "CONTRACT_ADDR"} {
		setRequiredEnv(t)
		t.Setenv("SIGNER_MODE", "kms")
		t.Setenv("KMS_URL", "http://web3signer:9000")
		t.Setenv("KMS_PUBLIC_KEYS", "0xaaa")
		t.Setenv(missing, "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("%s: want required error, got %v", missing, err)
		}
	}
}

func TestLoad_KMSModeDuplicateKeys(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SIGNER_MODE", "kms")
	t.Setenv("KMS_URL", "http://web3signer:9000")
	t.Setenv("KMS_PUBLIC_KEYS", "0xaaa,0xaaa")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestLoad_RegistryAddr(t *testing.T) {
	const addr = "0x00000000000000000000000000000000000000aa"
	cases := []struct {
		name    string
		env     string
		file    string
		want    string
		wantErr string
	}{
		{name: "не задан", want: ""},
		{name: "из env", env: addr, want: addr},
		{name: "из файла", file: addr + "\n", want: addr},
		{name: "невалидный адрес", env: "0xnothex", wantErr: "REGISTRY_ADDR: invalid address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("REGISTRY_ADDR", tc.env)
			t.Setenv("REGISTRY_ADDR_FILE", "")
			if tc.file != "" {
				path := filepath.Join(t.TempDir(), "registry_addr.txt")
				if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
					t.Fatalf("setup: %v", err)
				}
				t.Setenv("REGISTRY_ADDR_FILE", path)
			}
			cfg, err := Load()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.RegistryAddr != tc.want {
				t.Errorf("RegistryAddr = %q, want %q", cfg.RegistryAddr, tc.want)
			}
		})
	}
}
