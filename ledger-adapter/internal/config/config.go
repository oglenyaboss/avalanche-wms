package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

// Config holds the application configuration.
type Config struct {
	Port               string
	KafkaBrokers       string
	KafkaGroupID       string
	DbURL              string
	RpcURL             string
	ContractAddr       string
	RegistryAddr       string
	PrivateKey         string
	BatchSize          int
	BatchTimeout       time.Duration
	DLQTopic           string
	ReceiptPollTimeout time.Duration
	ReconcileInterval  time.Duration
	ReconcileMinAge    time.Duration
	PipelineWindow     int
	LogLevel           string
	// PrivateKeys — signer-аккаунт, ровно один: подписанные action'ы
	// (ConsortiumRegistry.applyKeyActions) держат Kafka-порядок действий одной
	// nonce-последовательностью, поэтому multi-signer шардирование запрещено
	// (Load возвращает ошибку при >1). Если задан PRIVATE_KEYS (comma-separated)
	// — используется он; иначе [PrivateKey].
	PrivateKeys []string

	// SignerMode: "static" (дефолт, ТОЛЬКО локальная разработка — приватник в env
	// открытым текстом) | "kms"
	// (подпись через внешний Web3Signer — ключевой материал адаптеру не нужен).
	SignerMode string
	// KMSURL / KMSPublicKeys — параметры kms-режима. KMSPublicKeys — identifier
	// (uncompressed X||Y public key) единственного ключа, загруженного в
	// Web3Signer; поддерживает KMS_PUBLIC_KEYS_FILE (comma-separated в файле).
	KMSURL        string
	KMSPublicKeys []string
}

// perSignerWindow — сколько batch-tx in-flight держим на ОДИН signer-аккаунт. Узел
// держит TxPoolAccountSlots=16 исполняемых слотов на аккаунт; 8 — это 50% запас (НЕ
// сам лимит, см. obs 10635) против "account slots" реджекта sequential-nonce tx'ов
// одного signer'а. Signer ровно один (multi-signer запрещён: signed actions держат
// Kafka-порядок одной nonce-цепочкой), поэтому потолок окна = perSignerWindow.
// На WAN-цепи глубина окна = throughput (Little: in-flight ÷ latency), потолок НЕ
// в газе — гео-чейн при 200M gasLimit жжёт ~0.04%/блок (запас ~30×); лимитирует
// feeding-cadence и WAN receipt-latency.
const perSignerWindow = 8

// Load reads configuration from environment variables with sensible defaults.
//
// Для чувствительных значений (RPC_URL / PRIVATE_KEY / CONTRACT_ADDR /
// KAFKA_BROKERS / DB_URL) поддерживается <NAME>_FILE override: если путь задан
// и файл читается — его содержимое (trim'нутое) используется вместо прямого env.
// Нужно для test-профиля, где contract-deploy пишет артефакты в shared volume.
//
// Обязательные поля (static): KAFKA_BROKERS, DB_URL, RPC_URL, CONTRACT_ADDR,
// PRIVATE_KEY. В kms-режиме (SIGNER_MODE=kms) вместо PRIVATE_KEY требуются
// KMS_URL и KMS_PUBLIC_KEYS.
func Load() (*Config, error) {
	c := &Config{
		Port:               getDefault("PORT", "8085"),
		KafkaGroupID:       getDefault("KAFKA_GROUP_ID", "ledger-adapter"),
		DLQTopic:           getDefault("DLQ_TOPIC", "wms.dlq.v1"),
		LogLevel:           getDefault("LOG_LEVEL", "info"),
		SignerMode:         getDefault("SIGNER_MODE", "static"),
		BatchSize:          getIntDefault("BATCH_SIZE", 10),
		BatchTimeout:       getDurationDefault("BATCH_TIMEOUT", 100*time.Millisecond),
		ReceiptPollTimeout: getDurationDefault("RECEIPT_POLL_TIMEOUT", 30*time.Second),
		// Reconcile loop (N1): сверяет stuck SENT/FAILED строки с on-chain receipt'ом.
		// MinAge должен быть > ReceiptPollTimeout, чтобы не гоняться с in-flight WaitReceipt.
		ReconcileInterval: getDurationDefault("RECONCILE_INTERVAL", 30*time.Second),
		ReconcileMinAge:   getDurationDefault("RECONCILE_MIN_AGE", time.Minute),
		// PipelineWindow: сколько batch-tx держать in-flight одновременно (flusher
		// pipeline). 1 = старое serial-поведение (один batch на блок). Дефолт 3.
		PipelineWindow: getIntDefault("PIPELINE_WINDOW", 3),
	}

	// Общие обязательные переменные — для любого signer-режима. PRIVATE_KEY
	// требуется только в static; в kms вместо него KMS_URL + KMS_PUBLIC_KEYS.
	common := []struct {
		name  string
		field *string
	}{
		{"KAFKA_BROKERS", &c.KafkaBrokers},
		{"DB_URL", &c.DbURL},
		{"RPC_URL", &c.RpcURL},
		{"CONTRACT_ADDR", &c.ContractAddr},
	}
	for _, r := range common {
		v, err := GetRequired(r.name)
		if err != nil {
			return nil, err
		}
		*r.field = v
	}

	// REGISTRY_ADDR необязателен: без него /registry/keys отвечает 502.
	regAddr, err := GetOptional("REGISTRY_ADDR")
	if err != nil {
		return nil, err
	}
	if regAddr != "" && !ethcommon.IsHexAddress(regAddr) {
		return nil, fmt.Errorf("REGISTRY_ADDR: invalid address")
	}
	c.RegistryAddr = regAddr

	switch c.SignerMode {
	case "kms":
		var err error
		if c.KMSURL, err = GetRequired("KMS_URL"); err != nil {
			return nil, err
		}
		rawKeys, err := GetRequired("KMS_PUBLIC_KEYS")
		if err != nil {
			return nil, err
		}
		seen := make(map[string]bool)
		for _, id := range strings.Split(rawKeys, ",") {
			if id = strings.TrimSpace(id); id != "" {
				// Дубль identifier'а = два Client'а с одним адресом и независимыми
				// локальными nonce → replacement-коллизии под нагрузкой.
				if seen[id] {
					return nil, fmt.Errorf("KMS_PUBLIC_KEYS: duplicate %s", id)
				}
				seen[id] = true
				c.KMSPublicKeys = append(c.KMSPublicKeys, id)
			}
		}
		if len(c.KMSPublicKeys) == 0 {
			return nil, fmt.Errorf("KMS_PUBLIC_KEYS is empty")
		}
	case "static":
		v, err := GetRequired("PRIVATE_KEY")
		if err != nil {
			return nil, err
		}
		c.PrivateKey = v
	default:
		return nil, fmt.Errorf("SIGNER_MODE must be \"static\" or \"kms\", got %q", c.SignerMode)
	}

	// PRIVATE_KEYS (comma-separated) исторически задавал несколько signer'ов;
	// теперь ключ ровно один (см. проверку ниже). Падаем на единственный
	// PRIVATE_KEY, если не задан. PRIVATE_KEY остаётся required (back-compat).
	// В kms-режиме не применяется — signer определяется KMS_PUBLIC_KEYS.
	if c.SignerMode == "static" {
		if raw := os.Getenv("PRIVATE_KEYS"); raw != "" {
			for _, k := range strings.Split(raw, ",") {
				if k = strings.TrimSpace(k); k != "" {
					c.PrivateKeys = append(c.PrivateKeys, k)
				}
			}
		}
		if len(c.PrivateKeys) == 0 {
			c.PrivateKeys = []string{c.PrivateKey}
		}
	}

	// Подписанные action'ы (ConsortiumRegistry.applyKeyActions) отправляются
	// ровно одним signer'ом: порядок действий держится одной nonce-
	// последовательностью, шардирование по нескольким аккаунтам нарушило бы
	// Kafka-порядок (ADR-0001 §9). Отклоняем конфиг с более чем одним ключом —
	// и в static, и в kms.
	signerCount := len(c.PrivateKeys)
	if c.SignerMode == "kms" {
		signerCount = len(c.KMSPublicKeys)
	}
	if signerCount > 1 {
		return nil, fmt.Errorf("only one signer is supported (signed actions keep Kafka order by one nonce sequence), got %d", signerCount)
	}

	// Потолок in-flight окна = perSignerWindow × signerCount; signer ровно один,
	// в kms-режиме это единственный KMS-ключ (лимит TxPoolAccountSlots тот же).
	pipelineWindowMax := perSignerWindow * signerCount
	if c.PipelineWindow < 1 {
		c.PipelineWindow = 1
	}
	if c.PipelineWindow > pipelineWindowMax {
		fmt.Fprintf(os.Stderr, "config: PIPELINE_WINDOW=%d clamped to %d (perSignerWindow=%d × %d signers)\n",
			c.PipelineWindow, pipelineWindowMax, perSignerWindow, signerCount)
		c.PipelineWindow = pipelineWindowMax
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

// GetRequired — GetOptional, но пустое значение = ошибка.
// Используется и вне config.Load (cmd/registry-cli), поэтому экспортирована.
func GetRequired(name string) (string, error) {
	v, err := GetOptional(name)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("missing required env: %s", name)
	}
	return v, nil
}

// GetOptional сначала читает <NAME>_FILE, потом <NAME>. Пустая строка = не задано.
// Ошибка чтения существующего файла — fatal (явная мисконфигурация).
// Если <NAME>_FILE задан, но файл не найден — логируем warning в stderr
// (опечатка в пути молча бы привела к использованию env-var с другим значением).
func GetOptional(name string) (string, error) {
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
	return os.Getenv(name), nil
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
