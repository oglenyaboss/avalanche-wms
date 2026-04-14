package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/handler"
)

func main() {
	if err := run(); err != nil {
		log.Printf("ledger-adapter exited: %v", err)
		os.Exit(1)
	}
}

// run инкапсулирует main, чтобы defer'ы отработали перед os.Exit.
// log.Fatalf выходит через os.Exit и скипает defer — поэтому run возвращает
// error, а main логирует и выходит.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	client, err := chain.NewClient(cfg.RpcURL, cfg.PrivateKey, cfg.ContractAddr)
	if err != nil {
		return fmt.Errorf("chain client: %w", err)
	}
	defer client.Close()

	h := handler.New()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	log.Printf("ledger-adapter starting on :%s (signer=%s, contract=%s)",
		cfg.Port, client.FromAddress().Hex(), cfg.ContractAddr)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", cfg.Port), mux); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
