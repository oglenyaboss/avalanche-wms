package config

import "os"

type Config struct {
	Port             string
	DBHost           string
	DBPort           string
	DBUser           string
	DBPassword       string
	DBName           string
	KafkaBroker      string
	LedgerAdapterURL string
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
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
