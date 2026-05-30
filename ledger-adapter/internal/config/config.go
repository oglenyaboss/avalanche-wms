package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the application configuration.
type Config struct {
	Port               string
	KafkaBrokers       string
	KafkaGroupID       string
	DbURL              string
	RpcURL             string
	ContractAddr       string
	PrivateKey         string
	BatchSize          int
	BatchTimeout       time.Duration
	DLQTopic           string
	ReceiptPollTimeout time.Duration
	ReconcileInterval  time.Duration
	ReconcileMinAge    time.Duration
	LogLevel           string
}

// Load reads configuration from environment variables with sensible defaults.
//
// Для чувствительных значений (RPC_URL / PRIVATE_KEY / CONTRACT_ADDR /
// KAFKA_BROKERS / DB_URL) поддерживается <NAME>_FILE override: если путь задан
// и файл читается — его содержимое (trim'нутое) используется вместо прямого env.
// Нужно для test-профиля, где contract-deploy пишет артефакты в shared volume.
//
// Обязательные поля: KAFKA_BROKERS, DB_URL, RPC_URL, CONTRACT_ADDR, PRIVATE_KEY.
// Если хоть одно отсутствует — возвращает ошибку.
func Load() (*Config, error) {
	c := &Config{
		Port:               getDefault("PORT", "8085"),
		KafkaGroupID:       getDefault("KAFKA_GROUP_ID", "ledger-adapter"),
		DLQTopic:           getDefault("DLQ_TOPIC", "wms.dlq.v1"),
		LogLevel:           getDefault("LOG_LEVEL", "info"),
		BatchSize:          getIntDefault("BATCH_SIZE", 10),
		BatchTimeout:       getDurationDefault("BATCH_TIMEOUT", 100*time.Millisecond),
		ReceiptPollTimeout: getDurationDefault("RECEIPT_POLL_TIMEOUT", 30*time.Second),
		// Reconcile loop (N1): сверяет stuck SENT/FAILED строки с on-chain receipt'ом.
		// MinAge должен быть > ReceiptPollTimeout, чтобы не гоняться с in-flight WaitReceipt.
		ReconcileInterval: getDurationDefault("RECONCILE_INTERVAL", 30*time.Second),
		ReconcileMinAge:   getDurationDefault("RECONCILE_MIN_AGE", time.Minute),
	}

	required := []struct {
		name  string
		field *string
	}{
		{"KAFKA_BROKERS", &c.KafkaBrokers},
		{"DB_URL", &c.DbURL},
		{"RPC_URL", &c.RpcURL},
		{"CONTRACT_ADDR", &c.ContractAddr},
		{"PRIVATE_KEY", &c.PrivateKey},
	}
	for _, r := range required {
		v, err := getRequired(r.name)
		if err != nil {
			return nil, err
		}
		*r.field = v
	}

	// Инвариант: reconcile-loop не должен трогать строку, пока её ещё дожимает
	// синхронный WaitReceipt. ReconcileMinAge > ReceiptPollTimeout гарантирует, что
	// строка станет reconcile-eligible только после того, как WaitReceipt завершился.
	// Без этого loop мог бы пометить SENT-строку COMMITTED, а последующий timeout —
	// FAILED (см. MarkFailed). Проверяем на старте, а не в 3 часа ночи.
	if c.ReconcileMinAge <= c.ReceiptPollTimeout {
		return nil, fmt.Errorf("RECONCILE_MIN_AGE (%s) must be > RECEIPT_POLL_TIMEOUT (%s): a shorter min-age lets the reconcile loop race the flusher's in-flight WaitReceipt", c.ReconcileMinAge, c.ReceiptPollTimeout)
	}
	// time.ParseDuration accepts "0s"/"-5s" without error, so getDurationDefault keeps
	// them verbatim. A non-positive interval would panic time.NewTicker in the reconcile
	// loop (reconcile.go) — crashing the process inside the errgroup. Reject at startup.
	if c.ReconcileInterval <= 0 {
		return nil, fmt.Errorf("RECONCILE_INTERVAL (%s) must be > 0", c.ReconcileInterval)
	}
	return c, nil
}

// getRequired сначала читает <NAME>_FILE, потом <NAME>. Пустая строка = missing.
// Ошибка чтения существующего файла — fatal (явная мисконфигурация).
// Если <NAME>_FILE задан, но файл не найден — логируем warning в stderr
// (опечатка в пути молча бы привела к использованию env-var с другим значением).
func getRequired(name string) (string, error) {
	if path := os.Getenv(name + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(b)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read %s_FILE=%q: %w", name, path, err)
		}
		fmt.Fprintf(os.Stderr, "config: %s_FILE=%q not found, falling back to %s env var\n", name, path, name)
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("missing required env: %s", name)
	}
	return v, nil
}

func getDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func getIntDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid int %s=%q, using default %d\n", name, v, def)
		return def
	}
	return n
}

func getDurationDefault(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid duration %s=%q, using default %s\n", name, v, def)
		return def
	}
	return d
}
