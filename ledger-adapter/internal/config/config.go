package config

import "os"

// Config holds the application configuration.
type Config struct {
	Port         string
	RpcURL       string
	PrivateKey   string
	ContractAddr string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	rpcURL := os.Getenv("RPC_URL")
	privateKey := os.Getenv("PRIVATE_KEY")
	contractAddr := os.Getenv("CONTRACT_ADDR")

	return &Config{
		Port:         port,
		RpcURL:       rpcURL,
		PrivateKey:   privateKey,
		ContractAddr: contractAddr,
	}
}
