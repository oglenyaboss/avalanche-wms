package config

import (
	"os"
	"time"
)

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

func Load() *Config {
	return &Config{
		Port:             getEnv("PORT", "8080"),
		DBHost:           getEnv("DB_HOST", "localhost"),
		DBPort:           getEnv("DB_PORT", "5432"),
		DBUser:           getEnv("POSTGRES_USER", "root"),
		DBPassword:       getEnv("POSTGRES_PASSWORD", "root"),
		DBName:           getEnv("POSTGRES_DB", "wms_blockchain_db"),
		KafkaBroker:      getEnv("KAFKA_BROKER", "localhost:9092"),
		LedgerAdapterURL: getEnv("LEDGER_ADAPTER_URL", ""),
		JWTSecret:        getEnv("JWT_SECRET", "dev-secret"),
		JWTAccessTTL:     getDurationEnv("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:    getDurationEnv("JWT_REFRESH_TTL", 7*24*time.Hour),
	}
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
		return fallback
	}

	return parsed
}
