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
