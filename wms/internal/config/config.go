package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

const minJWTSecretLength = 32

var insecureJWTSecrets = map[string]struct{}{
	"change-me":                            {},
	"dev-secret":                           {},
	"replace-me":                           {},
	"replace-with-a-random-32-byte-secret": {},
	"replace-with-a-random-64-character-secret": {},
}

type Config struct {
	Port             string
	DBHost           string
	DBPort           string
	DBUser           string
	DBPassword       string
	DBName           string
	KafkaBroker      string
	LedgerAdapterURL string
	JWTSecret        string
	JWTAccessTTL     time.Duration
	JWTRefreshTTL    time.Duration
}

func Load() (*Config, error) {
	jwtSecret, err := getJWTSecret()
	if err != nil {
		return nil, err
	}

	return &Config{
		Port:             getEnv("PORT", "8080"),
		DBHost:           getEnv("DB_HOST", "localhost"),
		DBPort:           getEnv("DB_PORT", "5432"),
		DBUser:           getEnv("POSTGRES_USER", "root"),
		DBPassword:       getEnv("POSTGRES_PASSWORD", "root"),
		DBName:           getEnv("POSTGRES_DB", "wms_blockchain_db"),
		KafkaBroker:      getEnv("KAFKA_BROKER", "localhost:9092"),
		LedgerAdapterURL: getEnv("LEDGER_ADAPTER_URL", ""),
		JWTSecret:        jwtSecret,
		JWTAccessTTL:     getDurationEnv("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:    getDurationEnv("JWT_REFRESH_TTL", 7*24*time.Hour),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getDurationEnv retrieves a duration from the environment variable specified by key.
func getDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("WARNING: invalid %s=%q, using default %s", key, raw, fallback)
		return fallback
	}

	return parsed
}

func getJWTSecret() (string, error) {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		return "", errors.New("JWT_SECRET environment variable is required")
	}

	if _, insecure := insecureJWTSecrets[secret]; insecure {
		return "", fmt.Errorf("JWT_SECRET=%q is insecure; set a unique random secret", secret)
	}

	if len(secret) < minJWTSecretLength {
		return "", fmt.Errorf("JWT_SECRET must be at least %d characters long", minJWTSecretLength)
	}

	return secret, nil
}
