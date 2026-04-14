package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the application configuration.
type Config struct {
	Port         string
	RpcURL       string
	PrivateKey   string
	ContractAddr string
}

// Load reads configuration from environment variables with sensible defaults.
//
// Для чувствительных значений (RPC_URL / PRIVATE_KEY / CONTRACT_ADDR) поддерживается
// <NAME>_FILE override: если задан путь и файл читается — его содержимое (trim'нутое)
// используется вместо прямого env. Иначе fallback на <NAME>. Нужно для test-профиля,
// где contract-deploy пишет артефакты в shared volume, а default профиль продолжает
// работать через обычные env.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	return &Config{
		Port:         port,
		RpcURL:       readEnvOrFile("RPC_URL"),
		PrivateKey:   readEnvOrFile("PRIVATE_KEY"),
		ContractAddr: readEnvOrFile("CONTRACT_ADDR"),
	}
}

// readEnvOrFile проверяет <NAME>_FILE; если путь задан и файл читается — возвращает
// содержимое. Ошибка чтения существующего пути — fatal (явная мисконфигурация).
// Если <NAME>_FILE пуст или файл отсутствует — fallback на <NAME>.
func readEnvOrFile(name string) string {
	if path := os.Getenv(name + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "config: read %s_FILE=%q: %v (falling back to %s)\n", name, path, err, name)
		}
	}
	return os.Getenv(name)
}
