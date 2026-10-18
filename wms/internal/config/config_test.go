package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET environment variable is required") {
		t.Fatalf("expected required JWT secret error, got %v", err)
	}
}

func TestLoadRejectsInsecureJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "dev-secret")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "is insecure") {
		t.Fatalf("expected insecure JWT secret error, got %v", err)
	}
}

func TestLoadRejectsShortJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "short-secret")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "at least 32 characters") {
		t.Fatalf("expected JWT secret length error, got %v", err)
	}
}

func TestLoadParsesJWTConfig(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("a", minJWTSecretLength))
	t.Setenv("JWT_ACCESS_TTL", "30m")
	t.Setenv("JWT_REFRESH_TTL", "240h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.JWTSecret != strings.Repeat("a", minJWTSecretLength) {
		t.Fatalf("unexpected JWT secret: %q", cfg.JWTSecret)
	}
	if cfg.JWTAccessTTL != 30*time.Minute {
		t.Fatalf("unexpected JWTAccessTTL: %s", cfg.JWTAccessTTL)
	}
	if cfg.JWTRefreshTTL != 240*time.Hour {
		t.Fatalf("unexpected JWTRefreshTTL: %s", cfg.JWTRefreshTTL)
	}
}

func TestLoadWebAuthnDefaults(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("a", minJWTSecretLength))
	t.Setenv("WEBAUTHN_RP_ID", "")
	t.Setenv("WEBAUTHN_RP_NAME", "")
	t.Setenv("WEBAUTHN_RP_ORIGINS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.WebAuthnRPID != "localhost" {
		t.Fatalf("unexpected WebAuthnRPID: %q", cfg.WebAuthnRPID)
	}
	if cfg.WebAuthnRPName != "Avalanche WMS" {
		t.Fatalf("unexpected WebAuthnRPName: %q", cfg.WebAuthnRPName)
	}
	if len(cfg.WebAuthnRPOrigins) != 1 || cfg.WebAuthnRPOrigins[0] != "http://localhost:5173" {
		t.Fatalf("unexpected WebAuthnRPOrigins: %v", cfg.WebAuthnRPOrigins)
	}
}

func TestLoadParsesWebAuthnOrigins(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("a", minJWTSecretLength))
	t.Setenv("WEBAUTHN_RP_ID", "wms.example.com")
	t.Setenv("WEBAUTHN_RP_ORIGINS", " https://wms.example.com , ,https://admin.example.com ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.WebAuthnRPID != "wms.example.com" {
		t.Fatalf("unexpected WebAuthnRPID: %q", cfg.WebAuthnRPID)
	}
	want := []string{"https://wms.example.com", "https://admin.example.com"}
	if len(cfg.WebAuthnRPOrigins) != len(want) {
		t.Fatalf("unexpected WebAuthnRPOrigins: %v", cfg.WebAuthnRPOrigins)
	}
	for i := range want {
		if cfg.WebAuthnRPOrigins[i] != want[i] {
			t.Fatalf("WebAuthnRPOrigins[%d] = %q, want %q", i, cfg.WebAuthnRPOrigins[i], want[i])
		}
	}
}
