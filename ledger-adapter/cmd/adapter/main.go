package main

import (
	"fmt"
	"log"
	"net/http"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/handler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	client, err := chain.NewClient(cfg)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	h := handler.New(client)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	log.Print("server starting...")
	if err := http.ListenAndServe(fmt.Sprintf(":%s", cfg.Port), mux); err != nil {
		log.Printf("server failed: %v", err)
	}
}
